// Package unidiff parses unified diffs and patches into the files and hunks a
// reviewer actually talks about.
//
// Tolerant and structural, like internal/protoschema and internal/mermaid: a
// patch that a stricter tool would reject — a truncated hunk, a header shape
// nobody standardised, output from a tool that is not git — still yields the
// parts it does understand, because refusing to render a diff is worse than
// rendering it imperfectly. Nothing here applies a patch or checks that it
// would apply; Marginalia reviews, it does not merge.
package unidiff

import (
	"strconv"
	"strings"
)

// Kind of change a file underwent, as far as the patch says.
const (
	Modified = "modified"
	Added    = "added"
	Deleted  = "deleted"
	Renamed  = "renamed"
	Binary   = "binary"
)

// Line is one line inside a hunk, kept with the marker that classifies it so
// the renderer does not have to re-derive it.
type Line struct {
	// Kind is "+", "-", " " (context), or "\" for git's
	// "\ No newline at end of file" marker.
	Kind string
	Text string
}

// Hunk is one contiguous run of change within a file.
type Hunk struct {
	// Header is the @@ line verbatim, so what is shown is what was written.
	Header string
	// Section is the text git puts after the closing @@ — usually the
	// enclosing function. Advisory: many patches have none.
	Section  string
	Lines    []Line
	OldStart int
	NewStart int
	Added    int
	Removed  int
}

// File is one file's changes.
type File struct {
	// Path is what the file is called after the change, falling back to its
	// old name for a deletion. This is what a block is anchored by.
	Path string
	// Old and New are the raw a/ and b/ sides, kept for a rename.
	Old, New string
	Status   string
	// Header is every line before the first hunk — `diff --git`, `index`,
	// mode changes, the ---/+++ pair. Shown, never parsed for meaning.
	Header  []string
	Hunks   []*Hunk
	Added   int
	Removed int
	// Note carries a one-line explanation where there are no hunks to show:
	// a binary file, a pure rename, a mode change.
	Note string
}

// Patch is a whole diff file.
type Patch struct {
	// Preamble is everything before the first file — a commit message and
	// mail headers from `git format-patch`, or a covering note someone typed.
	// It is worth reviewing, so it is kept rather than skipped.
	Preamble []string
	Files    []*File
}

// Parse reads a unified diff. It never fails: anything it cannot classify is
// preserved as text on the part it was found in.
func Parse(src []byte) *Patch {
	p := &Patch{}
	lines := splitLines(string(src))
	var file *File
	var hunk *Hunk

	flushHunk := func() { hunk = nil }
	startFile := func(f *File) {
		flushHunk()
		file = f
		p.Files = append(p.Files, f)
	}

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch {
		// `diff --git a/x b/y` opens a file even when the ---/+++ pair is
		// missing, which is how a mode-only or binary change arrives.
		case strings.HasPrefix(line, "diff --git "):
			old, new := gitPaths(line)
			startFile(&File{Old: old, New: new, Path: prefer(new, old), Status: Modified, Header: []string{line}})

		// A bare ---/+++ pair opens a file for every tool that is not git.
		case strings.HasPrefix(line, "--- ") && i+1 < len(lines) && strings.HasPrefix(lines[i+1], "+++ "):
			old := trimPath(strings.TrimPrefix(line, "--- "))
			new := trimPath(strings.TrimPrefix(lines[i+1], "+++ "))
			if file == nil || len(file.Hunks) > 0 || file.Path != "" && file.Path != prefer(new, old) {
				startFile(&File{Status: Modified})
			}
			file.Old, file.New = old, new
			if file.Path == "" {
				file.Path = prefer(new, old)
			}
			file.Header = append(file.Header, line, lines[i+1])
			i++

		case strings.HasPrefix(line, "@@"):
			if file == nil {
				// A hunk with no file header: still reviewable, just unnamed.
				startFile(&File{Path: "", Status: Modified})
			}
			hunk = parseHunkHeader(line)
			file.Hunks = append(file.Hunks, hunk)

		case hunk != nil && isHunkLine(line):
			// An empty line in a patch is a context line whose single leading
			// space some mailer ate. Treating it as the end of the hunk would
			// truncate the change — and slicing it before checking is how a
			// tolerant parser panics on the input it exists to tolerate.
			l := Line{Kind: " "}
			if line != "" {
				l = Line{Kind: line[:1], Text: line[1:]}
			}
			switch l.Kind {
			case "+":
				hunk.Added++
				file.Added++
			case "-":
				hunk.Removed++
				file.Removed++
			}
			hunk.Lines = append(hunk.Lines, l)

		case file != nil && hunk == nil:
			// Still in a file's header: mode lines, index lines, rename
			// lines, and whatever else a tool chose to emit.
			file.Header = append(file.Header, line)
			classify(file, line)

		case file == nil:
			p.Preamble = append(p.Preamble, line)

		default:
			// A line inside a hunk that carries no marker. Tolerated as
			// context rather than ending the file.
			hunk.Lines = append(hunk.Lines, Line{Kind: " ", Text: line})
		}
	}
	for _, f := range p.Files {
		note(f)
	}
	trimTrailing(&p.Preamble)
	return p
}

