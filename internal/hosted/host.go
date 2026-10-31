package hosted

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gruesomeparty/marginalia/internal/diagram"
	"github.com/gruesomeparty/marginalia/internal/document"
	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/identity"
	"github.com/gruesomeparty/marginalia/internal/mcpserver"
	"github.com/gruesomeparty/marginalia/internal/review"
	"github.com/gruesomeparty/marginalia/internal/server"
	"github.com/gruesomeparty/marginalia/internal/session"
	"github.com/gruesomeparty/marginalia/internal/storage"
	"github.com/gruesomeparty/marginalia/internal/web"
)

// Options configures a Host. Every field is an operator's decision made at
// launch, never a tool input.
type Options struct {
	// Store holds every byte of review state. Required.
	Store storage.Store
	// PublicURL is the origin reviewers and agents reach the host at —
	// "https://review.example". Every URL handed to an agent is built from
	// it, never from the bind address. Empty means Serve derives one from
	// the listener, which is only right on a laptop.
	PublicURL string
	// Token gates /mcp (mcpserver.StaticTokenVerifier). Empty leaves it
	// open, which the command allows on loopback only.
	Token string
	// AgentName is the identity an agent holding Token writes under; empty
	// means mcpserver.DefaultAgentName.
	AgentName string
	// HumanAuth verifies the edge's JWT on page routes. Required unless
	// DevIdentity is set: a page route with no way to tell who is asking
	// would let anyone who reaches the port write the reviewer's verdicts.
	HumanAuth *identity.Verifier
	// DevIdentity treats every page request as this human, for a laptop
	// with no edge in front of it. The command allows it on loopback only.
	DevIdentity string
	// Reviewers, when set, are the only humans page routes admit, by the
	// name their identity resolves to (an email at the edge). Empty admits
	// every human the edge verified, as before the list existed.
	Reviewers []string
	// OAuth verifies access tokens on /mcp issued by OAuthIssuer, the
	// authorization server named in the protected-resource metadata; the
	// token's aud must be OAuthAudience, which defaults to the public /mcp
	// URL. Nil leaves only the static token. See oauth.go.
	OAuth         *identity.Verifier
	OAuthIssuer   string
	OAuthAudience string
	// Log is where notices go; nil means stderr.
	Log io.Writer
	// Now is the clock, injectable for tests. Nil means time.Now.
	Now func() time.Time
	// Limits are the caps and retention (limits.go). Zero fields take the
	// defaults.
	Limits Limits
	// Notify tells the human when an agent publishes or replies; nil is
	// off. See notify.go.
	Notify *Notifier
	// Diagrams draws mermaid when set; nil shows the anchored source. Off
	// by default in hosted mode: mermaid-cli is the expensive path, and an
	// agent's input is not something to shell out for unless the operator
	// said so.
	Diagrams *diagram.Renderer
}

// Host is one running instance: the reviews it serves, in memory, and the
// store they are written through to.
type Host struct {
	opts  Options
	store *storage.WriteThrough
	mu    sync.RWMutex
	// reviews is every review the store holds, by id. It is the inbox's
	// source and the router's table, and it is complete: startup loaded it
	// all, and this process is the only writer.
	reviews map[string]*Review
	mux     *http.ServeMux
	mcp     *mcp.Server
	// closing is closed once when the host is going down, so an in-flight
	// await returns "cancelled" instead of being waited out by the drain.
	closing   chan struct{}
	closeOnce sync.Once
	// pub serialises publishes. A revision number is read from the review
	// being revised and written as a new key, and two agents revising the
	// same review at once must get consecutive numbers, not the same one —
	// put-if-absent would refuse the second, but only after its first
	// document had been written.
	pub sync.Mutex
	// limits and use are the caps and what counts against them.
	limits Limits
	use    usage
}

func (h *Host) interrupt() { h.closeOnce.Do(func() { close(h.closing) }) }

