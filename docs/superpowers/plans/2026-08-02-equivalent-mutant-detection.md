# Equivalent-Mutant Detection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an `Equivalent` mutant status — a survivor whose mutation provably cannot change any parsed manifest — excluded from the mutation score's denominator and named in all five report formats.

**Architecture:** A post-pass in `Session.Run` re-renders each survivor with Helm's own engine under every covering test job's value set. If the parsed documents are identical everywhere *and* a maximal-perturbation probe proves the mutated span actually executed, the mutant becomes `Equivalent`. Every other path leaves it `Survived`.

**Tech Stack:** Go 1.24, `helm.sh/helm/v3` v3.19.0 (`pkg/engine`, `pkg/chartutil`), helm-unittest v1.0.3, `gopkg.in/yaml.v3`.

**Spec:** `docs/superpowers/specs/2026-08-02-equivalent-mutant-detection-design.md`

## Global Constraints

- **Always run tests with `-race`.** Use `make test`, or `go test ./... -race`. A plain `go test` hid a real global-state race in this project once already.
- **The score must never flatter itself.** Any uncertainty — a render failure, an unbuildable probe, a missing context — resolves to `Survived`, never to `Equivalent`.
- **`internal/runner/unittest.go` is the only file that may import helm-unittest.** No exceptions.
- **`internal/equivalence` must not import `internal/runner`.** It may import `internal/source` and Helm. `runner` is the adapter that bridges them.
- **Never call `cache.StoreToFileIfNeeded()`**, and never write to the chart under test.
- **Never write through `chartutil.DefaultCapabilities`.** It is a package-level pointer in Helm; always `.Copy()` it first. This is the exact bug that forced worker subprocesses.
- **Keep backticks out of pflag usage strings.** pflag's `UnquoteUsage` turns the first backquoted run into the flag's displayed value type.
- Helm templates are not valid YAML before rendering; the project `Write` hook rejects files under `testdata/charts/*/templates/`. Create those with a shell heredoc.
- `sed -i ''` fails here (GNU sed). Use `python3` for in-place edits in throwaway scripts.

---

### Task 1: The `Equivalent` status in the model

`internal/model` is the canonical model and must stay free of Helm and helm-unittest imports. This task only adds the vocabulary; nothing produces the status yet.

**Files:**
- Modify: `internal/model/model.go`
- Test: `internal/model/model_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `model.StatusEquivalent Status`; `Tally.Equivalent int`; `(*Run).Equivalent() []Mutant`; `Run.EquivalenceChecked bool`.

- [ ] **Step 1: Write the failing tests**

Add to `internal/model/model_test.go`:

```go
func TestEquivalentIsExcludedFromTheScore(t *testing.T) {
	// An equivalent mutant cannot change any rendered manifest, so no assertion
	// could ever catch it. Counting it as survived would depress the score by an
	// amount no test author can fix.
	if StatusEquivalent.CountsTowardScore() {
		t.Fatal("Equivalent must not count toward the score")
	}
	tl := Tally{Killed: 9, Survived: 1, Equivalent: 90}
	if got := tl.Score(); got != 90 {
		t.Fatalf("Score() = %v, want 90 (equivalents out of the denominator)", got)
	}
	if got := tl.Total(); got != 100 {
		t.Fatalf("Total() = %d, want 100 (equivalents still counted as mutants)", got)
	}
}

