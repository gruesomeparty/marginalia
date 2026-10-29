package review

import (
	"strings"
	"testing"
)

// A requester note has an id a reply can name (issue #90): the one the config
// gave it, or one derived from what it says, always with the prefix that
// keeps it apart from a log id.
func TestRequesterNotesHaveIDs(t *testing.T) {
	c := mustLoad(t, `
notes:
  - id: retry-policy
    block: "1/2"
    text: is three retries deliberate?
  - block: "1/2"
    text: and who owns it?
`)
	named, derived := c.Notes[0].ID, c.Notes[1].ID
	if named != "req:retry-policy" {
		t.Errorf("named note id = %q, want req:retry-policy", named)
	}
	if !strings.HasPrefix(derived, RequesterPrefix) || len(derived) != len(RequesterPrefix)+12 {
		t.Errorf("derived note id = %q, want req: plus 12 hex", derived)
	}
	// The same note in a fresh load gets the same id, so an answer written
	// in one session still names its question in the next.
	again := mustLoad(t, "notes:\n  - block: \"1/2\"\n    text: and who owns it?\n")
	if again.Notes[0].ID != derived {
		t.Errorf("derived id is not stable: %q then %q", derived, again.Notes[0].ID)
	}
	if got := c.NotesFor("1/2"); got[0].ID != named || got[1].ID != derived {
		t.Errorf("NotesFor lost the ids: %+v", got)
	}
	if n, ok := c.Requester(derived); !ok || n.Text != "and who owns it?" {
		t.Errorf("Requester(%q) = %+v, %v", derived, n, ok)
	}
	if _, ok := c.Requester("retry-policy"); ok {
		t.Error("an id without the prefix is not a requester note")
	}
	// Validating again (an overlay does) must not stack prefixes.
	if err := c.validate(); err != nil || c.Notes[0].ID != named {
		t.Errorf("revalidating changed the id to %q (err %v)", c.Notes[0].ID, err)
	}
}

// A note built in code, never validated, still answers to the id the page
// shows for it.
func TestUnvalidatedNoteKeyMatchesTheDerivedID(t *testing.T) {
	n := Note{Block: "1/2", Text: "why?"}
	c := &Config{Notes: []Note{n}}
	key := n.Key()
	if !strings.HasPrefix(key, RequesterPrefix) {
		t.Fatalf("key %q has no prefix", key)
	}
	if _, ok := c.Requester(key); !ok {
		t.Error("Requester does not find an unvalidated note by its key")
	}
	if got := c.NotesFor("1/2"); got[0].ID != key {
		t.Errorf("NotesFor id = %q, want %q", got[0].ID, key)
	}
}

// A requester note only answers in the documents it is addressed to.
func TestRequesterIsScopedToItsDocument(t *testing.T) {
	c := mustLoad(t, "notes:\n  - id: q\n    doc: a.md\n    block: \"1/2\"\n    text: why?\n")
	if _, ok := c.Requester("req:q", "a.md"); !ok {
		t.Error("note not found in its own document")
	}
	if _, ok := c.Requester("req:q", "b.md"); ok {
		t.Error("note found in a document it is not addressed to")
	}
}

func TestBadNoteIDsAreRefused(t *testing.T) {
	for name, body := range map[string]string{
		"bad characters": "notes:\n  - id: \"two words\"\n    block: \"1\"\n    text: x\n",
		"duplicate":      "notes:\n  - id: q\n    block: \"1\"\n    text: x\n  - id: q\n    block: \"2\"\n    text: y\n",
	} {
		if _, err := load(t, body); err == nil {
			t.Errorf("%s: loaded, want an error", name)
		}
	}
}
