package source

import (
	"strings"
	"testing"
	"text/template/parse"
)

func TestParseTemplateAcceptsHelmBuiltins(t *testing.T) {
	// The whole point of parse.SkipFuncCheck: none of these functions exist in
	// text/template, and we must not have to reconstruct Helm's funcmap to parse.
	src := `
apiVersion: apps/v1
metadata:
  name: {{ include "chart.fullname" . }}
  labels: {{- toYaml .Values.labels | nindent 4 }}
spec:
  replicas: {{ .Values.replicaCount | default 3 }}
  token: {{ required "token is required" .Values.token | quote }}
  ver: {{ .Chart.AppVersion | trunc 63 | trimSuffix "-" }}
`
	if _, err := ParseTemplate(mk(t, src)); err != nil {
		t.Fatalf("ParseTemplate rejected Helm builtins: %v", err)
	}
}

func TestParseTemplateReturnsErrorOnBrokenTemplate(t *testing.T) {
	// A parse failure must be reported, not panic: callers downgrade to
	// line-based mutators and record the file as skipped.
	if _, err := ParseTemplate(mk(t, `{{ if .Values.x }}no end tag`)); err == nil {
		t.Fatal("expected a parse error for an unterminated if")
	}
}

func TestWalkVisitsDefineBlocks(t *testing.T) {
	// _helpers.tpl is nothing but define blocks. Walking only the main root would
	// find zero mutation sites in it.
	src := `{{- define "chart.name" -}}
{{ .Values.nameOverride | default "fallback" }}
{{- end -}}
{{- define "chart.other" -}}
{{ if .Values.flag }}yes{{ end }}
{{- end -}}`
	tree, err := ParseTemplate(mk(t, src))
	if err != nil {
		t.Fatal(err)
	}

	var strs []string
	var ifs int
	tree.Walk(func(n parse.Node) bool {
		switch node := n.(type) {
		case *parse.StringNode:
			strs = append(strs, node.Text)
		case *parse.IfNode:
			ifs++
		}
		return true
	})

	if len(strs) != 1 || strs[0] != "fallback" {
		t.Errorf("expected to find the string inside the first define, got %v", strs)
	}
	if ifs != 1 {
		t.Errorf("expected to find the if inside the second define, got %d", ifs)
	}
}

func TestWalkDoesNotVisitNodesTwice(t *testing.T) {
	tree, err := ParseTemplate(mk(t, `{{ if .Values.a }}x{{ end }}`))
	if err != nil {
		t.Fatal(err)
	}
	var ifs int
	tree.Walk(func(n parse.Node) bool {
		if _, ok := n.(*parse.IfNode); ok {
			ifs++
		}
		return true
	})
	if ifs != 1 {
		t.Errorf("if node visited %d times, want exactly 1", ifs)
	}
}

func TestWalkSkipsChildrenWhenFnReturnsFalse(t *testing.T) {
	tree, err := ParseTemplate(mk(t, `{{ if .Values.a }}{{ .Values.b }}{{ end }}`))
	if err != nil {
		t.Fatal(err)
	}
	var fields int
	tree.Walk(func(n parse.Node) bool {
		if _, ok := n.(*parse.IfNode); ok {
			return false // prune
		}
		if _, ok := n.(*parse.FieldNode); ok {
			fields++
		}
		return true
	})
	if fields != 0 {
		t.Errorf("pruning the if should hide its descendants, but found %d field nodes", fields)
	}
}

