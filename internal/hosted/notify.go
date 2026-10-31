package hosted

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// The inbox shows what is waiting, but only when the human opens it; an
// agent that publishes and then blocks on await_review_done waits until they
// happen to look. A Notifier tells them: one POST per agent action to each
// configured URL — an ntfy topic as is, or any webhook with the JSON form.
//
// Three rules shape it. It fires on the agent's side of the loop only —
// a publish, a republish, a reply or claim after the human finished — and
// never on the human's own actions, which would be telling them what they
// just did. It carries titles, identities, counts and the URL, and never a
// word of a document or a note: a notification service is a third party.
// And it is off the request path: delivery happens in the background with a
// short timeout and one retry, a failure is a line on stderr, and a broken
// notifier never fails a publish.

// Notification kinds.
const (
	NotifyPublished   = "published"
	NotifyRepublished = "republished"
	NotifyReplied     = "replied"
)

// Notification is one thing that happened to a review.
type Notification struct {
	Event    string `json:"event"`
	ReviewID string `json:"review_id"`
	Title    string `json:"title"`
	Agent    string `json:"agent"`
	URL      string `json:"url"`
	At       string `json:"at"`
	// Updates is how many events this one stands for, when a burst was
	// collapsed; 1 otherwise.
	Updates int `json:"updates,omitempty"`
	// Blocking says the agent is stopped until the reviewer answers. It is
	// delivered at once, whatever the debounce (issue #220).
	Blocking bool `json:"blocking,omitempty"`
}

// Notifier delivers notifications to a set of URLs, debounced per review.
type Notifier struct {
	URLs   []string
	Format string // "text" (ntfy-ready) or "json"
	// Client is the HTTP client; nil means one with the Timeout.
	Client *http.Client
	// Timeout bounds one delivery attempt; zero means 5s. There is one
	// retry after a failure.
	Timeout time.Duration
	// Debounce is the least time between two notifications for one review;
	// events inside it collapse into one "n updates". Zero means 60s.
	Debounce time.Duration
	// Now is the clock, injectable for tests.
	Now func() time.Time
	// Log is where failures go; nil means stderr.
	Log io.Writer

	mu      sync.Mutex
	last    map[string]time.Time    // last delivery per review
	pending map[string]Notification // a burst waiting for the window to pass
	wg      sync.WaitGroup
}

func (n *Notifier) now() time.Time {
	if n.Now != nil {
		return n.Now()
	}
	return time.Now()
}

func (n *Notifier) timeout() time.Duration {
	if n.Timeout > 0 {
		return n.Timeout
	}
	return 5 * time.Second
}

func (n *Notifier) debounce() time.Duration {
	if n.Debounce > 0 {
		return n.Debounce
	}
	return time.Minute
}

func (n *Notifier) client() *http.Client {
	if n.Client != nil {
		return n.Client
	}
	return &http.Client{Timeout: n.timeout()}
}

func (n *Notifier) logf(format string, args ...any) {
	out := n.Log
	if out == nil {
		out = os.Stderr
	}
	_, _ = fmt.Fprintf(out, format, args...)
}

// Notify records that something happened and delivers it — now, if the
// review's window has passed, or folded into the pending burst otherwise.
// It never blocks the caller on the network.
func (n *Notifier) Notify(ev Notification) {
	if n == nil || len(n.URLs) == 0 {
		return
	}
	if ev.Updates == 0 {
		ev.Updates = 1
	}
	if ev.At == "" {
		ev.At = n.now().UTC().Format(time.RFC3339)
	}
	n.mu.Lock()
	if n.last == nil {
		n.last = map[string]time.Time{}
		n.pending = map[string]Notification{}
	}
	now := n.now()
	if last, sent := n.last[ev.ReviewID]; sent && now.Sub(last) < n.debounce() && !ev.Blocking {
		// Inside the window: fold into what is waiting, keeping the latest
		// event's words and counting the rest.
		if p, ok := n.pending[ev.ReviewID]; ok {
			ev.Updates += p.Updates
		}
		n.pending[ev.ReviewID] = ev
		n.mu.Unlock()
		return
	}
	// Whatever was waiting rides along rather than following a minute later.
	if p, ok := n.pending[ev.ReviewID]; ok {
		ev.Updates += p.Updates
		delete(n.pending, ev.ReviewID)
	}
	n.last[ev.ReviewID] = now
	n.mu.Unlock()
	n.deliver(ev)
}

