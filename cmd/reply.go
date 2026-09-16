package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/gruesomeparty/marginalia/internal/document"
	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/review"
)

// thread is one note and what has been said back, as `reply` reports it: the
// id an answer names it by, what it said, and whether it is still open.
type thread struct {
	ID      string   `json:"id"`
	Block   string   `json:"block"`
	Type    string   `json:"type"`
	Text    string   `json:"text"`
	Author  string   `json:"author"`
	Ts      string   `json:"ts"`
	Stale   bool     `json:"stale"`
	Replies []string `json:"replies"`
}

func newReplyCmd() *cobra.Command {
	var to, text, author string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "reply <doc>",
		Short: "Answer a reviewer's note in the document's feedback log",
		Long: `Answer a note without opening the page.

With no --to, list the document's notes and the ids an answer names them by;
questions nobody has answered come first. With --to, append the answer.

A reply is append-only like every other event: it points at the note it
answers and changes nothing about it. The reviewer sees it under their own
note the next time the page renders.`,
		Args: requirePaths("reply"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("reply takes one document — e.g. `marginalia reply spec.md --to <id> --text \"...\"`")
			}
			if to == "" {
				return listThreads(cmd.OutOrStdout(), args[0], asJSON)
			}
			if author == "" {
				author = defaultAuthor()
			}
			return appendReply(cmd.OutOrStdout(), args[0], to, text, author)
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "id of the note to answer (see `marginalia reply <doc>`)")
	cmd.Flags().StringVar(&text, "text", "", "the answer")
	cmd.Flags().StringVar(&author, "author", "", "who is answering (defaults to $USER)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit machine-readable JSON")
	return cmd
}

// threadsOf materializes the log against the document as it now reads, so the
// listing says which notes the document has outrun as well as which are open.
func threadsOf(doc string) ([]thread, error) {
	parsed, err := document.Parse(doc)
	if err != nil {
		return nil, err
	}
	events, err := feedback.NewStore(doc).Load()
	if err != nil {
		return nil, err
	}
	hashes := make(map[string]string, len(parsed.Blocks))
	order := make([]string, 0, len(parsed.Blocks))
	for _, b := range parsed.Blocks {
		hashes[b.ID] = b.Hash
		order = append(order, b.ID)
	}
	res := feedback.Materialize(events, hashes, order)
	var open, answered []thread
	for _, st := range res.States {
		for _, n := range st.History {
			t := thread{
				ID: n.ID, Block: st.Block, Type: n.Event.Type, Text: n.Event.Text,
				Author: n.Event.Author, Ts: n.Event.Ts, Stale: n.Stale,
				Replies: make([]string, 0, len(n.Replies)),
			}
			for _, r := range n.Replies {
				t.Replies = append(t.Replies, r.Event.Text)
			}
			// An unanswered question is the reason this command exists, so it
			// is what the listing leads with.
			if n.Event.Type == feedback.TypeQuestion && len(n.Replies) == 0 {
				open = append(open, t)
				continue
			}
			answered = append(answered, t)
		}
	}
	return append(open, answered...), nil
}

func listThreads(w io.Writer, doc string, asJSON bool) error {
	threads, err := threadsOf(doc)
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if threads == nil {
			threads = []thread{}
		}
		return enc.Encode(threads)
	}
	if len(threads) == 0 {
		_, err := fmt.Fprintf(w, "%s: nothing said yet\n", doc)
		return err
	}
	var b strings.Builder
	for _, t := range threads {
		fmt.Fprintf(&b, "%s  %s  %s", t.ID, t.Block, t.Type)
		if t.Stale {
			b.WriteString("  (stale — block edited since)")
		}
		b.WriteString("\n")
		if t.Text != "" {
			fmt.Fprintf(&b, "    %s\n", t.Text)
		}
		for _, r := range t.Replies {
			fmt.Fprintf(&b, "    ↳ %s\n", r)
		}
	}
	_, err = io.WriteString(w, b.String())
	return err
}

// appendReply writes one answer. The id has to resolve in this document's own
// log — the same rule the server enforces, for the same reason: an answer to
// a note nobody holds is an answer nobody reads.
func appendReply(w io.Writer, doc, to, text, author string) error {
	if err := review.ValidateProtocol(review.TypeReply, text, to); err != nil {
		return err
	}
	store := feedback.NewStore(doc)
	events, err := store.Load()
	if err != nil {
		return err
	}
	var target *feedback.Event
	for i := range events {
		if feedback.NoteID(events[i]) == to {
			target = &events[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("no note %q in %s — run `marginalia reply %s` to see the ids", to, store.Path(), doc)
	}
	e := feedback.Event{
		Doc: doc, Block: target.Block, Quote: target.Quote, Hash: target.Hash,
		Type: review.TypeReply, Text: text, Author: author,
		Ts: time.Now().UTC().Format(time.RFC3339), ReplyTo: to,
	}
	if err := store.Append(e); err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "marginalia: replied to %s (%s on %s) → %s\n", to, target.Type, target.Block, store.Path())
	return err
}
