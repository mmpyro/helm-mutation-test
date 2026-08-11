package source

import (
	"fmt"
	"text/template/parse"
)

// Tree is a parsed Helm template. Positions in its nodes are byte offsets into
// the File the Tree was parsed from.
type Tree struct {
	// Roots holds every parse tree found in the file: the file's own body plus one
	// per {{ define }} block. _helpers.tpl is nothing but define blocks, so walking
	// only the main root would find no mutation sites there at all.
	Roots []*parse.Tree
}

// ParseTemplate parses a Helm template for mutation analysis.
//
// Two deliberate choices:
//   - parse.SkipFuncCheck: Helm templates call sprig and Helm builtins (include,
//     toYaml, required, ...). Without this the parser rejects them as undefined
//     and we would have to reconstruct Helm's entire funcmap just to find literals.
//   - Delimiters are fixed at {{ }}: charts overriding them are rare, and a
//     mis-parse degrades to "no AST mutators for this file", not to a wrong edit.
func ParseTemplate(f *File) (*Tree, error) {
	t := parse.New(f.Path)
	t.Mode = parse.SkipFuncCheck

	treeSet := map[string]*parse.Tree{}
	root, err := t.Parse(f.Text(), "{{", "}}", treeSet)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", f.Path, err)
	}

	out := &Tree{Roots: []*parse.Tree{root}}
	// treeSet contains the define blocks. It may also contain root under its own
	// name, so skip that entry to avoid walking the same nodes twice.
	for name, sub := range treeSet {
		if name == root.Name || sub == nil || sub == root {
			continue
		}
		out.Roots = append(out.Roots, sub)
	}
	return out, nil
}

// Walk calls fn for every node in every root, depth first. Returning false from
// fn skips that node's children.
//
// Node order across roots is not deterministic (treeSet is a map), so callers
// that need stable output must sort their results by position. Mutators do.
func (t *Tree) Walk(fn func(parse.Node) bool) {
	for _, root := range t.Roots {
		if root == nil || root.Root == nil {
			continue
		}
		walkNode(root.Root, fn)
	}
}

func walkNode(n parse.Node, fn func(parse.Node) bool) {
	if isNilNode(n) {
		return
	}
	if !fn(n) {
		return
	}
	switch node := n.(type) {
	case *parse.ListNode:
		if node == nil {
			return
		}
		for _, child := range node.Nodes {
			walkNode(child, fn)
		}
	case *parse.ActionNode:
		walkNode(node.Pipe, fn)
	case *parse.PipeNode:
		if node == nil {
			return
		}
		for _, decl := range node.Decl {
			walkNode(decl, fn)
		}
		for _, cmd := range node.Cmds {
			walkNode(cmd, fn)
		}
	case *parse.CommandNode:
		for _, arg := range node.Args {
			walkNode(arg, fn)
		}
	case *parse.IfNode:
		walkBranch(&node.BranchNode, fn)
	case *parse.RangeNode:
		walkBranch(&node.BranchNode, fn)
	case *parse.WithNode:
		walkBranch(&node.BranchNode, fn)
	case *parse.TemplateNode:
		walkNode(node.Pipe, fn)
	case *parse.ChainNode:
		walkNode(node.Node, fn)
	// Leaves: TextNode, StringNode, NumberNode, BoolNode, NilNode, DotNode,
	// FieldNode, VariableNode, IdentifierNode, CommentNode, BreakNode, ContinueNode.
	default:
	}
}

func walkBranch(b *parse.BranchNode, fn func(parse.Node) bool) {
	walkNode(b.Pipe, fn)
	walkNode(b.List, fn)
	walkNode(b.ElseList, fn)
}

