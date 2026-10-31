package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/review"
	"github.com/gruesomeparty/marginalia/internal/web"
)

// maxFeedbackBody caps a POSTed event. The largest legitimate one is a
// suggest_edit carrying a replacement block, so a megabyte is generous; the
// point is that an unbounded body should not be readable into memory even on
// a trusted interface.
const maxFeedbackBody = 1 << 20

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// entry resolves which document a request is about: an explicit `doc` (a Rel
// path or the document's own path), else the first document in the set.
func (s *Server) entry(r *http.Request) *Entry {
	if key := r.URL.Query().Get("doc"); key != "" {
		return s.lookup(key)
	}
	return &s.docs[0]
}

func (s *Server) lookup(key string) *Entry {
	if e, ok := s.byRel[key]; ok {
		return e
	}
	if e, ok := s.byPath[key]; ok {
		return e
	}
	return nil
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	s.renderPage(w, r, &s.docs[0])
}

func (s *Server) handleDocPage(w http.ResponseWriter, r *http.Request) {
	e := s.lookup(r.PathValue("doc"))
	if e == nil {
		http.NotFound(w, r)
		return
	}
	s.renderPage(w, r, e)
}

// resolution materializes a document's log against the document as it now
// reads, so a re-render can say which notes still apply, which predate an edit
// and which have lost their block entirely.
func (s *Server) resolution(e *Entry, events []feedback.Event) feedback.Resolution {
	doc := s.docOf(e)
	blocks := make([]feedback.Block, 0, len(doc.Blocks))
	for _, b := range doc.Blocks {
		blocks = append(blocks, feedback.Block{ID: b.ID, Hash: b.Hash, Text: b.PlainText})
	}
	res := feedback.Materialize(events, blocks)
	res.Complete(s.Review().Completion())
	return res
}

// renderPage renders one document plus the navigation state of the whole set,
// which means reading every log: cheap, and it keeps the sidebar's counts and
// done ticks true on every load. The page is rendered into memory first: once
// bytes are on the wire the status is already sent, and a half-written
// document under a 200 is worse than an honest 500.
func (s *Server) renderPage(w http.ResponseWriter, r *http.Request, current *Entry) {
	page := web.Page{
		Doc:      s.docOf(current),
		Watch:    s.opts.Watch,
		Revision: s.Revision(),
		Author:   s.authorOf(r),
		Title:    s.opts.Title,
		Nested:   s.opts.Nested,
		Review:   s.opts.Review,
		Rel:      current.Rel,
		Theme:    s.opts.Theme,
		Base:     s.opts.BasePath,
		Inbox:    s.opts.InboxURL,
	}
	setDone := true
	rule := s.Review().Completion()
	for i := range s.docs {
		e := &s.docs[i]
		events, err := e.Store.Load()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if e == current {
			page.Events = events
			page.Resolution = s.resolution(e, events)
		}
		nav := web.NavDoc{
			Label:   e.Label,
			Rel:     e.Rel,
			Count:   countComments(events),
			Done:    rule.Met(feedback.Reviewers(events)),
			Current: e == current,
		}
		if !nav.Done {
			setDone = false
		}
		page.Docs = append(page.Docs, nav)
	}
	page.SetDone = setDone && len(s.docs) > 1
	var buf bytes.Buffer
	if err := web.Render(&buf, page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

func countComments(events []feedback.Event) int {
	n := 0
	for _, e := range events {
		if e.Type != feedback.TypeReviewDone {
			n++
		}
	}
	return n
}

func (s *Server) handleDoc(w http.ResponseWriter, r *http.Request) {
	e := s.entry(r)
	if e == nil {
		http.NotFound(w, r)
		return
	}
	events, err := e.Store.Load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if events == nil {
		events = []feedback.Event{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"doc":        s.docOf(e),
		"events":     events,
		"author":     s.authorOf(r),
		"docs":       s.docList(),
		"resolution": s.resolution(e, events),
		// The vocabulary is part of the answer: an agent reading `blocker`
		// out of the log needs to see that it was asked for, and what it was
		// labelled when the human tapped it.
		"review": web.ReviewInfo(s.opts.Review, s.docOf(e), e.Rel),
		// Resolved against this document, so a consuming agent can see which
		// blocks were deliberately not reviewed rather than re-deriving it
		// from the patterns.
		"readonly": s.blocksWhere(e, s.opts.Review.Locked),
		"skipped":  s.blocksWhere(e, s.opts.Review.Skipped),
	})
}

// blocksWhere lists the document's blocks a review rule applies to.
func (s *Server) blocksWhere(e *Entry, rule func(string) bool) []string {
	out := []string{}
	for _, b := range e.Doc.Blocks {
		if rule(b.ID) {
			out = append(out, b.ID)
		}
	}
	return out
}

// docList describes the served set so an agent reading the API knows what the
// human was handed, not just the document it asked about.
func (s *Server) docList() []map[string]string {
	out := make([]map[string]string, 0, len(s.docs))
	for i := range s.docs {
		e := &s.docs[i]
		out = append(out, map[string]string{"path": s.docOf(e).Path, "rel": e.Rel, "label": e.Label})
	}
	return out
}

// handleResolution serves the second-pass view an agent needs after revising a
// document: the current state per block, which notes an edit has outrun, and
// which have lost their block altogether.
func (s *Server) handleResolution(w http.ResponseWriter, r *http.Request) {
	e := s.entry(r)
	if e == nil {
		http.NotFound(w, r)
		return
	}
	events, err := e.Store.Load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, s.resolution(e, events))
}

// handleRevision lets the page notice that a document it is showing has been
// re-parsed under it. Deliberately tiny: the page polls it while --watch is on.
func (s *Server) handleRevision(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"rev": s.Revision(), "watch": s.opts.Watch})
}

