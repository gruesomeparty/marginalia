package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/review"
)

type replyIn struct {
	Doc    string `json:"doc" jsonschema:"the document whose log holds the note, relative to the server's root"`
	NoteID string `json:"note_id" jsonschema:"id of the note to answer, as review_status and feedback_since report it"`
	Text   string `json:"text" jsonschema:"the answer the human will read under their own note"`
	Author string `json:"author,omitempty" jsonschema:"who is answering; defaults to the agent"`
}

type replyOut struct {
	Doc string `json:"doc"`
	// ID is the reply's own id, so an answer can itself be answered — and so a
	// caller can tell its own reply apart from the reviewer's next note.
	ID       string `json:"id"`
	ReplyTo  string `json:"reply_to"`
	Block    string `json:"block"`
	Answered string `json:"answered" jsonschema:"the type of the note that was answered"`
	Ts       string `json:"ts"`
}

type addressedIn struct {
	Doc    string `json:"doc" jsonschema:"the document whose log holds the note, relative to the server's root"`
	NoteID string `json:"note_id" jsonschema:"id of the note you acted on, as review_status and feedback_since report it"`
	Text   string `json:"text" jsonschema:"what you changed, in a sentence the reviewer can check against the block"`
	Author string `json:"author,omitempty" jsonschema:"who acted; defaults to the agent"`
}

// replyToNote appends one answer to a note in a document's log.
//
// It writes through the store rather than the running server on purpose: the
// log is the interface, every read here is disk-backed for the same reason,
// and an agent should be able to answer a question whether or not a page is
// still open. The server re-reads the log on each request, so a reply written
// this way is there the next time the page renders.
func (s *Server) replyToNote(_ context.Context, _ *mcp.CallToolRequest, in replyIn) (*mcp.CallToolResult, replyOut, error) {
	return s.about(in.Doc, in.NoteID, in.Text, in.Author, review.TypeReply)
}

// markAddressed records that the agent changed the document in answer to a
// note. Edit the document first: this records the claim, it does not make it,
// and Marginalia never writes the source document.
func (s *Server) markAddressed(_ context.Context, _ *mcp.CallToolRequest, in addressedIn) (*mcp.CallToolResult, replyOut, error) {
	return s.about(in.Doc, in.NoteID, in.Text, in.Author, review.TypeAddressed)
}

// about appends one event naming an existing note. Both tools land here
// because both are the same operation with a different word, and the rules
// that matter — the id resolves in this document's log, the type is the
// tool's and not the caller's — should not be written twice.
//
// It deliberately does not implement `confirm` or `reopen`: those are the
// reviewer's verdict on the agent's work, and a tool that let the agent
// settle its own note would make the whole loop decorative.
func (s *Server) about(doc, noteID, text, author, typ string) (*mcp.CallToolResult, replyOut, error) {
	in := replyIn{Doc: doc, NoteID: noteID, Text: text, Author: author}
	if err := review.ValidateProtocol(typ, in.Text, in.NoteID); err != nil {
		return nil, replyOut{}, err
	}
	docs, err := s.docsOf("", []string{in.Doc})
	if err != nil {
		return nil, replyOut{}, err
	}
	if len(docs) != 1 {
		return nil, replyOut{}, fmt.Errorf("this answers one note in one document; %q named %d", in.Doc, len(docs))
	}
	path := docs[0]
	store := feedback.NewStore(path)
	events, err := store.Load()
	if err != nil {
		return nil, replyOut{}, err
	}
	var target *feedback.Event
	for i := range events {
		if feedback.NoteID(events[i]) == in.NoteID {
			target = &events[i]
			break
		}
	}
	if target == nil {
		// The same rule the HTTP API enforces: an answer to a note nobody
		// holds is an answer nobody reads.
		return nil, replyOut{}, fmt.Errorf("no note %q in %s — read review_status for the ids", in.NoteID, store.Path())
	}
	if author == "" {
		author = "agent"
	}
	e := feedback.Event{
		// The note's own hash is the hash the block had when the note was
		// written, which is what makes an `addressed` checkable: if the block
		// still hashes to it at render time, nothing changed.
		Doc: path, Block: target.Block, Quote: target.Quote, Hash: target.Hash,
		Type: typ, Text: in.Text, Author: author,
		Ts: time.Now().UTC().Format(time.RFC3339), ReplyTo: in.NoteID,
	}
	if err := store.Append(e); err != nil {
		return nil, replyOut{}, err
	}
	return nil, replyOut{
		Doc: path, ID: feedback.NoteID(e), ReplyTo: in.NoteID,
		Block: target.Block, Answered: target.Type, Ts: e.Ts,
	}, nil
}
