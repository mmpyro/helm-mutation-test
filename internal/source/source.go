// Package source loads chart files and resolves positions within them.
//
// The central technique of this tool lives here: mutations are *located* with a
// real parser (Go's text/template AST for templates, yaml.v3 nodes for values)
// but *applied* as byte-range edits to the original bytes. Positions come from a
// parser, so they are correct; edits touch only the mutated span, so all
// surrounding formatting — including Helm's whitespace-sensitive trim markers —
// survives untouched.
package source

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Kind distinguishes the analyses a file supports.
type Kind int

const (
	// KindTemplate is a Go-templated file under templates/. Not valid YAML before
	// rendering, so it gets AST analysis plus line-based YAML heuristics.
	KindTemplate Kind = iota
	// KindValues is a plain YAML file (values.yaml), safe for yaml.v3 node analysis.
	KindValues
)

// File is a loaded chart source file with a line index for position lookups.
type File struct {
	// Path is slash-separated and relative to the chart root; it appears in reports.
	Path string
	// AbsPath is where the file was read from.
	AbsPath string
	Kind    Kind
	// Bytes is the original, unmodified content.
	Bytes []byte

	// lineStarts[i] is the byte offset of line i+1 (1-based lines).
	lineStarts []int
}

// Load reads a file and builds its line index. relPath is stored as-is (callers
// pass a slash-separated chart-relative path).
func Load(absPath, relPath string, kind Kind) (*File, error) {
	b, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", relPath, err)
	}
	return New(b, absPath, relPath, kind), nil
}

// New builds a File from bytes already in memory. Used by tests and by callers
// that have the content already.
func New(b []byte, absPath, relPath string, kind Kind) *File {
	f := &File{
		Path:    filepath.ToSlash(relPath),
		AbsPath: absPath,
		Kind:    kind,
		Bytes:   b,
	}
	f.indexLines()
	return f
}

// indexLines records the byte offset that starts each line. Works for LF and
// CRLF alike: we index on '\n' and strip any trailing '\r' when returning text.
func (f *File) indexLines() {
	f.lineStarts = []int{0}
	for i, c := range f.Bytes {
		if c == '\n' {
			f.lineStarts = append(f.lineStarts, i+1)
		}
	}
}

// Text returns the file content as a string.
func (f *File) Text() string { return string(f.Bytes) }

// LineCount is the number of lines in the file.
func (f *File) LineCount() int { return len(f.lineStarts) }

// Position converts a byte offset to a 1-based line and column. Offsets past the
// end clamp to the final position rather than panicking, so a parser reporting a
// position at EOF cannot crash a run.
func (f *File) Position(offset int) (line, col int) {
	if offset < 0 {
		offset = 0
	}
	if offset > len(f.Bytes) {
		offset = len(f.Bytes)
	}
	// Largest i such that lineStarts[i] <= offset.
	i := sort.Search(len(f.lineStarts), func(i int) bool { return f.lineStarts[i] > offset }) - 1
	if i < 0 {
		i = 0
	}
	return i + 1, offset - f.lineStarts[i] + 1
}

// Offset converts a 1-based line and column to a byte offset.
func (f *File) Offset(line, col int) (int, error) {
	if line < 1 || line > len(f.lineStarts) {
		return 0, fmt.Errorf("%s: line %d out of range (1..%d)", f.Path, line, len(f.lineStarts))
	}
	if col < 1 {
		return 0, fmt.Errorf("%s: column %d out of range", f.Path, col)
	}
	off := f.lineStarts[line-1] + col - 1
	if off > len(f.Bytes) {
		return 0, fmt.Errorf("%s: line %d column %d is past end of file", f.Path, line, col)
	}
	return off, nil
}

// LineRange returns the byte range of a 1-based line, excluding its terminator.
func (f *File) LineRange(line int) (start, end int, err error) {
	if line < 1 || line > len(f.lineStarts) {
		return 0, 0, fmt.Errorf("%s: line %d out of range (1..%d)", f.Path, line, len(f.lineStarts))
	}
	start = f.lineStarts[line-1]
	if line < len(f.lineStarts) {
		end = f.lineStarts[line] - 1 // drop the '\n'
	} else {
		end = len(f.Bytes)
	}
	// Drop a CRLF's '\r' so callers see logical line text.
	if end > start && f.Bytes[end-1] == '\r' {
		end--
	}
	return start, end, nil
}

// LineTextAt returns the text of the line containing the given byte offset,
// with its terminator removed.
func (f *File) LineTextAt(offset int) string {
	line, _ := f.Position(offset)
	return f.LineText(line)
}

// LineText returns the text of a 1-based line, without its terminator. An
// out-of-range line yields "" rather than an error: this is display-only.
func (f *File) LineText(line int) string {
	start, end, err := f.LineRange(line)
	if err != nil {
		return ""
	}
	return string(f.Bytes[start:end])
}

// Slice returns the bytes in [start,end) as a string, clamped to the file.
func (f *File) Slice(start, end int) string {
	start, end = f.clamp(start, end)
	return string(f.Bytes[start:end])
}

// Apply returns a copy of the file bytes with [start,end) replaced. The receiver
// is never modified, so one File can serve as the source for every mutant of it.
func (f *File) Apply(start, end int, replacement string) []byte {
	start, end = f.clamp(start, end)
	out := make([]byte, 0, len(f.Bytes)-(end-start)+len(replacement))
	out = append(out, f.Bytes[:start]...)
	out = append(out, replacement...)
	out = append(out, f.Bytes[end:]...)
	return out
}

// MutatedLine renders the line containing start as it would read after the edit.
//
// A whole-line deletion returns "": the line is gone, and there is no "after"
// text to show. Naively splicing prefix and suffix in that case would join the
// deleted line's indentation to the *following* line, producing a diff that looks
// like the mutation replaced one field with the next one down.
func (f *File) MutatedLine(start, end int, replacement string) string {
	start, end = f.clamp(start, end)
	if replacement == "" && f.isWholeLineSpan(start, end) {
		return ""
	}
	startLine, _ := f.Position(start)
	endLine, _ := f.Position(end)

	lineStart, _, err := f.LineRange(startLine)
	if err != nil {
		return replacement
	}
	_, lineEnd, err := f.LineRange(endLine)
	if err != nil {
		return replacement
	}
	prefix := string(f.Bytes[lineStart:start])
	suffix := ""
	if end <= lineEnd {
		suffix = string(f.Bytes[end:lineEnd])
	}
	return strings.TrimRight(prefix+replacement+suffix, "\r")
}

// isWholeLineSpan reports whether [start,end) covers one or more entire lines:
// it begins at a line start and ends at a line start or at end of file.
func (f *File) isWholeLineSpan(start, end int) bool {
	if end <= start {
		return false
	}
	atLineStart := start == 0 || (start <= len(f.Bytes) && f.Bytes[start-1] == '\n')
	endsLine := end == len(f.Bytes) || f.Bytes[end-1] == '\n'
	return atLineStart && endsLine
}

// DeletedLineCount is how many source lines a span covers, for reports that need
// to say "3 lines removed" rather than showing only the first.
func (f *File) DeletedLineCount(start, end int) int {
	start, end = f.clamp(start, end)
	if end <= start {
		return 0
	}
	startLine, _ := f.Position(start)
	endLine, _ := f.Position(end - 1)
	return endLine - startLine + 1
}

func (f *File) clamp(start, end int) (int, int) {
	if start < 0 {
		start = 0
	}
	if end > len(f.Bytes) {
		end = len(f.Bytes)
	}
	if end < start {
		end = start
	}
	return start, end
}