func (s *Server) handleGetFeedback(w http.ResponseWriter, r *http.Request) {
	e := s.entry(r)
	if e == nil {
		http.NotFound(w, r)
		return
	}
	events, err := e.Store.Load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if events == nil {
		events = []feedback.Event{}
	}
	writeJSON(w, http.StatusOK, events)
}

func (s *Server) handlePostFeedback(w http.ResponseWriter, r *http.Request) {
	var e feedback.Event
	r.Body = http.MaxBytesReader(w, r.Body, maxFeedbackBody)
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	// The event names its own document, but only a document in the served set
	// may be written to: a stale page must not be able to append anywhere.
	target := &s.docs[0]
	if e.Doc != "" {
		if target = s.lookup(e.Doc); target == nil {
			http.Error(w, "unknown document", http.StatusBadRequest)
			return
		}
	} else if key := r.URL.Query().Get("doc"); key != "" {
		if target = s.lookup(key); target == nil {
			http.Error(w, "unknown document", http.StatusBadRequest)
			return
		}
	}
	if code, err := s.check(target, &e); err != nil {
		http.Error(w, err.Error(), code)
		return
	}
	if err := s.append(r, target, &e); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// The reply pointer is a note id, and only the server can derive one — a
	// page has no sha256 it can rely on in every context it runs in. So the
	// saved event comes back wearing its id, and the page can offer a reply on
	// a note the reviewer just wrote.
	writeJSON(w, http.StatusCreated, saved{Event: e, ID: feedback.NoteID(e), At: s.locate(target, &e)})
}

// saved is the POSTed event as it is echoed back: the event itself, flattened,
// plus the two things only the server can work out — the id a reply names it
// by, and where a sub-anchor landed. Embedding keeps the shape a client
// already parses; both are added keys, not a new envelope.
type saved struct {
	feedback.Event
	ID string            `json:"id"`
	At *feedback.Located `json:"at,omitempty"`
}