func TestEquivalentTallyAndAccessor(t *testing.T) {
	r := Run{Mutants: []Mutant{
		{ID: "1", Status: StatusEquivalent},
		{ID: "2", Status: StatusSurvived},
		{ID: "3", Status: StatusEquivalent},
	}}
	r.ComputeTally()
	if r.Tally.Equivalent != 2 {
		t.Fatalf("Tally.Equivalent = %d, want 2", r.Tally.Equivalent)
	}
	got := r.Equivalent()
	if len(got) != 2 || got[0].ID != "1" || got[1].ID != "3" {
		t.Fatalf("Equivalent() = %+v", got)
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/model/ -race -run 'TestEquivalent' -v`
Expected: compile failure — `undefined: StatusEquivalent`.

- [ ] **Step 3: Add the status, the tally field and the accessor**

In `internal/model/model.go`, add to the `Status` const block after `StatusInvalid`:

```go
	// StatusEquivalent means the mutation provably cannot change any rendered
	// manifest: the chart renders to identical parsed documents under every value
	// set the covering tests use, and the mutated span was proven to execute. No
	// assertion could distinguish it, so it is excluded from the score.
	StatusEquivalent Status = "Equivalent"
```

`CountsTowardScore` needs no change — it already allow-lists `Killed` and `Survived`.

Add to `Tally`, after `Invalid`:

```go
	Equivalent int `json:"equivalent"`
```

Add to `Tally.Add`:

```go
	case StatusEquivalent:
		t.Equivalent++
```

Add `t.Equivalent` to the sum in `Tally.Total()`.

Add beside `NoCoverage()`:

```go
// Equivalent returns the mutants proven unable to change any rendered manifest.
func (r *Run) Equivalent() []Mutant { return r.withStatus(StatusEquivalent) }
```

Add to `Run`, next to `Capped`:

```go
	// EquivalenceChecked records whether the equivalence pass ran. Without it,
	// "equivalent 0" is ambiguous between "checked and found none" and "never
	// looked", and the second reads as the first.
	EquivalenceChecked bool `json:"equivalenceChecked"`
```

- [ ] **Step 4: Run the whole model package**

Run: `go test ./internal/model/ -race -v`
Expected: PASS, including the pre-existing `TestStatusCountsTowardScore` and `TestTallyTotalAndScored`.

- [ ] **Step 5: Commit**

```bash
git add internal/model/model.go internal/model/model_test.go
git commit -m "feat(model): add the Equivalent status, excluded from the score"
```

---

### Task 2: Render a chart under a value context

The first half of `internal/equivalence`: turn a chart plus a value context into manifests, and compare two renders.

**Files:**
- Create: `internal/equivalence/equivalence.go`
- Create: `internal/equivalence/equivalence_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `type RenderContext struct { Name string; Values map[string]any; Release chartutil.ReleaseOptions; Capabilities *chartutil.Capabilities; ChartVersion, ChartAppVersion string }`
  - `func Render(chrt *chart.Chart, ctx RenderContext) (map[string]string, error)`
  - `func Compare(a, b map[string]string) (bool, error)`

- [ ] **Step 1: Write the failing tests**

Create `internal/equivalence/equivalence_test.go`:

```go
package equivalence

import (
	"strings"
	"testing"

	"helm.sh/helm/v3/pkg/chart"
)

// testChart builds a minimal in-memory chart. Templates are given as
// name -> body, where name is chart-relative ("templates/x.yaml").
func testChart(values map[string]any, templates map[string]string) *chart.Chart {
	c := &chart.Chart{
		Metadata: &chart.Metadata{APIVersion: "v2", Name: "probe", Version: "0.1.0"},
		Values:   values,
	}
	for name, body := range templates {
		c.Templates = append(c.Templates, &chart.File{Name: name, Data: []byte(body)})
	}
	return c
}

func TestRenderUsesTheContextValues(t *testing.T) {
	c := testChart(map[string]any{"replicas": 1}, map[string]string{
		"templates/a.yaml": "replicas: {{ .Values.replicas }}\n",
	})
	out, err := Render(c, RenderContext{Name: "ctx", Values: map[string]any{"replicas": 7}})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := out["probe/templates/a.yaml"]
	if !strings.Contains(got, "replicas: 7") {
		t.Fatalf("context values did not reach the render: %q", got)
	}
}

func TestCompareIgnoresIndentationButNotData(t *testing.T) {
	// Indentation depth carries no meaning in a YAML block mapping, which is why
	// `nindent N -> N+1` is unkillable. Byte comparison alone would miss that.
	a := map[string]string{"x": "spec:\n  a: 1\n"}
	b := map[string]string{"x": "spec:\n    a: 1\n"}
	equal, err := Compare(a, b)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if !equal {
		t.Fatal("re-indented but structurally identical documents must compare equal")
	}

	c := map[string]string{"x": "spec:\n  a: 2\n"}
	equal, err = Compare(a, c)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if equal {
		t.Fatal("a changed value must not compare equal")
	}
}

func TestCompareIsSensitiveToDocumentOrder(t *testing.T) {
	// documentIndex assertions can observe order, so a reordering is observable.
	a := map[string]string{"x": "a: 1\n---\nb: 2\n"}
	b := map[string]string{"x": "b: 2\n---\na: 1\n"}
	equal, err := Compare(a, b)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if equal {
		t.Fatal("reordered documents must not compare equal")
	}
}

func TestCompareTreatsAMissingTemplateAsADifference(t *testing.T) {
	a := map[string]string{"x": "a: 1\n", "y": "b: 2\n"}
	b := map[string]string{"x": "a: 1\n"}
	equal, err := Compare(a, b)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if equal {
		t.Fatal("a template that stopped rendering must not compare equal")
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/equivalence/ -race -v`
Expected: compile failure — the package does not exist yet.

- [ ] **Step 3: Write the implementation**

Create `internal/equivalence/equivalence.go`:

```go
// Package equivalence decides whether a mutation can change what a chart
// renders.
//
// A mutant is equivalent when the chart renders to identical parsed documents
// with and without it, under every value set the covering tests use. That is a
// proof rather than a heuristic: helm-unittest computes every assertion —
// snapshots included — from the parsed manifest tree, never from the rendered
// text, so two renders that parse identically cannot be told apart by any
// assertion in any suite.
//
// This package imports Helm but nothing from internal/runner. The runner adapts
// helm-unittest's suites into the RenderContexts consumed here, which keeps the
// helm-unittest dependency confined to internal/runner/unittest.go.
package equivalence

import (
	"fmt"
	"reflect"

	"gopkg.in/yaml.v3"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"
)

// RenderContext is one set of inputs a chart can be rendered under. It mirrors
// what a helm-unittest test job supplies, without depending on its types.
type RenderContext struct {
	// Name identifies the context in report detail text.
	Name string
	// Values is the fully merged user value set, as helm-unittest would compute it.
	Values       map[string]any
	Release      chartutil.ReleaseOptions
	Capabilities *chartutil.Capabilities
	// ChartVersion and ChartAppVersion override the chart metadata when non-empty,
	// matching a test job's chart settings.
	ChartVersion    string
	ChartAppVersion string
}

// Render renders the chart under ctx and returns manifests keyed by their
// chart-prefixed template path.
func Render(chrt *chart.Chart, ctx RenderContext) (map[string]string, error) {
	chrt = withMetadata(chrt, ctx)

	caps := ctx.Capabilities
	if caps == nil {
		// Copy: DefaultCapabilities is a package-level pointer in Helm, and writing
		// through it is the race that forced worker subprocesses on us.
		caps = chartutil.DefaultCapabilities.Copy()
	}

	values, err := chartutil.ToRenderValues(chrt, ctx.Values, ctx.Release, caps)
	if err != nil {
		return nil, fmt.Errorf("composing render values for %s: %w", ctx.Name, err)
	}
	out, err := engine.Engine{}.Render(chrt, values)
	if err != nil {
		return nil, fmt.Errorf("rendering under %s: %w", ctx.Name, err)
	}
	return out, nil
}

// withMetadata returns chrt, or a shallow clone carrying the context's chart
// version overrides. The clone keeps the original untouched so one loaded chart
// can serve every context.
func withMetadata(chrt *chart.Chart, ctx RenderContext) *chart.Chart {
	if ctx.ChartVersion == "" && ctx.ChartAppVersion == "" {
		return chrt
	}
	clone := *chrt
	md := *chrt.Metadata
	if ctx.ChartVersion != "" {
		md.Version = ctx.ChartVersion
	}
	if ctx.ChartAppVersion != "" {
		md.AppVersion = ctx.ChartAppVersion
	}
	clone.Metadata = &md
	return &clone
}

// Compare reports whether two renders are indistinguishable to an assertion.
//
// Bytes are checked first because byte equality is unambiguous and cheap. Only
// on a difference do we parse, because parsed equality is the property that
// actually matters: `nindent 4 -> 5` changes every byte of a block and none of
// its data.
func Compare(a, b map[string]string) (bool, error) {
	if len(a) != len(b) {
		return false, nil
	}
	for name, textA := range a {
		textB, ok := b[name]
		if !ok {
			return false, nil
		}
		if textA == textB {
			continue
		}
		docsA, err := parseDocs(textA)
		if err != nil {
			return false, fmt.Errorf("parsing rendered %s: %w", name, err)
		}
		docsB, err := parseDocs(textB)
		if err != nil {
			return false, fmt.Errorf("parsing rendered %s: %w", name, err)
		}
		if !reflect.DeepEqual(docsA, docsB) {
			return false, nil
		}
	}
	return true, nil
}

// parseDocs decodes a multi-document manifest, preserving document order:
// documentIndex assertions can observe it, so a reordering is a real difference.
func parseDocs(text string) ([]any, error) {
	dec := yaml.NewDecoder(strings.NewReader(text))
	var out []any
	for {
		var doc any
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if doc == nil {
			continue // an empty document renders nothing and asserts nothing
		}
		out = append(out, doc)
	}
}
```

The import block for this file is `errors`, `fmt`, `io`, `reflect`, `strings`, `gopkg.in/yaml.v3`, `helm.sh/helm/v3/pkg/chart`, `helm.sh/helm/v3/pkg/chartutil` and `helm.sh/helm/v3/pkg/engine`.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/equivalence/ -race -v`
Expected: PASS, all four tests.

- [ ] **Step 5: Commit**

```bash
git add internal/equivalence/
git commit -m "feat(equivalence): render a chart under a value context and compare parsed output"
```

---

### Task 3: Apply a mutation to an in-memory chart

Rendering does not need a chart on disk. Swapping one file's bytes in a loaded chart is what makes this phase cheap: no temp directories, no copying, no cleanup.

**Files:**
- Create: `internal/equivalence/chart.go`
- Create: `internal/equivalence/chart_test.go`

**Interfaces:**
- Consumes: `Render` from Task 2.
- Produces: `func WithMutatedFile(chrt *chart.Chart, relPath string, data []byte) (*chart.Chart, error)`

- [ ] **Step 1: Write the failing tests**

Create `internal/equivalence/chart_test.go`:

```go
package equivalence

import (
	"strings"
	"testing"
)

func TestWithMutatedFileReplacesATemplate(t *testing.T) {
	c := testChart(nil, map[string]string{"templates/a.yaml": "a: original\n"})
	mutated, err := WithMutatedFile(c, "templates/a.yaml", []byte("a: mutated\n"))
	if err != nil {
		t.Fatalf("WithMutatedFile: %v", err)
	}
	out, err := Render(mutated, RenderContext{Name: "ctx"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out["probe/templates/a.yaml"], "a: mutated") {
		t.Fatalf("template was not replaced: %q", out["probe/templates/a.yaml"])
	}
}

func TestWithMutatedFileLeavesTheOriginalChartIntact(t *testing.T) {
	// One loaded chart serves every mutant, so a mutation must never be visible
	// through the base chart or the next mutant inherits it.
	c := testChart(nil, map[string]string{"templates/a.yaml": "a: original\n"})
	if _, err := WithMutatedFile(c, "templates/a.yaml", []byte("a: mutated\n")); err != nil {
		t.Fatalf("WithMutatedFile: %v", err)
	}
	if got := string(c.Templates[0].Data); got != "a: original\n" {
		t.Fatalf("base chart was modified: %q", got)
	}
}

func TestWithMutatedFileReparsesValuesYAML(t *testing.T) {
	c := testChart(map[string]any{"replicas": 1}, map[string]string{
		"templates/a.yaml": "replicas: {{ .Values.replicas }}\n",
	})
	mutated, err := WithMutatedFile(c, "values.yaml", []byte("replicas: 4\n"))
	if err != nil {
		t.Fatalf("WithMutatedFile: %v", err)
	}
	out, err := Render(mutated, RenderContext{Name: "ctx"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out["probe/templates/a.yaml"], "replicas: 4") {
		t.Fatalf("mutated values.yaml did not reach the render: %q", out["probe/templates/a.yaml"])
	}
}

func TestWithMutatedFileRejectsAnUnknownPath(t *testing.T) {
	// Silently rendering the unmutated chart would compare it against itself and
	// declare every such mutant equivalent.
	c := testChart(nil, map[string]string{"templates/a.yaml": "a: 1\n"})
	if _, err := WithMutatedFile(c, "templates/nope.yaml", []byte("x: 1\n")); err == nil {
		t.Fatal("want an error for a path the chart does not contain")
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/equivalence/ -race -run TestWithMutatedFile -v`
Expected: FAIL — `undefined: WithMutatedFile`.

- [ ] **Step 3: Write the implementation**

Create `internal/equivalence/chart.go`:

```go
package equivalence

import (
	"fmt"

	"gopkg.in/yaml.v3"
	"helm.sh/helm/v3/pkg/chart"
)

// WithMutatedFile returns a shallow clone of chrt with one chart-relative file
// replaced by data. The receiver is never modified: a single loaded chart is the
// base for every mutant, and a mutation leaking into it would contaminate the
// rest of the run.
//
// An unknown path is an error rather than a no-op. Rendering the unmutated chart
// would compare it against itself and declare the mutant equivalent — exactly
// the false verdict this feature must not produce.
func WithMutatedFile(chrt *chart.Chart, relPath string, data []byte) (*chart.Chart, error) {
	if relPath == "values.yaml" {
		var values map[string]any
		if err := yaml.Unmarshal(data, &values); err != nil {
			return nil, fmt.Errorf("parsing mutated values.yaml: %w", err)
		}
		clone := *chrt
		clone.Values = values
		return &clone, nil
	}

	for i, f := range chrt.Templates {
		if f.Name != relPath {
			continue
		}
		templates := make([]*chart.File, len(chrt.Templates))
		copy(templates, chrt.Templates)
		templates[i] = &chart.File{Name: f.Name, Data: data}
		clone := *chrt
		clone.Templates = templates
		return &clone, nil
	}
	return nil, fmt.Errorf("chart has no file %q", relPath)
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/equivalence/ -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/equivalence/chart.go internal/equivalence/chart_test.go
git commit -m "feat(equivalence): apply a mutation to an in-memory chart clone"
```

---

### Task 4: `source.EnclosingPipelineSpan`

The template probe replaces the pipeline of the innermost enclosing action with `fail "canary"`. This task computes that span. It belongs in `internal/source` beside the other span helpers.

**Files:**
- Modify: `internal/source/tmplast.go`
- Test: `internal/source/tmplast_test.go` (create the test function in the existing file; if the file does not exist, create it)

**Interfaces:**
- Consumes: `source.FindAction`, `source.Action`, `source.isSpace` (already present).
- Produces: `func EnclosingPipelineSpan(f *File, offset int) (start, end int, ok bool)` — the span to overwrite with a probe expression, excluding any leading `if` / `range` / `with` / `else if` keyword and excluding trim markers and padding.

- [ ] **Step 1: Write the failing test**

Add to `internal/source/tmplast_test.go`:

```go
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
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := New([]byte(tc.src), "t.yaml", "templates/t.yaml", KindTemplate)
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
	f := New([]byte(src), "t.yaml", "templates/t.yaml", KindTemplate)
	if _, _, ok := EnclosingPipelineSpan(f, strings.Index(src, "plain")); ok {
		t.Fatal("want !ok for an offset outside any action")
	}
}
```

Ensure `strings` is imported in the test file.

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/source/ -race -run TestEnclosingPipelineSpan -v`
Expected: FAIL — `undefined: EnclosingPipelineSpan`.

- [ ] **Step 3: Write the implementation**

Add to the end of `internal/source/tmplast.go`:

```go
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
	if end <= start {
		return 0, 0, false
	}
	return start, end, true
}
```

- [ ] **Step 4: Run the source package**

Run: `go test ./internal/source/ -race -v`
Expected: PASS, including the pre-existing `TestFieldNodePositionIsNotTheFieldStart` and `TestWalkIsRobustAgainstTypedNilElseList`.

- [ ] **Step 5: Commit**

```bash
git add internal/source/tmplast.go internal/source/tmplast_test.go
git commit -m "feat(source): locate the pipeline of an enclosing action for probing"
```

---

### Task 5: The execution probe

A maximal perturbation at the mutated span. If even this changes nothing, the span never executed, and identical renders prove nothing about equivalence.

**Files:**
- Create: `internal/equivalence/probe.go`
- Create: `internal/equivalence/probe_test.go`

**Interfaces:**
- Consumes: `source.EnclosingPipelineSpan` (Task 4), `source.File`.
- Produces:
  - `const Canary = "__helm_mutation_test_canary__"`
  - `func ProbeBytes(f *source.File, start, end int) ([]byte, bool)`

- [ ] **Step 1: Write the failing tests**

Create `internal/equivalence/probe_test.go`:

```go
package equivalence

import (
	"strings"
	"testing"

	"github.com/mmpyro/helm-mutation-test/internal/source"
)

func templateFile(src string) *source.File {
	return source.New([]byte(src), "t.yaml", "templates/t.yaml", source.KindTemplate)
}

func valuesFile(src string) *source.File {
	return source.New([]byte(src), "values.yaml", "values.yaml", source.KindValues)
}

func TestProbeBytesSubstitutesFailIntoTemplateActions(t *testing.T) {
	tests := []struct {
		name, src, needle, want string
	}{
		{
			"plain action",
			"x: {{ .Values.a }}\n", ".Values.a",
			`x: {{ fail "` + Canary + `" }}` + "\n",
		},
		{
			"if keyword survives",
			"{{- if .Values.a }}\nx: 1\n{{- end }}\n", ".Values.a",
			`{{- if fail "` + Canary + `" }}` + "\nx: 1\n{{- end }}\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := templateFile(tc.src)
			off := strings.Index(tc.src, tc.needle)
			got, ok := ProbeBytes(f, off, off+len(tc.needle))
			if !ok {
				t.Fatal("ProbeBytes returned !ok")
			}
			if string(got) != tc.want {
				t.Fatalf("probe =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

func TestProbeBytesRewritesAValuesKeyToTheCanary(t *testing.T) {
	// yaml-key-delete spans a whole key. Replacing the span outright would break
	// the YAML, so the key is kept and only its value becomes the canary — a
	// non-empty string, therefore truthy where false, 0 or "" used to be.
	src := "autoscaling:\n  enabled: false\n"
	f := valuesFile(src)
	start := strings.Index(src, "  enabled")
	end := len(src)
	got, ok := ProbeBytes(f, start, end)
	if !ok {
		t.Fatal("ProbeBytes returned !ok")
	}
	want := "autoscaling:\n  enabled: \"" + Canary + "\"\n"
	if string(got) != want {
		t.Fatalf("probe = %q, want %q", got, want)
	}
}

func TestProbeBytesReplacesABareValuesScalar(t *testing.T) {
	src := "replicas: 3\n"
	f := valuesFile(src)
	start := strings.Index(src, "3")
	got, ok := ProbeBytes(f, start, start+1)
	if !ok {
		t.Fatal("ProbeBytes returned !ok")
	}
	want := "replicas: \"" + Canary + "\"\n"
	if string(got) != want {
		t.Fatalf("probe = %q, want %q", got, want)
	}
}

func TestProbeBytesRefusesWhatItCannotPerturb(t *testing.T) {
	// No probe means no proof of execution, which must leave the mutant Survived
	// rather than let it be called equivalent on weaker evidence.
	f := templateFile("metadata:\n  name: plain\n")
	if _, ok := ProbeBytes(f, 0, 8); ok {
		t.Fatal("want !ok for template text outside any action")
	}
	g := valuesFile("- item\n")
	if _, ok := ProbeBytes(g, 0, 6); ok {
		t.Fatal("want !ok for a values span with no key to rewrite")
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/equivalence/ -race -run TestProbeBytes -v`
Expected: FAIL — `undefined: ProbeBytes`.

- [ ] **Step 3: Write the implementation**

Create `internal/equivalence/probe.go`:

```go
package equivalence

import (
	"strings"

	"github.com/mmpyro/helm-mutation-test/internal/source"
)

// Canary is the sentinel the execution probe injects. It is deliberately unlike
// anything a chart would contain, and non-empty so that it is truthy wherever
// the original value was false, 0 or "".
const Canary = "__helm_mutation_test_canary__"

// ProbeBytes returns the file content with the span at [start,end) perturbed as
// disruptively as the span allows, reporting false when no probe can be built.
//
// The probe is what separates "unkillable by anyone" from "no test reaches this
// line". Both produce identical renders; only the first should leave the score's
// denominator. A probe that changes nothing anywhere means the span never ran.
//
// The probe depends only on the span, not on the mutation, so one result serves
// every mutant at that site.
func ProbeBytes(f *source.File, start, end int) ([]byte, bool) {
	if f.Kind == source.KindTemplate {
		ps, pe, ok := source.EnclosingPipelineSpan(f, start)
		if !ok {
			return nil, false
		}
		return f.Apply(ps, pe, `fail "`+Canary+`"`), true
	}
	return valuesProbe(f, start, end)
}

// valuesProbe rewrites a values.yaml span so that any template reading the key
// sees a different value.
//
// Two shapes occur. A yaml-key-delete span covers "key: value" — possibly a
// whole nested block — and is rebuilt as `key: "<canary>"`, which also flattens
// a map into a scalar and so is maximally disruptive. Any other span is the
// scalar value itself and is simply replaced.
func valuesProbe(f *source.File, start, end int) ([]byte, bool) {
	span := f.Slice(start, end)
	quoted := `"` + Canary + `"`

	colon := strings.IndexByte(span, ':')
	if colon < 0 {
		// A bare scalar. Reject anything that looks structural rather than a value.
		if strings.ContainsAny(span, "\n-") {
			return nil, false
		}
		return f.Apply(start, end, quoted), true
	}

	key := span[:colon]
	if strings.ContainsAny(key, "\n#") || strings.TrimSpace(key) == "" {
		return nil, false
	}
	trailing := ""
	if strings.HasSuffix(span, "\n") {
		trailing = "\n"
	}
	return f.Apply(start, end, key+": "+quoted+trailing), true
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/equivalence/ -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/equivalence/probe.go internal/equivalence/probe_test.go
git commit -m "feat(equivalence): add the maximal-perturbation execution probe"
```

---

### Task 6: The verdict

Assemble contexts, comparison and probe into the decision, with the render caches that keep it cheap.

**Files:**
- Create: `internal/equivalence/checker.go`
- Create: `internal/equivalence/checker_test.go`

**Interfaces:**
- Consumes: `Render`, `Compare` (Task 2), `WithMutatedFile` (Task 3), `ProbeBytes` (Task 5).
- Produces:
  - `type Mutation struct { File string; Start, End int; Replacement string }`
  - `type Verdict struct { Equivalent bool; Detail string }`
  - `func NewChecker(base *chart.Chart, files map[string]*source.File) *Checker`
  - `func (c *Checker) Judge(m Mutation, ctxs []RenderContext) Verdict`

- [ ] **Step 1: Write the failing tests**

Create `internal/equivalence/checker_test.go`:

```go
package equivalence

import (
	"strings"
	"testing"

	"github.com/mmpyro/helm-mutation-test/internal/source"
)

// judgeFixture builds a one-template chart plus the source.File map the checker
// needs, and returns a checker over both.
func judgeFixture(t *testing.T, templateSrc string, values map[string]any) (*Checker, *source.File) {
	t.Helper()
	c := testChart(values, map[string]string{"templates/a.yaml": templateSrc})
	f := source.New([]byte(templateSrc), "a.yaml", "templates/a.yaml", source.KindTemplate)
	return NewChecker(c, map[string]*source.File{"templates/a.yaml": f}), f
}

func mutationAt(t *testing.T, f *source.File, needle, replacement string) Mutation {
	t.Helper()
	start := strings.Index(f.Text(), needle)
	if start < 0 {
		t.Fatalf("needle %q not in source", needle)
	}
	return Mutation{File: f.Path, Start: start, End: start + len(needle), Replacement: replacement}
}

func TestJudgeCallsAReindentationEquivalent(t *testing.T) {
	// The canonical equivalent: YAML does not care how deep a block mapping is
	// indented, only that it is deeper than its parent.
	src := "spec:\n  {{ .Values.block | nindent 2 }}\n"
	c, f := judgeFixture(t, src, map[string]any{"block": "a: 1"})
	v := c.Judge(mutationAt(t, f, "nindent 2", "nindent 3"), []RenderContext{{Name: "ctx"}})
	if !v.Equivalent {
		t.Fatalf("want equivalent, got Survived: %s", v.Detail)
	}
}

func TestJudgeKeepsAnObservableMutationSurvived(t *testing.T) {
	src := "replicas: {{ .Values.replicas }}\n"
	c, f := judgeFixture(t, src, map[string]any{"replicas": 3})
	v := c.Judge(mutationAt(t, f, ".Values.replicas", "99"), []RenderContext{{Name: "ctx"}})
	if v.Equivalent {
		t.Fatal("a mutation that changes the output must not be called equivalent")
	}
}

func TestJudgeKeepsAnUnexercisedBranchSurvived(t *testing.T) {
	// Identical renders, because no context enables the branch. That is a missing
	// test, not an unkillable mutant, so it must stay in the denominator.
	src := "{{- if .Values.enabled }}\nreplicas: {{ .Values.replicas }}\n{{- end }}\n"
	c, f := judgeFixture(t, src, map[string]any{"enabled": false, "replicas": 3})
	v := c.Judge(mutationAt(t, f, ".Values.replicas", "99"), []RenderContext{{Name: "ctx"}})
	if v.Equivalent {
		t.Fatal("a mutation in an unreached branch must not be called equivalent")
	}
	if !strings.Contains(v.Detail, "not exercised") {
		t.Fatalf("detail should say why: %q", v.Detail)
	}
}

func TestJudgeRequiresEveryContextToAgree(t *testing.T) {
	// Equivalent under the default context, observable under the second. One
	// dissenting context is enough to keep the mutant survived.
	src := "{{- if .Values.enabled }}\nreplicas: {{ .Values.replicas }}\n{{- end }}\n"
	c, f := judgeFixture(t, src, map[string]any{"enabled": false, "replicas": 3})
	ctxs := []RenderContext{
		{Name: "defaults"},
		{Name: "enabled", Values: map[string]any{"enabled": true}},
	}
	v := c.Judge(mutationAt(t, f, ".Values.replicas", "99"), ctxs)
	if v.Equivalent {
		t.Fatal("a context that reveals the difference must veto equivalence")
	}
}

func TestJudgeIsInconclusiveWithoutContexts(t *testing.T) {
	src := "replicas: {{ .Values.replicas }}\n"
	c, f := judgeFixture(t, src, map[string]any{"replicas": 3})
	v := c.Judge(mutationAt(t, f, ".Values.replicas", "99"), nil)
	if v.Equivalent {
		t.Fatal("no contexts means no evidence, which must not mean equivalent")
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/equivalence/ -race -run TestJudge -v`
Expected: FAIL — `undefined: NewChecker`.

- [ ] **Step 3: Write the implementation**

Create `internal/equivalence/checker.go`:

```go
package equivalence

import (
	"fmt"
	"sync"

	"github.com/mmpyro/helm-mutation-test/internal/source"
	"helm.sh/helm/v3/pkg/chart"
)

// Mutation is one mutation to judge, as a byte-range edit on a chart file.
type Mutation struct {
	// File is chart-relative and slash-separated, matching source.File.Path.
	File        string
	Start, End  int
	Replacement string
}

func (m Mutation) spanKey() string { return fmt.Sprintf("%s:%d:%d", m.File, m.Start, m.End) }

// Verdict is the outcome of judging one mutation. Detail is human-facing and
// lands in Mutant.Detail, so it must explain the verdict either way.
type Verdict struct {
	Equivalent bool
	Detail     string
}

// Checker judges mutations against one loaded chart.
//
// It caches the two renders that do not depend on the mutation — the original,
// and the probe at a given span — so a survivor typically costs a single extra
// render. Safe for concurrent use.
type Checker struct {
	base  *chart.Chart
	files map[string]*source.File

	mu       sync.Mutex
	original map[string]map[string]string // context name -> manifests
	probed   map[string]bool              // span key -> was the span proven to execute
}

// NewChecker returns a checker over a loaded chart and the source files the
// mutations refer to, keyed by chart-relative path.
func NewChecker(base *chart.Chart, files map[string]*source.File) *Checker {
	return &Checker{
		base:     base,
		files:    files,
		original: map[string]map[string]string{},
		probed:   map[string]bool{},
	}
}

// Judge decides whether a mutation can change what the chart renders under any
// of the given contexts.
//
// Every uncertain path returns a non-equivalent verdict. Removing a mutant from
// the score's denominator raises the score, so it happens only on positive
// evidence: identical parsed output under every context, plus a probe proving
// the span executed under at least one.
func (c *Checker) Judge(m Mutation, ctxs []RenderContext) Verdict {
	if len(ctxs) == 0 {
		return Verdict{Detail: "inconclusive: no usable test-job value sets"}
	}
	f, ok := c.files[m.File]
	if !ok {
		return Verdict{Detail: "inconclusive: no loaded source for " + m.File}
	}

	mutated, err := WithMutatedFile(c.base, m.File, f.Apply(m.Start, m.End, m.Replacement))
	if err != nil {
		return Verdict{Detail: "inconclusive: " + err.Error()}
	}

	for _, ctx := range ctxs {
		before, err := c.renderOriginal(ctx)
		if err != nil {
			return Verdict{Detail: "inconclusive: " + err.Error()}
		}
		after, err := Render(mutated, ctx)
		if err != nil {
			// The mutant rendered fine under helm-unittest or it would not have
			// survived, so a failure here is our renderer disagreeing, not evidence.
			return Verdict{Detail: "inconclusive: " + err.Error()}
		}
		same, err := Compare(before, after)
		if err != nil {
			return Verdict{Detail: "inconclusive: " + err.Error()}
		}
		if !same {
			return Verdict{Detail: fmt.Sprintf("output differs under %s", ctx.Name)}
		}
	}

	executed, err := c.probeExecuted(m, f, ctxs)
	if err != nil {
		return Verdict{Detail: "inconclusive: " + err.Error()}
	}
	if !executed {
		return Verdict{Detail: fmt.Sprintf(
			"not exercised by any covering test: the span produced no output under any of %d value sets",
			len(ctxs))}
	}
	return Verdict{Equivalent: true, Detail: fmt.Sprintf(
		"identical under %d test-job value sets; span proven executed", len(ctxs))}
}

// renderOriginal renders the unmutated chart under ctx, memoised by context name
// so every survivor shares one render per context.
func (c *Checker) renderOriginal(ctx RenderContext) (map[string]string, error) {
	c.mu.Lock()
	cached, ok := c.original[ctx.Name]
	c.mu.Unlock()
	if ok {
		return cached, nil
	}
	out, err := Render(c.base, ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.original[ctx.Name] = out
	c.mu.Unlock()
	return out, nil
}

// probeExecuted reports whether the mutated span demonstrably runs. The probe
// depends only on the span, so the answer is memoised per span.
//
// A render error counts as proof: the probe's `fail` evaluates only when it is
// reached. So does any change in output, which is what the values.yaml sentinel
// produces.
func (c *Checker) probeExecuted(m Mutation, f *source.File, ctxs []RenderContext) (bool, error) {
	key := m.spanKey()
	c.mu.Lock()
	cached, ok := c.probed[key]
	c.mu.Unlock()
	if ok {
		return cached, nil
	}

	data, ok := ProbeBytes(f, m.Start, m.End)
	if !ok {
		return false, fmt.Errorf("no probe can be built for %s", key)
	}
	probeChart, err := WithMutatedFile(c.base, m.File, data)
	if err != nil {
		return false, err
	}

	executed := false
	for _, ctx := range ctxs {
		before, err := c.renderOriginal(ctx)
		if err != nil {
			return false, err
		}
		after, err := Render(probeChart, ctx)
		if err != nil {
			executed = true // the probe's fail was evaluated
			break
		}
		same, err := Compare(before, after)
		if err != nil {
			return false, err
		}
		if !same {
			executed = true
			break
		}
	}

	c.mu.Lock()
	c.probed[key] = executed
	c.mu.Unlock()
	return executed, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/equivalence/ -race -v`
Expected: PASS, all of Tasks 2, 3, 5 and 6.

- [ ] **Step 5: Commit**

```bash
git add internal/equivalence/checker.go internal/equivalence/checker_test.go
git commit -m "feat(equivalence): judge a mutation against every covering value set"
```

---

### Task 7: Extract render contexts from helm-unittest suites

The riskiest component: reproducing the value merge helm-unittest performs in the unexported `polishTestJobsPathInfo` and `getUserValues`. It goes in `internal/runner/unittest.go` because that file is the only place allowed to import helm-unittest.

**Files:**
- Modify: `internal/runner/unittest.go`
- Test: `internal/runner/unittest_test.go` (create)

**Interfaces:**
- Consumes: `equivalence.RenderContext` (Task 2); `Suite` and `SuiteKey` (existing).
- Produces: `func RenderContexts(chartDir string, s *Suite) ([]equivalence.RenderContext, error)`; `func (s *Suite) UsesKubernetesProvider() bool`.

- [ ] **Step 1: Write the failing test**

Create `internal/runner/unittest_test.go`:

```go
package runner

import (
	"path/filepath"
	"testing"
)

const fixtureChart = "../../testdata/charts/sample"

func TestRenderContextsMergeSuiteAndJobValues(t *testing.T) {
	// helm-unittest merges suite-level `set` under each job's own `set`, and
	// prepends suite `values` files to the job's. Getting this wrong renders the
	// wrong branch, which would make killed mutants look equivalent.
	chart, err := LoadChart(fixtureChart)
	if err != nil {
		t.Fatalf("LoadChart: %v", err)
	}
	suites, err := DiscoverSuites(fixtureChart, chart, Options{TestFiles: []string{"tests/*_test.yaml"}})
	if err != nil {
		t.Fatalf("DiscoverSuites: %v", err)
	}
	if len(suites) == 0 {
		t.Fatal("no suites discovered")
	}

	total := 0
	for _, s := range suites {
		ctxs, err := RenderContexts(fixtureChart, s)
		if err != nil {
			t.Fatalf("RenderContexts(%s): %v", s.Key, err)
		}
		total += len(ctxs)
		for _, ctx := range ctxs {
			if ctx.Name == "" {
				t.Errorf("%s: a context with no name cannot be cached or reported", s.Key)
			}
			if ctx.Capabilities == nil {
				t.Errorf("%s/%s: capabilities must be set explicitly, never left nil", s.Key, ctx.Name)
			}
			if ctx.Release.Name == "" {
				t.Errorf("%s/%s: release name must be populated", s.Key, ctx.Name)
			}
		}
	}
	if total == 0 {
		t.Fatal("no render contexts extracted from the fixture chart")
	}
}

func TestRenderContextNamesAreUnique(t *testing.T) {
	// Original renders are memoised by context name; a collision would silently
	// compare a mutant against the wrong baseline.
	chart, err := LoadChart(fixtureChart)
	if err != nil {
		t.Fatalf("LoadChart: %v", err)
	}
	suites, err := DiscoverSuites(fixtureChart, chart, Options{TestFiles: []string{"tests/*_test.yaml"}})
	if err != nil {
		t.Fatalf("DiscoverSuites: %v", err)
	}
	seen := map[string]bool{}
	for _, s := range suites {
		ctxs, err := RenderContexts(fixtureChart, s)
		if err != nil {
			t.Fatalf("RenderContexts: %v", err)
		}
		for _, ctx := range ctxs {
			if seen[ctx.Name] {
				t.Fatalf("duplicate context name %q", ctx.Name)
			}
			seen[ctx.Name] = true
		}
	}
	_ = filepath.Separator // keep the import if the compiler complains
}
```

Drop the trailing `filepath` line and its import if unused.

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/runner/ -race -run TestRenderContext -v`
Expected: FAIL — `undefined: RenderContexts`.

- [ ] **Step 3: Write the implementation**

Add to `internal/runner/unittest.go`. Extend the import block with:

```go
	"github.com/helm-unittest/helm-unittest/pkg/unittest/valueutils"
	"github.com/mmpyro/helm-mutation-test/internal/equivalence"
	v3util "helm.sh/helm/v3/pkg/chartutil"
```

Then append:

```go
// UsesKubernetesProvider reports whether the suite installs a fake Kubernetes
// client. Such a suite's `lookup` calls return objects our own renderer will not
// see, so two renders could agree here that helm-unittest would find different.
// Mutants covered by such a suite skip equivalence detection entirely.
func (s *Suite) UsesKubernetesProvider() bool {
	if s.suite == nil {
		return false
	}
	if len(s.suite.KubernetesProvider.Objects) > 0 || s.suite.KubernetesProvider.Scheme != nil {
		return true
	}
	for _, job := range s.suite.Tests {
		if job == nil {
			continue
		}
		if len(job.KubernetesProvider.Objects) > 0 || job.KubernetesProvider.Scheme != nil {
			return true
		}
	}
	return false
}

// RenderContexts returns one equivalence.RenderContext per non-skipped test job
// in the suite.
//
// This reproduces the suite-to-job merge helm-unittest performs internally in
// polishTestJobsPathInfo and getUserValues, both unexported: suite values files
// are prepended to the job's, the suite's `set` acts as a global set merged
// before the job's own, and suite-level release, chart and capability settings
// fill in wherever the job leaves them empty.
//
// Reproducing it is the price of rendering under the values the tests actually
// used, which is what makes an Equivalent verdict safe. It is also the most
// likely thing here to go wrong, which is why
// TestNoKilledMutantIsJudgedEquivalent exists.
func RenderContexts(chartDir string, s *Suite) ([]equivalence.RenderContext, error) {
	if s == nil || s.suite == nil {
		return nil, nil
	}
	ts := s.suite
	suiteDir := filepath.Dir(filepath.Join(chartDir, filepath.FromSlash(s.Key.File)))

	out := make([]equivalence.RenderContext, 0, len(ts.Tests))
	for i, job := range ts.Tests {
		if job == nil || job.Skip.Reason != "" || ts.Skip.Reason != "" {
			continue
		}

		values, err := mergedValues(suiteDir, ts, job)
		if err != nil {
			return nil, fmt.Errorf("%s: job %q: %w", s.Key, job.Name, err)
		}

		out = append(out, equivalence.RenderContext{
			Name:            fmt.Sprintf("%s#%d/%d %s", s.Key.File, s.Key.Ordinal, i, job.Name),
			Values:          values,
			Release:         releaseOptions(ts, job),
			Capabilities:    capabilities(ts, job),
			ChartVersion:    cmpOr(job.Chart.Version, ts.Chart.Version),
			ChartAppVersion: cmpOr(job.Chart.AppVersion, ts.Chart.AppVersion),
		})
	}
	return out, nil
}

// mergedValues reproduces TestJob.getUserValues: values files first, then the
// suite-level set, then the job's own set, each merged over what came before.
func mergedValues(suiteDir string, ts *unittest.TestSuite, job *unittest.TestJob) (map[string]any, error) {
	base := map[string]any{}

	files := append(slices.Clone(ts.Values), job.Values...)
	for _, p := range files {
		path := p
		if !filepath.IsAbs(path) {
			path = filepath.Join(suiteDir, path)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading values file %s: %w", p, err)
		}
		var v map[string]any
		if err := yaml.Unmarshal(b, &v); err != nil {
			return nil, fmt.Errorf("parsing values file %s: %w", p, err)
		}
		base = v3util.MergeTables(v, base)
	}

	for _, set := range []map[string]any{ts.Set, job.Set} {
		for path, val := range set {
			built, err := valueutils.BuildValueOfSetPath(val, path)
			if err != nil {
				return nil, fmt.Errorf("building set path %s: %w", path, err)
			}
			base = v3util.MergeTables(built, base)
		}
	}
	return base, nil
}

// releaseOptions reproduces polishReleaseSettings: the job wins, the suite fills
// in, and a name is always present because Helm's own default is "RELEASE-NAME".
func releaseOptions(ts *unittest.TestSuite, job *unittest.TestJob) v3util.ReleaseOptions {
	return v3util.ReleaseOptions{
		Name:      cmpOr(job.Release.Name, ts.Release.Name, "RELEASE-NAME"),
		Namespace: cmpOr(job.Release.Namespace, ts.Release.Namespace, "NAMESPACE"),
		Revision:  max(max(job.Release.Revision, ts.Release.Revision), 1),
		IsUpgrade: job.Release.IsUpgrade || ts.Release.IsUpgrade,
	}
}

// capabilities reproduces polishCapabilitiesSettings and capabilitiesV3.
//
// The Copy() is not optional: helm-unittest itself does `capabilities :=
// v3util.DefaultCapabilities` and writes through that package-level pointer,
// which is the race that forced worker subprocesses. We must not repeat it.
func capabilities(ts *unittest.TestSuite, job *unittest.TestJob) *v3util.Capabilities {
	job.SetCapabilities() // fills job.Capabilities from its CapabilitiesFields map

	caps := v3util.DefaultCapabilities.Copy()
	major := cmpOr(job.Capabilities.MajorVersion, ts.Capabilities.MajorVersion, caps.KubeVersion.Major)
	minor := cmpOr(job.Capabilities.MinorVersion, ts.Capabilities.MinorVersion, caps.KubeVersion.Minor)
	caps.KubeVersion = v3util.KubeVersion{
		Version: fmt.Sprintf("v%s.%s.0", major, minor),
		Major:   major,
		Minor:   minor,
	}

	apis := append(slices.Clone(job.Capabilities.APIVersions), ts.Capabilities.APIVersions...)
	if len(apis) > 0 {
		caps.APIVersions = v3util.VersionSet(apis)
	}
	return caps
}

// cmpOr returns the first non-zero argument.
func cmpOr[T comparable](vals ...T) T {
	var zero T
	for _, v := range vals {
		if v != zero {
			return v
		}
	}
	return zero
}
```

Add `os` and `gopkg.in/yaml.v3` to the imports if they are not already there. `slices`, `fmt` and `path/filepath` already are.

If `KubernetesProvider` does not expose `Objects` / `Scheme` in v1.0.3, adjust `UsesKubernetesProvider` to whatever fields it does expose — check with `go doc github.com/helm-unittest/helm-unittest/pkg/unittest.KubernetesFakeClientProvider`. The requirement is only that a suite declaring a fake provider returns true.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/runner/ -race -run TestRenderContext -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runner/unittest.go internal/runner/unittest_test.go
git commit -m "feat(runner): extract a render context per helm-unittest test job"
```

---

### Task 8: Wire the pass into the session

**Files:**
- Create: `internal/runner/equivalence.go`
- Modify: `internal/runner/session.go`
- Test: `internal/runner/equivalence_test.go`

**Interfaces:**
- Consumes: `RenderContexts`, `UsesKubernetesProvider` (Task 7); `equivalence.NewChecker`, `Checker.Judge`, `Mutation` (Task 6); `model.StatusEquivalent` (Task 1).
- Produces: `func CheckEquivalence(ctx context.Context, mutants []model.Mutant, in EquivalenceInput) int`, where

```go
type EquivalenceInput struct {
	ChartDir string
	Suites   []*Suite
	Files    map[string]*source.File
	Parallel int
}
```

  It mutates `mutants` in place and returns how many became `Equivalent`.

- [ ] **Step 1: Write the failing test**

Create `internal/runner/equivalence_test.go`:

```go
package runner

import (
	"context"
	"testing"

	"github.com/mmpyro/helm-mutation-test/internal/model"
)

func TestCheckEquivalenceOnlyTouchesSurvivors(t *testing.T) {
	// The pass may only ever turn Survived into Equivalent. Anything else would
	// let it rewrite a kill, an invalid or a no-coverage verdict.
	mutants := []model.Mutant{
		{ID: "k", Status: model.StatusKilled, File: "templates/deployment.yaml"},
		{ID: "n", Status: model.StatusNoCoverage, File: "templates/deployment.yaml"},
		{ID: "i", Status: model.StatusInvalid, File: "templates/deployment.yaml"},
	}
	before := make([]model.Status, len(mutants))
	for i, m := range mutants {
		before[i] = m.Status
	}

	CheckEquivalence(context.Background(), mutants, EquivalenceInput{
		ChartDir: fixtureChart,
		Parallel: 2,
	})

	for i, m := range mutants {
		if m.Status != before[i] {
			t.Errorf("%s: status changed from %s to %s", m.ID, before[i], m.Status)
		}
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/runner/ -race -run TestCheckEquivalenceOnlyTouchesSurvivors -v`
Expected: FAIL — `undefined: CheckEquivalence`.

- [ ] **Step 3: Write the implementation**

Create `internal/runner/equivalence.go`:

```go
package runner

import (
	"context"
	"sync"

	"github.com/mmpyro/helm-mutation-test/internal/equivalence"
	"github.com/mmpyro/helm-mutation-test/internal/model"
	"github.com/mmpyro/helm-mutation-test/internal/source"
)

// EquivalenceInput is what the equivalence pass needs beyond the mutants.
type EquivalenceInput struct {
	ChartDir string
	Suites   []*Suite
	Files    map[string]*source.File
	Parallel int
}

// CheckEquivalence re-renders every survivor and promotes the provably
// unkillable ones to Equivalent, in place. It returns how many it promoted.
//
// It runs in-process rather than in the worker subprocesses: those exist only
// because helm-unittest writes through Helm's package-level DefaultCapabilities,
// and our renderer copies that struct instead of aliasing it.
//
// Errors are not returned. A failure to prove equivalence is not a failure of
// the run — the mutant simply stays Survived, with the reason in its Detail.
func CheckEquivalence(ctx context.Context, mutants []model.Mutant, in EquivalenceInput) int {
	idx := survivorIndexes(mutants)
	if len(idx) == 0 {
		return 0
	}

	chart, err := LoadChart(in.ChartDir)
	if err != nil {
		markInconclusive(mutants, idx, "inconclusive: "+err.Error())
		return 0
	}

	ctxsBySuiteFile, skipFiles := contextsBySuiteFile(in)
	checker := equivalence.NewChecker(chart, in.Files)

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		queue   = make(chan int)
		changed int
	)
	workers := min(max(in.Parallel, 1), len(idx))
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range queue {
				m := mutants[i]
				verdict := judgeOne(checker, m, ctxsBySuiteFile, skipFiles)
				mu.Lock()
				if verdict.Equivalent {
					mutants[i].Status = model.StatusEquivalent
					changed++
				}
				mutants[i].Detail = verdict.Detail
				mu.Unlock()
			}
		}()
	}
	for _, i := range idx {
		select {
		case <-ctx.Done():
		case queue <- i:
			continue
		}
		break
	}
	close(queue)
	wg.Wait()
	return changed
}

// judgeOne assembles the contexts for one mutant's covering suites and judges it.
func judgeOne(
	checker *equivalence.Checker,
	m model.Mutant,
	ctxsBySuiteFile map[string][]equivalence.RenderContext,
	skipFiles map[string]bool,
) equivalence.Verdict {
	var ctxs []equivalence.RenderContext
	for _, file := range m.CoveringSuites {
		if skipFiles[file] {
			return equivalence.Verdict{Detail: "not checked: a covering suite uses a fake Kubernetes provider"}
		}
		ctxs = append(ctxs, ctxsBySuiteFile[file]...)
	}
	return checker.Judge(equivalence.Mutation{
		File:        m.File,
		Start:       m.StartByte,
		End:         m.EndByte,
		Replacement: m.Mutated,
	}, ctxs)
}

// contextsBySuiteFile groups every suite's render contexts by suite file, which
// is the granularity Mutant.CoveringSuites records. A suite whose contexts
// cannot be extracted, or which uses a fake Kubernetes provider, is recorded in
// the skip set so its mutants are left alone rather than judged on partial input.
func contextsBySuiteFile(in EquivalenceInput) (map[string][]equivalence.RenderContext, map[string]bool) {
	byFile := map[string][]equivalence.RenderContext{}
	skip := map[string]bool{}
	for _, s := range in.Suites {
		if s.UsesKubernetesProvider() {
			skip[s.Key.File] = true
			continue
		}
		ctxs, err := RenderContexts(in.ChartDir, s)
		if err != nil {
			skip[s.Key.File] = true
			continue
		}
		byFile[s.Key.File] = append(byFile[s.Key.File], ctxs...)
	}
	return byFile, skip
}

func survivorIndexes(mutants []model.Mutant) []int {
	var out []int
	for i, m := range mutants {
		if m.Status == model.StatusSurvived {
			out = append(out, i)
		}
	}
	return out
}

func markInconclusive(mutants []model.Mutant, idx []int, detail string) {
	for _, i := range idx {
		mutants[i].Detail = detail
	}
}
```

- [ ] **Step 4: Run the test**

Run: `go test ./internal/runner/ -race -run TestCheckEquivalenceOnlyTouchesSurvivors -v`
Expected: PASS.

- [ ] **Step 5: Call it from `Session.Run`**

In `internal/runner/session.go`, replace

```go
	run.Mutants = mutants
	run.ComputeTally()
```

with

```go
	run.Mutants = mutants
	if s.Cfg.EquivalenceCheck {
		s.phase("Checking survivors for equivalence")
		n := CheckEquivalence(ctx, run.Mutants, EquivalenceInput{
			ChartDir: s.Cfg.ChartPath,
			Suites:   baseline.Suites,
			Files:    byPath,
			Parallel: s.Cfg.Parallel,
		})
		run.EquivalenceChecked = true
		if n > 0 {
			s.phase(fmt.Sprintf("%d survivors are equivalent and cannot be killed", n))
		}
	}
	run.ComputeTally()
```

`config.EquivalenceCheck` does not exist yet, so this will not compile until Task 9. Do Task 9 before running the suite.

- [ ] **Step 6: Commit**

```bash
git add internal/runner/equivalence.go internal/runner/equivalence_test.go internal/runner/session.go
git commit -m "feat(runner): run the equivalence pass over survivors after evaluation"
```

---

### Task 9: Config field and CLI flag

**Files:**
- Modify: `internal/config/config.go:60` (the `SkipEquivalent` field), `:99` (`Defaults`)
- Modify: `cmd/helm-mutation-test/main.go` (the "Mutation control" flag block, around line 141)
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `config.Config.EquivalenceCheck bool`, default `true`; the `--no-equivalence-check` flag.

- [ ] **Step 1: Write the failing test**

Add to `internal/config/config_test.go`:

```go
func TestEquivalenceCheckDefaultsOn(t *testing.T) {
	// A survivor that no assertion could ever catch is a false positive, and on a
	// well-tested chart it is most of the output. Detection is worth having by
	// default; --no-equivalence-check is the escape hatch.
	if !Defaults().EquivalenceCheck {
		t.Fatal("EquivalenceCheck should default to true")
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/config/ -race -run TestEquivalenceCheck -v`
Expected: FAIL — `cfg.EquivalenceCheck undefined`.

- [ ] **Step 3: Rename the dead field and default it on**

In `internal/config/config.go`, replace the `SkipEquivalent bool` line with:

```go
	// EquivalenceCheck re-renders each survivor to find mutations that cannot
	// change any manifest. They are reported as Equivalent and left out of the
	// score, which they would otherwise depress by an amount no test can fix.
	EquivalenceCheck bool
```

In `Defaults()`, add `EquivalenceCheck: true,`.

In `cmd/helm-mutation-test/main.go`, in the "Mutation control" block after the `--seed` line, add:

```go
	// pflag has no native negated bool, so bind the negation and invert it after
	// parsing. Keep backticks out of the usage string: pflag's UnquoteUsage turns
	// the first backquoted run into the flag's displayed value type.
	f.BoolVar(&noEquivalenceCheck, "no-equivalence-check", false,
		"skip the post-pass that identifies survivors no assertion could ever catch")
```

Declare `noEquivalenceCheck` as a variable in the same scope as `cfg`, and after `f.Parse` (or wherever the config is finalised, before `cfg.Validate`) set:

```go
	cfg.EquivalenceCheck = !noEquivalenceCheck
```

- [ ] **Step 4: Build and run everything**

Run: `make build && go test ./internal/config/ ./internal/runner/ -race`
Expected: PASS, and the session change from Task 8 now compiles.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go cmd/helm-mutation-test/main.go
git commit -m "feat(config): replace the dead SkipEquivalent field with --no-equivalence-check"
```

---

### Task 10: Name `Equivalent` in all five report formats

Every exclusion from the score is named in every format. A new status that only some formats mention would let a reader of the others mistake the score for full coverage.

**Files:**
- Modify: `internal/report/console.go` (`writeScore` around :53, `Console` around :34)
- Modify: `internal/report/markdown.go` (`rows` around :38, body around :60)
- Modify: `internal/report/html.go` (`summaryTable` rows around :79)
- Modify: `internal/report/junit.go` (`skipReason` around :101)
- Modify: `internal/report/stryker.go` (`strykerStatus` around :75, `describeMutant` around :102)
- Test: `internal/report/report_test.go`

**Interfaces:**
- Consumes: `model.StatusEquivalent`, `Tally.Equivalent`, `Run.Equivalent()`, `Run.EquivalenceChecked` (Task 1).
- Produces: no new exported API.

- [ ] **Step 1: Write the failing tests**

Add to `internal/report/report_test.go`:

```go
func TestEveryFormatNamesEquivalentMutants(t *testing.T) {
	// An exclusion no format mentions reads as full coverage to anyone looking at
	// that format. Equivalent joins no-coverage and invalid in all five.
	run := &model.Run{
		ChartName:          "sample",
		EquivalenceChecked: true,
		Mutants: []model.Mutant{
			{ID: "1", Mutator: "num-literal", File: "templates/a.yaml", Line: 3, Column: 5,
				Status: model.StatusKilled},
			{ID: "2", Mutator: "num-literal", File: "templates/a.yaml", Line: 7, Column: 9,
				Original: "nindent 4", Mutated: "nindent 5", Status: model.StatusEquivalent,
				Detail: "identical under 12 test-job value sets; span proven executed"},
		},
	}
	run.ComputeTally()

	for _, tc := range allFormats(t, run) {
		if !strings.Contains(strings.ToLower(tc.output), "equivalent") {
			t.Errorf("%s does not name equivalent mutants:\n%s", tc.name, tc.output)
		}
	}
}

func TestEveryFormatSaysWhenEquivalenceWasNotChecked(t *testing.T) {
	// "equivalent 0" with the check disabled would read as "checked, found none".
	run := &model.Run{
		ChartName:          "sample",
		EquivalenceChecked: false,
		Mutants: []model.Mutant{
			{ID: "1", Mutator: "num-literal", File: "templates/a.yaml", Line: 3, Column: 5,
				Status: model.StatusSurvived},
		},
	}
	run.ComputeTally()

	for _, tc := range allFormats(t, run) {
		if !strings.Contains(strings.ToLower(tc.output), "equivalence") {
			t.Errorf("%s does not say the equivalence check was skipped:\n%s", tc.name, tc.output)
		}
	}
}
```

If `report_test.go` has no `allFormats` helper, add one that renders the run through all five formats and returns `[]struct{ name, output string }`. Reuse whatever the existing "formats never disagree" test uses to enumerate formats rather than writing a second enumeration.

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/report/ -race -run 'TestEveryFormat.*Equivalen' -v`
Expected: FAIL for every format.

- [ ] **Step 3: Update the five formats**

`console.go`, in `writeScore`, insert before the `Invalid` clause:

```go
	if run.Tally.Equivalent > 0 {
		excluded = append(excluded, fmt.Sprintf("%d equivalent", run.Tally.Equivalent))
	}
```

and after the `excluded` block:

```go
	if !run.EquivalenceChecked {
		fmt.Fprintf(b, "  %s\n", c.faint(
			"equivalence check skipped (--no-equivalence-check): some survivors may be unkillable"))
	}
```

Add a section function beside `writeNoCoverage` and call it from `Console` between `writeNoCoverage` and `writeInvalid`:

```go
// writeEquivalent lists mutants no assertion could ever catch. They are not
// work items, so they are shown compactly — the point is to account for them,
// not to send anyone chasing them.
func writeEquivalent(b *strings.Builder, run *model.Run, c colours) {
	eq := run.Equivalent()
	if len(eq) == 0 {
		return
	}
	fmt.Fprintf(b, "  %s  %s\n", c.bold("Equivalent"),
		c.faint("cannot change any rendered manifest; not scored"))
	for _, m := range eq {
		fmt.Fprintf(b, "    %s:%d:%d  %s\n", m.File, m.Line, m.Column, c.faint(m.Mutator))
	}
	b.WriteString("\n")
}
```

`markdown.go`, add to `rows` after the No coverage entry:

```go
		{"Equivalent", "cannot change any rendered manifest — unkillable", run.Tally.Equivalent},
```

and after the `Score counts only...` line:

```go
	if !run.EquivalenceChecked {
		b.WriteString("> The equivalence check was skipped (`--no-equivalence-check`); " +
			"some survivors may be unkillable.\n\n")
	}
```

`html.go`, add to the `rows` slice after No coverage:

```go
		{"Equivalent", "cannot change any rendered manifest — unkillable", run.Tally.Equivalent, "muted"},
```

and after the `Scored` row:

```go
	if !run.EquivalenceChecked {
		fmt.Fprint(b, `<tr class="warn"><th>Equivalence</th><td>-</td>`+
			`<td>check skipped (--no-equivalence-check); some survivors may be unkillable</td></tr>`)
	}
```

`junit.go`, add to `skipReason`:

```go
	case model.StatusEquivalent:
		return "equivalent: the mutation cannot change any rendered manifest, so no assertion could catch it"
```

and extend the file's leading comment so it lists `Equivalent` among the statuses that become `<skipped>`. If the JUnit report emits a run-level notice for `--max-mutants` (`cappedNotice`), add the same shape of notice when `!run.EquivalenceChecked`.

`stryker.go`, add to `strykerStatus`:

```go
	case model.StatusEquivalent:
		return "Ignored"
```

extend that function's comment to explain the mapping, and add to `describeMutant`:

```go
	case model.StatusEquivalent:
		return fmt.Sprintf("%s: %s", m.Mutator, "cannot change any rendered manifest")
```

- [ ] **Step 4: Run the report package**

Run: `go test ./internal/report/ -race -v`
Expected: PASS, including the pre-existing cross-format agreement tests.

- [ ] **Step 5: Commit**

```bash
git add internal/report/
git commit -m "feat(report): name equivalent mutants in all five formats"
```

---

### Task 11: The fixture invariants

The end-to-end guard. This is where a wrong value-context reconstruction gets caught.

**Files:**
- Modify: `internal/runner/session_test.go`
- Test: same file

**Interfaces:**
- Consumes: everything above.
- Produces: no API.

- [ ] **Step 1: Write the failing tests**

Add to `internal/runner/session_test.go`:

```go
func TestNoKilledMutantIsJudgedEquivalent(t *testing.T) {
	// The safety net for RenderContexts. A killed mutant demonstrably changed
	// something a test observed, so its render MUST differ under some covering
	// context. If the suite-to-job value merge is wrong we render the wrong
	// branch, killed mutants start looking identical, and this fails.
	run := scoreFixture(t, "tests/strong_test.yaml")

	var killed []model.Mutant
	for _, m := range run.Mutants {
		if m.Status == model.StatusKilled {
			killed = append(killed, m)
		}
	}
	if len(killed) == 0 {
		t.Fatal("no killed mutants to check")
	}
	// Pretend they survived, then judge them. None may be called equivalent.
	for i := range killed {
		killed[i].Status = model.StatusSurvived
	}
	CheckEquivalence(context.Background(), killed, equivalenceInputForFixture(t))

	for _, m := range killed {
		if m.Status == model.StatusEquivalent {
			t.Errorf("killed mutant judged equivalent: %s %s:%d (%q -> %q): %s",
				m.Mutator, m.File, m.Line, m.Original, m.Mutated, m.Detail)
		}
	}
}

func TestStrongSuiteHasNoRealSurvivors(t *testing.T) {
	// Every one of the strong suite's survivors is an equivalent mutant, verified
	// by hand in docs/concepts.md. With detection on, its score is the honest 100%
	// and its actionable output is empty.
	run := scoreFixture(t, "tests/strong_test.yaml")
	if run.Tally.Survived != 0 {
		t.Errorf("strong suite has %d survivors, want 0; first: %+v",
			run.Tally.Survived, run.Survived()[0])
	}
	if run.Tally.Equivalent == 0 {
		t.Error("strong suite should have equivalent mutants; found none")
	}
	if got := run.Score(); got != 100 {
		t.Errorf("strong suite score = %.1f, want 100", got)
	}
}

func TestEquivalenceVerdictsAreIndependentOfParallelism(t *testing.T) {
	one := scoreFixtureParallel(t, "tests/strong_test.yaml", 1)
	many := scoreFixtureParallel(t, "tests/strong_test.yaml", 4)
	if one.Tally.Equivalent != many.Tally.Equivalent {
		t.Fatalf("equivalent count differs by worker count: %d at -p1, %d at -p4",
			one.Tally.Equivalent, many.Tally.Equivalent)
	}
}
```

Use whatever fixture helpers `session_test.go` already has. If there is no `scoreFixture`/`scoreFixtureParallel`, adapt the names to the existing helper and add a thin wrapper rather than duplicating setup. `equivalenceInputForFixture` builds an `EquivalenceInput` for `testdata/charts/sample` with the strong suite's discovered suites and loaded files — factor it out of whatever the existing test setup already does.

- [ ] **Step 2: Run them**

Run: `go test ./internal/runner/ -race -run 'TestNoKilledMutantIsJudgedEquivalent|TestStrongSuiteHasNoRealSurvivors|TestEquivalenceVerdictsAreIndependent' -v`
Expected: These take minutes — the runner package spawns worker subprocesses and renders the real fixture. That is normal, not a hang.

If `TestNoKilledMutantIsJudgedEquivalent` fails, **do not weaken it.** It means `RenderContexts` produced the wrong values; the failure message names the mutant, so render that context by hand and compare against `helm unittest` before changing anything.

If `TestStrongSuiteHasNoRealSurvivors` reports a nonzero survivor count, inspect the named survivor. Either it is a genuine equivalent the probe rejected (fix the probe) or `docs/concepts.md`'s claim that all 21 are equivalent is wrong for that site (fix the doc, and say so).

- [ ] **Step 3: Update the existing fixture expectations**

`TestWeakSuiteScoresLowAndStrongScoresHigh` still holds — strong is now higher, weak barely moves — but re-run it and confirm. The weak suite's score rises as some of its 275 survivors reclassify; the assertion is `< 45%` and CI's gate is 50%, and the arithmetic leaves a wide margin (6 killed against any plausible survivor count stays under 20%). If it does breach, that is a real finding about the fixture, not a number to edit.

Run: `make test`
Expected: PASS across all packages.

- [ ] **Step 4: Update the integration suite**

Add to `test/integration`: one run with default flags asserting the console output names equivalent mutants, and one with `--no-equivalence-check` asserting it says the check was skipped. Exit codes are unchanged in both.

Run: `make integration-tests`
Expected: PASS (~11s plus the new cases).

- [ ] **Step 5: Commit**

```bash
git add internal/runner/session_test.go test/integration/
git commit -m "test: pin that no killed mutant is equivalent and the strong suite scores 100%"
```

---

### Task 12: Documentation

**Files:**
- Modify: `docs/concepts.md` (the equivalent-mutant section, around :120-200, and the status-precedence list around :205)
- Modify: `docs/cli-reference.md`
- Modify: `docs/reports.md`
- Modify: `README.md` (the demonstration table)
- Modify: `CLAUDE.md`

**Interfaces:**
- Consumes: the measured numbers from Task 11.
- Produces: no API.

- [ ] **Step 1: Rewrite the concepts section**

In `docs/concepts.md`, the paragraph beginning "**The tool does not detect or exclude them.**" is now false. Replace it with a description of the feature: the parsed-render comparison, the per-test-job contexts, the execution probe, and why an unexercised span stays `Survived`. Keep the by-hand recipe — it is still how a reader verifies a verdict. Update the strong-suite numbers to the measured post-detection figures.

Add `Equivalent` to the status-precedence list, noting that it is decided after classification rather than inside `Classify`, and only for survivors.

- [ ] **Step 2: Document the flag and the reports**

`docs/cli-reference.md`: add `--no-equivalence-check` to the mutation-control flags, with a sentence on the cost and on why the default is on.

`docs/reports.md`: add `Equivalent` to the status table, with its Stryker (`Ignored`) and JUnit (`<skipped>`) mappings, and note that every format states when the check was skipped.

- [ ] **Step 3: Correct the README table**

The demonstration table is stale independently of this work: it claims weak = 6.6% (7 killed / 99 survived / 4 invalid) against an actual 2.1%, and it has no no-coverage column, which hides 163 no-coverage mutants — the larger of the two findings. Re-run both suites, and rebuild the table with columns for killed, survived, equivalent, no-coverage, invalid and score.

Run: `make demo`
Use its output for the numbers. Do not copy the figures from this plan or from `features.md`; take them from the run.

- [ ] **Step 4: Update CLAUDE.md**

Add to the architecture table: `internal/equivalence` — "Decide whether a mutation can change any rendered manifest. Imports Helm, never `runner`."

Add to "Core design rules", beside the existing exclusion rule, that `Equivalent` is the fourth excluded status and that every uncertain verdict must resolve to `Survived`.

Add to "Gotchas that have already cost time": the reason `RenderContexts` reproduces helm-unittest's unexported value merge, and that `TestNoKilledMutantIsJudgedEquivalent` is the test that catches it drifting.

- [ ] **Step 5: Verify and commit**

Run: `make lint && make test && make integration-tests`
Expected: all PASS.

```bash
git add docs/ README.md CLAUDE.md
git commit -m "docs: describe equivalent-mutant detection and refresh the stale README table"
```

---

## Self-Review

**Spec coverage.** Every section of the spec maps to a task: model changes → 1; render and compare → 2; in-memory chart mutation → 3; probe span → 4; probe → 5; verdict table → 6; `RenderContext` extraction and the `kubernetesProvider` skip → 7; post-pass placement, parallelism and the `Survived → Equivalent`-only rule → 8; config and CLI → 9; five formats plus the Stryker and JUnit mappings → 10; the killed-mutant invariant, fixture expectations, parallelism invariance and integration → 11; all four documentation files and the README correction → 12. The spec's "out of scope" items (the `num-literal` guard, a `NotExercised` status, dead-configuration reporting) appear in no task, which is correct.

**Type consistency.** `RenderContext`, `Mutation`, `Verdict`, `Checker`, `Render`, `Compare`, `WithMutatedFile`, `ProbeBytes`, `Canary`, `EnclosingPipelineSpan`, `RenderContexts`, `UsesKubernetesProvider`, `CheckEquivalence`, `EquivalenceInput`, `StatusEquivalent`, `Tally.Equivalent`, `Run.Equivalent()`, `Run.EquivalenceChecked` and `Config.EquivalenceCheck` are each defined in exactly one task and used with the same name and signature everywhere after.

**Known soft spots**, flagged rather than hidden:

- Task 7's `UsesKubernetesProvider` guesses at `KubernetesFakeClientProvider`'s field names. The task says to check with `go doc` and adjust; the requirement is behavioural.
- Task 10's tests assume a format-enumeration helper in `report_test.go`. The task says to reuse the existing one rather than add a second.
- Task 11's helper names (`scoreFixture`, `scoreFixtureParallel`, `equivalenceInputForFixture`) are placeholders for whatever `session_test.go` already provides; the task says to adapt rather than duplicate.
