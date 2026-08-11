package source

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Scalar is a YAML scalar with its resolved byte span in the source file.
type Scalar struct {
	// Tag is the resolved YAML tag: "!!bool", "!!int", "!!float", "!!str", "!!null".
	Tag string
	// Value is the decoded scalar value.
	Value string
	// Start and End delimit the scalar in the source, including any quotes.
	Start, End int
	// Line and Column are 1-based.
	Line, Column int
	// IsKey is true for mapping keys. Mutating a key renames a field rather than
	// changing a value, which is what yaml-key-delete already covers, so value
	// mutators skip keys.
	IsKey bool
	// Path is the dotted path to the scalar, e.g. "image.pullPolicy". Used to
	// implement guards such as "never mutate apiVersion".
	Path string
}

// ScalarsOf returns every scalar in a YAML document, sorted by position so
// mutant generation is deterministic.
//
// Block scalars (| and >) and multi-line plain scalars are skipped: their source
// extent spans lines and replacing them reliably is not worth it in a first pass.
func ScalarsOf(f *File) ([]Scalar, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(f.Bytes, &root); err != nil {
		return nil, fmt.Errorf("parsing %s as YAML: %w", f.Path, err)
	}

	var out []Scalar
	for _, doc := range documentRoots(&root) {
		collectScalars(f, doc, "", false, &out)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out, nil
}

func documentRoots(n *yaml.Node) []*yaml.Node {
	if n.Kind == yaml.DocumentNode {
		return n.Content
	}
	if n.Kind == 0 {
		return nil // empty file
	}
	return []*yaml.Node{n}
}

func collectScalars(f *File, n *yaml.Node, path string, isKey bool, out *[]Scalar) {
	if n == nil {
		return
	}
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			collectScalars(f, c, path, false, out)
		}
	case yaml.MappingNode:
		// Content alternates key, value, key, value.
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			collectScalars(f, k, join(path, k.Value), true, out)
			collectScalars(f, v, join(path, k.Value), false, out)
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			collectScalars(f, c, fmt.Sprintf("%s[%d]", path, i), false, out)
		}
	case yaml.ScalarNode:
		start, end, ok := scalarSpan(f, n)
		if !ok {
			return
		}
		*out = append(*out, Scalar{
			Tag: n.Tag, Value: n.Value,
			Start: start, End: end,
			Line: n.Line, Column: n.Column,
			IsKey: isKey, Path: path,
		})
	case yaml.AliasNode:
		// An alias has no independent source value to mutate.
	}
}

func join(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

// scalarSpan resolves a scalar node's byte extent. yaml.v3 records only a start
// position, so the end is derived from the source text.
func scalarSpan(f *File, n *yaml.Node) (start, end int, ok bool) {
	switch n.Style {
	case yaml.LiteralStyle, yaml.FoldedStyle:
		return 0, 0, false // block scalars span lines
	}
	if strings.Contains(n.Value, "\n") {
		return 0, 0, false // multi-line plain scalar
	}

	start, err := f.Offset(n.Line, n.Column)
	if err != nil {
		return 0, 0, false
	}
	_, lineEnd, err := f.LineRange(n.Line)
	if err != nil || start >= lineEnd {
		return 0, 0, false
	}

	b := f.Bytes
	if q := b[start]; q == '"' || q == '\'' {
		close := skipQuoted(b, start)
		if close <= start {
			return 0, 0, false
		}
		return start, close + 1, true
	}

	// Plain scalar: runs to end of line, minus a trailing comment and padding.
	end = lineEnd
	for i := start; i < lineEnd; i++ {
		if b[i] == '#' && i > start && isSpace(b[i-1]) {
			end = i
			break
		}
	}
	for end > start && isSpace(b[end-1]) {
		end--
	}
	if end <= start {
		return 0, 0, false
	}
	// A flow collection is not a scalar we can swap wholesale.
	if b[start] == '[' || b[start] == '{' {
		return 0, 0, false
	}
	return start, end, true
}

// KeyBlock is a mapping key together with the source extent of the whole
// "key: value" entry, including any nested block beneath it.
type KeyBlock struct {
	Name string
	Path string
	// Line is the 1-based line the key sits on.
	Line int
	// Indent is the key's 0-based column, used to find where its block ends.
	Indent int
	// Start and End delimit the entry: from the start of the key's line through
	// the end of its last nested line, including the trailing newline.
	Start, End int
}

// KeyBlocksOf returns every mapping key in a YAML document with the byte extent
// of its full entry, sorted by position. This is what yaml-key-delete removes.
func KeyBlocksOf(f *File) ([]KeyBlock, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(f.Bytes, &root); err != nil {
		return nil, fmt.Errorf("parsing %s as YAML: %w", f.Path, err)
	}

	var out []KeyBlock
	for _, doc := range documentRoots(&root) {
		collectKeyBlocks(f, doc, "", &out)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out, nil
}

func collectKeyBlocks(f *File, n *yaml.Node, path string, out *[]KeyBlock) {
	if n == nil {
		return
	}
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			collectKeyBlocks(f, c, path, out)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			p := join(path, k.Value)
			if start, end, ok := f.BlockExtent(k.Line, k.Column-1); ok {
				*out = append(*out, KeyBlock{
					Name: k.Value, Path: p, Line: k.Line,
					Indent: k.Column - 1, Start: start, End: end,
				})
			}
			collectKeyBlocks(f, v, p, out)
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			collectKeyBlocks(f, c, fmt.Sprintf("%s[%d]", path, i), out)
		}
	}
}

// BlockExtent returns the byte range covering the given 1-based line plus every
// following line that belongs to it: lines indented deeper than indent, and
// blank lines that are followed by such a line.
//
// Deleting this range removes a key together with its nested value, which is
// what makes yaml-key-delete produce structurally valid YAML.
func (f *File) BlockExtent(line, indent int) (start, end int, ok bool) {
	start, _, err := f.LineRange(line)
	if err != nil {
		return 0, 0, false
	}

	last := line
	for probe := line + 1; probe <= f.LineCount(); probe++ {
		text := f.LineText(probe)
		if strings.TrimSpace(text) == "" {
			continue // decided by whatever follows
		}
		if lineIndent(text) <= indent {
			break
		}
		last = probe
	}

	// End just past the last line's terminator, so the deletion leaves no blank gap.
	_, lineEnd, err := f.LineRange(last)
	if err != nil {
		return 0, 0, false
	}
	end = lineEnd
	for end < len(f.Bytes) && (f.Bytes[end] == '\r' || f.Bytes[end] == '\n') {
		end++
		if end > 0 && f.Bytes[end-1] == '\n' {
			break
		}
	}
	return start, end, true
}

func lineIndent(text string) int {
	n := 0
	for n < len(text) && (text[n] == ' ' || text[n] == '\t') {
		n++
	}
	return n
}