// isNilNode reports whether n is nil or a non-nil interface wrapping a nil
// pointer. An {{if}} with no {{else}} has a typed-nil ElseList, so a bare
// `n == nil` check misses it and callers panic on Position(). Enumerating the
// pointer types keeps this out of reflect.
func isNilNode(n parse.Node) bool {
	switch v := n.(type) {
	case nil:
		return true
	case *parse.ListNode:
		return v == nil
	case *parse.PipeNode:
		return v == nil
	case *parse.ActionNode:
		return v == nil
	case *parse.CommandNode:
		return v == nil
	case *parse.IfNode:
		return v == nil
	case *parse.RangeNode:
		return v == nil
	case *parse.WithNode:
		return v == nil
	case *parse.TemplateNode:
		return v == nil
	case *parse.ChainNode:
		return v == nil
	case *parse.StringNode:
		return v == nil
	case *parse.NumberNode:
		return v == nil
	case *parse.BoolNode:
		return v == nil
	case *parse.IdentifierNode:
		return v == nil
	case *parse.FieldNode:
		return v == nil
	case *parse.VariableNode:
		return v == nil
	case *parse.TextNode:
		return v == nil
	}
	return false
}

// Action is the source extent of one {{ ... }} action in a template.
type Action struct {
	// Start and End delimit the whole action, "{{" through "}}" inclusive.
	Start, End int
	// InnerStart and InnerEnd delimit the content between the delimiters,
	// excluding any "-" trim markers and the padding whitespace they require.
	InnerStart, InnerEnd int
}

// Inner returns the action's content, without delimiters or trim markers.
func (a Action) Inner(f *File) string { return f.Slice(a.InnerStart, a.InnerEnd) }

// FindAction locates the {{ ... }} action containing the given byte offset.
//
// AST nodes carry a position but not an extent, so to edit an action's source we
// scan outward from a node offset to the enclosing delimiters. Scanning is done
// on the original bytes, which is why trim markers and whitespace survive.
func FindAction(f *File, offset int) (Action, bool) {
	b := f.Bytes
	if offset > len(b) {
		return Action{}, false
	}

	// Walk back to the nearest "{{" at or before offset. A "}}" first means the
	// offset is in plain text, not inside an action.
	start := -1
	for i := min(offset, len(b)-1); i >= 1; i-- {
		if b[i-1] == '{' && b[i] == '{' {
			start = i - 1
			break
		}
		if b[i-1] == '}' && b[i] == '}' && i-1 < offset {
			return Action{}, false
		}
	}
	if start < 0 {
		return Action{}, false
	}

	// Forward to the matching "}}". Skip over quoted strings so a "}}" inside a
	// string literal does not terminate the action early.
	end := -1
	for i := start + 2; i < len(b)-1; i++ {
		switch b[i] {
		case '"', '\'', '`':
			if j := skipQuoted(b, i); j > i {
				i = j
				continue
			}
		case '}':
			if b[i+1] == '}' {
				end = i + 2
				goto found
			}
		}
	}
found:
	if end < 0 {
		return Action{}, false
	}

	a := Action{Start: start, End: end, InnerStart: start + 2, InnerEnd: end - 2}
	// Strip trim markers: "{{- x -}}" must become "x", and the space after "{{-"
	// is required syntax, not content.
	if a.InnerStart < a.InnerEnd && b[a.InnerStart] == '-' {
		a.InnerStart++
	}
	if a.InnerEnd > a.InnerStart && b[a.InnerEnd-1] == '-' {
		a.InnerEnd--
	}
	return a, true
}

// skipQuoted returns the index of the closing quote for the quoted run starting
// at i, or i if it is unterminated. Handles backslash escapes in "..." and `...`
// is treated as raw.
func skipQuoted(b []byte, i int) int {
	q := b[i]
	for j := i + 1; j < len(b); j++ {
		if q != '`' && b[j] == '\\' {
			j++
			continue
		}
		if b[j] == q {
			return j
		}
		if b[j] == '\n' && q != '`' {
			return i // unterminated on this line
		}
	}
	return i
}

