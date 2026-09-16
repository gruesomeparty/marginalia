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

// replyToNote appends one answer to a note in a document's log.
//
// It writes through the store rather than the running server on purpose: the
// log is the interface, every read here is disk-backed for the same reason,
// and an agent should be able to answer a question whether or not a page is
// still open. The server re-reads the log on each request, so a reply written
// this way is there the next time the page renders.
func (s *Server) replyToNote(_ context.Context, _ *mcp.CallToolRequest, in replyIn) (*mcp.CallToolResult, replyOut, error) {
	if err := review.ValidateProtocol(review.TypeReply, in.Text, in.NoteID); err != nil {
		return nil, replyOut{}, err
	}
	docs, err := s.docsOf("", []string{in.Doc})
	if err != nil {
		return nil, replyOut{}, err
	}
	if len(docs) != 1 {
		return nil, replyOut{}, fmt.Errorf("reply_to_note answers one note in one document; %q named %d", in.Doc, len(docs))
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
	author := in.Author
	if author == "" {
		author = "agent"
	}
	e := feedback.Event{
		Doc: path, Block: target.Block, Quote: target.Quote, Hash: target.Hash,
		Type: review.TypeReply, Text: in.Text, Author: author,
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