// untilClosed bounds a call's context by the host's lifetime as well as the
// caller's.
func (h *Host) untilClosed(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-h.closing:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

// Review is one review as the host serves it: its metadata, its documents as
// parsed from the current revision, and the per-review server that renders
// them under /r/<id>/.
type Review struct {
	Meta   Meta
	Server *server.Server
}

// Open loads every review in opts.Store and returns a host serving them. A
// restart loses nothing: this is the only full listing the store ever gets.
func Open(ctx context.Context, opts Options) (*Host, error) {
	if opts.Store == nil {
		return nil, errors.New("hosted: a store is required")
	}
	if opts.HumanAuth == nil && opts.DevIdentity == "" {
		return nil, errors.New("hosted: no human identity: configure the edge's JWT (--human-jwt-issuer, --human-jwt-audience) or, on loopback only, --insecure-dev-identity <email>")
	}
	if opts.OAuth != nil && opts.OAuthIssuer != "" && opts.PublicURL == "" {
		// The metadata names this resource by its public URL; a derived one
		// would send every client to the wrong place.
		return nil, errors.New("hosted: an OAuth authorization server needs --public-url, which names the protected resource")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	h := &Host{opts: opts, reviews: map[string]*Review{}, closing: make(chan struct{}), limits: opts.Limits.withDefaults()}
	h.use.publishes = map[string][]time.Time{}
	h.store = storage.NewWriteThrough(&metered{Store: opts.Store, h: h, bytes: map[string]int64{}})
	if err := h.load(ctx); err != nil {
		return nil, err
	}
	// The sweep at startup: what outlived its retention while the process
	// was down goes to the archive before it is served again.
	if archived, err := h.Sweep(ctx); err != nil {
		return nil, err
	} else if len(archived) > 0 {
		h.logf("marginalia: %d review(s) archived at startup\n", len(archived))
	}
	h.mcp = mcp.NewServer(&mcp.Implementation{Name: "marginalia", Title: "Marginalia document review (hosted)", Version: session.Version}, nil)
	h.tools()
	h.routes()
	return h, nil
}

// load rebuilds every review from the store: one listing, then the latest
// metadata and current revision of each, and each log read through once so
// every later read is served from memory.
func (h *Host) load(ctx context.Context) error {
	keys, err := h.store.List(ctx, prefixReviews)
	if err != nil {
		return fmt.Errorf("hosted: listing reviews: %w", err)
	}
	latest := map[string]int{}
	for _, key := range keys {
		id, ok := reviewOf(key)
		if !ok {
			continue
		}
		if n := metaVersion(id, key); n > latest[id] {
			latest[id] = n
		}
	}
	for id, n := range latest {
		r, err := h.open(ctx, id, n)
		if err != nil {
			// One broken review must not take the rest down; say which.
			h.logf("marginalia: review %s could not be loaded and is not served: %v\n", id, err)
			continue
		}
		h.reviews[id] = r
		h.recordStored(r.Meta.Bytes)
	}
	return nil
}

// open builds a Review from metadata version n and the revision it names.
func (h *Host) open(ctx context.Context, id string, n int) (*Review, error) {
	raw, err := h.store.GetBlob(ctx, metaKey(id, n))
	if err != nil {
		return nil, err
	}
	var meta Meta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, fmt.Errorf("meta/%d.json: %w", n, err)
	}
	if meta.ID != id {
		return nil, fmt.Errorf("meta/%d.json names review %q", n, meta.ID)
	}
	docs := make([]server.Entry, 0, len(meta.Docs))
	for _, name := range meta.Docs {
		src, err := h.store.GetBlob(ctx, revKey(id, meta.Revision, name))
		if err != nil {
			return nil, err
		}
		doc, err := document.ParseBytes(name, src)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		e := server.Entry{Doc: doc, Store: feedback.OpenStore(h.store, logKey(id, name)), Label: name, Rel: name}
		// Warm the log: a read through now is the last time the backend
		// sees a read for it.
		events, err := e.Store.Load()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Store.Path(), err)
		}
		// What changes are measured from: this revision if the reviewer
		// already finished it, else the last one they did.
		switch b := meta.Baseline[name]; {
		case finishedSince(events, meta.From[name]):
			e.Base = doc
		case b > 0:
			old, err := h.store.GetBlob(ctx, revKey(id, b, name))
			if err != nil {
				return nil, err
			}
			if e.Base, err = document.ParseBytes(name, old); err != nil {
				return nil, fmt.Errorf("%s revision %d: %w", name, b, err)
			}
		}
		docs = append(docs, e)
	}
	return h.build(meta, docs)
}

// build wires parsed documents and metadata into the per-review server that
// renders them. Diagrams are off: a hosted instance has no mermaid-cli and
// should not shell out for an agent's input in any case.
func (h *Host) build(meta Meta, docs []server.Entry) (*Review, error) {
	cfg, err := reviewConfig(meta.Review, meta.Config)
	if err != nil {
		return nil, err
	}
	theme, err := web.ThemeFor(meta.Theme)
	if err != nil {
		return nil, session.Advertise(err)
	}
	srv := server.New(server.Options{
		Docs:     docs,
		Title:    meta.Title,
		Review:   cfg,
		Theme:    theme,
		Diagrams: h.opts.Diagrams,
		Author:   "reviewer",
		AuthorOf: authorOf,
		Log:      h.out(),
		BasePath: "/r/" + meta.ID,
		// The inbox is at the host's root: --public-url is an origin and
		// nothing else (publicOrigin), so a host is never mounted below "/".
		InboxURL: "/",
	})
	return &Review{Meta: meta, Server: srv}, nil
}

