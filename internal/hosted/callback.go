package hosted

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/review"
)

// Callbacks call an agent back (issue #219). An agent that publishes with a
// callback URL gets a signed, content-free POST there when the reviewer
// submits Done or asks a question, so a job can stop instead of holding a
// runner open on await_review_done and be restarted by the call.
//
// It is the reverse of everything else the host does — the host making a
// request to a URL an agent named — so it is fenced three ways: the
// operator's allow-list of hosts (none means the feature is off), https
// only, and a dialer that refuses any address that is not public after
// resolution, so a name on the list cannot be pointed at the host's own
// network. Redirects are not followed.
//
// The body says which review, which document, what happened and the cursor
// to read from; never a title, a quote or a word of a note. It is signed
// with HMAC-SHA256 under a per-review secret derived from the operator's
// secret and the review id, handed to the agent by publish_document and
// never stored or logged.
type Callbacks struct {
	// Hosts is the allow-list: an exact host name, or "*.example.com" for
	// any name below it (not the apex).
	Hosts []string
	// Secret is the operator's key (MARGINALIA_CALLBACK_SECRET). Required.
	Secret []byte
	// Timeout bounds one attempt; zero means 5s.
	Timeout time.Duration
	// Debounce collapses a burst of questions on one review into one call
	// carrying the latest cursor; zero means a minute. Done is never held.
	Debounce time.Duration
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Log is where a failed delivery is reported; nil means stderr.
	Log io.Writer

	// client is the HTTP client; nil builds the guarded one. Tests set it
	// to reach an httptest server on loopback.
	client *http.Client

	once    sync.Once
	guarded *http.Client
	mu      sync.Mutex
	last    map[string]time.Time
	pending map[string]pendingCall
	wg      sync.WaitGroup
}

type pendingCall struct {
	url  string
	body Callback
}

// Callback is what a callback POST carries.
type Callback struct {
	// Event is "review_done" or "question".
	Event    string `json:"event"`
	ReviewID string `json:"review_id"`
	Revision int    `json:"revision"`
	// Name is the document the event is on.
	Name string `json:"name"`
	// Done says the review is now complete by its rule; a review_done that
	// leaves others to finish is false.
	Done bool `json:"done"`
	// Cursor is the events per document name to pass to feedback_since or
	// await_review_done, and Since the count for GET /agent/r/{id}/wait,
	// both taken just before the event: reading from them returns it (and,
	// for a collapsed burst, every question it stands for).
	Cursor map[string]int `json:"cursor"`
	Since  uint64         `json:"since"`
	At     string         `json:"at"`
}

// SignatureHeader carries "sha256=<hex>" of HMAC-SHA256 over the body.
const SignatureHeader = "X-Marginalia-Signature"

// NewCallbacks is the feature as the environment configures it: off (nil)
// without MARGINALIA_CALLBACK_HOSTS, and an error when hosts are listed but
// MARGINALIA_CALLBACK_SECRET is missing or short.
func NewCallbacks(hosts, secret string) (*Callbacks, error) {
	var list []string
	for _, h := range strings.Split(hosts, ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			list = append(list, h)
		}
	}
	if len(list) == 0 {
		return nil, nil
	}
	if len(secret) < 32 {
		return nil, errors.New("MARGINALIA_CALLBACK_HOSTS is set, so MARGINALIA_CALLBACK_SECRET must be too, at least 32 characters (openssl rand -hex 32)")
	}
	return &Callbacks{Hosts: list, Secret: []byte(secret)}, nil
}

// ReviewSecret is the key a review's callbacks are signed with. It is
// derived, so nothing secret is stored beside the review, and an operator
// who rotates MARGINALIA_CALLBACK_SECRET rotates every review's.
func (c *Callbacks) ReviewSecret(id string) string {
	m := hmac.New(sha256.New, c.Secret)
	m.Write([]byte("marginalia callback\x00" + id))
	return hex.EncodeToString(m.Sum(nil))
}

// Sign is the signature header value for body under a review's secret.
func Sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// Check refuses a callback URL the host will not call: callbacks off, not
// https, credentials in it, a host not on the list, or an address that is
// not public. It runs at publish time, before anything is written; the
// dialer checks the address again at delivery, since a name can move.
func (c *Callbacks) Check(raw string) error {
	if c == nil {
		return errors.New("callbacks are off on this host (the operator sets MARGINALIA_CALLBACK_HOSTS); use await_review_done")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("callback_url %q is not an absolute URL", raw)
	}
	if u.Scheme != "https" {
		return errors.New("callback_url must be https")
	}
	if u.User != nil {
		return errors.New("callback_url must not carry credentials; verify the signature instead")
	}
	host := strings.ToLower(u.Hostname())
	if !c.allowed(host) {
		return fmt.Errorf("callback host %q is not on this host's allow-list", host)
	}
	if ip, err := netip.ParseAddr(host); err == nil && !public(ip) {
		return fmt.Errorf("callback host %q is not a public address", host)
	}
	return nil
}

func (c *Callbacks) allowed(host string) bool {
	for _, h := range c.Hosts {
		if suffix, ok := strings.CutPrefix(h, "*."); ok {
			if strings.HasSuffix(host, "."+suffix) {
				return true
			}
		} else if host == h {
			return true
		}
	}
	return false
}

// internalRanges are global-unicast-looking ranges that are not public
// endpoints: shared address space (a Tailscale tailnet among others), "this
// network", benchmarking, reserved, and the IPv6 prefixes that translate to
// an IPv4 address (NAT64, 6to4), which could reach an internal one.
var internalRanges = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2002::/16"),
}

