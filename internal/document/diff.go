package document

import (
	"fmt"
	"html"
	"strings"

	"github.com/gruesomeparty/marginalia/internal/unidiff"
)

// Block kinds a diff contributes.
const (
	KindPatchNote = "patch-note"
	KindDiffFile  = "diff-file"
	KindHunk      = "hunk"
)

// parseDiff turns a unified diff into blocks anchored by file and hunk, which
// is how a reviewer talks about a change: "this hunk locks the table", not
// "line 41".
//
// A file is a top-level node keyed by its own path — so `readonly: internal/`
// mutes a whole directory, because the review config's patterns already treat
// `/` as a separator — and each hunk hangs off it by ordinal, the same shape
// markdown uses for `section/ordinal`. Deliberately *not* keyed by the hunk's
// line numbers: regenerating a patch after an earlier hunk changes shifts
// every number below it, which would orphan every note under it. An ordinal
// only shifts when a hunk is inserted before, and the hash catches that.
func parseDiff(path string, src []byte) (*Document, error) {
	patch := unidiff.Parse(src)
	tb := newTreeBuilder()

	// The commit message is part of what is being signed off, so it is
	// reviewable rather than chrome.
	if len(patch.Preamble) > 0 {
		text := strings.Join(patch.Preamble, "\n")
		tb.add(node{
			ID:   "@message",
			Kind: KindPatchNote,
			Text: text,
			HTML: `<pre class="patch-note">` + html.EscapeString(text) + `</pre>`,
		})
	}

	for _, f := range patch.Files {
		id := tb.add(node{
			ID:   filePath(f),
			Kind: KindDiffFile,
			Text: fileText(f),
			HTML: fileHTML(f),
		})
		for i, h := range f.Hunks {
			tb.add(node{
				Parent: id,
				ID:     fmt.Sprintf("%s/%d", id, i+1),
				Kind:   KindHunk,
				Level:  1,
				Text:   hunkText(h),
				HTML:   hunkHTML(h),
			})
		}
	}
	// A patch with nothing in it is a mistake upstream — `git diff` with no
	// changes, a truncated download — and a blank page does not say so. One
	// block does, and keeps every consumer's "documents have blocks"
	// assumption true.
	if len(tb.blocks) == 0 {
		tb.add(node{
			ID:   "@empty",
			Kind: KindPatchNote,
			Text: "This patch contains no changes.",
			HTML: `<div class="diff-file"><span class="note">This patch contains no changes.</span></div>`,
		})
	}
	return &Document{Path: path, Format: FormatDiff, Blocks: tb.blocks}, nil
}

// filePath is what a file's blocks are anchored by. A patch with no usable
// name still needs one, so it falls back to a stable placeholder rather than
// an empty id.
func filePath(f *unidiff.File) string {
	if f.Path != "" {
		return f.Path
	}
	return "(unnamed)"
}

// fileText is what the file block hashes and quotes: its identity and the
// shape of the change, not the change itself — that belongs to the hunks. So
// editing a hunk does not make a note about the file stale.
func fileText(f *unidiff.File) string {
	s := fmt.Sprintf("%s %s +%d -%d", f.Status, filePath(f), f.Added, f.Removed)
	if f.Note != "" {
		s += " (" + f.Note + ")"
	}
	return s
}

func fileHTML(f *unidiff.File) string {
	var b strings.Builder
	b.WriteString(`<div class="diff-file">`)
	fmt.Fprintf(&b, `<span class="status %s">%s</span>`, f.Status, f.Status)
	fmt.Fprintf(&b, `<span class="path">%s</span>`, html.EscapeString(filePath(f)))
	if f.Added > 0 || f.Removed > 0 {
		fmt.Fprintf(&b, `<span class="tally"><span class="plus">+%d</span> <span class="minus">−%d</span></span>`, f.Added, f.Removed)
	}
	if f.Note != "" {
		fmt.Fprintf(&b, `<span class="note">%s</span>`, html.EscapeString(f.Note))
	}
	b.WriteString(`</div>`)
	return b.String()
}

// hunkText is the changed lines with their markers. The markers are part of
// the meaning — "+ if n > 2000" and "- if n > 2000" are opposite statements —
// so they are hashed and quoted with the text.
func hunkText(h *unidiff.Hunk) string {
	var b strings.Builder
	for _, l := range h.Lines {
		b.WriteString(l.Kind)
		b.WriteString(l.Text)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func hunkHTML(h *unidiff.Hunk) string {
	var b strings.Builder
	b.WriteString(`<div class="hunk">`)
	b.WriteString(`<div class="at">` + html.EscapeString(h.Header) + `</div>`)
	b.WriteString(`<pre class="lines">`)
	old, new := h.OldStart, h.NewStart
	for _, l := range h.Lines {
		cls := "ctx"
		var oldNo, newNo string
		switch l.Kind {
		case "+":
			cls, newNo = "add", num(new)
			new++
		case "-":
			cls, oldNo = "del", num(old)
			old++
		case `\`:
			// git's "\ No newline at end of file" — a remark about the file,
			// not a line of it, so it is numbered on neither side.
			cls = "eof"
		default:
			oldNo, newNo = num(old), num(new)
			old++
			new++
		}
		// No newline between rows: `.ln` is display:block, so a literal one
		// inside the <pre> would break the line a second time and render the
		// whole diff double-spaced. Browsers still put line breaks between
		// block elements when the text is copied.
		fmt.Fprintf(&b, `<span class="ln %s"><span class="no old">%s</span><span class="no new">%s</span><span class="mark">%s</span>%s</span>`,
			cls, oldNo, newNo, html.EscapeString(l.Kind), html.EscapeString(l.Text))
	}
	b.WriteString(`</pre></div>`)
	return b.String()
}

// num renders a line number, or nothing when the header gave no starting
// point to count from — a made-up number is worse than a blank gutter.
func num(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprint(n)
}
