package mutator

import (
	"slices"
	"strings"
	"testing"
	"text/template/parse"

	"github.com/mmpyro/helm-mutation-test/internal/source"
)

func tmpl(content string) *source.File {
	return source.New([]byte(content), "/c/templates/x.yaml", "templates/x.yaml", source.KindTemplate)
}

func vals(content string) *source.File {
	return source.New([]byte(content), "/c/values.yaml", "values.yaml", source.KindValues)
}

// applied returns the full mutated file for each candidate, so tests assert on
// real resulting source rather than on internal offsets.
func applied(f *source.File, cands []Candidate) []string {
	out := make([]string, len(cands))
	for i, c := range cands {
		out[i] = string(f.Apply(c.Start, c.End, c.Replacement))
	}
	return out
}

// spans returns the original text each candidate replaces.
func spans(f *source.File, cands []Candidate) []string {
	out := make([]string, len(cands))
	for i, c := range cands {
		out[i] = f.Slice(c.Start, c.End)
	}
	return out
}

func mustRun(t *testing.T, id string, f *source.File) []Candidate {
	t.Helper()
	m, ok := Get(id)
	if !ok {
		t.Fatalf("mutator %q is not registered", id)
	}
	return m.Mutate(f)
}

// ---------- registry ----------

func TestRegistryHasAllNineMutators(t *testing.T) {
	want := []string{
		IDBoolFlip, IDComparisonSwap, IDCondNegate, IDDefaultDrop,
		IDNumLiteral, IDRangeEmpty, IDRequiredDrop, IDStrLiteral, IDYAMLKeyDelete,
	}
	got := IDs()
	if len(got) != len(want) {
		t.Fatalf("registry has %d mutators (%v), want %d", len(got), got, len(want))
	}
	for _, id := range want {
		if !slices.Contains(got, id) {
			t.Errorf("mutator %q is missing from the registry", id)
		}
	}
	if !slices.IsSorted(got) {
		t.Errorf("IDs() must be sorted for deterministic output, got %v", got)
	}
}

func TestSelectIsDeterministicRegardlessOfInputOrder(t *testing.T) {
	a := Select([]string{IDStrLiteral, IDBoolFlip, IDCondNegate})
	b := Select([]string{IDCondNegate, IDStrLiteral, IDBoolFlip})
	if len(a) != 3 || len(b) != 3 {
		t.Fatalf("expected 3 mutators, got %d and %d", len(a), len(b))
	}
	for i := range a {
		if a[i].ID() != b[i].ID() {
			t.Fatalf("Select is order-dependent: %s vs %s", a[i].ID(), b[i].ID())
		}
	}
}

func TestSelectSkipsUnknownIDs(t *testing.T) {
	if got := Select([]string{"nope", IDBoolFlip}); len(got) != 1 || got[0].ID() != IDBoolFlip {
		t.Fatalf("Select should skip unknown IDs, got %v", got)
	}
}

func TestEveryMutatorHasADescription(t *testing.T) {
	for _, id := range IDs() {
		m, _ := Get(id)
		if strings.TrimSpace(m.Describe()) == "" {
			t.Errorf("mutator %q has no description", id)
		}
	}
}

// TestNoMutatorPanicsOnUnparseableTemplate is the contract the executor relies
// on: a broken template degrades to "no candidates", never a crash.
func TestNoMutatorPanicsOnUnparseableTemplate(t *testing.T) {
	broken := []string{
		`{{ if .Values.x }}no end`,
		`{{ end }}`,
		`{{`,
		`{{ .Values.a | }}`,
		"",
		`{{ range $k, $v := }}`,
		`{{ range .Values.x }}no end`,
	}
	for _, src := range broken {
		for _, id := range IDs() {
			m, _ := Get(id)
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("mutator %q panicked on %q: %v", id, src, r)
					}
				}()
				m.Mutate(tmpl(src))
			}()
		}
	}
}

// TestMutantsKeepTemplatesParseable guards the Invalid-mutant rate: a mutation
// that breaks rendering is killed by every test and tells us nothing.
func TestMutantsKeepTemplatesParseable(t *testing.T) {
	src := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ include "chart.fullname" . }}