func TestFindAction(t *testing.T) {
	tests := []struct {
		name      string
		src       string
		wantInner string
	}{
		{"plain", `a: {{ .Values.x }}`, ".Values.x "},
		{"left trim", `a: {{- .Values.x }}`, ".Values.x "},
		{"right trim", `a: {{ .Values.x -}}`, ".Values.x "},
		{"both trims", `a: {{- .Values.x -}}`, ".Values.x "},
		{"if action", `{{- if .Values.enabled }}`, `if .Values.enabled `},
		{"closing braces in a string literal", `a: {{ printf "%s}}" .x }}`, `printf "%s}}" .x `},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := mk(t, tc.src)
			offset := strings.Index(tc.src, "{{") + 3
			a, ok := FindAction(f, offset)
			if !ok {
				t.Fatalf("FindAction found no action in %q", tc.src)
			}
			// Inner drops delimiters and trim markers but deliberately preserves
			// interior padding; trimming for comparison is the caller's job.
			if got := strings.TrimSpace(a.Inner(f)); got != strings.TrimSpace(tc.wantInner) {
				t.Errorf("Inner() = %q, want %q", got, strings.TrimSpace(tc.wantInner))
			}
			if strings.ContainsAny(a.Inner(f), "{}") && !strings.Contains(tc.src, `"`) {
				t.Errorf("Inner() leaked a delimiter or trim marker: %q", a.Inner(f))
			}
			if f.Slice(a.Start, a.Start+2) != "{{" {
				t.Errorf("action does not start at {{: %q", f.Slice(a.Start, a.Start+4))
			}
			if f.Slice(a.End-2, a.End) != "}}" {
				t.Errorf("action does not end at }}: %q", f.Slice(a.End-4, a.End))
			}
		})
	}
}

func TestFindActionRejectsOffsetsInPlainText(t *testing.T) {
	f := mk(t, "kind: Deployment\nname: {{ .Values.name }}\n")
	// Offset 3 is inside "kind:", which is plain text, not an action.
	if _, ok := FindAction(f, 3); ok {
		t.Error("FindAction should not claim plain text is inside an action")
	}
	// An offset after a closed action is also plain text.
	if _, ok := FindAction(f, len(f.Bytes)-1); ok {
		t.Error("FindAction should not match text after the closing }}")
	}
}