// public reports whether ip is an address on the public internet.
func public(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, p := range internalRanges {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// refuseInternal is the dialer's Control: it sees the resolved address of
// every connection attempt, so a public name that resolves to 127.0.0.1 or
// a metadata endpoint is refused there, after resolution.
func refuseInternal(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return err
	}
	if !public(ap.Addr()) {
		return fmt.Errorf("refusing to call back %s: not a public address", ap.Addr())
	}
	return nil
}

func (c *Callbacks) http() *http.Client {
	if c.client != nil {
		return c.client
	}
	c.once.Do(func() {
		d := &net.Dialer{Timeout: c.timeout(), Control: refuseInternal}
		c.guarded = &http.Client{
			Transport: &http.Transport{
				DialContext:         d.DialContext,
				TLSHandshakeTimeout: c.timeout(),
				MaxIdleConns:        4,
				IdleConnTimeout:     30 * time.Second,
			},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	})
	return c.guarded
}

func (c *Callbacks) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return 5 * time.Second
}

func (c *Callbacks) debounce() time.Duration {
	if c.Debounce > 0 {
		return c.Debounce
	}
	return time.Minute
}

func (c *Callbacks) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Callbacks) logf(format string, args ...any) {
	w := c.Log
	if w == nil {
		w = os.Stderr
	}
	_, _ = fmt.Fprintf(w, format, args...)
}

// Send queues one call. A question inside the debounce window of the last
// call on that review waits, replaced by any later one; a review_done goes
// at once and takes a waiting question's place, since its cursor covers it.
func (c *Callbacks) Send(target string, cb Callback) {
	if c == nil || target == "" {
		return
	}
	if cb.At == "" {
		cb.At = c.now().UTC().Format(time.RFC3339)
	}
	c.mu.Lock()
	if c.last == nil {
		c.last = map[string]time.Time{}
		c.pending = map[string]pendingCall{}
	}
	now := c.now()
	// A call that takes a waiting one's place keeps that one's cursor, the
	// earlier: reading from it returns every event the burst stood for.
	if p, ok := c.pending[cb.ReviewID]; ok {
		cb.Cursor, cb.Since = p.body.Cursor, p.body.Since
	}
	if last, sent := c.last[cb.ReviewID]; sent && cb.Event == feedback.TypeQuestion && now.Sub(last) < c.debounce() {
		c.pending[cb.ReviewID] = pendingCall{url: target, body: cb}
		c.mu.Unlock()
		return
	}
	delete(c.pending, cb.ReviewID)
	c.last[cb.ReviewID] = now
	c.mu.Unlock()
	c.deliver(target, cb)
}

// Flush sends every waiting question whose window has passed.
func (c *Callbacks) Flush() {
	if c == nil {
		return
	}
	c.mu.Lock()
	now := c.now()
	var due []pendingCall
	for id, p := range c.pending {
		if now.Sub(c.last[id]) >= c.debounce() {
			due = append(due, p)
			delete(c.pending, id)
			c.last[id] = now
		}
	}
	c.mu.Unlock()
	for _, p := range due {
		c.deliver(p.url, p.body)
	}
}

// Run flushes on a ticker until ctx ends.
func (c *Callbacks) Run(ctx context.Context) {
	if c == nil {
		return
	}
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.Flush()
		}
	}
}

// Close waits for deliveries in flight.
func (c *Callbacks) Close() {
	if c != nil {
		c.wg.Wait()
	}
}

// deliver posts in the background: one try, one retry, and a failure is a
// line on stderr naming the review, never the URL's query or the body.
func (c *Callbacks) deliver(target string, cb Callback) {
	body, err := json.Marshal(cb)
	if err != nil {
		return
	}
	sig := Sign(c.ReviewSecret(cb.ReviewID), body)
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		var err error
		for attempt := 0; attempt < 2; attempt++ {
			if attempt > 0 {
				time.Sleep(time.Second)
			}
			if err = c.attempt(target, body, sig); err == nil {
				return
			}
		}
		c.logf("marginalia: callback for review %s: %v\n", cb.ReviewID, err)
	}()
}

func (c *Callbacks) attempt(target string, body []byte, sig string) error {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(SignatureHeader, sig)
	req.Header.Set("User-Agent", "marginalia-callback")
	res, err := c.http().Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			return uerr.Err // the error without the URL, which may carry a token
		}
		return err
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	if res.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return nil
}

// recorded is the per-review server's hook: a human's review_done or
// question on a review that asked to be called back becomes a call. Page
// routes are human-only (requireHuman), so every event here is the
// reviewer's.
func (h *Host) recorded(id, rel string, e feedback.Event) {
	if h.opts.Callbacks == nil || (e.Type != review.TypeReviewDone && e.Type != feedback.TypeQuestion) {
		return
	}
	r := h.Review(id)
	if r == nil || r.Meta.Callback == "" {
		return
	}
	since, done, err := h.tally(id)
	if err != nil {
		h.logf("marginalia: callback for review %s: %v\n", id, err)
		return
	}
	// The counts now include the event; the cursor is the position before
	// it, so the agent reads the event it was called about.
	if since > 0 {
		since--
	}
	cursor := map[string]int{}
	for _, en := range r.Server.Entries() {
		if events, err := en.Store.Load(); err == nil {
			cursor[en.Rel] = len(events)
			if en.Rel == rel && cursor[en.Rel] > 0 {
				cursor[en.Rel]--
			}
		}
	}
	h.opts.Callbacks.Send(r.Meta.Callback, Callback{
		Event: e.Type, ReviewID: id, Revision: r.Meta.Revision, Name: rel,
		Done: done, Cursor: cursor, Since: since,
		At: h.opts.Now().UTC().Format(time.RFC3339),
	})
}
