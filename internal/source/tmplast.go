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

// cutDeclarations strips a pipeline's leading variable declarations — "$x := "
// or "$k, $v := " — reporting whether one was there.
//
// Two callers need this. A mutator replacing the whole pipeline of
// {{ range $k, $v := .Values.m }} leaves $k and $v undeclared, and text/template
// rejects that at parse time, so the mutant would be Invalid and grade nothing.
// The equivalence probe doing the same thing is worse: an unparseable probe fails
// to render for a reason unrelated to execution, and the checker reads any
// difference between original and probe as proof the span executed — which would
// exclude a killable mutant from the score.
//
// The scan is lexical and stops at the first byte that cannot appear in a
// declaration, so a ":=" inside a string literal is never mistaken for one.
func cutDeclarations(s string) (rest string, cut bool) {
	i := 0
	for {
		for i < len(s) && isSpace(s[i]) {
			i++
		}
		if i >= len(s) || s[i] != '$' {
			return s, false
		}
		i++
		for i < len(s) && isIdentByte(s[i]) {
			i++
		}
		for i < len(s) && isSpace(s[i]) {
			i++
		}
		if i < len(s) && s[i] == ',' {
			i++
			continue
		}
		break
	}
	if i+1 >= len(s) || s[i] != ':' || s[i+1] != '=' {
		return s, false
	}
	i += 2
	for i < len(s) && isSpace(s[i]) {
		i++
	}
	if i >= len(s) {
		return s, false // "$x :=" with no expression is not something to cut
	}
	return s[i:], true
}

func isIdentByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// SpanOfRangedExpression returns the span of the expression a range iterates
// over, excluding any "$k, $v :=" declaration prefix.
//
// The start is recovered by suffix length rather than index arithmetic, because
// cutDeclarations returns a suffix of the slice it was given and there is then no
// offset to get wrong.
func SpanOfRangedExpression(f *File, pipe *parse.PipeNode) (start, end int, ok bool) {
	start, end, ok = SpanOfCondition(f, pipe)
	if !ok {
		return 0, 0, false
	}
	if rest, cut := cutDeclarations(f.Slice(start, end)); cut {
		start = end - len(rest)
	}
	if start >= end {
		return 0, 0, false
	}
	return start, end, true
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

	// A declaration must survive the probe. Overwriting "$k, $v :=" leaves the
	// body's variables undeclared, so the probed template no longer parses — and a
	// parse failure is a difference the checker reads as proof of execution, which
	// would exclude a killable mutant from the score. The keyword cut above is
	// independent, so this also covers a bare declaration action with no keyword,
	// such as the {{- $fullName := include "chart.fullname" . -}} that helm create
	// scaffolds into every chart.
	if rest, cut := cutDeclarations(inner[lead:]); cut {
		lead = len(inner) - len(rest)
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

// ProbeSpan returns the span an execution probe must overwrite to prove that the
// mutation at [start,end) was evaluated.
//
// Overwriting the whole enclosing pipeline only proves that much when reaching
// the action implies evaluating the mutated sub-expression. Go templates
// short-circuit `and` and `or`, so in
//
//	{{- if and .Values.ingress.enabled (eq .Values.ingress.className "nginx") }}
//
// with ingress.enabled false under every context, the `eq` never runs — yet a
// probe over the whole `and ...` pipeline still errors, proving the action was
// reached and nothing at all about the mutated operand. Believing it reclassifies
// a mutant that a missing test would have killed as unkillable, which raises the
// score.
//
// So when the mutation sits behind a short circuit the span narrows to the
// innermost parenthesised sub-pipeline containing it: `(fail "canary")` in an
// operand position parses, and errors only when that operand is actually
// evaluated. When nothing isolates the mutation — a bare unparenthesised operand,
// or a template that will not parse — this reports false and the caller must
// leave the mutant survived. No probe is better than a probe that proves the
// wrong thing.
func ProbeSpan(f *File, start, end int) (ps, pe int, ok bool) {
	ps, pe, ok = EnclosingPipelineSpan(f, start)
	if !ok {
		return 0, 0, false
	}
	tree, err := ParseTemplate(f)
	if err != nil {
		// Without a parse tree we cannot see whether a short circuit guards the
		// span, so we cannot claim the wide probe is sound.
		return 0, 0, false
	}

	allGuards, undelimited := shortCircuitGuards(f, tree)
	for _, idPos := range undelimited {
		if !mutationPrecedesUndelimitedCall(start, idPos) {
			return 0, 0, false
		}
	}

	var guards []shortCircuitGuard
	for _, g := range allGuards {
		if g.covers(start, end) {
			guards = append(guards, g)
		}
	}
	if len(guards) == 0 {
		return ps, pe, true
	}

	sub, found := innermostParenPipeline(f, tree, start, end)
	if !found || sub.start < ps || sub.end > pe {
		return 0, 0, false
	}
	// The narrowed span must sit strictly inside every short circuit guarding the
	// mutation. Equal spans mean the sub-pipeline *is* the guarded call — probing
	// it would reproduce the very flaw this function exists to avoid.
	for _, g := range guards {
		if !g.call.strictlyCovers(sub) {
			return 0, 0, false
		}
	}
	return sub.start, sub.end, true
}

// byteSpan is a half-open byte range into a File.
type byteSpan struct{ start, end int }

func (s byteSpan) covers(start, end int) bool { return start >= s.start && end <= s.end }

// strictlyCovers reports whether s contains o and is strictly wider.
func (s byteSpan) strictlyCovers(o byteSpan) bool {
	return o.start >= s.start && o.end <= s.end && (o.start > s.start || o.end < s.end)
}

// shortCircuitGuard is one `and`/`or` call: the span of the operands it may skip,
// plus the span of the whole call those operands belong to.
type shortCircuitGuard struct {
	byteSpan // the skippable region: from the end of the first operand to the call's end
	call     byteSpan
}

// shortCircuitGuards returns every region of the template that an `and` or `or`
// may decline to evaluate, plus the id-token position of every `and`/`or` call it
// found but could not delimit. They are the only text/template constructs that
// skip an argument: both stop at the operand that decides the result, so anything
// from the second operand onward may never run even when the surrounding action
// does. The first operand is always evaluated once the call is, and so is
// excluded — otherwise a mutation of the whole condition would lose its probe for
// no reason.
//
// An undelimitable call is reported rather than silently dropped: ProbeSpan
// cannot tell whether the mutation it was asked about sits in that call's
// skippable region, and omitting it here would let the wide pipeline probe stand
// unchallenged — the exact unsoundness this function exists to prevent.
func shortCircuitGuards(f *File, t *Tree) (guards []shortCircuitGuard, undelimited []int) {
	t.Walk(func(n parse.Node) bool {
		cmd, ok := n.(*parse.CommandNode)
		if !ok || isNilNode(cmd) || len(cmd.Args) < 3 {
			return true // fewer than two operands: nothing can be skipped
		}
		id, ok := cmd.Args[0].(*parse.IdentifierNode)
		if !ok || (id.Ident != "and" && id.Ident != "or") {
			return true
		}
		callStart, callEnd, ok := commandExtent(f, int(id.Position()))
		if !ok {
			undelimited = append(undelimited, int(id.Position()))
			return true
		}
		firstOperand := SkipSpaceForward(f, callStart+len(id.Ident), callEnd)
		skipFrom, ok := operandEnd(f, firstOperand, callEnd)
		if !ok {
			undelimited = append(undelimited, int(id.Position()))
			return true
		}
		guards = append(guards, shortCircuitGuard{
			byteSpan: byteSpan{skipFrom, callEnd},
			call:     byteSpan{callStart, callEnd},
		})
		return true
	})
	return guards, undelimited
}

// mutationPrecedesUndelimitedCall reports whether a mutation at start is provably
// outside the and/or call whose id token sits at idPos, when that call's extent
// could not be established.
//
// In `and X Y` / `or X Y` every operand appears after the id token, so a mutation
// starting strictly before idPos cannot be inside the call regardless of where
// the call actually ends — it is safe to ignore. A mutation at or after idPos
// might sit in an operand the call could skip, and with no delimited extent to
// check that against, ProbeSpan must refuse rather than guess.
func mutationPrecedesUndelimitedCall(start, idPos int) bool {
	return start < idPos
}

// innermostParenPipeline returns the content span of the narrowest parenthesised
// sub-pipeline containing [start,end).
//
// A PipeNode in a command's argument list is always a parenthesised
// sub-pipeline, and unlike FieldNode its position is the true offset of its first
// token even when that token is a field.
func innermostParenPipeline(f *File, t *Tree, start, end int) (byteSpan, bool) {
	var best byteSpan
	found := false
	t.Walk(func(n parse.Node) bool {
		cmd, ok := n.(*parse.CommandNode)
		if !ok || isNilNode(cmd) {
			return true
		}
		for _, arg := range cmd.Args {
			pipe, ok := arg.(*parse.PipeNode)
			if !ok || isNilNode(pipe) {
				continue
			}
			s, e, ok := parenPipelineSpan(f, int(pipe.Position()))
			if !ok || !(byteSpan{s, e}).covers(start, end) {
				continue
			}
			if !found || e-s < best.end-best.start {
				best, found = byteSpan{s, e}, true
			}
		}
		return true
	})
	return best, found
}

// parenPipelineSpan returns the content span of the parenthesised sub-pipeline
// whose first token starts at innerStart: everything up to the matching ")".
func parenPipelineSpan(f *File, innerStart int) (start, end int, ok bool) {
	limit, found := ActionInnerEnd(f, innerStart)
	if !found || innerStart >= limit {
		return 0, 0, false
	}
	end = scanArgList(f, innerStart, limit, false)
	if end >= limit {
		return 0, 0, false // no closing ")" inside this action
	}
	for end > innerStart && isSpace(f.Bytes[end-1]) {
		end--
	}
	if end <= innerStart {
		return 0, 0, false
	}
	return innerStart, end, true
}

// commandExtent returns the source span of one command in a pipeline: its first
// argument through whichever comes first — the ")" closing an enclosing
// parenthesised sub-pipeline, a "|" at the same nesting depth, or the end of the
// action. Parse nodes record a start but no extent, so the end comes from a scan
// over the original bytes.
func commandExtent(f *File, start int) (int, int, bool) {
	limit, found := ActionInnerEnd(f, start)
	if !found || start >= limit {
		return 0, 0, false
	}
	end := scanArgList(f, start, limit, true)
	for end > start && isSpace(f.Bytes[end-1]) {
		end--
	}
	if end <= start {
		return 0, 0, false
	}
	return start, end, true
}

// operandEnd returns the offset just past the single command argument starting at
// from, reporting false for anything it cannot delimit confidently.
func operandEnd(f *File, from, limit int) (int, bool) {
	if from >= limit {
		return 0, false
	}
	switch c := f.Bytes[from]; {
	case c == '(':
		end := scanArgList(f, from+1, limit, false)
		if end >= limit {
			return 0, false
		}
		return end + 1, true
	case c == '"' || c == '\'' || c == '`':
		j := skipQuoted(f.Bytes, from)
		if j <= from {
			return 0, false
		}
		return j + 1, true
	}
	i := from
	for i < limit && !isSpace(f.Bytes[i]) && f.Bytes[i] != ')' && f.Bytes[i] != '|' {
		i++
	}
	if i == from {
		return 0, false
	}
	return i, true
}

// scanArgList walks forward to the end of an argument list: the first ")" not
// matched by a "(" after from, or a "|" at nesting depth zero when stopAtBar is
// set, bounded by limit. Quoted strings are skipped so a bracket or bar inside a
// literal does not end the scan early.
func scanArgList(f *File, from, limit int, stopAtBar bool) int {
	depth := 0
	for i := from; i < limit; i++ {
		switch f.Bytes[i] {
		case '"', '\'', '`':
			if j := skipQuoted(f.Bytes, i); j > i {
				i = j
			}
		case '(':
			depth++
		case ')':
			if depth == 0 {
				return i
			}
			depth--
		case '|':
			if stopAtBar && depth == 0 {
				return i
			}
		}
	}
	return limit
}