spec:
  replicas: {{ .Values.replicaCount | default 3 }}
  paused: false
  strategy: RollingUpdate
  {{- if .Values.serviceAccount.create }}
  serviceAccountName: {{ .Values.serviceAccount.name }}
  {{- end }}
  {{- with .Values.nodeSelector }}
  nodeSelector: {{- toYaml . | nindent 4 }}
  {{- end }}
  token: {{ required "token required" .Values.token | quote }}
  env: {{ if eq .Values.env "prod" }}production{{ else }}dev{{ end }}
  {{- range .Values.ports }}
  - {{ . }}
  {{- end }}
  {{- range $k, $v := .Values.labels }}
  {{ $k }}: {{ $v }}
  {{- end }}
`
	f := tmpl(src)
	for _, id := range IDs() {
		m, _ := Get(id)
		for _, c := range m.Mutate(f) {
			mutated := f.Apply(c.Start, c.End, c.Replacement)
			if _, err := source.ParseTemplate(tmpl(string(mutated))); err != nil {
				t.Errorf("mutator %q produced an unparseable template (replacing %q with %q): %v\n%s",
					id, f.Slice(c.Start, c.End), c.Replacement, err, mutated)
			}
		}
	}
}

// ---------- cond-negate ----------

func TestCondNegate(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			"simple if",
			"{{- if .Values.enabled }}\nx: 1\n{{- end }}",
			[]string{"not (.Values.enabled)"},
		},
		{
			"with block",
			"{{- with .Values.resources }}\nx: 1\n{{- end }}",
			[]string{"not (.Values.resources)"},
		},
		{
			"compound condition is parenthesised",
			"{{- if and .Values.a .Values.b }}\nx: 1\n{{- end }}",
			[]string{"not (and .Values.a .Values.b)"},
		},
		{
			"eq condition",
			`{{ if eq .Values.env "prod" }}x{{ end }}`,
			[]string{`not (eq .Values.env "prod")`},
		},
		{
			"else if produces a second candidate",
			"{{- if .Values.a }}\nx: 1\n{{- else if .Values.b }}\ny: 2\n{{- end }}",
			[]string{"not (.Values.a)", "not (.Values.b)"},
		},
		{"range is not negated", "{{- range .Values.list }}\nx\n{{- end }}", nil},
		{"no conditions", "kind: Deployment\n", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := tmpl(tc.src)
			got := mustRun(t, IDCondNegate, f)
			var reps []string
			for _, c := range got {
				reps = append(reps, c.Replacement)
			}
			slices.Sort(reps)
			wantSorted := slices.Clone(tc.want)
			slices.Sort(wantSorted)
			if strings.Join(reps, "|") != strings.Join(wantSorted, "|") {
				t.Errorf("replacements = %v, want %v", reps, wantSorted)
			}
		})
	}
}

func TestCondNegateInsideDefineBlock(t *testing.T) {
	// _helpers.tpl is nothing but define blocks; missing them means missing all
	// the conditions in a chart's helper logic.
	src := "{{- define \"chart.labels\" -}}\n{{- if .Values.extraLabels }}\nx: 1\n{{- end }}\n{{- end -}}"
	if got := mustRun(t, IDCondNegate, tmpl(src)); len(got) != 1 {
		t.Fatalf("expected 1 candidate inside the define block, got %d", len(got))
	}
}

func TestCondNegatePreservesTrimMarkers(t *testing.T) {
	f := tmpl("{{- if .Values.a -}}\nx\n{{- end -}}")
	got := mustRun(t, IDCondNegate, f)
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1", len(got))
	}
	out := applied(f, got)[0]
	if !strings.HasPrefix(out, "{{- if not (.Values.a) -}}") {
		t.Errorf("trim markers were not preserved: %q", out)
	}
}

// ---------- range-empty ----------

func TestRangeEmpty(t *testing.T) {
	tests := []struct {
		name, src, wantSpan, want string
	}{
		{
			"plain range",
			"{{- range .Values.ports }}\n- {{ . }}\n{{- end }}",
			".Values.ports",
			"{{- range list }}\n- {{ . }}\n{{- end }}",
		},
		{
			"one declaration survives",
			"{{- range $v := .Values.ports }}\n- {{ $v }}\n{{- end }}",
			".Values.ports",
			"{{- range $v := list }}\n- {{ $v }}\n{{- end }}",
		},
		{
			"two declarations survive",
			"{{- range $k, $v := .Values.labels }}\n{{ $k }}: {{ $v }}\n{{- end }}",
			".Values.labels",
			"{{- range $k, $v := list }}\n{{ $k }}: {{ $v }}\n{{- end }}",
		},
		{
			"whole piped expression is replaced",
			"{{- range .Values.x | sortAlpha }}\n- {{ . }}\n{{- end }}",
			".Values.x | sortAlpha",
			"{{- range list }}\n- {{ . }}\n{{- end }}",
		},
		{
			// An {{else}} makes the mutant render the else branch rather than
			// nothing. Still a manifest change, so still a useful mutant.
			"range with else",
			"{{ range .Values.p }}a{{ else }}b{{ end }}",
			".Values.p",
			"{{ range list }}a{{ else }}b{{ end }}",
		},
		{
			// _helpers.tpl is nothing but define blocks; missing them would mean
			// missing all of a chart's helper logic.
			"inside a define block",
			`{{ define "c.x" }}{{- range $i, $e := .Values.l }}{{ $e }}{{- end }}{{ end }}`,
			".Values.l",
			`{{ define "c.x" }}{{- range $i, $e := list }}{{ $e }}{{- end }}{{ end }}`,
		},
		{
			// A ":=" inside a string literal is not a declaration.
			"assignment inside a string literal",
			"{{- range .Values.x | replace \":=\" \"-\" }}\n- {{ . }}\n{{- end }}",
			`.Values.x | replace ":=" "-"`,
			"{{- range list }}\n- {{ . }}\n{{- end }}",
		},
		{
			// Here the "$" begins the expression rather than a declaration.
			"range over a declared variable",
			"{{- $items := .Values.x }}\n{{- range $items }}\n- {{ . }}\n{{- end }}",
			"$items",
			"{{- $items := .Values.x }}\n{{- range list }}\n- {{ . }}\n{{- end }}",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := tmpl(tc.src)
			got := mustRun(t, IDRangeEmpty, f)
			if len(got) != 1 {
				t.Fatalf("got %d candidates, want 1: %v", len(got), spans(f, got))
			}
			if s := spans(f, got)[0]; s != tc.wantSpan {
				t.Fatalf("replaced span = %q, want %q", s, tc.wantSpan)
			}
			if a := applied(f, got)[0]; a != tc.want {
				t.Fatalf("mutant =\n%q\nwant\n%q", a, tc.want)
			}
		})
	}
}

// TestRangeEmptyProducesNoCandidateForAnAlreadyEmptyRange: a candidate whose
// replacement equals the original can only ever "survive", inflating the
// survived count with a non-mutation.
func TestRangeEmptyProducesNoCandidateForAnAlreadyEmptyRange(t *testing.T) {
	f := tmpl("{{- range list }}\n- {{ . }}\n{{- end }}")
	if got := mustRun(t, IDRangeEmpty, f); len(got) != 0 {
		t.Fatalf("got %d candidates, want 0: %v", len(got), spans(f, got))
	}
}

// TestRangeEmptyMutantsParseWithDeclarations is the property that made this
// mutator non-trivial. RangeNode's Pipe.Position() is the offset of "$k", so the
// obvious whole-pipeline replacement leaves $k and $v undeclared and the mutant
// fails to parse — making it Invalid, which grades nothing at all.
func TestRangeEmptyMutantsParseWithDeclarations(t *testing.T) {
	srcs := []string{
		"{{- range $v := .Values.ports }}\n- {{ $v }}\n{{- end }}",
		"{{- range $k, $v := .Values.labels }}\n{{ $k }}: {{ $v }}\n{{- end }}",
	}
	for _, src := range srcs {
		f := tmpl(src)
		for _, c := range mustRun(t, IDRangeEmpty, f) {
			mutated := f.Apply(c.Start, c.End, c.Replacement)
			if _, err := source.ParseTemplate(tmpl(string(mutated))); err != nil {
				t.Errorf("mutant does not parse: %v\n%s", err, mutated)
			}
		}
	}
}

// ---------- comparison-swap ----------

func TestComparisonSwap(t *testing.T) {
	tests := []struct {
		src  string
		want []string
	}{
		{`{{ if eq .a .b }}x{{ end }}`, []string{"ne"}},
		{`{{ if ne .a .b }}x{{ end }}`, []string{"eq"}},
		{`{{ if gt .a .b }}x{{ end }}`, []string{"le"}},
		{`{{ if le .a .b }}x{{ end }}`, []string{"gt"}},
		{`{{ if lt .a .b }}x{{ end }}`, []string{"ge"}},
		{`{{ if ge .a .b }}x{{ end }}`, []string{"lt"}},
		{`{{ if and .a .b }}x{{ end }}`, []string{"or"}},
		{`{{ if or .a .b }}x{{ end }}`, []string{"and"}},
		{`{{ if not .a }}x{{ end }}`, nil},
		{`{{ .Values.eq }}`, nil}, // a field named eq is not the operator
	}
	for _, tc := range tests {
		t.Run(tc.src, func(t *testing.T) {
			got := mustRun(t, IDComparisonSwap, tmpl(tc.src))
			var reps []string
			for _, c := range got {
				reps = append(reps, c.Replacement)
			}
			if strings.Join(reps, "|") != strings.Join(tc.want, "|") {
				t.Errorf("got %v, want %v", reps, tc.want)
			}
		})
	}
}

func TestComparisonSwapReplacesOnlyTheOperator(t *testing.T) {
	f := tmpl(`{{ if eq .Values.env "prod" }}x{{ end }}`)
	got := mustRun(t, IDComparisonSwap, f)
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1", len(got))
	}
	if s := spans(f, got)[0]; s != "eq" {
		t.Errorf("replaced span = %q, want %q", s, "eq")
	}
	if out := applied(f, got)[0]; out != `{{ if ne .Values.env "prod" }}x{{ end }}` {
		t.Errorf("got %q", out)
	}
}

// ---------- default-drop ----------

func TestDefaultDrop(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			"trailing default",
			`tag: {{ .Values.image.tag | default .Chart.AppVersion }}`,
			`tag: {{ .Values.image.tag }}`,
		},
		{
			"numeric default",
			`replicas: {{ .Values.replicaCount | default 3 }}`,
			`replicas: {{ .Values.replicaCount }}`,
		},
		{
			"default in the middle of a pipeline",
			`tag: {{ .Values.tag | default "latest" | quote }}`,
			`tag: {{ .Values.tag | quote }}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := tmpl(tc.src)
			got := mustRun(t, IDDefaultDrop, f)
			if len(got) != 1 {
				t.Fatalf("got %d candidates, want 1", len(got))
			}
			if out := applied(f, got)[0]; out != tc.want {
				t.Errorf("got  %q\nwant %q", out, tc.want)
			}
		})
	}
}

