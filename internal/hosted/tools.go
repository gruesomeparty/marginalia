package hosted

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gruesomeparty/marginalia/internal/document"
	"github.com/gruesomeparty/marginalia/internal/mcpserver"
	"github.com/gruesomeparty/marginalia/internal/review"
	"github.com/gruesomeparty/marginalia/internal/server"
	"github.com/gruesomeparty/marginalia/internal/session"
)

// The hosted tools are the stdio tools with one substitution: a review id
// and a document name where a path was. review_document is not registered —
// a path means nothing to an agent on another machine — and publish_document
// takes its place, carrying the bytes. Every read goes through the same
// mcpserver helpers the stdio tools use, over a Target built from the
// review's current revision and the log under its key, so the two modes
// cannot drift in what they report.
//
// As on stdio, two tools write and each writes one fixed type. An agent may
// answer and claim, never decide: there is no tool that appends a comment, a
// verdict, a confirm, a reopen or a review_done.

type publishDoc struct {
	Name    string `json:"name" jsonschema:"file name; the extension picks the format (plan.md, api.proto, flow.mmd, config.yaml)"`
	Content string `json:"content" jsonschema:"the document's text"`
}

type publishIn struct {
	Documents []publishDoc `json:"documents" jsonschema:"one or more documents to review together"`
	Title     string       `json:"title,omitempty" jsonschema:"what the review is called in the inbox"`
	Review    string       `json:"review,omitempty" jsonschema:"shipped framing to use: adr, copy, schema, security"`
	Config    string       `json:"config,omitempty" jsonschema:"review config YAML, layered over the framing (same schema as --config)"`
	Theme     string       `json:"theme,omitempty" jsonschema:"page palette"`
	ReviewID  string       `json:"review_id,omitempty" jsonschema:"an existing review to publish a new revision of; omit for a new review"`
	Callback  string       `json:"callback_url,omitempty" jsonschema:"https URL to POST a signed, content-free notice to when the reviewer submits Done or asks a question, so a job can stop instead of waiting; the host must allow its domain. Verify X-Marginalia-Signature (sha256=HMAC-SHA256 of the body under callback_secret). A revision without it keeps the review's"`
}

type publishedDoc struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Blocks int    `json:"blocks"`
}

type publishOut struct {
	ReviewID       string          `json:"review_id"`
	Revision       int             `json:"revision"`
	URL            string          `json:"url"`
	Docs           []publishedDoc  `json:"docs"`
	Actions        []review.Action `json:"actions"`
	Instructions   string          `json:"instructions,omitempty"`
	RequireVerdict bool            `json:"require_verdict"`
	// CallbackSecret is the key the callbacks are signed with, returned
	// when the publish named a callback_url. Keep it; it is not shown
	// anywhere else.
	CallbackSecret string `json:"callback_secret,omitempty"`
}

type readIn struct {
	ReviewID string         `json:"review_id" jsonschema:"the review, from publish_document"`
	Name     string         `json:"name,omitempty" jsonschema:"one document of the review; omit for all of them"`
	Cursor   map[string]int `json:"cursor,omitempty" jsonschema:"events already consumed per document name, from a previous cursor"`
}

type feedbackOut struct {
	Docs []mcpserver.DocEvents `json:"docs"`
}

type statusOut struct {
	Docs []mcpserver.DocStatus `json:"docs"`
}

type awaitIn struct {
	ReviewID       string         `json:"review_id"`
	Name           string         `json:"name,omitempty"`
	Cursor         map[string]int `json:"cursor,omitempty"`
	TimeoutSeconds int            `json:"timeout_seconds,omitempty" jsonschema:"how long to wait; default 45, capped at 1800. Keep it under your client's tool timeout (often 60s) and call again with the returned cursor"`
}

type awaitOut struct {
	Done   bool                  `json:"done"`
	Reason string                `json:"reason" jsonschema:"review_done, question (the reviewer asked something: answer it with reply_to_note, then wait again from the returned cursor), timeout or cancelled"`
	Docs   []mcpserver.DocEvents `json:"docs"`
}

type replyIn struct {
	ReviewID string `json:"review_id"`
	Name     string `json:"name" jsonschema:"the document whose log holds the note"`
	NoteID   string `json:"note_id" jsonschema:"id of the note, as review_status and feedback_since report it"`
	Text     string `json:"text"`
}

type closeIn struct {
	ReviewID string `json:"review_id"`
}

