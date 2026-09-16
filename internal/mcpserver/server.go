package mcpserver

import (
	"context"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gruesomeparty/marginalia/internal/review"
	"github.com/gruesomeparty/marginalia/internal/session"
)

// Options configures the MCP server itself, as distinct from any one review.
// Everything here is an operator's decision made at launch, never a tool
// input: a model asked to "make it reachable from my phone" would otherwise
// happily put an unauthenticated review server on the LAN.
type Options struct {
	Root     string    // confinement root; "" means MARGINALIA_MCP_ROOT or the working directory
	Host     string    // bind address for review servers; "" means 127.0.0.1
	Diagrams string    // "auto" or "off", as for serve
	MMDC     string    // explicit mermaid-cli path
	Log      io.Writer // where review servers write their notices — never stdout under stdio
}

// Server holds the reviews this process is serving.
type Server struct {
	opts     Options
	root     string
	mu       sync.Mutex
	sessions map[string]*live
}

// New resolves the confinement root and returns a server with no reviews yet.
func New(opts Options) (*Server, error) {
	if opts.Host == "" {
		opts.Host = "127.0.0.1"
	}
	root, err := resolveRoot(opts.Root)
	if err != nil {
		return nil, err
	}
	return &Server{opts: opts, root: root, sessions: map[string]*live{}}, nil
}

// Root is the directory paths are confined to.
func (s *Server) Root() string { return s.root }

// --- tool inputs and outputs ---

type reviewDocumentIn struct {
	Paths  []string `json:"paths" jsonschema:"documents or directories to review, relative to the server's root"`
	Review string   `json:"review,omitempty" jsonschema:"shipped framing to use: adr, copy, schema, security"`
	Config string   `json:"config,omitempty" jsonschema:"path to a review config file, layered over the framing"`
	Theme  string   `json:"theme,omitempty" jsonschema:"page palette"`
	Watch  bool     `json:"watch,omitempty" jsonschema:"re-parse a document when its file changes"`
	Author string   `json:"author,omitempty" jsonschema:"name recorded on the human's events"`
}

type docInfo struct {
	Doc    string `json:"doc"`
	Rel    string `json:"rel"`
	URL    string `json:"url"`
	Log    string `json:"feedback_log"`
	Blocks int    `json:"blocks"`
}

type reviewDocumentOut struct {
	SessionID      string          `json:"session_id"`
	URL            string          `json:"url"`
	Docs           []docInfo       `json:"docs"`
	Actions        []review.Action `json:"actions"`
	Instructions   string          `json:"instructions,omitempty"`
	RequireVerdict bool            `json:"require_verdict"`
	ServeCommand   string          `json:"serve_command"`
}

type readIn struct {
	SessionID string         `json:"session_id,omitempty" jsonschema:"a session from review_document; optional, paths work just as well"`
	Paths     []string       `json:"paths,omitempty" jsonschema:"documents to read, when no session is named"`
	Cursor    map[string]int `json:"cursor,omitempty" jsonschema:"events already consumed per document, from a previous cursor"`
}

type feedbackOut struct {
	Docs []DocEvents `json:"docs"`
}

type statusIn struct {
	SessionID string   `json:"session_id,omitempty"`
	Paths     []string `json:"paths,omitempty"`
}

type statusOut struct {
	Docs []DocStatus `json:"docs"`
}

type awaitIn struct {
	SessionID      string         `json:"session_id,omitempty"`
	Paths          []string       `json:"paths,omitempty"`
	Cursor         map[string]int `json:"cursor,omitempty"`
	TimeoutSeconds int            `json:"timeout_seconds,omitempty" jsonschema:"how long to wait; default 300, capped at 1800"`
}

type awaitOut struct {
	Done   bool        `json:"done"`
	Reason string      `json:"reason" jsonschema:"review_done, timeout or cancelled"`
	Docs   []DocEvents `json:"docs"`
}

type closeIn struct {
	SessionID string `json:"session_id"`
}

type closeOut struct {
	Stopped bool        `json:"stopped"`
	Docs    []DocStatus `json:"docs"`
}

// MCP registers the tools and returns the server to run on a transport.
func (s *Server) MCP() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "marginalia",
		Title:   "Marginalia document review",
		Version: session.Version,
	}, nil)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "review_document",
		Description: "Serve one or more documents for block-anchored human review and return the URL to give the human. Pick a framing with `review` (" + session.PresetList() + ") so the words the human answers in are the words you will read back.",
	}, s.reviewDocument)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "feedback_since",
		Description: "Read feedback events appended since a cursor. Pass back the cursor from the previous call to get only what is new. Works whether or not a review server is running.",
	}, s.feedbackSince)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "review_status",
		Description: "The feedback log materialized against the document as it now reads: per-block state, which notes went stale, which blocks are gone, and which suggested edits are safe to apply verbatim.",
	}, s.reviewStatus)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "reply_to_note",
		Description: "Answer one of the human's notes in place, by its id. A question you answer this way shows up under their own note the next time the page renders — the alternative is revising the document and hoping they notice.",
	}, s.replyToNote)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "await_review_done",
		Description: "Block until the human marks every named document done, then return everything new. A timeout is not an error: it reports done=false so you can tell the human they still have it.",
	}, s.awaitReviewDone)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "close_review",
		Description: "Stop serving a review and return its final status. Comments already saved are on disk and stay readable.",
	}, s.closeReview)

	return srv
}