func TestSpanOfKeywordArgument(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		keyword string
		want    string
	}{
		{"simple if", `{{ if .Values.enabled }}`, "if", ".Values.enabled"},
		{"trimmed if", `{{- if .Values.enabled }}`, "if", ".Values.enabled"},
		{"both trims", `{{- if .Values.enabled -}}`, "if", ".Values.enabled"},
		{"compound condition", `{{- if and .Values.a .Values.b }}`, "if", "and .Values.a .Values.b"},
		{"eq condition", `{{ if eq .Values.env "prod" }}`, "if", `eq .Values.env "prod"`},
		{"with", `{{- with .Values.resources }}`, "with", ".Values.resources"},
		{"else if", `{{- else if .Values.other }}`, "else", `if .Values.other`},
		{"no padding", `{{if .Values.x}}`, "if", ".Values.x"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := mk(t, tc.src)
			start, end, ok := SpanOfKeywordArgument(f, strings.Index(tc.src, "{{")+2, tc.keyword)
			if !ok {
				t.Fatalf("SpanOfKeywordArgument(%q, %q) failed", tc.src, tc.keyword)
			}
			if got := f.Slice(start, end); got != tc.want {
				t.Errorf("span = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSpanOfKeywordArgumentRejectsWrongKeyword(t *testing.T) {
	f := mk(t, `{{ range .Values.list }}`)
	if _, _, ok := SpanOfKeywordArgument(f, 2, "if"); ok {
		t.Error("asking for 'if' in a range action should not match")
	}
	// "iffy" must not be mistaken for the "if" keyword.
	f2 := mk(t, `{{ iffy .Values.x }}`)
	if _, _, ok := SpanOfKeywordArgument(f2, 2, "if"); ok {
		t.Error("'iffy' must not match the keyword 'if'")
	}
}

func TestSpanOfKeywordArgumentRejectsBareKeyword(t *testing.T) {
	// "{{ end }}" has no argument to negate.
	f := mk(t, `{{ end }}`)
	if _, _, ok := SpanOfKeywordArgument(f, 2, "end"); ok {
		t.Error("a keyword with no argument should not yield a span")
	}
}

func TestSpanOfKeywordArgumentWrapsCorrectly(t *testing.T) {
	// The end-to-end contract cond-negate depends on: replacing the returned span
	// with "not (span)" must produce a valid, still-trimmed action.
	src := "{{- if .Values.ingress.enabled }}\nhost: x\n{{- end }}"
	f := mk(t, src)
	start, end, ok := SpanOfKeywordArgument(f, 2, "if")
	if !ok {
		t.Fatal("no span found")
	}
	got := string(f.Apply(start, end, "not ("+f.Slice(start, end)+")"))
	want := "{{- if not (.Values.ingress.enabled) }}\nhost: x\n{{- end }}"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	if _, err := ParseTemplate(mk(t, got)); err != nil {
		t.Errorf("the mutated template must still parse: %v", err)
	}
}

// TestFieldNodePositionIsNotTheFieldStart pins down a text/template quirk that
// silently corrupts edits if assumed away: a FieldNode's Position() points just
// past its first segment, not at the leading dot. CommandArgSpan therefore
// refuses FieldNode rather than returning a shifted span.
func TestFieldNodePositionIsNotTheFieldStart(t *testing.T) {
	f := mk(t, `{{ .Values.token }}`)
	tree, err := ParseTemplate(f)
	if err != nil {
		t.Fatal(err)
	}
	var field *parse.FieldNode
	tree.Walk(func(n parse.Node) bool {
		if fn, ok := n.(*parse.FieldNode); ok {
			field = fn
		}
		return true
	})
	if field == nil {
		t.Fatal("no FieldNode found")
	}
	if got := f.Slice(int(field.Position()), len(f.Bytes)); !strings.HasPrefix(got, ".token") {
		t.Fatalf("this test encodes the quirk; Position() now yields %q", got)
	}
	if _, _, ok := CommandArgSpan(field); ok {
		t.Error("CommandArgSpan must refuse FieldNode: its position is not the field start")
	}
}

func TestCommandArgSpanSupportedNodes(t *testing.T) {
	f := mk(t, `{{ printf "lit" 42 true }}`)
	tree, err := ParseTemplate(f)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	tree.Walk(func(n parse.Node) bool {
		start, length, ok := CommandArgSpan(n)
		if !ok {
			return true
		}
		switch n.(type) {
		case *parse.StringNode:
			found["string"] = f.Slice(start, start+length)
		case *parse.NumberNode:
			found["number"] = f.Slice(start, start+length)
		case *parse.BoolNode:
			found["bool"] = f.Slice(start, start+length)
		case *parse.IdentifierNode:
			found["ident"] = f.Slice(start, start+length)
		}
		return true
	})
	want := map[string]string{"string": `"lit"`, "number": "42", "bool": "true", "ident": "printf"}
	for k, v := range want {
		if found[k] != v {
			t.Errorf("%s span = %q, want %q", k, found[k], v)
		}
	}
}

func TestSegmentsOfComputesDropSpans(t *testing.T) {
	f := mk(t, `x: {{ .Values.a | default "y" | quote }}`)
	tree, err := ParseTemplate(f)
	if err != nil {
		t.Fatal(err)
	}
	var segs []PipeSegment
	tree.Walk(func(n parse.Node) bool {
		if p, ok := n.(*parse.PipeNode); ok {
			segs = SegmentsOf(f, p)
		}
		return true
	})
	if len(segs) != 3 {
		t.Fatalf("got %d segments, want 3", len(segs))
	}
	if segs[0].Droppable {
		t.Error("the first command must not be droppable: nothing would feed the pipeline")
	}
	// Dropping the middle segment leaves the rest of the pipeline intact.
	if got := string(f.Apply(segs[1].DropStart, segs[1].DropEnd, "")); got != `x: {{ .Values.a | quote }}` {
		t.Errorf("dropping segment 1 gave %q", got)
	}
	if got := string(f.Apply(segs[2].DropStart, segs[2].DropEnd, "")); got != `x: {{ .Values.a | default "y" }}` {
		t.Errorf("dropping segment 2 gave %q", got)
	}
}

func TestSkipQuoted(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		advance bool
	}{
		{"double quoted", `"abc"`, true},
		{"escaped quote inside", `"a\"b"`, true},
		{"backtick raw", "`a\"b`", true},
		{"unterminated", `"abc`, false},
		{"newline before close", "\"abc\ndef\"", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := skipQuoted([]byte(tc.src), 0)
			if tc.advance && got <= 0 {
				t.Errorf("skipQuoted(%q) = %d, expected it to advance past the close quote", tc.src, got)
			}
			if !tc.advance && got != 0 {
				t.Errorf("skipQuoted(%q) = %d, expected 0 for an unterminated run", tc.src, got)
			}
		})
	}
}

// TestProbeSpanPreservesVariableDeclarations guards a score-inflating bug. The
// probe replaces a span with `fail "canary"`, which errors only when evaluated —
// so a render error is evidence the span ran. Overwriting a "$k, $v :=" prefix
// leaves the body's variables undeclared and the probe fails to *parse* instead,
// which happens whether or not the code path runs. The checker cannot tell those
// apart: it reads any difference from the original as proof of execution, so an
// unparseable probe would promote a killable mutant to Equivalent.
func TestProbeSpanPreservesVariableDeclarations(t *testing.T) {
	tests := []struct {
		name, src, mutated, wantSpan string
	}{
		{
			"range with two declarations",
			"{{- range $k, $v := .Values.labels }}\n{{ $k }}: {{ $v }}\n{{- end }}",
			".Values.labels", ".Values.labels",
		},
		{
			"with and one declaration",
			"{{- with $cfg := .Values.config }}\nkey: {{ $cfg.a }}\n{{- end }}",
			".Values.config", ".Values.config",
		},
		{
			// No keyword at all — what helm create scaffolds into every chart.
			"bare declaration action",
			"{{- $fullName := include \"c.fullname\" . -}}\nname: {{ $fullName }}\n",
			"include", `include "c.fullname" .`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := mk(t, tc.src)
			lo := strings.Index(tc.src, tc.mutated)
			if lo < 0 {
				t.Fatalf("needle %q not in source", tc.mutated)
			}
			ps, pe, ok := ProbeSpan(f, lo, lo+len(tc.mutated))
			if !ok {
				t.Fatal("ProbeSpan returned !ok")
			}
			if got := f.Slice(ps, pe); got != tc.wantSpan {
				t.Fatalf("probe span = %q, want %q", got, tc.wantSpan)
			}
			// The whole point: the probed template must still parse, or the render
			// error proves nothing about execution.
			probed := f.Apply(ps, pe, `fail "canary"`)
			if _, err := ParseTemplate(New(probed, f.AbsPath, f.Path, f.Kind)); err != nil {
				t.Fatalf("probed template does not parse: %v\n%s", err, probed)
			}
		})
	}
}