// SpanOfCondition returns the source span of a branch node's condition: the
// pipeline between the keyword and the closing delimiter.
//
// Branch node positions point at the condition itself rather than at the "{{",
// which is what makes this keyword-agnostic: `{{ if x }}`, `{{- else if x }}`,
// `{{ with x }}` and `{{ else with x }}` all resolve without special cases.
func SpanOfCondition(f *File, pipe *parse.PipeNode) (start, end int, ok bool) {
	if isNilNode(pipe) {
		return 0, 0, false
	}
	start = int(pipe.Position())
	a, found := FindAction(f, start)
	if !found || start < a.InnerStart || start >= a.InnerEnd {
		return 0, 0, false
	}
	end = a.InnerEnd
	for end > start && isSpace(f.Bytes[end-1]) {
		end--
	}
	if end <= start {
		return 0, 0, false
	}
	return start, end, true
}

// PipeSegment is one command in a pipeline, with the span that would be removed
// to drop it — its leading "|" and preceding whitespace included.
type PipeSegment struct {
	Cmd   *parse.CommandNode
	Index int
	// DropStart and DropEnd delimit the span to delete to remove this segment.
	// Only meaningful for Index > 0: dropping the first command would leave a
	// pipeline with nothing to pipe from.
	DropStart, DropEnd int
	Droppable          bool
}

// SegmentsOf splits a pipeline into its commands and computes each one's
// removal span. Used by default-drop to excise `| default X` from a pipeline
// while leaving the rest of the expression untouched.
func SegmentsOf(f *File, pipe *parse.PipeNode) []PipeSegment {
	if isNilNode(pipe) || len(pipe.Cmds) == 0 {
		return nil
	}
	a, found := FindAction(f, int(pipe.Position()))
	if !found {
		return nil
	}

	// The pipeline's own end, ignoring trailing padding before the delimiter.
	tail := a.InnerEnd
	for tail > a.InnerStart && isSpace(f.Bytes[tail-1]) {
		tail--
	}

	out := make([]PipeSegment, 0, len(pipe.Cmds))
	for i, cmd := range pipe.Cmds {
		seg := PipeSegment{Cmd: cmd, Index: i}
		if i > 0 {
			seg.DropStart = barBefore(f, int(cmd.Position()), a.InnerStart)
			if i+1 < len(pipe.Cmds) {
				seg.DropEnd = barBefore(f, int(pipe.Cmds[i+1].Position()), a.InnerStart)
			} else {
				seg.DropEnd = tail
			}
			seg.Droppable = seg.DropStart > a.InnerStart && seg.DropEnd > seg.DropStart
		}
		out = append(out, seg)
	}
	return out
}

// barBefore scans back from a command's start to its "|" separator and then over
// the whitespace in front of it, so removing the segment leaves no double space.
func barBefore(f *File, from, limit int) int {
	i := from
	for i > limit && f.Bytes[i-1] != '|' {
		i--
	}
	if i <= limit {
		return -1
	}
	i-- // step onto the '|'
	for i > limit && isSpace(f.Bytes[i-1]) {
		i--
	}
	return i
}

// CommandArgSpan returns the source span of a command argument. Nodes carry a
// start position but no extent, so the length comes from the node's own
// recorded source text.
//
// FieldNode is deliberately unsupported. Its Position() does not point at the
// start of the field: for `.Values.deep.x` it reports the offset of
// `.deep.x`, i.e. just past the first segment. Treating that as a start offset
// yields a span shifted into the middle of the expression, so any mutator
// needing a field's extent must anchor on the enclosing CommandNode instead.
func CommandArgSpan(n parse.Node) (start, length int, ok bool) {
	if isNilNode(n) {
		return 0, 0, false
	}
	start = int(n.Position())
	switch v := n.(type) {
	case *parse.StringNode:
		return start, len(v.Quoted), true // Quoted is the original source form
	case *parse.NumberNode:
		return start, len(v.Text), true
	case *parse.IdentifierNode:
		return start, len(v.Ident), true
	case *parse.BoolNode:
		if v.True {
			return start, len("true"), true
		}
		return start, len("false"), true
	}
	return 0, 0, false
}