func (s *Server) reviewDocument(_ context.Context, _ *mcp.CallToolRequest, in reviewDocumentIn) (*mcp.CallToolResult, reviewDocumentOut, error) {
	paths, err := s.confine(in.Paths)
	if err != nil {
		return nil, reviewDocumentOut{}, err
	}
	opts := session.Options{
		Watch: in.Watch, Author: in.Author, Config: in.Config, Review: in.Review,
		Theme: in.Theme, Diagrams: s.opts.Diagrams, MMDC: s.opts.MMDC,
	}
	l, srv, err := s.start(paths, opts)
	if err != nil {
		return nil, reviewDocumentOut{}, err
	}
	cfg := srv.Review()
	out := reviewDocumentOut{
		SessionID:      l.id,
		URL:            l.url,
		Actions:        cfg.Actions(),
		Instructions:   cfg.Instructions,
		RequireVerdict: cfg.RequireVerdict,
		ServeCommand:   serveCommand(in),
	}
	for _, e := range srv.Entries() {
		out.Docs = append(out.Docs, docInfo{
			Doc: e.Doc.Path, Rel: e.Rel, URL: l.url + "/d/" + e.Rel,
			Log: e.Store.Path(), Blocks: len(e.Doc.Blocks),
		})
		l.docs = append(l.docs, e.Doc.Path)
	}
	s.remember(l)
	return nil, out, nil
}

func (s *Server) feedbackSince(_ context.Context, _ *mcp.CallToolRequest, in readIn) (*mcp.CallToolResult, feedbackOut, error) {
	docs, err := s.docsOf(in.SessionID, in.Paths)
	if err != nil {
		return nil, feedbackOut{}, err
	}
	out, _, err := readAll(docs, s.normalizeCursor(in.Cursor))
	if err != nil {
		return nil, feedbackOut{}, err
	}
	return nil, feedbackOut{Docs: out}, nil
}

func (s *Server) reviewStatus(_ context.Context, _ *mcp.CallToolRequest, in statusIn) (*mcp.CallToolResult, statusOut, error) {
	docs, err := s.docsOf(in.SessionID, in.Paths)
	if err != nil {
		return nil, statusOut{}, err
	}
	out, err := statuses(docs)
	return nil, statusOut{Docs: out}, err
}

func (s *Server) awaitReviewDone(ctx context.Context, _ *mcp.CallToolRequest, in awaitIn) (*mcp.CallToolResult, awaitOut, error) {
	docs, err := s.docsOf(in.SessionID, in.Paths)
	if err != nil {
		return nil, awaitOut{}, err
	}
	done, reason, events, err := waitForDone(ctx, docs, s.normalizeCursor(in.Cursor), time.Duration(in.TimeoutSeconds)*time.Second)
	if err != nil {
		return nil, awaitOut{}, err
	}
	return nil, awaitOut{Done: done, Reason: reason, Docs: events}, nil
}

func (s *Server) closeReview(_ context.Context, _ *mcp.CallToolRequest, in closeIn) (*mcp.CallToolResult, closeOut, error) {
	l := s.lookup(in.SessionID)
	if l == nil {
		return nil, closeOut{}, session.Advertise(errUnknownSession(in.SessionID))
	}
	docs := l.docs
	if err := s.stop(in.SessionID); err != nil {
		return nil, closeOut{}, err
	}
	out, err := statuses(docs)
	return nil, closeOut{Stopped: true, Docs: out}, err
}

func statuses(docs []string) ([]DocStatus, error) {
	out := make([]DocStatus, 0, len(docs))
	for _, d := range docs {
		st, err := statusOf(d)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

// serveCommand is the CLI equivalent of this call. An MCP session's reviews
// stop when the process does, so the human gets a way to bring their page
// back that does not depend on the agent still being there.
func serveCommand(in reviewDocumentIn) string {
	parts := []string{"marginalia", "serve"}
	parts = append(parts, in.Paths...)
	if in.Review != "" {
		parts = append(parts, "--review", in.Review)
	}
	if in.Config != "" {
		parts = append(parts, "--config", in.Config)
	}
	if in.Theme != "" {
		parts = append(parts, "--theme", in.Theme)
	}
	if in.Watch {
		parts = append(parts, "--watch")
	}
	return strings.Join(parts, " ")
}