type closeOut struct {
	ReviewID string                `json:"review_id"`
	URL      string                `json:"url"`
	Docs     []mcpserver.DocStatus `json:"docs"`
}

// tools registers the hosted tool set on the host's MCP server.
func (h *Host) tools() {
	srv := h.mcp
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "publish_document",
		Description: "Publish one or more documents for block-anchored human review and get back the URL to give the human, plus the vocabulary you will read back. Send the content, not a path: this instance shares no filesystem with you. Pass review_id to publish a new revision of an existing review — earlier revisions are kept, and the reviewer's notes re-anchor against the new text (stale where a block changed, orphaned where it is gone). A revision replaces the whole document set, so send every document again. Pick a framing with `review` (" + session.PresetList() + ").",
	}, h.publishDocument)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "feedback_since",
		Description: "Read feedback events appended since a cursor. Pass back the cursor from the previous call to get only what is new.",
	}, h.feedbackSince)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "review_status",
		Description: "The feedback log materialized against the current revision: per-block state, which notes went stale, which blocks are gone, and which suggested edits are safe to apply verbatim.",
	}, h.reviewStatus)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "reply_to_note",
		Description: "Answer one of the human's notes in place, by its id. The answer shows up under their own note the next time the page renders.",
	}, h.replyToNote)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "mark_addressed",
		Description: "Record that you changed the document in answer to a note. Publish the revision first — this records the claim, it never writes the document. You cannot settle your own note: confirming or reopening is the reviewer's.",
	}, h.markAddressed)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "await_review_done",
		Description: "Wait until the human marks every document of the review done, or asks a question (reason \"question\": answer it with reply_to_note while they are still on the page), then return everything new since the cursor. Waits are short by default (45s), under common client tool timeouts such as claude.ai's 60s: call it in a loop, passing each docs[].cursor back as cursor, until done. A timeout is not an error: it reports done=false with the new cursor.",
	}, h.awaitReviewDone)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "close_review",
		Description: "Return a review's final status. A hosted review stays at its URL — there is no server to stop — and comments already saved stay readable.",
	}, h.closeReview)
}

func (h *Host) publishDocument(ctx context.Context, req *mcp.CallToolRequest, in publishIn) (*mcp.CallToolResult, publishOut, error) {
	p := Publish{Title: in.Title, Agent: h.agentOf(req), Review: in.Review, Config: in.Config, Theme: in.Theme, Callback: in.Callback}
	for _, d := range in.Documents {
		p.Docs = append(p.Docs, Document{Name: d.Name, Content: []byte(d.Content)})
	}
	var (
		r   *Review
		err error
	)
	if in.ReviewID == "" {
		r, err = h.Publish(ctx, p)
	} else {
		r, err = h.Republish(ctx, in.ReviewID, p)
	}
	if err != nil {
		return nil, publishOut{}, err
	}
	out := h.published(r)
	if in.Callback != "" {
		out.CallbackSecret = h.opts.Callbacks.ReviewSecret(r.Meta.ID)
	}
	return nil, out, nil
}

func (h *Host) published(r *Review) publishOut {
	cfg := r.Server.Review()
	out := publishOut{
		ReviewID: r.Meta.ID, Revision: r.Meta.Revision, URL: h.URL(r.Meta.ID),
		Actions: cfg.Actions(), Instructions: cfg.Instructions, RequireVerdict: cfg.RequireVerdict,
		Docs: []publishedDoc{},
	}
	for _, e := range r.Server.Entries() {
		out.Docs = append(out.Docs, publishedDoc{Name: e.Rel, URL: h.URL(r.Meta.ID) + "d/" + e.Rel, Blocks: len(e.Doc.Blocks)})
	}
	return out
}

// targets resolves a review id and an optional document name into the
// targets the shared reads work on: the entry's current parse and its log.
func (h *Host) targets(reviewID, name string) (*Review, []mcpserver.Target, error) {
	r := h.Review(reviewID)
	if r == nil {
		return nil, nil, fmt.Errorf("no review %q here — the id comes from publish_document", reviewID)
	}
	var out []mcpserver.Target
	for _, e := range r.Server.Entries() {
		if name != "" && e.Rel != name {
			continue
		}
		out = append(out, h.target(r, e))
	}
	if len(out) == 0 {
		return nil, nil, fmt.Errorf("review %s has no document %q — it has: %s", reviewID, name, strings.Join(r.Meta.Docs, ", "))
	}
	return r, out, nil
}