// locate reports where a just-saved sub-anchor sits, so the page can mark the
// sentence without waiting for a re-render. It is the same call Materialize
// makes, against the same text, so the mark cannot drift from the one a reload
// would paint.
func (s *Server) locate(target *Entry, e *feedback.Event) *feedback.Located {
	if e.Sub == nil {
		return nil
	}
	for _, b := range s.docOf(target).Blocks {
		if b.ID != e.Block {
			continue
		}
		if at, ok := feedback.Locate(e.Sub, b.PlainText); ok {
			return &at
		}
	}
	return nil
}

// check is the server-side gate on what a page may write. The page renders
// the same rules, but rendering them is not enforcing them: a tab left open
// across a restart, or a script pointed at the API, must not be able to
// invent vocabulary the agent never asked for or comment on a block it
// handed over read-only.
func (s *Server) check(target *Entry, e *feedback.Event) (int, error) {
	cfg := s.opts.Review
	if e.Type == feedback.TypeReviewDone {
		// review_done is the protocol, not review vocabulary — but it is
		// what require_verdict withholds.
		return s.checkVerdicts(target)
	}
	if review.IsProtocol(e.Type) {
		if err := review.ValidateProtocol(e.Type, e.Text, e.ReplyTo); err != nil {
			return http.StatusBadRequest, err
		}
		// A requester note is a question to answer, not a note in the
		// revision loop: nobody addresses, confirms or reopens it.
		if strings.HasPrefix(e.ReplyTo, review.RequesterPrefix) && e.Type != feedback.TypeReply {
			return http.StatusBadRequest, fmt.Errorf("a requester note can be answered with a reply, not a %s", e.Type)
		}
		// The note being answered has to exist in *this* document's log, or
		// be a requester note addressed to it. Rendering a reply box is not
		// enforcing anything.
		if !s.knows(target, e.ReplyTo) {
			return http.StatusBadRequest, fmt.Errorf("unknown note %q — nothing in this document's log answers to it", e.ReplyTo)
		}
		// The one protocol event that carries fields is a tap on a
		// multiple-choice requester note, and the choice must be one the
		// agent offered.
		if len(e.Fields) > 0 {
			if e.Type != feedback.TypeReply || !strings.HasPrefix(e.ReplyTo, review.RequesterPrefix) {
				return http.StatusBadRequest, fmt.Errorf("a %s carries no fields", e.Type)
			}
			d := s.docOf(target)
			if err := cfg.CheckAnswer(e.ReplyTo, e.Fields, d.Path, target.Rel); err != nil {
				return http.StatusBadRequest, err
			}
		}
		// A reply is deliberately not blocked by readonly/skip: a pattern
		// added after the fact must not strand an open thread. Root
		// vocabulary on those blocks is still refused, below.
		return http.StatusOK, nil
	}
	if err := cfg.Validate(e.Type, e.Text, e.Fields); err != nil {
		return http.StatusBadRequest, err
	}
	if cfg.Locked(e.Block) {
		return http.StatusForbidden, fmt.Errorf("block %s is read-only in this review", e.Block)
	}
	if code, err := s.anchorSub(target, e); err != nil {
		return code, err
	}
	return http.StatusOK, nil
}

// anchorSub rebuilds a sub-anchor from the quote alone, against the server's
// own copy of the block.
//
// The page sends what the reviewer selected; everything else — the surrounding
// context, the offset — is derived here. That is deliberate: the page and the
// server would otherwise each compute an anchor from their own idea of the
// block's text, and the one time they disagreed the note would be written in a
// form the reader could never resolve. Deriving it once, on the side that will
// do the resolving, makes that impossible rather than unlikely.
//
// A quote that is not in the block is refused. An anchor that cannot resolve
// even at the moment it is written is not worth storing.
func (s *Server) anchorSub(target *Entry, e *feedback.Event) (int, error) {
	if e.Sub == nil {
		return http.StatusOK, nil
	}
	if feedback.NormalizeSpace(e.Sub.Quote) == "" {
		// Nothing selected: a note about the whole block, said clumsily.
		e.Sub = nil
		return http.StatusOK, nil
	}
	for _, b := range s.docOf(target).Blocks {
		if b.ID != e.Block {
			continue
		}
		sub := feedback.MakeSub(b.PlainText, e.Sub.Quote)
		if sub == nil {
			return http.StatusBadRequest, fmt.Errorf("that selection is not in block %s any more — the document may have changed since the page loaded", e.Block)
		}
		e.Sub = sub
		return http.StatusOK, nil
	}
	return http.StatusBadRequest, fmt.Errorf("unknown block %s", e.Block)
}