// reviewConfig is a preset, an inline YAML, or the file's worth of both —
// the hosted form of session.ReviewConfig, where the config travels in the
// request rather than on disk.
func reviewConfig(preset, inline string) (*review.Config, error) {
	cfg := review.Default()
	if preset != "" {
		var err error
		if cfg, err = review.Preset(preset); err != nil {
			return nil, session.Advertise(err)
		}
	}
	if strings.TrimSpace(inline) != "" {
		if err := cfg.OverlayYAML(inline); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

// Document is one document an agent publishes: a name whose extension picks
// the format, and the bytes.
type Document struct {
	Name    string
	Content []byte
}

// Publish is one publish request: the documents and how the review is
// framed.
type Publish struct {
	Title  string
	Agent  string
	Docs   []Document
	Review string // shipped preset
	Config string // inline review YAML
	Theme  string
}

// Publish stores a new review — revision 1 of every document, then its
// metadata — and serves it. Nothing is written until every document has
// parsed and the framing has validated: a refusal leaves no trace.
func (h *Host) Publish(ctx context.Context, p Publish) (*Review, error) {
	h.pub.Lock()
	defer h.pub.Unlock()
	id := NewID()
	now := h.opts.Now().UTC().Format(time.RFC3339)
	meta := Meta{
		ID: id, Title: p.Title, Agent: p.Agent, Revision: 1,
		Review: p.Review, Config: p.Config, Theme: p.Theme,
		Created: now, Updated: now,
	}
	return h.publish(ctx, meta, p, nil)
}

// Republish stores revision n+1 of an existing review and serves it. Earlier
// revisions stay as written — put-if-absent makes overwriting impossible —
// and the logs keep their keys, so every note the reviewer wrote re-anchors
// against the new text through feedback.Materialize: stale where its block
// changed, orphaned where it is gone. Framing left empty is inherited from
// the revision before.
func (h *Host) Republish(ctx context.Context, id string, p Publish) (*Review, error) {
	h.pub.Lock()
	defer h.pub.Unlock()
	prev := h.Review(id)
	if prev == nil {
		return nil, fmt.Errorf("no review %q here — the id comes from publish_document", id)
	}
	meta := prev.Meta
	meta.Revision++
	meta.Updated = h.opts.Now().UTC().Format(time.RFC3339)
	if p.Title != "" {
		meta.Title = p.Title
	}
	if p.Agent != "" {
		meta.Agent = p.Agent
	}
	if p.Review != "" || p.Config != "" {
		meta.Review, meta.Config = p.Review, p.Config
	}
	if p.Theme != "" {
		meta.Theme = p.Theme
	}
	meta.Docs = nil
	// A document this revision drops keeps its events in the wait's count;
	// one it brings back is counted live again.
	retired := maps.Clone(meta.Retired)
	for _, d := range p.Docs {
		delete(retired, d.Name)
	}
	for _, e := range prev.Server.Entries() {
		if slices.ContainsFunc(p.Docs, func(d Document) bool { return d.Name == e.Rel }) {
			continue
		}
		events, err := e.Store.Load()
		if err != nil {
			return nil, fmt.Errorf("hosted: %s: %w", e.Store.Path(), err)
		}
		if retired == nil {
			retired = map[string]uint64{}
		}
		retired[e.Rel] = uint64(len(events))
	}
	if len(retired) == 0 {
		retired = nil
	}
	meta.Retired = retired
	// Each document the new revision keeps is compared with the revision
	// the reviewer last finished: this one if they did, else whatever this
	// one was compared with.
	meta.Baseline = nil
	bases := map[string]*document.Document{}
	for _, e := range prev.Server.Entries() {
		if !slices.ContainsFunc(p.Docs, func(d Document) bool { return d.Name == e.Rel }) {
			continue
		}
		events, err := e.Store.Load()
		if err != nil {
			return nil, fmt.Errorf("hosted: %s: %w", e.Store.Path(), err)
		}
		b, base := prev.Meta.Baseline[e.Rel], prev.Server.BaseOf(e.Rel)
		if finishedSince(events, prev.Meta.From[e.Rel]) {
			b, base = prev.Meta.Revision, prev.Server.DocOf(e.Rel)
		}
		if b > 0 && base != nil {
			if meta.Baseline == nil {
				meta.Baseline = map[string]int{}
			}
			meta.Baseline[e.Rel] = b
			bases[e.Rel] = base
		}
	}
	return h.publish(ctx, meta, p, bases)
}

// finishedSince reports whether a log holds a review_done at or after index
// from: the reviewer finished the revision that started there.
func finishedSince(events []feedback.Event, from int) bool {
	for _, e := range events[min(from, len(events)):] {
		if e.Type == feedback.TypeReviewDone {
			return true
		}
	}
	return false
}

// publish validates everything, then writes the revision's blobs and the new
// metadata version, then swaps the served review. The order is the point: a
// refusal happens before the first write, and the page only changes once
// every byte of the revision is in the store.
func (h *Host) publish(ctx context.Context, meta Meta, p Publish, bases map[string]*document.Document) (*Review, error) {
	if len(p.Docs) == 0 {
		return nil, errors.New("nothing to publish — send at least one document")
	}
	// The caps, before anything is parsed or written. A refusal names the
	// limit and its value.
	if err := h.checkPublish(meta, p); err != nil {
		return nil, err
	}
	id, n := meta.ID, meta.Revision
	var docBytes int64
	for _, d := range p.Docs {
		docBytes += int64(len(d.Content))
	}
	meta.Bytes += docBytes
	seen := map[string]bool{}
	entries := make([]server.Entry, 0, len(p.Docs))
	for _, d := range p.Docs {
		if err := CheckName(d.Name); err != nil {
			return nil, err
		}
		if seen[d.Name] {
			return nil, fmt.Errorf("document %q is listed twice", d.Name)
		}
		seen[d.Name] = true
		if len(d.Content) == 0 {
			return nil, fmt.Errorf("document %q is empty", d.Name)
		}
		if document.FormatFor(d.Name) == "" {
			return nil, session.UnsupportedInput(filepath.Ext(d.Name))
		}
		doc, err := document.ParseBytes(d.Name, d.Content)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", d.Name, err)
		}
		entries = append(entries, server.Entry{Doc: doc, Store: feedback.OpenStore(h.store, logKey(id, d.Name)), Label: d.Name, Rel: d.Name, Base: bases[d.Name]})
		meta.Docs = append(meta.Docs, d.Name)
	}
	// Where this revision starts in each log, so a wait can tell this
	// round's review_done from the last one's.
	meta.From = nil
	if n > 1 {
		for _, e := range entries {
			events, err := e.Store.Load()
			if err != nil {
				return nil, fmt.Errorf("hosted: %s: %w", e.Store.Path(), err)
			}
			if len(events) > 0 {
				if meta.From == nil {
					meta.From = map[string]int{}
				}
				meta.From[e.Rel] = len(events)
			}
		}
	}
	// Validate the framing before the first write, for the same reason.
	r, err := h.build(meta, entries)
	if err != nil {
		return nil, err
	}
	for _, d := range p.Docs {
		if err := h.store.PutBlob(ctx, revKey(id, n, d.Name), d.Content); err != nil {
			return nil, fmt.Errorf("hosted: storing %s: %w", d.Name, err)
		}
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	if err := h.store.PutBlob(ctx, metaKey(id, n), raw); err != nil {
		return nil, fmt.Errorf("hosted: storing metadata: %w", err)
	}
	// Warm the logs now, as startup does, so the first render of the inbox
	// or the page is served from memory like every one after it.
	for _, e := range entries {
		if _, err := e.Store.Load(); err != nil {
			return nil, fmt.Errorf("hosted: %s: %w", e.Store.Path(), err)
		}
	}
	h.mu.Lock()
	h.reviews[id] = r
	h.mu.Unlock()
	h.recordPublish(meta.Agent, docBytes)
	h.logf("marginalia: review %s revision %d published (%d document(s)) → %s\n", id, n, len(p.Docs), h.URL(id))
	if n == 1 {
		h.notify(NotifyPublished, r)
	} else {
		h.notify(NotifyRepublished, r)
	}
	return r, nil
}

// Review returns one review by id, or nil.
func (h *Host) Review(id string) *Review {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.reviews[id]
}

// Reviews returns every review, newest first by its latest publish.
func (h *Host) Reviews() []*Review {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]*Review, 0, len(h.reviews))
	for _, r := range h.reviews {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Meta.Updated != out[j].Meta.Updated {
			return out[i].Meta.Updated > out[j].Meta.Updated
		}
		return out[i].Meta.ID < out[j].Meta.ID
	})
	return out
}

