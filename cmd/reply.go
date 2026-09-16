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
	ID     string `json:"id"`
	Block  string `json:"block"`
	Type   string `json:"type"`
	Text   string `json:"text"`
	Author string `json:"author"`
	Ts     string `json:"ts"`
	Stale  bool   `json:"stale"`
	// Status is where the note stands in the revision loop, and Unclaimed
	// flags an `addressed` the document does not bear out.
	Status    string `json:"status"`
	Unclaimed bool   `json:"unclaimed,omitempty"`
	// Sub is the sentence the note is about, when it is about less than the
	// whole block. An agent should quote this back rather than the block.
	Sub      string   `json:"sub,omitempty"`
	Replies  []string `json:"replies"`
	Progress []string `json:"progress,omitempty"`
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
	blocks := make([]feedback.Block, 0, len(parsed.Blocks))
	for _, b := range parsed.Blocks {
		blocks = append(blocks, feedback.Block{ID: b.ID, Hash: b.Hash, Text: b.PlainText})
	}
	res := feedback.Materialize(events, blocks)
	var open, answered []thread
	for _, st := range res.States {
		for _, n := range st.History {
			t := thread{
				ID: n.ID, Block: st.Block, Type: n.Event.Type, Text: n.Event.Text,
				Author: n.Event.Author, Ts: n.Event.Ts, Stale: n.Stale,
				Status: n.Status, Unclaimed: n.Unclaimed, Sub: subQuote(n),
				Replies: make([]string, 0, len(n.Replies)),
			}
			for _, r := range n.Replies {
				t.Replies = append(t.Replies, r.Event.Text)
			}
			for _, pr := range n.Progress {
				t.Progress = append(t.Progress, pr.Event.Type+": "+pr.Event.Text)
			}
			// What still wants doing comes first: an unanswered question, or
			// any note nobody has claimed to act on. That ordering is the
			// point of the listing — it is the agent's work queue.
			if n.Status == feedback.StatusOutstanding || n.Status == feedback.StatusReopened {
				if n.Event.Type != feedback.TypeApprove {
					open = append(open, t)
					continue
				}
			}
			answered = append(answered, t)
		}
	}
	return append(open, answered...), nil
}

// subQuote is the sentence a note is about, or empty for a whole-block note.
func subQuote(n feedback.Note) string {
	if n.Event.Sub == nil {
		return ""
	}
	return n.Event.Sub.Quote
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
		fmt.Fprintf(&b, "%s  %s  %s  [%s]", t.ID, t.Block, t.Type, t.Status)
		if t.Sub != "" {
			// Which sentence, so the agent acts on that one rather than the
			// paragraph around it.
			fmt.Fprintf(&b, "  on \u201c%s\u201d", t.Sub)
		}
		if t.Stale {
			// Same rule as the page: name the thing that moved. For a note
			// about one sentence, "the block was edited" is true of almost
			// any edit and says nothing.
			if t.Sub != "" {
				b.WriteString("  (stale — this sentence was edited)")
			} else {
				b.WriteString("  (stale — block edited since)")
			}
		}
		b.WriteString("\n")
		if t.Text != "" {
			fmt.Fprintf(&b, "    %s\n", t.Text)
		}
		for _, r := range t.Replies {
			fmt.Fprintf(&b, "    ↳ %s\n", r)
		}
		for _, pr := range t.Progress {
			fmt.Fprintf(&b, "    · %s\n", pr)
		}
		if t.Unclaimed {
			what := "block"
			if t.Sub != "" {
				what = "sentence"
			}
			fmt.Fprintf(&b, "    ⚠ marked addressed, but the %s has not changed since\n", what)
		}
	}
	_, err = io.WriteString(w, b.String())
	return err
}

// appendReply writes one answer.
func appendReply(w io.Writer, doc, to, text, author string) error {
	return appendProgress(w, doc, to, text, author, review.TypeReply)
}

// appendProgress writes one event about an existing note — an answer, or a
// step in the revision loop. The id has to resolve in this document's own log
// — the same rule the server enforces, for the same reason: an event about a
// note nobody holds is an event nobody reads.
//
// The hash it records is the note's own, which is the hash the block had when
// the note was written. That is what makes an `addressed` checkable: if the
// block still hashes to it, nothing changed and the claim is not borne out.
// Taking it from the note rather than asking the caller for it means the
// check costs the agent nothing and cannot be fudged by forgetting.
func appendProgress(w io.Writer, doc, to, text, author, typ string) error {
	if err := review.ValidateProtocol(typ, text, to); err != nil {
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
		Type: typ, Text: text, Author: author,
		Ts: time.Now().UTC().Format(time.RFC3339), ReplyTo: to,
	}
	if err := store.Append(e); err != nil {
		return err
	}
	verb := map[string]string{
		review.TypeReply:     "replied to",
		review.TypeAddressed: "marked addressed",
		review.TypeConfirm:   "settled",
		review.TypeReopen:    "reopened",
	}[typ]
	_, err = fmt.Fprintf(w, "marginalia: %s %s (%s on %s) → %s\n", verb, to, target.Type, target.Block, store.Path())
	return err
}

func newAddressedCmd() *cobra.Command {
	var to, text, author string
	cmd := &cobra.Command{
		Use:   "addressed <doc>",
		Short: "Record that you acted on a note, so the reviewer can check it",
		Long: `Record that you changed the document in answer to a note.

Edit the document first, then say which note you were answering and what you
did. The reviewer's next look shows that block as "addressed — is this right?"
with their original note beside what it now says, so a second round is a short
queue instead of a full re-read.

The claim is checkable: the event records the hash the block had when the note
was written, and a re-render that finds the block unchanged says so. Marginalia
still never edits your document — you make the change, this records it.`,
		Args: requirePaths("addressed"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("addressed takes one document — e.g. `marginalia addressed spec.md --to <id> --text \"raised the cap\"`")
			}
			if author == "" {
				author = defaultAuthor()
			}
			return appendProgress(cmd.OutOrStdout(), args[0], to, text, author, review.TypeAddressed)
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "id of the note you acted on (see `marginalia reply <doc>`)")
	cmd.Flags().StringVar(&text, "text", "", "what you changed")
	cmd.Flags().StringVar(&author, "author", "", "who acted (defaults to $USER)")
	return cmd
}