func TestEnclosingPipelineSpanKeepsKeywordsAndTrimMarkers(t *testing.T) {
	// The probe replaces a pipeline with `fail "canary"`, which errors only when
	// evaluated. Overwriting the keyword too would break block structure and turn
	// a runtime signal into a parse error, which proves nothing about execution.
	tests := []struct {
		name string
		src  string
		// needle is a substring of src; the span is looked up at its offset.
		needle  string
		wantSub string
	}{
		{"plain action", `x: {{ .Values.a }}`, ".Values.a", ".Values.a"},
		{"trimmed action", "x: {{- .Values.a -}}\n", ".Values.a", ".Values.a"},
		{"pipeline", `x: {{ include "c.name" . | nindent 4 }}`, "nindent", `include "c.name" . | nindent 4`},
		{"if", "{{- if .Values.a.enabled }}\nx: 1\n{{- end }}", ".Values.a.enabled", ".Values.a.enabled"},
		{"with", "{{- with .Values.a }}\nx: 1\n{{- end }}", ".Values.a", ".Values.a"},
		{"range", "{{- range .Values.list }}\n- {{ . }}\n{{- end }}", ".Values.list", ".Values.list"},
		{"else if", "{{- if .A }}\n{{- else if .Values.b }}\nx: 1\n{{- end }}", ".Values.b", ".Values.b"},
		{"range with declarations", "{{- range $k, $v := .Values.labels }}\n{{ $k }}\n{{- end }}", ".Values.labels", ".Values.labels"},
		{"with declaration", "{{- with $cfg := .Values.a }}\nx: 1\n{{- end }}", ".Values.a", ".Values.a"},
		{"declaration action", "{{- $n := include \"c.name\" . -}}\n", "include", `include "c.name" .`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := mk(t, tc.src)
			off := strings.Index(tc.src, tc.needle)
			if off < 0 {
				t.Fatalf("needle %q not in source", tc.needle)
			}
			start, end, ok := EnclosingPipelineSpan(f, off)
			if !ok {
				t.Fatal("EnclosingPipelineSpan returned !ok")
			}
			if got := f.Slice(start, end); got != tc.wantSub {
				t.Fatalf("span = %q, want %q", got, tc.wantSub)
			}
		})
	}
}

