package unidiff

import "testing"

const gitPatch = `From 9f2a1c4b Mon Sep 17 00:00:00 2001
Subject: [PATCH] raise the cap

The queue's batch limit moved, so the cap follows.

diff --git a/internal/server/handlers.go b/internal/server/handlers.go
index abc1234..def5678 100644
--- a/internal/server/handlers.go
+++ b/internal/server/handlers.go
@@ -12,7 +12,7 @@ func check(e *Event) error {
 	// the cap comes from upstream
-	if n > 500 {
+	if n > 2000 {
 		return errTooBig
 	}
 	return nil
@@ -40,3 +40,4 @@ func other() {
 	done()
+	log()
 }
diff --git a/docs/plan.md b/docs/plan.md
new file mode 100644
--- /dev/null
+++ b/docs/plan.md
@@ -0,0 +1,2 @@
+# Plan
+Ship it.
`

func TestParseAGitPatch(t *testing.T) {
	p := Parse([]byte(gitPatch))

	// The commit message is worth reviewing, so it is kept rather than skipped.
	if len(p.Preamble) == 0 {
		t.Fatal("the commit message should be preserved")
	}
	if got := p.Preamble[1]; got != "Subject: [PATCH] raise the cap" {
		t.Errorf("preamble[1] = %q", got)
	}

	if len(p.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(p.Files))
	}
	h := p.Files[0]
	if h.Path != "internal/server/handlers.go" {
		t.Errorf("path = %q", h.Path)
	}
	if h.Status != Modified {
		t.Errorf("status = %q", h.Status)
	}
	if len(h.Hunks) != 2 {
		t.Fatalf("expected 2 hunks, got %d", len(h.Hunks))
	}
	if h.Added != 2 || h.Removed != 1 {
		t.Errorf("counts: +%d -%d, want +2 -1", h.Added, h.Removed)
	}
	if h.Hunks[0].Section != "func check(e *Event) error {" {
		t.Errorf("section = %q", h.Hunks[0].Section)
	}
	if h.Hunks[0].OldStart != 12 || h.Hunks[1].NewStart != 40 {
		t.Errorf("line numbers: %+v %+v", h.Hunks[0], h.Hunks[1])
	}
	// The markers are kept, so the renderer never re-derives them.
	var kinds string
	for _, l := range h.Hunks[0].Lines {
		kinds += l.Kind
	}
	if kinds != " -+   " {
		t.Errorf("line kinds = %q", kinds)
	}

	added := p.Files[1]
	if added.Path != "docs/plan.md" || added.Status != Added {
		t.Errorf("second file: path=%q status=%q", added.Path, added.Status)
	}
	if added.Added != 2 || added.Removed != 0 {
		t.Errorf("added file counts: +%d -%d", added.Added, added.Removed)
	}
}

// A patch from something that is not git: no `diff --git`, and a timestamp
// after the path. Refusing it would mean refusing every non-git tool.
func TestParseAPlainUnifiedDiff(t *testing.T) {
	src := `--- old/config.yaml	2026-09-01 10:00:00.000000000 +0000
+++ new/config.yaml	2026-09-02 10:00:00.000000000 +0000
@@ -1,3 +1,3 @@
 replicas: 3
-image: app:1.0
+image: app:1.1
`
	p := Parse([]byte(src))
	if len(p.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(p.Files))
	}
	f := p.Files[0]
	// The timestamp is not part of the name.
	if f.Path != "new/config.yaml" {
		t.Errorf("path = %q", f.Path)
	}
	if f.Added != 1 || f.Removed != 1 {
		t.Errorf("counts: +%d -%d", f.Added, f.Removed)
	}
}

// Deletions, renames and binaries have nothing to show, and must say why
// rather than rendering as an empty file with no explanation.
func TestFilesWithNothingToShow(t *testing.T) {
	src := `diff --git a/old.txt b/old.txt
deleted file mode 100644
--- a/old.txt
+++ /dev/null
@@ -1 +0,0 @@
-gone
diff --git a/a.png b/a.png
index 1234..5678 100644
Binary files a/a.png and b/a.png differ
diff --git a/from.go b/to.go
similarity index 100%
rename from from.go
rename to to.go
`
	p := Parse([]byte(src))
	if len(p.Files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(p.Files))
	}
	del, bin, ren := p.Files[0], p.Files[1], p.Files[2]
	if del.Status != Deleted || del.Path != "old.txt" {
		t.Errorf("deleted: status=%q path=%q", del.Status, del.Path)
	}
	// /dev/null is not a path, so the surviving side names the file.
	if del.Removed != 1 {
		t.Errorf("deleted should count its removed line, got %d", del.Removed)
	}
	if bin.Status != Binary || bin.Note == "" {
		t.Errorf("binary: status=%q note=%q", bin.Status, bin.Note)
	}
	if ren.Status != Renamed || ren.Path != "to.go" {
		t.Errorf("renamed: status=%q path=%q", ren.Status, ren.Path)
	}
	if ren.Note != "renamed from from.go" {
		t.Errorf("rename note = %q", ren.Note)
	}
}

// The house rule: a file that does not parse cleanly still renders. Every
// input below is malformed in a different way and none may lose the change.
func TestTolerance(t *testing.T) {
	t.Run("truncated hunk", func(t *testing.T) {
		p := Parse([]byte("--- a/x\n+++ b/x\n@@ -1,9 +1,9 @@\n-one\n+two\n"))
		if len(p.Files) != 1 || len(p.Files[0].Hunks) != 1 {
			t.Fatalf("%+v", p.Files)
		}
		// The header claims nine lines and two arrived. The body wins.
		if got := len(p.Files[0].Hunks[0].Lines); got != 2 {
			t.Errorf("lines = %d, want 2", got)
		}
	})
	t.Run("hunk with no file header", func(t *testing.T) {
		p := Parse([]byte("@@ -1 +1 @@\n-a\n+b\n"))
		if len(p.Files) != 1 || len(p.Files[0].Hunks) != 1 {
			t.Fatalf("an unnamed change is still a change: %+v", p.Files)
		}
	})
	t.Run("mailer ate the context space", func(t *testing.T) {
		p := Parse([]byte("--- a/x\n+++ b/x\n@@ -1,3 +1,3 @@\n a\n\n-b\n+c\n"))
		if got := len(p.Files[0].Hunks[0].Lines); got != 4 {
			t.Errorf("an empty line is context, not the end of the hunk: got %d lines", got)
		}
	})
	t.Run("empty input", func(t *testing.T) {
		p := Parse(nil)
		if len(p.Files) != 0 || len(p.Preamble) != 0 {
			t.Errorf("%+v", p)
		}
	})
	t.Run("not a diff at all", func(t *testing.T) {
		p := Parse([]byte("just some prose\nwith no markers\n"))
		if len(p.Files) != 0 {
			t.Errorf("nothing here is a file: %+v", p.Files)
		}
		if len(p.Preamble) != 2 {
			t.Errorf("but the text is kept: %+v", p.Preamble)
		}
	})
}

// A path with a space is why the a/ and b/ prefixes are the anchor rather
// than the whitespace between them.
func TestPathWithASpace(t *testing.T) {
	p := Parse([]byte("diff --git a/my docs/plan.md b/my docs/plan.md\n@@ -1 +1 @@\n-a\n+b\n"))
	if len(p.Files) != 1 || p.Files[0].Path != "my docs/plan.md" {
		t.Fatalf("path = %q", p.Files[0].Path)
	}
}