// SkipSpaceForward returns the first offset at or after from that is not
// whitespace, bounded by limit.
func SkipSpaceForward(f *File, from, limit int) int {
	i := from
	for i < limit && i < len(f.Bytes) && isSpace(f.Bytes[i]) {
		i++
	}
	return i
}

// ActionInnerEnd returns the end of the action containing offset, excluding the
// closing delimiter and any trim marker.
func ActionInnerEnd(f *File, offset int) (int, bool) {
	a, ok := FindAction(f, offset)
	if !ok {
		return 0, false
	}
	return a.InnerEnd, true
}

// SpanOfKeywordArgument finds the source span of a branch action's condition,
// i.e. everything after the keyword and before the closing delimiter.
//
// For `{{- if .Values.a.enabled }}` with keyword "if", it returns the span
// covering `.Values.a.enabled`, which cond-negate then wraps in `not (...)`.
func SpanOfKeywordArgument(f *File, actionOffset int, keyword string) (start, end int, ok bool) {
	a, found := FindAction(f, actionOffset)
	if !found {
		return 0, 0, false
	}
	inner := f.Slice(a.InnerStart, a.InnerEnd)

	// The keyword must be the first token, followed by whitespace.
	rest, cut := cutKeyword(inner, keyword)
	if !cut {
		return 0, 0, false
	}
	lead := len(inner) - len(rest)

	start = a.InnerStart + lead
	end = a.InnerEnd
	// Trim trailing whitespace so the wrapped expression stays tidy.
	for end > start && isSpace(f.Bytes[end-1]) {
		end--
	}
	if end <= start {
		return 0, 0, false
	}
	return start, end, true
}

// cutKeyword strips a leading keyword plus its following whitespace, reporting
// whether the keyword was actually there as a whole token.
func cutKeyword(s, keyword string) (rest string, ok bool) {
	i := 0
	for i < len(s) && isSpace(s[i]) {
		i++
	}
	if len(s)-i < len(keyword) || s[i:i+len(keyword)] != keyword {
		return s, false
	}
	j := i + len(keyword)
	if j >= len(s) || !isSpace(s[j]) {
		return s, false // e.g. "iffy", or a bare "{{if}}"
	}
	for j < len(s) && isSpace(s[j]) {
		j++
	}
	return s[j:], true
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// EnclosingPipelineSpan returns the span of the pipeline in the innermost action
// containing offset, excluding any leading control keyword.
//
// It is used to place the execution probe: replacing the pipeline with
// `fail "canary"` yields an expression that errors only when it is evaluated, so
// a render error proves the span executed. The keyword must survive — rewriting
// `{{- if X }}` to `{{ fail "canary" }}` would orphan the matching `{{ end }}`
// and produce a parse error, which happens whether or not the branch runs and
// therefore proves nothing.
func EnclosingPipelineSpan(f *File, offset int) (start, end int, ok bool) {
	a, found := FindAction(f, offset)
	if !found {
		return 0, 0, false
	}
	inner := f.Slice(a.InnerStart, a.InnerEnd)

	// Longest keyword first, so "else if" wins over a bare "if" prefix match.
	lead := 0
	for _, kw := range []string{"else with", "else if", "range", "with", "if"} {
		if rest, cut := cutKeyword(inner, kw); cut {
			lead = len(inner) - len(rest)
			break
		}
	}

	start = a.InnerStart + lead
	end = a.InnerEnd
	for start < end && isSpace(f.Bytes[start]) {
		start++
	}
	for end > start && isSpace(f.Bytes[end-1]) {
		end--
	}
	if start >= end {
		return 0, 0, false
	}

	// Bare block keywords (else, end, break, continue) have no pipeline to probe.
	// Overwriting them would orphan the block structure, yielding a parse error
	// that proves nothing about execution. Detect a single bare keyword by checking
	// if the remaining content is an identifier.
	for i := start; i < end; i++ {
		c := f.Bytes[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
			return start, end, true
		}
	}
	// Content is a single identifier: a bare keyword with no arguments.
	return 0, 0, false
}