func TestEnclosingPipelineSpanRefusesPlainText(t *testing.T) {
	// A yaml-key-delete span in a template's literal text has no action to probe.
	src := "metadata:\n  name: plain\n"
	f := mk(t, src)
	if _, _, ok := EnclosingPipelineSpan(f, strings.Index(src, "plain")); ok {
		t.Fatal("want !ok for an offset outside any action")
	}
}

func TestEnclosingPipelineSpanRefusesBareKeywords(t *testing.T) {
	// A bare block keyword (else, end, break, continue) has no pipeline to probe.
	// Overwriting it with `fail "canary"` would orphan the block structure and
	// produce a parse error, which happens whether or not the code path runs and
	// therefore proves nothing about execution. Detecting equivalence requires proof
	// that the span was actually evaluated — a probe that errors unconditionally
	// proves nothing.
	tests := []struct {
		name   string
		src    string
		needle string
	}{
		{"else keyword", "{{- if .A }}\n{{ .B }}\n{{- else }}\n{{ .C }}\n{{- end }}", "else }}"},
		{"end keyword", "{{- if .A }}{{ .B }}{{- end }}", "end }}"},
		{"end trimmed", "{{- if .A }}{{ .B }}{{- end -}}", "end -}}"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := mk(t, tc.src)
			off := strings.Index(tc.src, tc.needle)
			if off < 0 {
				t.Fatalf("needle %q not in source", tc.needle)
			}
			if _, _, ok := EnclosingPipelineSpan(f, off); ok {
				t.Fatalf("EnclosingPipelineSpan should return !ok for a bare keyword")
			}
		})
	}

	// Verify that else if with a condition still returns the condition span.
	f := mk(t, "{{- if .A }}\n{{ .B }}\n{{- else if .Values.enabled }}\n{{ .C }}\n{{- end }}")
	off := strings.Index(f.Text(), ".Values.enabled")
	if start, end, ok := EnclosingPipelineSpan(f, off); !ok {
		t.Fatal("EnclosingPipelineSpan should work for else if with a condition")
	} else if got := f.Slice(start, end); got != ".Values.enabled" {
		t.Errorf("else if condition span = %q, want %q", got, ".Values.enabled")
	}
}