func (h *Host) target(r *Review, e server.Entry) mcpserver.Target {
	id := r.Meta.ID
	return mcpserver.Target{
		Name: e.Rel,
		// The parse of whatever revision is current when asked, not the one
		// current when the target was made: a republish between two reads
		// must be seen by the second.
		Parse: func() (*document.Document, error) {
			cur := h.Review(id)
			if cur == nil {
				return nil, fmt.Errorf("review %s is gone", id)
			}
			for _, ce := range cur.Server.Entries() {
				if ce.Rel == e.Rel {
					return ce.Doc, nil
				}
			}
			return nil, fmt.Errorf("review %s no longer has %s", id, e.Rel)
		},
		Store:      e.Store,
		Completion: r.Server.Review().Completion(),
	}
}

func (h *Host) feedbackSince(_ context.Context, _ *mcp.CallToolRequest, in readIn) (*mcp.CallToolResult, feedbackOut, error) {
	_, targets, err := h.targets(in.ReviewID, in.Name)
	if err != nil {
		return nil, feedbackOut{}, err
	}
	out := feedbackOut{Docs: []mcpserver.DocEvents{}}
	for _, t := range targets {
		ev, err := mcpserver.EventsSince(t, in.Cursor[t.Name])
		if err != nil {
			return nil, feedbackOut{}, err
		}
		out.Docs = append(out.Docs, ev)
	}
	return nil, out, nil
}

func (h *Host) reviewStatus(_ context.Context, _ *mcp.CallToolRequest, in readIn) (*mcp.CallToolResult, statusOut, error) {
	_, targets, err := h.targets(in.ReviewID, in.Name)
	if err != nil {
		return nil, statusOut{}, err
	}
	docs, err := statuses(targets)
	return nil, statusOut{Docs: docs}, err
}

func statuses(targets []mcpserver.Target) ([]mcpserver.DocStatus, error) {
	out := make([]mcpserver.DocStatus, 0, len(targets))
	for _, t := range targets {
		st, err := mcpserver.StatusOf(t)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

func (h *Host) replyToNote(_ context.Context, req *mcp.CallToolRequest, in replyIn) (*mcp.CallToolResult, mcpserver.ReplyOut, error) {
	return h.about(req, in, review.TypeReply)
}

func (h *Host) markAddressed(_ context.Context, req *mcp.CallToolRequest, in replyIn) (*mcp.CallToolResult, mcpserver.ReplyOut, error) {
	return h.about(req, in, review.TypeAddressed)
}

func (h *Host) about(req *mcp.CallToolRequest, in replyIn, typ string) (*mcp.CallToolResult, mcpserver.ReplyOut, error) {
	if in.Name == "" {
		return nil, mcpserver.ReplyOut{}, errors.New("name the document whose note you are answering")
	}
	r, targets, err := h.targets(in.ReviewID, in.Name)
	if err != nil {
		return nil, mcpserver.ReplyOut{}, err
	}
	out, err := mcpserver.Answer(targets[0], in.NoteID, in.Text, h.agentOf(req), typ)
	if err != nil {
		return nil, mcpserver.ReplyOut{}, err
	}
	// An answer or a claim after the human pressed Done is the loop coming
	// back to them, which they would otherwise only see by looking.
	if isDone(r) {
		h.notify(NotifyReplied, r)
	}
	return nil, out, nil
}

func (h *Host) awaitReviewDone(ctx context.Context, _ *mcp.CallToolRequest, in awaitIn) (*mcp.CallToolResult, awaitOut, error) {
	_, targets, err := h.targets(in.ReviewID, in.Name)
	if err != nil {
		return nil, awaitOut{}, err
	}
	ctx, cancel := h.untilClosed(ctx)
	defer cancel()
	done, reason, events, err := mcpserver.WaitForDone(ctx, targets, in.Cursor, time.Duration(in.TimeoutSeconds)*time.Second)
	if err != nil {
		return nil, awaitOut{}, err
	}
	return nil, awaitOut{Done: done, Reason: reason, Docs: events}, nil
}

func (h *Host) closeReview(_ context.Context, _ *mcp.CallToolRequest, in closeIn) (*mcp.CallToolResult, closeOut, error) {
	r, targets, err := h.targets(in.ReviewID, "")
	if err != nil {
		return nil, closeOut{}, err
	}
	docs, err := statuses(targets)
	if err != nil {
		return nil, closeOut{}, err
	}
	return nil, closeOut{ReviewID: r.Meta.ID, URL: h.URL(r.Meta.ID), Docs: docs}, nil
}