// URL is where a review is, for a human or an agent: the public origin, never
// the bind address.
func (h *Host) URL(id string) string {
	return strings.TrimSuffix(h.opts.PublicURL, "/") + "/r/" + id + "/"
}

// MCP is the server behind /mcp, carrying the hosted tools (tools.go).
func (h *Host) MCP() *mcp.Server { return h.mcp }

// routes mounts the one listener: the inbox's place at /, a review's own
// server under /r/<id>/, the MCP transport at /mcp, and /healthz.
func (h *Host) routes() {
	mux := http.NewServeMux()
	// Page routes are the human's: every one of them resolves the reviewer
	// first. /mcp is the agent's, and a credential for one is refused by
	// the other.
	mux.Handle("GET /{$}", h.requireHuman(http.HandlerFunc(h.handleRoot)))
	mux.Handle("GET /api/reviews", h.requireHuman(http.HandlerFunc(h.handleReviews)))
	mux.HandleFunc("GET /healthz", mcpserver.Healthz)
	mux.Handle("/mcp", h.transport())
	if h.oauthEnabled() {
		meta := auth.ProtectedResourceMetadataHandler(h.metadata())
		mux.Handle(metadataPath, meta)
		mux.Handle(metadataPath+mcpPath, meta)
	}
	mux.Handle("GET "+agentWaitPrefix+"{id}/wait", h.agentWait())
	mux.Handle("/r/{id}", h.requireHuman(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The page's URLs are relative to its mount, so it has to be
		// opened with the trailing slash.
		http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently) //nolint:gosec // same-origin: the route only matches /r/{id}, so the target is that path plus a slash
	})))
	mux.Handle("/r/{id}/", h.requireHuman(http.HandlerFunc(h.handleReview)))
	h.mux = mux
}