// TestProbeSpanNarrowsInsideAShortCircuit is the span-level half of the guarantee
// that an unexercised operand never passes for an unkillable mutant. Go templates
// short-circuit and/or, so a probe covering more than the mutated operand would
// error on reaching the action rather than on evaluating the operand, and the
// difference is exactly "missing test" versus "equivalent mutant".
func TestProbeSpanNarrowsInsideAShortCircuit(t *testing.T) {
	tests := []struct {
		name string
		src  string
		// needle locates the mutation span, which is the needle itself.
		needle string
		// wantSpan is the source the probe should overwrite, or "" for no probe.
		wantSpan string
	}{
		{
			"guarded operand narrows to its own sub-pipeline",
			`{{- if and .Values.a (eq .Values.b "x") }}y{{- end }}`,
			`"x"`, `eq .Values.b "x"`,
		},
		{
			"guarded operator narrows to its own sub-pipeline",
			`{{- if and .Values.a (eq .Values.b "x") }}y{{- end }}`,
			`eq`, `eq .Values.b "x"`,
		},
		{
			"nested short circuit narrows to the innermost sub-pipeline",
			`{{- if or .Values.a (and .Values.b (ne .Values.c "x")) }}y{{- end }}`,
			`"x"`, `ne .Values.c "x"`,
		},
		{
			"bare guarded operand has no probe",
			`{{- if and .Values.a (eq 1 2) }}y{{- end }}`,
			`(eq 1 2)`, "",
		},
		{
			"sub-pipeline that is itself the guarded call has no probe",
			`{{- if or .Values.a (and .Values.b 3) }}y{{- end }}`,
			`3`, "",
		},
		{
			"first operand is always evaluated, so the whole pipeline still works",
			`{{- if and (eq .Values.b "x") .Values.a }}y{{- end }}`,
			`"x"`, `and (eq .Values.b "x") .Values.a`,
		},
		{
			"a mutation covering the whole and keeps the whole pipeline",
			`{{- if and .Values.a .Values.b }}y{{- end }}`,
			`and .Values.a .Values.b`, `and .Values.a .Values.b`,
		},
		{
			"no short circuit leaves the pipeline span alone",
			`x: {{ eq .Values.b "x" }}`,
			`"x"`, `eq .Values.b "x"`,
		},
		{
			"a parenthesised operand outside any short circuit keeps the pipeline",
			`x: {{ printf "%s" (upper .Values.b) }}`,
			`.Values.b`, `printf "%s" (upper .Values.b)`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := mk(t, tc.src)
			off := strings.Index(tc.src, tc.needle)
			if off < 0 {
				t.Fatalf("needle %q not in source", tc.needle)
			}
			start, end, ok := ProbeSpan(f, off, off+len(tc.needle))
			if tc.wantSpan == "" {
				if ok {
					t.Fatalf("want no probe span, got %q", f.Slice(start, end))
				}
				return
			}
			if !ok {
				t.Fatal("ProbeSpan returned !ok")
			}
			if got := f.Slice(start, end); got != tc.wantSpan {
				t.Fatalf("probe span = %q, want %q", got, tc.wantSpan)
			}
		})
	}
}

// TestProbeSpanRefusesUnparseableTemplates keeps the pass conservative: with no
// parse tree there is no way to know whether a short circuit guards the span, so
// the wide probe cannot be claimed sound.
func TestProbeSpanRefusesUnparseableTemplates(t *testing.T) {
	src := `x: {{ .Values.a }}` + "\n{{ if .Values.b }}no end tag\n"
	f := mk(t, src)
	off := strings.Index(src, ".Values.a")
	if _, _, ok := ProbeSpan(f, off, off+len(".Values.a")); ok {
		t.Fatal("want !ok when the template does not parse")
	}
}

// TestMutationPrecedesUndelimitedCall pins the soundness argument ProbeSpan
// relies on when shortCircuitGuards cannot delimit an and/or call: operands
// always start at or after the call's id token, so a mutation strictly before
// that token is provably outside the call and safe to probe widely, while
// anything at or after it might sit in a skipped operand and must be refused.
//
// commandExtent/operandEnd failing on a real and/or is not reachable through
// any template a reviewer could construct — FindAction refuses first — so this
// exercises the position comparison directly rather than contorting a template
// to reach it.
func TestMutationPrecedesUndelimitedCall(t *testing.T) {
	tests := []struct {
		name         string
		start, idPos int
		want         bool
	}{
		{"mutation strictly before the id token: outside the call, probe stands", 5, 10, true},
		{"mutation at the id token: may be inside the call, refuse", 10, 10, false},
		{"mutation after the id token: may be inside the call, refuse", 15, 10, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mutationPrecedesUndelimitedCall(tc.start, tc.idPos); got != tc.want {
				t.Fatalf("mutationPrecedesUndelimitedCall(%d, %d) = %v, want %v", tc.start, tc.idPos, got, tc.want)
			}
		})
	}
}