func TestDefaultDropIgnoresNonDefaultPipelines(t *testing.T) {
	for _, src := range []string{
		`x: {{ .Values.a | quote }}`,
		`x: {{ .Values.a | nindent 4 }}`,
		`x: {{ .Values.default }}`,   // a field named default
		`x: {{ default .Values.a }}`, // leading position: nothing to pipe from
	} {
		if got := mustRun(t, IDDefaultDrop, tmpl(src)); len(got) != 0 {
			t.Errorf("%s: expected no candidates, got %d", src, len(got))
		}
	}
}

// ---------- required-drop ----------

func TestRequiredDrop(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			"bare required",
			`token: {{ required "token is required" .Values.token }}`,
			`token: {{ .Values.token }}`,
		},
		{
			"required inside a pipeline keeps the rest",
			`token: {{ required "msg" .Values.token | quote }}`,
			`token: {{ .Values.token | quote }}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := tmpl(tc.src)
			got := mustRun(t, IDRequiredDrop, f)
			if len(got) != 1 {
				t.Fatalf("got %d candidates, want 1", len(got))
			}
			if out := applied(f, got)[0]; out != tc.want {
				t.Errorf("got  %q\nwant %q", out, tc.want)
			}
		})
	}
}

func TestRequiredDropIgnoresIncompleteCalls(t *testing.T) {
	// `required` with no value argument has nothing to keep.
	if got := mustRun(t, IDRequiredDrop, tmpl(`x: {{ required "msg" }}`)); len(got) != 0 {
		t.Errorf("expected no candidates, got %d", len(got))
	}
}

// ---------- bool-flip ----------

func TestBoolFlipInTemplates(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"ast bool literal", `x: {{ default true .Values.a }}`, `x: {{ default false .Values.a }}`},
		{"plain yaml true", "hostNetwork: true\n", "hostNetwork: false\n"},
		{"plain yaml false", "paused: false\n", "paused: true\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := tmpl(tc.src)
			got := mustRun(t, IDBoolFlip, f)
			if len(got) != 1 {
				t.Fatalf("got %d candidates, want 1: %v", len(got), spans(f, got))
			}
			if out := applied(f, got)[0]; out != tc.want {
				t.Errorf("got %q, want %q", out, tc.want)
			}
		})
	}
}

func TestBoolFlipInValues(t *testing.T) {
	f := vals("enabled: true\ncreate: false\nname: notabool\n")
	got := mustRun(t, IDBoolFlip, f)
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2: %v", len(got), spans(f, got))
	}
	out := applied(f, got)
	if !strings.Contains(out[0], "enabled: false") {
		t.Errorf("first candidate: %q", out[0])
	}
	if !strings.Contains(out[1], "create: true") {
		t.Errorf("second candidate: %q", out[1])
	}
}

func TestBoolFlipPreservesCaseAndQuotes(t *testing.T) {
	tests := []struct{ in, want string }{
		{"true", "false"},
		{"TRUE", "FALSE"},
		{"True", "False"},
		{`"true"`, `"false"`},
		{`'false'`, `'true'`},
		{"notabool", "notabool"},
	}
	for _, tc := range tests {
		if got := negateBoolText(tc.in); got != tc.want {
			t.Errorf("negateBoolText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBoolFlipSkipsKeys(t *testing.T) {
	// A key literally named "true" must not be flipped; that would rename a field.
	f := vals("\"true\": someValue\n")
	for _, c := range mustRun(t, IDBoolFlip, f) {
		if strings.Contains(f.Slice(c.Start, c.End), "true") && c.Start < 8 {
			t.Errorf("a mapping key was flipped: span %q", f.Slice(c.Start, c.End))
		}
	}
}

// ---------- num-literal ----------

func TestNumLiteralProducesTwoVariants(t *testing.T) {
	f := tmpl(`replicas: {{ .Values.r | default 3 }}`)
	got := mustRun(t, IDNumLiteral, f)
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2 (increment and zero)", len(got))
	}
	reps := map[string]string{}
	for _, c := range got {
		reps[c.Note] = c.Replacement
	}
	if reps[variantIncrement] != "4" {
		t.Errorf("increment variant = %q, want 4", reps[variantIncrement])
	}
	if reps[variantZero] != "0" {
		t.Errorf("zero variant = %q, want 0", reps[variantZero])
	}
}

// TestNumLiteralMutatesLayoutArguments documents a corrected assumption. These
// were once skipped on the theory that reindenting breaks rendering and yields
// useless Invalid mutants. Measured against the fixture chart, `nindent 4 -> 0`
// and `-> 99` both render, survive the weak suite and are caught by the strong
// one, so they are exactly the signal this tool exists to surface.
func TestNumLiteralMutatesLayoutArguments(t *testing.T) {
	for _, src := range []string{
		`x: {{ toYaml .Values.a | nindent 4 }}`,
		`x: {{ toYaml .Values.a | indent 8 }}`,
		`x: {{ .Values.a | trunc 63 }}`,
		`x: {{ .Values.a | substr 0 5 }}`,
	} {
		if got := mustRun(t, IDNumLiteral, tmpl(src)); len(got) == 0 {
			t.Errorf("%s: expected the numeric argument to be mutated", src)
		}
	}
}

func TestNumLiteralInValuesAndPlainYAML(t *testing.T) {
	f := vals("replicaCount: 2\nport: 80\nratio: 1.5\nname: nginx\n")
	got := mustRun(t, IDNumLiteral, f)
	// 3 numeric scalars x 2 variants.
	if len(got) != 6 {
		t.Fatalf("got %d candidates, want 6: %v", len(got), spans(f, got))
	}
}

func TestNumLiteralZeroBecomesOne(t *testing.T) {
	// A zero-variant of 0 would be a no-op, so it must become 1 instead.
	f := vals("count: 0\n")
	got := mustRun(t, IDNumLiteral, f)
	reps := map[string]string{}
	for _, c := range got {
		reps[c.Note] = c.Replacement
	}
	if reps[variantIncrement] != "1" {
		t.Errorf("increment of 0 = %q, want 1", reps[variantIncrement])
	}
	// The zero variant is dropped as a duplicate of the increment.
	if len(got) != 1 {
		t.Errorf("got %d candidates for 0, want 1 (no no-op duplicate)", len(got))
	}
}

func TestNumLiteralFloatFormatting(t *testing.T) {
	tests := []struct {
		in       string
		wantInc  string
		wantZero string
	}{
		{"1.5", "2.5", "0.0"},
		{"0.25", "1.25", "0.00"},
		{"3", "4", "0"},
		{"-2", "-1", "0"},
	}
	for _, tc := range tests {
		inc, ok := incrementNumber(tc.in)
		if !ok || inc != tc.wantInc {
			t.Errorf("incrementNumber(%q) = %q,%v want %q", tc.in, inc, ok, tc.wantInc)
		}
		zero, ok := towardZero(tc.in)
		if !ok || zero != tc.wantZero {
			t.Errorf("towardZero(%q) = %q,%v want %q", tc.in, zero, ok, tc.wantZero)
		}
	}
}

// ---------- str-literal ----------

func TestStrLiteralInTemplates(t *testing.T) {
	f := tmpl(`env: {{ if eq .Values.env "prod" }}x{{ end }}`)
	got := mustRun(t, IDStrLiteral, f)
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1: %v", len(got), spans(f, got))
	}
	want := `env: {{ if eq .Values.env "` + Sentinel + `" }}x{{ end }}`
	if out := applied(f, got)[0]; out != want {
		t.Errorf("got  %q\nwant %q", out, want)
	}
}

func TestStrLiteralMutatesHardcodedFields(t *testing.T) {
	// The case that motivates PlainYAMLValues: a hardcoded field has no AST node,
	// but a suite that never asserts it is exactly the weakness we hunt.
	f := tmpl("imagePullPolicy: IfNotPresent\n")
	got := mustRun(t, IDStrLiteral, f)
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1", len(got))
	}
	if out := applied(f, got)[0]; out != `imagePullPolicy: "`+Sentinel+`"`+"\n" {
		t.Errorf("got %q", out)
	}
}

// TestStrLiteralGuards documents each exclusion and why it exists.
func TestStrLiteralGuards(t *testing.T) {
	tests := []struct {
		name string
		src  string
		why  string
	}{
		{"include target", `x: {{ include "chart.fullname" . }}`, "renaming a template is a render error, killed by every suite alike"},
		{"template target", `{{ template "chart.labels" . }}`, "same as include"},
		{"format string", `x: {{ printf "%s-%s" .a .b }}`, "a verb-less sentinel leaves args unconsumed, emitting %!(EXTRA ...)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustRun(t, IDStrLiteral, tmpl(tc.src)); len(got) != 0 {
				t.Errorf("%s should be skipped (%s), got %d candidates: %v",
					tc.name, tc.why, len(got), spans(tmpl(tc.src), got))
			}
		})
	}
}

// TestStrLiteralMutatesStructuralFields pins the corrected behaviour: kind and
// apiVersion are both mutable. Measurement showed a mutated apiVersion renders
// fine, survives the weak suite and is caught by the strong one, so excluding it
// was discarding a real finding.
func TestStrLiteralMutatesStructuralFields(t *testing.T) {
	for _, src := range []string{"kind: Deployment\n", "apiVersion: apps/v1\n"} {
		if got := mustRun(t, IDStrLiteral, tmpl(src)); len(got) != 1 {
			t.Errorf("%q should yield 1 candidate, got %d", src, len(got))
		}
	}
}

// TestStrLiteralMutatesRequiredMessage: a suite asserting failedTemplate's
// errorMessage catches this, a weak suite does not. That is a real finding.
func TestStrLiteralMutatesRequiredMessage(t *testing.T) {
	f := tmpl(`x: {{ required "must be set" .Values.a }}`)
	if got := mustRun(t, IDStrLiteral, f); len(got) != 1 {
		t.Fatalf("the required message should be mutable, got %d candidates", len(got))
	}
}

func TestStrLiteralInValues(t *testing.T) {
	f := vals("repository: nginx\ntag: \"v1.2\"\nport: 80\nenabled: true\n")
	got := mustRun(t, IDStrLiteral, f)
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2 (only the strings): %v", len(got), spans(f, got))
	}
	// Quote style is preserved.
	if out := applied(f, got)[1]; !strings.Contains(out, `tag: "`+Sentinel+`"`) {
		t.Errorf("quote style not preserved: %q", out)
	}
}

func TestStrLiteralIsIdempotent(t *testing.T) {
	// Mutating an already-sentinel value would be a no-op mutant.
	f := tmpl("name: " + Sentinel + "\n")
	if got := mustRun(t, IDStrLiteral, f); len(got) != 0 {
		t.Errorf("expected no candidates for an already-sentinel value, got %d", len(got))
	}
}

// ---------- yaml-key-delete ----------

func TestYAMLKeyDeleteInValues(t *testing.T) {
	f := vals("replicaCount: 1\nimage:\n  repository: nginx\n  tag: v1\n")
	got := mustRun(t, IDYAMLKeyDelete, f)
	// replicaCount, image, image.repository, image.tag
	if len(got) != 4 {
		t.Fatalf("got %d candidates, want 4: %v", len(got), spans(f, got))
	}
	// Deleting the image block must take its children with it.
	for _, c := range got {
		if f.Slice(c.Start, c.End) == "image:\n  repository: nginx\n  tag: v1\n" {
			if out := string(f.Apply(c.Start, c.End, "")); out != "replicaCount: 1\n" {
				t.Errorf("block delete left debris: %q", out)
			}
			return
		}
	}
	t.Error("no candidate deleted the whole image block")
}

func TestYAMLKeyDeleteInTemplates(t *testing.T) {
	f := tmpl("spec:\n  replicas: 3\n  paused: false\n")
	got := mustRun(t, IDYAMLKeyDelete, f)
	byspan := spans(f, got)
	if !slices.Contains(byspan, "  replicas: 3\n") {
		t.Errorf("expected a candidate deleting the replicas line, got %v", byspan)
	}
}

// TestYAMLKeyDeleteHasNoKeyExemptions pins another corrected assumption.
// apiVersion, kind and metadata were once skipped as "Helm rejects a manifest
// without them"; measurement showed all three render fine when deleted and
// discriminate weak suites from strong ones.
func TestYAMLKeyDeleteHasNoKeyExemptions(t *testing.T) {
	f := tmpl("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: x\n")
	got := spans(f, mustRun(t, IDYAMLKeyDelete, f))
	for _, want := range []string{"apiVersion:", "kind:", "metadata:"} {
		var found bool
		for _, s := range got {
			if strings.HasPrefix(strings.TrimSpace(s), want) {
				found = true
			}
		}
		if !found {
			t.Errorf("expected a candidate deleting %q, got %v", want, got)
		}
	}
}

// TestYAMLKeyDeleteNeverUnbalancesControlFlow is the correctness guard: deleting
// half of an if/end pair is a syntax error, not a mutation.
func TestYAMLKeyDeleteNeverUnbalancesControlFlow(t *testing.T) {
	src := `spec:
  {{- if .Values.enabled }}
  replicas: 3
  {{- end }}
  paused: false
  {{- with .Values.node }}
  nodeSelector: x
  {{- end }}
`
	f := tmpl(src)
	for _, c := range mustRun(t, IDYAMLKeyDelete, f) {
		mutated := f.Apply(c.Start, c.End, c.Replacement)
		if _, err := source.ParseTemplate(tmpl(string(mutated))); err != nil {
			t.Errorf("deleting %q unbalanced the template: %v\n%s", f.Slice(c.Start, c.End), err, mutated)
		}
	}
}

func TestBalancedControlFlow(t *testing.T) {
	tests := []struct {
		span string
		want bool
	}{
		{"  replicas: 3\n", true},
		{"  {{- if .a }}\n  x: 1\n  {{- end }}\n", true},
		{"  {{- if .a }}\n  x: 1\n", false},
		{"  {{- end }}\n", false},
		{"  {{- else }}\n", false},
		{"  {{- range .a }}\n  x\n  {{- end }}\n", true},
	}
	for _, tc := range tests {
		if got := balancedControlFlow(tc.span); got != tc.want {
			t.Errorf("balancedControlFlow(%q) = %v, want %v", tc.span, got, tc.want)
		}
	}
}

// ---------- shared invariants ----------

func TestCandidatesAreAlwaysChanges(t *testing.T) {
	// A candidate whose replacement equals the original is a wasted test run: it
	// can only ever "survive", inflating the survived count with a non-mutation.
	srcs := []string{
		"apiVersion: apps/v1\nkind: Deployment\nspec:\n  replicas: 3\n  paused: true\n  policy: IfNotPresent\n",
		`x: {{ .Values.a | default "y" }}` + "\n" + `{{- if eq .Values.e "p" }}z{{ end }}`,
		"{{- range .Values.ports }}\n- {{ . }}\n{{- end }}\n{{- range $k, $v := .Values.labels }}\n{{ $k }}: {{ $v }}\n{{- end }}\n",
	}
	for _, src := range srcs {
		f := tmpl(src)
		for _, id := range IDs() {
			m, _ := Get(id)
			for _, c := range m.Mutate(f) {
				if f.Slice(c.Start, c.End) == c.Replacement {
					t.Errorf("mutator %q produced a no-op candidate at %d:%d (%q)",
						id, c.Start, c.End, c.Replacement)
				}
				if c.End < c.Start {
					t.Errorf("mutator %q produced an inverted span %d:%d", id, c.Start, c.End)
				}
			}
		}
	}
}

func TestValuesFileNeverGetsTemplateMutators(t *testing.T) {
	// values.yaml has no actions, so AST mutators must find nothing in it.
	f := vals("enabled: true\nreplicaCount: 3\nname: nginx\n")
	for _, id := range []string{IDCondNegate, IDComparisonSwap, IDDefaultDrop, IDRequiredDrop} {
		if got := mustRun(t, id, f); len(got) != 0 {
			t.Errorf("mutator %q produced %d candidates for values.yaml", id, len(got))
		}
	}
}

func TestWalkIsRobustAgainstTypedNilElseList(t *testing.T) {
	// An {{if}} with no {{else}} has a typed-nil ElseList. A bare n == nil check
	// misses it and every AST mutator panics on Position().
	tree, err := source.ParseTemplate(tmpl("{{ if .Values.a }}x{{ end }}"))
	if err != nil {
		t.Fatal(err)
	}
	tree.Walk(func(n parse.Node) bool {
		_ = n.Position() // must not panic
		return true
	})
}