// isHunkLine reports whether a line belongs to the hunk being read.
func isHunkLine(line string) bool {
	if line == "" {
		return true
	}
	switch line[0] {
	case '+', '-', ' ', '\\':
		return true
	}
	return false
}

// classify reads the file-status lines git emits in a header.
func classify(f *File, line string) {
	switch {
	case strings.HasPrefix(line, "new file mode"):
		f.Status = Added
	case strings.HasPrefix(line, "deleted file mode"):
		f.Status = Deleted
	case strings.HasPrefix(line, "rename from "), strings.HasPrefix(line, "rename to "):
		f.Status = Renamed
		if strings.HasPrefix(line, "rename from ") {
			f.Old = strings.TrimPrefix(line, "rename from ")
		} else {
			f.New = strings.TrimPrefix(line, "rename to ")
			f.Path = f.New
		}
	case strings.HasPrefix(line, "Binary files "), strings.HasPrefix(line, "GIT binary patch"):
		f.Status = Binary
	}
}

// note gives a file with nothing to show a line saying why.
func note(f *File) {
	if len(f.Hunks) > 0 {
		return
	}
	switch f.Status {
	case Binary:
		f.Note = "binary file — no textual diff"
	case Renamed:
		f.Note = "renamed from " + f.Old
	default:
		f.Note = "no textual changes"
	}
}

// parseHunkHeader reads `@@ -12,7 +12,9 @@ func main() {`. The counts are
// advisory — a hunk whose header disagrees with its body is still shown, body
// first, because the body is the change.
func parseHunkHeader(line string) *Hunk {
	h := &Hunk{Header: line}
	rest := strings.TrimPrefix(line, "@@")
	if i := strings.Index(rest, "@@"); i >= 0 {
		h.Section = strings.TrimSpace(rest[i+2:])
		rest = rest[:i]
	}
	for _, field := range strings.Fields(rest) {
		if len(field) < 2 {
			continue
		}
		n := start(field[1:])
		switch field[0] {
		case '-':
			h.OldStart = n
		case '+':
			h.NewStart = n
		}
	}
	return h
}

// start reads the line number from `12,7` or `12`.
func start(s string) int {
	if i := strings.IndexByte(s, ','); i >= 0 {
		s = s[:i]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// gitPaths pulls the two paths out of a `diff --git` line. A path containing a
// space makes the split ambiguous, so the a/ and b/ prefixes are used as the
// anchor and the halves are taken around the midpoint when they are not.
func gitPaths(line string) (old, new string) {
	rest := strings.TrimPrefix(line, "diff --git ")
	if i := strings.Index(rest, " b/"); i >= 0 {
		return trimPath(rest[:i]), trimPath(rest[i+1:])
	}
	parts := strings.Fields(rest)
	if len(parts) == 2 {
		return trimPath(parts[0]), trimPath(parts[1])
	}
	return "", ""
}

// trimPath strips the a/ or b/ prefix and any tab-separated timestamp, which
// non-git tools append to the ---/+++ lines.
func trimPath(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\t'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if s == "/dev/null" {
		return ""
	}
	for _, p := range []string{"a/", "b/", "i/", "w/", "c/", "o/"} {
		if strings.HasPrefix(s, p) {
			return s[2:]
		}
	}
	return s
}

func prefer(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func trimTrailing(lines *[]string) {
	for len(*lines) > 0 && strings.TrimSpace((*lines)[len(*lines)-1]) == "" {
		*lines = (*lines)[:len(*lines)-1]
	}
}
