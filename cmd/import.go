package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gruesomeparty/marginalia/internal/feedback"
)

// sharedReview is what the share-mode page exports: the document it was
// reviewing and the events the reviewer wrote. A bare array of events is
// accepted too — someone will hand-edit one, and refusing it would be
// pedantry.
type sharedReview struct {
	Doc    string           `json:"doc"`
	Events []feedback.Event `json:"events"`
}

// readShared parses an exported review, in either shape.
func readShared(path string) (*sharedReview, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var wrapped sharedReview
	if err := json.Unmarshal(data, &wrapped); err == nil && wrapped.Events != nil {
		return &wrapped, nil
	}
	var bare []feedback.Event
	if err := json.Unmarshal(data, &bare); err != nil {
		return nil, fmt.Errorf("%s: not a Marginalia review export: %w", path, err)
	}
	return &sharedReview{Events: bare}, nil
}

// importReview merges an exported review into the document's own log. The log
// stays append-only: nothing is rewritten, and an event that is already there
// is skipped rather than duplicated, so importing the same file twice is
// safe — which matters, because someone will.
func importReview(path, doc string) (added, skipped int, target string, err error) {
	shared, err := readShared(path)
	if err != nil {
		return 0, 0, "", err
	}
	if doc == "" {
		doc = shared.Doc
	}
	if doc == "" {
		return 0, 0, "", fmt.Errorf("%s does not say which document it reviews — pass --doc <doc>", path)
	}
	if _, err := os.Stat(doc); err != nil {
		return 0, 0, "", fmt.Errorf("%s: %w (pass --doc if the document moved)", doc, err)
	}
	for i, e := range shared.Events {
		if e.Type == "" {
			return 0, 0, "", fmt.Errorf("%s: event %d has no type", path, i+1)
		}
		if e.Block == "" && e.Type != feedback.TypeReviewDone {
			return 0, 0, "", fmt.Errorf("%s: event %d (%s) has no block to anchor to", path, i+1, e.Type)
		}
	}
	store := feedback.NewStore(doc)
	existing, err := store.Load()
	if err != nil {
		return 0, 0, "", err
	}
	if err := checkReplies(path, shared.Events, existing, store.Path()); err != nil {
		return 0, 0, "", err
	}
	seen := make(map[string]bool, len(existing))
	for _, e := range existing {
		seen[identity(e)] = true
	}
	for _, e := range shared.Events {
		e.Doc = doc
		if seen[identity(e)] {
			skipped++
			continue
		}
		if err := store.Append(e); err != nil {
			return added, skipped, store.Path(), err
		}
		seen[identity(e)] = true
		added++
	}
	return added, skipped, store.Path(), nil
}

// identity is what makes two events the same event. Deliberately not the
// whole struct: the same note imported twice differs only in the document
// path it was exported under. It is the note's own id plus what it answers —
// two replies can otherwise agree on every other word and still be answers to
// different notes.
func identity(e feedback.Event) string {
	return feedback.NoteID(e) + "\x00" + e.ReplyTo
}

// checkReplies refuses an import carrying an answer to a note nobody here
// holds. Materialize would surface it as dangling rather than lose it, but a
// log that accrues answers to notes it does not have is a log that reads
// worse every merge — and the import is the last place the mismatch can still
// be reported against a file the sender still has.
func checkReplies(path string, incoming, existing []feedback.Event, logPath string) error {
	var known map[string]bool
	for i, e := range incoming {
		if e.ReplyTo == "" {
			continue
		}
		if known == nil {
			known = make(map[string]bool, len(existing)+len(incoming))
			for _, k := range append(append([]feedback.Event{}, existing...), incoming...) {
				known[feedback.NoteID(k)] = true
			}
		}
		if !known[e.ReplyTo] {
			return fmt.Errorf("%s: event %d replies to note %s, which is in neither this file nor %s — import the review it answers first",
				path, i+1, e.ReplyTo, logPath)
		}
	}
	return nil
}

func newImportCmd() *cobra.Command {
	var doc string
	cmd := &cobra.Command{
		Use:   "import <review.json>",
		Short: "Merge a shared review back onto the document's feedback log",
		Long: "Takes what the share-mode page exported and appends it to\n" +
			"<doc>.feedback.jsonl, skipping events already there. The log stays\n" +
			"append-only, so importing the same file twice changes nothing.",
		Args: requirePaths("import"),
		RunE: func(cmd *cobra.Command, args []string) error {
			var report strings.Builder
			total, skippedAll := 0, 0
			for _, path := range args {
				added, skipped, target, err := importReview(path, doc)
				if err != nil {
					return err
				}
				fmt.Fprintf(&report, "marginalia: %s → %s: %d added, %d already there\n",
					path, target, added, skipped)
				total += added
				skippedAll += skipped
			}
			if total == 0 && skippedAll > 0 {
				report.WriteString("marginalia: nothing new — this review was already merged\n")
			}
			_, err := io.WriteString(cmd.OutOrStdout(), report.String())
			return err
		},
	}
	cmd.Flags().StringVar(&doc, "doc", "", "document whose log to append to (default: the one named in the file)")
	return cmd
}