// handleReview hands a request under /r/<id>/ to that review's server, with
// the prefix stripped so the server sees the routes it registered. An id
// nobody published is a 404 like any other: the id is not the auth boundary,
// so there is nothing to hide by answering differently.
func (h *Host) handleReview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !ValidID(id) {
		http.NotFound(w, r)
		return
	}
	rv := h.Review(id)
	if rv == nil {
		http.NotFound(w, r)
		return
	}
	http.StripPrefix("/r/"+id, rv.Server.Handler()).ServeHTTP(w, r)
}

// Handler is the whole listener's mux.
func (h *Host) Handler() http.Handler { return h.mux }

// Serve runs the host on ln until ctx is cancelled, then drains, as `serve`
// does, so a comment saved as the process is told to stop still reaches the
// store.
func (h *Host) Serve(ctx context.Context, ln net.Listener) error {
	if h.opts.PublicURL == "" {
		h.opts.PublicURL = derivedURL(ln)
		h.logf("marginalia: no --public-url; using %s — set it to the address reviewers and agents reach this host at\n", h.opts.PublicURL)
	}
	httpSrv := &http.Server{Handler: h.mux, ReadHeaderTimeout: 5 * time.Second}
	stopped := make(chan struct{})
	defer close(stopped)
	drained := make(chan error, 1)
	go func() { //nolint:gosec // the drain runs after ctx is done, so it needs a context of its own
		select {
		case <-ctx.Done():
		case <-stopped:
			return
		}
		h.interrupt()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		drained <- httpSrv.Shutdown(shutCtx)
	}()
	h.mu.RLock()
	n := len(h.reviews)
	h.mu.RUnlock()
	var how []string
	if h.opts.Token != "" {
		how = append(how, "static token")
	}
	if h.oauthEnabled() {
		how = append(how, "OAuth via "+h.opts.OAuthIssuer)
	}
	if len(how) == 0 {
		how = append(how, "no credential: loopback only")
	}
	h.logf("marginalia: hosting %d review(s) at %s (listening on %s; MCP at /mcp, %s)\n", n, h.opts.PublicURL, ln.Addr(), strings.Join(how, " or "))
	h.logf("marginalia: limits: %s\n", h.describeLimits())
	go h.sweeper(ctx)
	if h.opts.Notify != nil {
		go h.opts.Notify.run(ctx)
		defer h.opts.Notify.Close()
	}
	err := httpSrv.Serve(ln)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return <-drained
}

// derivedURL is the best guess at a public URL when none was given: the
// listener's own address, with an unspecified host read as localhost.
func derivedURL(ln net.Listener) string {
	addr := ln.Addr().String()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + addr
	}
	if ip := net.ParseIP(host); ip == nil || ip.IsUnspecified() {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port)
}

func (h *Host) out() io.Writer {
	if h.opts.Log != nil {
		return h.opts.Log
	}
	return os.Stderr
}

func (h *Host) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(h.out(), format, args...)
}