// TestCutDeclarations pins the lexical rule that separates a pipeline's variable
// declarations from the expression after them. It matters in two places: a mutant
// that drops `$k, $v :=` leaves the body's variables undeclared and no longer
// parses, and the equivalence probe doing the same thing fails to parse for a
// reason unrelated to execution — which the checker reads as proof of execution.
func TestCutDeclarations(t *testing.T) {
	tests := []struct {
		name, in, wantRest string
		wantCut            bool
	}{
		{"single variable", "$x := .Values.a", ".Values.a", true},
		{"variable pair", "$k, $v := .Values.m", ".Values.m", true},
		{"generous spacing", "$k ,  $v   :=   .Values.m", ".Values.m", true},
		{"pipeline after the declaration", "$x := .Values.a | default 3", ".Values.a | default 3", true},
		{"no declaration at all", ".Values.a", ".Values.a", false},
		{"the dollar is the expression", "$items", "$items", false},
		{"root dollar", "$.Values.list", "$.Values.list", false},
		// A ":=" inside a string literal must never be read as a declaration; the
		// scan stops at the first byte that cannot appear in one, which is the ".".
		{"assignment inside a string literal", `.Values.x | replace ":=" "-"`, `.Values.x | replace ":=" "-"`, false},
		// Starts with "$", unlike the case above, so this pins that the scanner
		// keeps going through the string literal instead of stopping at byte 0.
		{"a variable then a string-literal assignment", `$items | replace ":=" "-"`, `$items | replace ":=" "-"`, false},
		{"declaration with nothing after it", "$x :=", "$x :=", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rest, cut := cutDeclarations(tc.in)
			if cut != tc.wantCut {
				t.Fatalf("cut = %v, want %v", cut, tc.wantCut)
			}
			if rest != tc.wantRest {
				t.Fatalf("rest = %q, want %q", rest, tc.wantRest)
			}
		})
	}
}

// rangePipe returns the pipeline of the first RangeNode in f.
func rangePipe(t *testing.T, f *File) *parse.PipeNode {
	t.Helper()
	tree, err := ParseTemplate(f)
	if err != nil {
		t.Fatalf("ParseTemplate: %v", err)
	}
	var pipe *parse.PipeNode
	tree.Walk(func(n parse.Node) bool {
		if rng, ok := n.(*parse.RangeNode); ok && pipe == nil {
			pipe = rng.Pipe
		}
		return true
	})
	if pipe == nil {
		t.Fatal("no RangeNode in source")
	}
	return pipe
}

// TestSpanOfRangedExpressionExcludesDeclarations: RangeNode's Pipe.Position() is
// the offset of "$k", not of the expression being ranged over. Treating it as the
// expression start produces a mutant that does not parse.
func TestSpanOfRangedExpressionExcludesDeclarations(t *testing.T) {
	tests := []struct {
		name, src, want string
	}{
		{"no declaration", "{{- range .Values.ports }}\n- {{ . }}\n{{- end }}", ".Values.ports"},
		{"one declaration", "{{- range $v := .Values.ports }}\n- {{ $v }}\n{{- end }}", ".Values.ports"},
		{"two declarations", "{{- range $k, $v := .Values.labels }}\n{{ $k }}\n{{- end }}", ".Values.labels"},
		{"piped expression", "{{- range .Values.x | sortAlpha }}\n- {{ . }}\n{{- end }}", ".Values.x | sortAlpha"},
		{"root dollar is the expression", "{{- range $.Values.list }}\n- {{ . }}\n{{- end }}", "$.Values.list"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := mk(t, tc.src)
			start, end, ok := SpanOfRangedExpression(f, rangePipe(t, f))
			if !ok {
				t.Fatal("SpanOfRangedExpression returned !ok")
			}
			if got := f.Slice(start, end); got != tc.want {
				t.Fatalf("span = %q, want %q", got, tc.want)
			}
		})
	}
}