// Flush sends every pending burst whose window has passed. The host calls
// it periodically; a test calls it after moving the clock.
func (n *Notifier) Flush() {
	if n == nil {
		return
	}
	n.mu.Lock()
	now := n.now()
	var due []Notification
	for id, ev := range n.pending {
		if now.Sub(n.last[id]) >= n.debounce() {
			due = append(due, ev)
			delete(n.pending, id)
			n.last[id] = now
		}
	}
	n.mu.Unlock()
	for _, ev := range due {
		n.deliver(ev)
	}
}

// run flushes until ctx is cancelled.
func (n *Notifier) run(ctx context.Context) {
	ticker := time.NewTicker(n.debounce() / 6)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n.Flush()
		}
	}
}

// Close waits for deliveries in flight, bounded by the timeout and retry.
func (n *Notifier) Close() {
	if n == nil {
		return
	}
	n.wg.Wait()
}

// deliver posts ev to every URL, in the background.
func (n *Notifier) deliver(ev Notification) {
	for _, url := range n.URLs {
		n.wg.Add(1)
		go func(url string) {
			defer n.wg.Done()
			if err := n.post(url, ev); err != nil {
				n.logf("marginalia: notify %s: %v\n", url, err)
			}
		}(url)
	}
}

// post tries once, and once more after a failure.
func (n *Notifier) post(url string, ev Notification) error {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Second)
		}
		if err = n.once(url, ev); err == nil {
			return nil
		}
	}
	return err
}

func (n *Notifier) once(url string, ev Notification) error {
	ctx, cancel := context.WithTimeout(context.Background(), n.timeout())
	defer cancel()
	var body []byte
	var contentType string
	if n.Format == "json" {
		body, _ = json.Marshal(ev)
		contentType = "application/json"
	} else {
		body = []byte(Text(ev))
		contentType = "text/plain; charset=utf-8"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	// ntfy reads these; anything else ignores them.
	req.Header.Set("Title", title(ev))
	req.Header.Set("Click", ev.URL)
	req.Header.Set("Tags", tag(ev))
	res, err := n.client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	if res.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return nil
}

func title(ev Notification) string {
	if ev.Title != "" {
		return ev.Title
	}
	return "Marginalia review"
}

func tag(ev Notification) string {
	if ev.Blocking {
		return "rotating_light"
	}
	switch ev.Event {
	case NotifyPublished:
		return "inbox_tray"
	case NotifyReplied:
		return "speech_balloon"
	}
	return "arrows_counterclockwise"
}

// Text is the plain-text body: what happened, to which review, from whom,
// and where — nothing from the document or the notes.
func Text(ev Notification) string {
	var what string
	switch ev.Event {
	case NotifyPublished:
		what = "published a review for you"
	case NotifyRepublished:
		what = "published a new revision"
	case NotifyReplied:
		what = "replied after you finished"
	default:
		what = ev.Event
	}
	who := ev.Agent
	if who == "" {
		who = "an agent"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s: %s", who, what, title(ev))
	if ev.Blocking {
		b.WriteString(" (blocking: the agent is waiting on you)")
	}
	if ev.Updates > 1 {
		fmt.Fprintf(&b, " (%d updates)", ev.Updates)
	}
	fmt.Fprintf(&b, "\n%s\n", ev.URL)
	return b.String()
}

// notify is the host's side: a notification for a review, built from what the
// host knows and nothing the agent sent beyond its name.
func (h *Host) notify(event string, r *Review) {
	if h.opts.Notify == nil {
		return
	}
	h.opts.Notify.Notify(Notification{
		Event: event, ReviewID: r.Meta.ID, Title: inboxTitle(r), Agent: r.Meta.Agent,
		URL: h.URL(r.Meta.ID), At: h.opts.Now().UTC().Format(time.RFC3339),
		Blocking: r.Meta.Blocking && event != NotifyReplied,
	})
}

// inboxTitle is the review's title, or its first document's name — the same
// rule the inbox uses.
func inboxTitle(r *Review) string {
	if r.Meta.Title != "" {
		return r.Meta.Title
	}
	if len(r.Meta.Docs) > 0 {
		return r.Meta.Docs[0]
	}
	return ""
}
