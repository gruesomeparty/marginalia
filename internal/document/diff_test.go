package document

import (
	"strings"
	"testing"
)

const patchDoc = `Subject: [PATCH] raise the cap

The queue's batch limit moved.

diff --git a/internal/server/handlers.go b/internal/server/handlers.go
--- a/internal/server/handlers.go
+++ b/internal/server/handlers.go
@@ -12,5 +12,5 @@ func check(e *Event) error {
 	// the cap comes from upstream
-	if n > 500 {
+	if n > 2000 {
 		return errTooBig
 	}
@@ -40,2 +40,3 @@ func other() {
 	done()
+	log()
diff --git a/docs/plan.md b/docs/plan.md
new file mode 100644
--- /dev/null
+++ b/docs/plan.md
@@ -0,0 +1,1 @@
+# Plan
`

func blocksOf(t *testing.T, name, src string) map[string]Block {
	t.Helper()
	doc, err := ParseBytes(name, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]Block, len(doc.Blocks))
	for _, b := range doc.Blocks {
		out[b.ID] = b
	}
	return out
}

// The acceptance criterion of issue #52 for this format: a patch serves as an
// anchored document, anchored by file and hunk rather than by line.
func TestDiffAnchorsByFileAndHunk(t *testing.T) {
	doc, err := ParseBytes("change.patch", []byte(patchDoc))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Format != FormatDiff || !doc.IsTree() {
		t.Fatalf("format=%q tree=%v", doc.Format, doc.IsTree())
	}
	var ids []string
	for _, b := range doc.Blocks {
		ids = append(ids, b.ID)
	}
	want := []string{
		"@message",
		"internal/server/handlers.go",
		"internal/server/handlers.go/1",
		"internal/server/handlers.go/2",
		"docs/plan.md",
		"docs/plan.md/1",
	}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("ids:\n got %v\nwant %v", ids, want)
	}
	// Hunks hang off their file, in document order, so the tree folds.
	blocks := blocksOf(t, "change.patch", patchDoc)
	if h := blocks["internal/server/handlers.go/1"]; h.Parent != "internal/server/handlers.go" || h.Level != 1 {
		t.Errorf("hunk parent=%q level=%d", h.Parent, h.Level)
	}
	if f := blocks["internal/server/handlers.go"]; !f.HasChildren {
		t.Error("a file with hunks has children")
	}
}

// Paths must be stable across unrelated edits elsewhere in the patch — the
// criterion every format in #52 has to meet. A note on a hunk of one file must
// survive another file's hunks changing entirely.
func TestDiffPathsSurviveUnrelatedChanges(t *testing.T) {
	before := blocksOf(t, "c.patch", patchDoc)
	// The *other* file gains lines; handlers.go is untouched.
	edited := strings.Replace(patchDoc, "@@ -0,0 +1,1 @@\n+# Plan\n", "@@ -0,0 +1,3 @@\n+# Plan\n+Ship it.\n+Soon.\n", 1)
	after := blocksOf(t, "c.patch", edited)

	for _, id := range []string{"internal/server/handlers.go", "internal/server/handlers.go/1", "internal/server/handlers.go/2"} {
		b, ok := after[id]
		if !ok {
			t.Fatalf("%s lost its anchor when another file changed", id)
		}
		if b.Hash != before[id].Hash {
			t.Errorf("%s: hash moved though nothing in it changed", id)
		}
	}
	// And the file that did change is flagged, rather than silently standing.
	if after["docs/plan.md/1"].Hash == before["docs/plan.md/1"].Hash {
		t.Error("the hunk that changed must not keep its hash")
	}
}

// The markers are part of the meaning: "+ if n > 2000" and "- if n > 2000"
// are opposite statements, so they have to be hashed with the text.
func TestHunkMarkersAreHashed(t *testing.T) {
	a := blocksOf(t, "c.patch", patchDoc)
	flipped := strings.Replace(patchDoc, "-	if n > 500 {\n+	if n > 2000 {", "+	if n > 500 {\n-	if n > 2000 {", 1)
	b := blocksOf(t, "c.patch", flipped)
	if a["internal/server/handlers.go/1"].Hash == b["internal/server/handlers.go/1"].Hash {
		t.Error("reversing which line is added and which removed is a different change")
	}
}

// A note about the file — "this whole file should not be in the patch" —
// must not go stale because a hunk inside it was edited.
func TestAFileBlockDoesNotHashItsHunks(t *testing.T) {
	a := blocksOf(t, "c.patch", patchDoc)
	edited := strings.Replace(patchDoc, "		return errTooBig", "		return errWayTooBig", 1)
	b := blocksOf(t, "c.patch", edited)
	if a["internal/server/handlers.go"].Hash != b["internal/server/handlers.go"].Hash {
		t.Error("a file block hashes its identity and tally, not its hunks")
	}
	if a["internal/server/handlers.go/1"].Hash == b["internal/server/handlers.go/1"].Hash {
		t.Error("but the hunk that changed must be flagged")
	}
}

// The house rule: a malformed file still renders.
func TestMalformedPatchesStillRender(t *testing.T) {
	for name, src := range map[string]string{
		"not a diff":     "just prose\nno markers here\n",
		"empty":          "",
		"hunk only":      "@@ -1 +1 @@\n-a\n+b\n",
		"truncated hunk": "--- a/x\n+++ b/x\n@@ -1,99 +1,99 @@\n-a\n",
		"header only":    "diff --git a/x b/x\nindex 1..2 100644\n",
	} {
		t.Run(name, func(t *testing.T) {
			doc, err := ParseBytes("x.diff", []byte(src))
			if err != nil {
				t.Fatalf("a malformed patch must still render: %v", err)
			}
			for _, b := range doc.Blocks {
				if b.ID == "" {
					t.Error("every block needs an anchor")
				}
			}
		})
	}
}

// The commit message is part of what is being signed off.
func TestTheCommitMessageIsReviewable(t *testing.T) {
	b := blocksOf(t, "c.patch", patchDoc)
	msg, ok := b["@message"]
	if !ok {
		t.Fatal("no block for the commit message")
	}
	if !strings.Contains(msg.PlainText, "raise the cap") {
		t.Errorf("message text = %q", msg.PlainText)
	}
	if msg.Level != 0 || msg.Parent != "" {
		t.Error("the message is a top-level block, not a child of a file")
	}
}

// Both extensions, since a patch is as likely to arrive as one as the other.
func TestBothExtensions(t *testing.T) {
	for _, name := range []string{"x.diff", "x.patch", "X.PATCH"} {
		if got := FormatFor(name); got != FormatDiff {
			t.Errorf("FormatFor(%q) = %q", name, got)
		}
	}
}

// An empty patch is a mistake upstream, and a blank page does not say so.
func TestAnEmptyPatchSaysSo(t *testing.T) {
	doc, err := ParseBytes("nothing.patch", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Blocks) != 1 {
		t.Fatalf("expected one explanatory block, got %d", len(doc.Blocks))
	}
	if !strings.Contains(doc.Blocks[0].PlainText, "no changes") {
		t.Errorf("block text = %q", doc.Blocks[0].PlainText)
	}
}