// knows reports whether a note with that id is in the document's log, or is
// one of the requester's notes addressed to this document (issue #90).
func (s *Server) knows(target *Entry, id string) bool {
	if _, ok := s.opts.Review.Requester(id, s.docOf(target).Path, target.Rel); ok {
		return true
	}
	events, err := target.Store.Load()
	if err != nil {
		return false
	}
	for _, e := range events {
		if feedback.NoteID(e) == id {
			return true
		}
	}
	return false
}

// checkVerdicts enforces require_verdict: the review is not done while a
// commentable block has nothing said about it. The error says how many are
// left, so the page can tell the reviewer rather than just refusing.
func (s *Server) checkVerdicts(target *Entry) (int, error) {
	if !s.opts.Review.RequireVerdict {
		return http.StatusOK, nil
	}
	events, err := target.Store.Load()
	if err != nil {
		return http.StatusInternalServerError, err
	}
	answered := make(map[string]bool, len(events))
	for _, ev := range events {
		// Only the review's own vocabulary answers a block. The protocol's
		// events are *about a note* — an agent's reply or its claim to have
		// addressed something would otherwise satisfy a gate that exists to
		// make the reviewer look at every block, and the agent would be
		// answering on their behalf. A confirm costs nothing here either:
		// the note it settles already marked the block answered.
		if !review.IsProtocol(ev.Type) {
			answered[ev.Block] = true
		}
	}
	missing := 0
	// The live parse, not the one from startup: under --watch the document
	// may have gained or lost blocks since, and the gate is about what the
	// reviewer is looking at now.
	for _, b := range s.docOf(target).Blocks {
		if !answered[b.ID] && !s.opts.Review.Locked(b.ID) {
			missing++
		}
	}
	if missing > 0 {
		return http.StatusConflict, fmt.Errorf("this review asks for a verdict on every block: %d still to go", missing)
	}
	return http.StatusOK, nil
}

// handleSessionDone marks the whole set reviewed: one review_done per
// document, so an agent watching any single log sees the handover finish.
func (s *Server) handleSessionDone(w http.ResponseWriter, r *http.Request) {
	written := make([]feedback.Event, 0, len(s.docs))
	for i := range s.docs {
		e := &s.docs[i]
		if code, err := s.checkVerdicts(e); err != nil {
			http.Error(w, fmt.Sprintf("%s: %s", e.Rel, err), code)
			return
		}
	}
	for i := range s.docs {
		e := &s.docs[i]
		event := feedback.Event{Type: feedback.TypeReviewDone, Text: "session"}
		if err := s.append(r, e, &event); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		written = append(written, event)
	}
	writeJSON(w, http.StatusCreated, written)
}

// authorOf is who a request writes as: the resolved identity when there is
// one, which is how the page tells a reviewer's own notes from everyone
// else's, and otherwise the configured author.
func (s *Server) authorOf(r *http.Request) string {
	if s.opts.AuthorOf != nil {
		return s.opts.AuthorOf(r)
	}
	return s.opts.Author
}

// append fills in what the page left out and writes the event to that
// document's log. With AuthorOf set, the author is who the request is,
// whatever the body claimed.
func (s *Server) append(r *http.Request, target *Entry, e *feedback.Event) error {
	e.Doc = s.docOf(target).Path
	if s.opts.AuthorOf != nil {
		e.Author = s.opts.AuthorOf(r)
	} else if e.Author == "" {
		e.Author = s.opts.Author
	}
	if e.Ts == "" {
		e.Ts = time.Now().UTC().Format(time.RFC3339)
	}
	if err := target.Store.Append(*e); err != nil {
		return err
	}
	s.recorded(target, *e)
	return nil
}
