# range-empty Mutator Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a ninth mutator that forces `{{ range }}` loops to iterate zero times, and fix the equivalence probe bug that discovering it uncovered.

**Architecture:** `range-empty` is a ninth `Mutator` in the existing registry, so it inherits generation, coverage, evaluation, classification, scoring and all five report formats without touching the pipeline. Both the mutator and the equivalence probe need to know where a ranged expression starts when a `$k, $v :=` declaration precedes it, so one lexical helper in `internal/source` serves both. End-to-end evidence comes from a new fixture chart, `testdata/charts/rangeloop`, leaving `testdata/charts/sample` untouched.

**Tech Stack:** Go 1.24, `text/template/parse`, helm-unittest v1.0.3, helm.sh/helm/v3 v3.19.0.

**Spec:** `docs/specs/2026-08-03-range-empty-mutator-design.md`

## Global Constraints

- **Always run `-race`.** `make test` is `go test ./... -race`. The helm-unittest global-state race was invisible without it.
- **`internal/coverage` imports nothing from this module.** Do not add an import there.
- **`internal/runner/unittest.go` is the only file that imports helm-unittest.** No task here touches it.
- **Mutator tests assert on resulting source, not byte offsets** — use the `applied` and `spans` helpers in `internal/mutator/mutator_test.go`.
- **A project `Write` hook validates YAML and rejects every file under `testdata/charts/*/templates/`**, because Helm templates are not valid YAML before rendering. Create those files with a shell heredoc, not the editor tool.
- **`sed` on this machine is GNU sed**, so BSD-style `sed -i ''` fails. Use `python3` for in-place edits in throwaway scripts.
- **Never call `cache.StoreToFileIfNeeded()`.**
- **`testdata/charts/sample` must not gain a `range`.** Its mutant count (458) and tally are quoted in ~35 places across `README.md` and `docs/`, and `internal/runner/session_test.go` asserts the strong suite scores exactly 100.0%.
- **Mutator descriptions must be backtick-free.** pflag's `UnquoteUsage` turns the first backquoted string into the flag's displayed value type.
- **Every uncertain equivalence verdict resolves to `Survived`, never `Equivalent`.** Excluding a mutant raises the score, so the burden of proof is high.

## Verified During Planning

These were confirmed by prototyping the whole change and then reverting it, so the code below is measured, not guessed:

- `{{- range $k, $v := .Values.labels }}` → replacing the whole pipeline yields `undefined variable "$v"` at **parse** time. Preserving the declaration prefix parses cleanly.
- With the prototype applied, `go test -race` passed for `internal/source`, `internal/equivalence`, `internal/config`, `internal/report`, `internal/model`, `internal/coverage`, `internal/workspace`. The **only** failure was `TestRegistryHasAllEightMutators` ("registry has 9 mutators … want 8"), which Task 5 renames.
- The built binary against `testdata/charts/sample -f 'tests/strong_test.yaml'` still reported **`458 mutants`**, `Score 100.0% (417 killed / 0 survived)`, `not scored: 21 equivalent · 20 invalid` — unchanged, because `sample` contains no `range`.
- The Task 6 fixture was built and run exactly as written below. `helm template` produced the intended `port-8080`, `port-9090`, `label-region`, `label-tier` keys; both suites passed `helm unittest`; and the full prototype scored it as:

  | suite | killed | survived | equivalent |
  |---|---:|---:|---:|
  | `weak_test.yaml` | 0 | 2 | 1 |
  | `strong_test.yaml` | 2 | 0 | 1 |

  The `extras` verdict read `identical under N test-job value sets; span proven executed`, which is the end-to-end confirmation that the declaration-carrying probe parses and proves execution. **Every assertion in Task 6 is therefore a measured value, not an estimate.**
- **Not verified during planning:** the `internal/runner` package as a whole, which takes ~3min under `-race`. Task 5 and Task 6 each run it.

---

### Task 1: Declaration-aware spans in `internal/source`

**Files:**
- Modify: `internal/source/tmplast.go`
- Test: `internal/source/tmplast_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `func cutDeclarations(s string) (rest string, cut bool)` — unexported, package `source`.
  - `func isIdentByte(c byte) bool` — unexported, package `source`.
  - `func SpanOfRangedExpression(f *File, pipe *parse.PipeNode) (start, end int, ok bool)` — used by Task 5.

- [ ] **Step 1: Write the failing test for `cutDeclarations`**

Add to `internal/source/tmplast_test.go`. The test file is package `source`, so unexported functions are directly reachable.

```go
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
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./internal/source/ -run TestCutDeclarations -v`
Expected: FAIL — compile error, `undefined: cutDeclarations`.

- [ ] **Step 3: Implement `cutDeclarations` and `isIdentByte`**

In `internal/source/tmplast.go`, insert immediately **above** the existing `func isSpace(c byte) bool {`:

```go
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
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/source/ -run TestCutDeclarations -v`
Expected: PASS, all nine subtests.

- [ ] **Step 5: Write the failing test for `SpanOfRangedExpression`**

Add to `internal/source/tmplast_test.go`. Confirm `"text/template/parse"` is in the file's import block; add it if not.

```go
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
```

- [ ] **Step 6: Run it to make sure it fails**

Run: `go test ./internal/source/ -run TestSpanOfRangedExpression -v`
Expected: FAIL — compile error, `undefined: SpanOfRangedExpression`.

- [ ] **Step 7: Implement `SpanOfRangedExpression`**

In `internal/source/tmplast.go`, insert directly **below** the `cutDeclarations`/`isIdentByte` block added in Step 3:

```go
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
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `go test -race ./internal/source/ -v -run 'TestCutDeclarations|TestSpanOfRangedExpression'`
Expected: PASS.

- [ ] **Step 9: Confirm nothing else in the package regressed**

Run: `go test -race ./internal/source/`
Expected: `ok`. No existing behaviour changed yet — this task only adds functions.

- [ ] **Step 10: Check formatting and commit**

```bash
gofmt -l internal/source/
go vet ./internal/source/
git add internal/source/tmplast.go internal/source/tmplast_test.go
git commit -m "feat(source): locate a ranged expression past its declarations

RangeNode's Pipe.Position() is the offset of \$k, not of the expression
being ranged over, so replacing the whole pipeline leaves the body's
variables undeclared and the template no longer parses."
```

---

### Task 2: Preserve declarations in the probe span

This is a live soundness fix, independent of the new mutator. `EnclosingPipelineSpan` cuts the control keyword but not the declarations, so the probe rewrites `{{- with $cfg := .Values.config }}` to `{{- with fail "canary" }}` and the template fails to parse with `undefined variable "$cfg"`. In `internal/equivalence/checker.go`, `renderOutcome` folds that into `outcome.errText`, `sameOutcome` reports "one errored and the other did not" as a difference, and any difference sets `executed = true`. The probe therefore claims execution **unconditionally**, which combined with identical mutant renders yields `Equivalent` on no evidence and raises the score.

It fires on the `{{- $fullName := include "chart.fullname" . -}}` that `helm create` scaffolds into every chart. Neither fixture uses that shape, which is why no test caught it.

**Files:**
- Modify: `internal/source/tmplast.go` (inside `EnclosingPipelineSpan`)
- Test: `internal/source/tmplast_test.go`

**Interfaces:**
- Consumes: `cutDeclarations` from Task 1.
- Produces: no new symbols. `EnclosingPipelineSpan` and `ProbeSpan` keep their existing signatures — that was the point of choosing a lexical helper.

- [ ] **Step 1: Write the failing test**

Add to `internal/source/tmplast_test.go`:

```go
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
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./internal/source/ -run TestProbeSpanPreservesVariableDeclarations -v`
Expected: FAIL on all three subtests — `probe span = "$k, $v := .Values.labels", want ".Values.labels"`, and the parse assertion reporting `undefined variable "$k"` / `"$cfg"` / `"$fullName"`.

- [ ] **Step 3: Cut declarations in `EnclosingPipelineSpan`**

In `internal/source/tmplast.go`, inside `EnclosingPipelineSpan`, the keyword loop currently ends and is followed by:

```go
	start = a.InnerStart + lead
	end = a.InnerEnd
	for start < end && isSpace(f.Bytes[start]) {
```

Insert the declaration cut immediately **before** `start = a.InnerStart + lead`:

```go
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
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/source/ -run TestProbeSpanPreservesVariableDeclarations -v`
Expected: PASS, all three subtests.

- [ ] **Step 5: Add the declaration shapes to the existing span table**

`TestEnclosingPipelineSpanKeepsKeywordsAndTrimMarkers` has a table of `{name, src, needle, wantSub}` cases. Append three rows to it, so the keyword-and-trim-marker property is also pinned for declarations:

```go
		{"range with declarations", "{{- range $k, $v := .Values.labels }}\n{{ $k }}\n{{- end }}", ".Values.labels", ".Values.labels"},
		{"with declaration", "{{- with $cfg := .Values.a }}\nx: 1\n{{- end }}", ".Values.a", ".Values.a"},
		{"declaration action", "{{- $n := include \"c.name\" . -}}\n", "include", `include "c.name" .`},
```

- [ ] **Step 6: Run the whole source package**

Run: `go test -race ./internal/source/`
Expected: `ok`. Confirmed during planning: no existing `source` test regresses from this change.

- [ ] **Step 7: Run the equivalence package, which consumes `ProbeSpan`**

Run: `go test -race ./internal/equivalence/`
Expected: `ok`. Confirmed during planning.

- [ ] **Step 8: Commit**

```bash
gofmt -l internal/source/
git add internal/source/tmplast.go internal/source/tmplast_test.go
git commit -m "fix(source): keep declarations inside the execution probe

Overwriting a \$k, \$v := prefix left the body's variables undeclared, so
the probe failed to parse rather than to render. sameOutcome reads that as
a difference and any difference sets executed = true, so the probe claimed
execution unconditionally and killable mutants were judged Equivalent."
```

---

### Task 3: Refuse a probe that cannot parse

Task 2 fixes the known shapes. This closes the class: a probe that does not parse fails for a reason unrelated to execution, whatever the cause.

**Files:**
- Modify: `internal/equivalence/probe.go`
- Test: `internal/equivalence/probe_test.go`

**Interfaces:**
- Consumes: `source.ParseTemplate`, `source.New` (both already exist); the Task 2 fix.
- Produces: `func probeParses(f *source.File, probed []byte) bool` — unexported, package `equivalence`.

- [ ] **Step 1: Write the failing test**

Add to `internal/equivalence/probe_test.go`. The file already has the `templateFile(src string) *source.File` helper.

```go
// TestProbeParsesRejectsBytesThatDoNotParse pins the general rule behind the
// declaration fix: the checker treats any difference between original and probe
// as proof the span executed, so a probe that fails to *parse* proves nothing
// while looking like proof of everything.
//
// After declarations are preserved, no naturally reachable input was found that
// still produces an unparseable probe, so this exercises the guard directly with
// deliberately broken bytes rather than through ProbeBytes. It is a safety net
// for future constructs, and is tested as one.
func TestProbeParsesRejectsBytesThatDoNotParse(t *testing.T) {
	f := templateFile("x: {{ .Values.a }}\n")

	if !probeParses(f, []byte("x: {{ fail \"c\" }}\n")) {
		t.Error("a well-formed probe must be accepted")
	}
	if probeParses(f, []byte("{{- range .Values.a }}\nno end\n")) {
		t.Error("an unterminated action must be rejected")
	}
	if probeParses(f, []byte("{{- range $k, $v := .Values.a }}\n{{ $k }}\n{{- end }}\n{{ $k }}\n")) {
		t.Error("a reference to an out-of-scope variable must be rejected")
	}
}

// TestProbeBytesKeepsDeclarationsSoTheProbeParses is the reachable regression for
// the same bug: every shape that carries a declaration must yield a probe that
// parses, so it is judged on whether it renders differently rather than on
// whether it compiles.
func TestProbeBytesKeepsDeclarationsSoTheProbeParses(t *testing.T) {
	tests := []struct{ name, src, mutated string }{
		{"range", "{{- range $k, $v := .Values.labels }}\n{{ $k }}: {{ $v }}\n{{- end }}", ".Values.labels"},
		{"with", "{{- with $cfg := .Values.config }}\nkey: {{ $cfg.a }}\n{{- end }}", ".Values.config"},
		{"declaration action", "{{- $n := include \"c.name\" . -}}\nname: {{ $n }}\n", "include"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := templateFile(tc.src)
			lo := strings.Index(tc.src, tc.mutated)
			data, ok := ProbeBytes(f, lo, lo+len(tc.mutated))
			if !ok {
				t.Fatal("ProbeBytes returned !ok; the declaration shape lost its probe")
			}
			if !strings.Contains(string(data), Canary) {
				t.Fatalf("probe does not contain the canary:\n%s", data)
			}
			if _, err := source.ParseTemplate(source.New(data, f.AbsPath, f.Path, f.Kind)); err != nil {
				t.Fatalf("probe does not parse: %v\n%s", err, data)
			}
		})
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./internal/equivalence/ -run 'TestProbeParses|TestProbeBytesKeepsDeclarations' -v`
Expected: FAIL — compile error, `undefined: probeParses`.

- [ ] **Step 3: Add the guard and wire it into `ProbeBytes`**

In `internal/equivalence/probe.go`, replace the template branch of `ProbeBytes`:

```go
	if f.Kind == source.KindTemplate {
		// ProbeSpan, not the enclosing pipeline: a probe wider than the mutation
		// proves only that the enclosing action was reached, which is a different
		// claim as soon as an and/or short circuit sits in between.
		ps, pe, ok := source.ProbeSpan(f, start, end)
		if !ok {
			return nil, false
		}
		return f.Apply(ps, pe, `fail "`+Canary+`"`), true
	}
```

with:

```go
	if f.Kind == source.KindTemplate {
		// ProbeSpan, not the enclosing pipeline: a probe wider than the mutation
		// proves only that the enclosing action was reached, which is a different
		// claim as soon as an and/or short circuit sits in between.
		ps, pe, ok := source.ProbeSpan(f, start, end)
		if !ok {
			return nil, false
		}
		probed := f.Apply(ps, pe, `fail "`+Canary+`"`)
		if !probeParses(f, probed) {
			return nil, false
		}
		return probed, true
	}
```

and add below `ProbeBytes`:

```go
// probeParses reports whether probed bytes still parse as a template.
//
// A probe that does not parse fails to render for a reason unrelated to
// execution, but the checker compares outcomes and treats any difference as proof
// the span ran — so an unparseable probe is indistinguishable from a real
// execution signal and would exclude a killable mutant from the score. Refusing
// leaves the mutant Survived and counted in Run.EquivalenceUnchecked, which is
// the direction the burden of proof requires.
//
// Cost is one parse per span, not per mutant: Checker.probed caches by span key,
// and a parse is negligible against a chart render.
func probeParses(f *source.File, probed []byte) bool {
	_, err := source.ParseTemplate(source.New(probed, f.AbsPath, f.Path, f.Kind))
	return err == nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/equivalence/ -run 'TestProbeParses|TestProbeBytesKeepsDeclarations' -v`
Expected: PASS.

- [ ] **Step 5: Run the whole equivalence package**

Run: `go test -race ./internal/equivalence/`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
gofmt -l internal/equivalence/
git add internal/equivalence/probe.go internal/equivalence/probe_test.go
git commit -m "fix(equivalence): refuse a probe that cannot parse

The checker reads any difference between original and probe as proof the
span executed, so a probe that fails to parse proves nothing while looking
like proof of everything. Refusing leaves the mutant survived."
```

---

### Task 4: Make the shared mutator invariants non-vacuous

`internal/mutator/mutator_test.go` has four invariants that loop over `IDs()`, so a new mutator inherits them automatically. Three of them contain **no `range` at all**, so they would pass vacuously for `range-empty` and buy zero protection. Extend the corpora *before* the mutator exists; they pass either way, which is what makes this a clean standalone commit.

**Files:**
- Test: `internal/mutator/mutator_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: nothing. Test-corpus change only.

- [ ] **Step 1: Add range sources to the parseability corpus**

In `TestMutantsKeepTemplatesParseable`, the `src` raw-string literal ends with:

```go
  env: {{ if eq .Values.env "prod" }}production{{ else }}dev{{ end }}
`
```

Append two range blocks so the last lines read:

```go
  env: {{ if eq .Values.env "prod" }}production{{ else }}dev{{ end }}
  {{- range .Values.ports }}
  - {{ . }}
  {{- end }}
  {{- range $k, $v := .Values.labels }}
  {{ $k }}: {{ $v }}
  {{- end }}
`
```

- [ ] **Step 2: Add range sources to the no-op corpus**

In `TestCandidatesAreAlwaysChanges`, append a third entry to `srcs`:

```go
		"{{- range .Values.ports }}\n- {{ . }}\n{{- end }}\n{{- range $k, $v := .Values.labels }}\n{{ $k }}: {{ $v }}\n{{- end }}\n",
```

- [ ] **Step 3: Add a broken range to the panic corpus**

In `TestNoMutatorPanicsOnUnparseableTemplate`, append two entries to `broken`:

```go
		`{{ range $k, $v := }}`,
		`{{ range .Values.x }}no end`,
```

- [ ] **Step 4: Run the invariants to confirm they still pass**

Run: `go test -race ./internal/mutator/ -run 'TestMutantsKeepTemplatesParseable|TestCandidatesAreAlwaysChanges|TestNoMutatorPanicsOnUnparseableTemplate' -v`
Expected: PASS. The existing eight mutators are being exercised against `range` sources for the first time; if any of them fails here, that is a real pre-existing bug — stop and report it rather than adjusting the corpus to hide it.

- [ ] **Step 5: Commit**

```bash
git add internal/mutator/mutator_test.go
git commit -m "test(mutator): put range loops in the shared invariant corpora

Three of the loop-over-IDs() invariants contained no range at all, so any
range mutator would inherit them vacuously."
```

---

### Task 5: The `range-empty` mutator

**Files:**
- Create: `internal/mutator/range_empty.go`
- Modify: `internal/mutator/mutator.go` (the ID const block)
- Test: `internal/mutator/mutator_test.go`

**Interfaces:**
- Consumes: `source.SpanOfRangedExpression` from Task 1; the corpora from Task 4.
- Produces: `mutator.IDRangeEmpty = "range-empty"`, used by Task 6's runner tests.

- [ ] **Step 1: Write the failing test**

Add to `internal/mutator/mutator_test.go`, after the `cond-negate` section:

```go
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
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./internal/mutator/ -run TestRangeEmpty -v`
Expected: FAIL — compile error, `undefined: IDRangeEmpty`.

- [ ] **Step 3: Add the ID const**

In `internal/mutator/mutator.go`, add to the const block:

```go
	IDRangeEmpty     = "range-empty"
```

- [ ] **Step 4: Create the mutator**

Create `internal/mutator/range_empty.go`:

```go
package mutator

import (
	"strings"
	"text/template/parse"

	"github.com/mmpyro/helm-mutation-test/internal/source"
)

func init() { register(rangeEmpty{}) }

// rangeEmpty forces every {{ range }} to iterate zero times.
//
// Loops were entirely unmutated before this: cond-negate covers if and with,
// and nothing targeted RangeNode, so a suite could assert nothing whatsoever
// about env vars, ports, ingress hosts or volume mounts and still score 100%.
// This is to range what cond-negate is to if.
//
// Where the range has an {{ else }}, the mutant renders the else branch rather
// than nothing. That is still a change to the manifest and still observable, so
// it needs no special case — but "the block disappears" is the natural intuition
// and it is wrong for that shape.
//
// The replacement is sprig's `list`, which with no arguments is a real empty
// list. A bare nil is a shape text/template treats inconsistently in an argument
// position.
type rangeEmpty struct{}

func (rangeEmpty) ID() string { return IDRangeEmpty }

func (rangeEmpty) Describe() string {
	return "force range loops to iterate zero times (range X -> range list)"
}

func (rangeEmpty) Mutate(f *source.File) []Candidate {
	tree, err := source.ParseTemplate(f)
	if err != nil {
		return nil
	}

	var out []Candidate
	tree.Walk(func(n parse.Node) bool {
		rng, ok := n.(*parse.RangeNode)
		if !ok {
			return true
		}
		// SpanOfRangedExpression, not SpanOfCondition: the latter starts at "$k"
		// for `range $k, $v := X`, and dropping the declaration leaves the body's
		// variables undefined so the mutant would not parse.
		start, end, ok := source.SpanOfRangedExpression(f, rng.Pipe)
		if !ok {
			return true
		}
		// An already-empty range would yield a no-op candidate, which can only
		// ever "survive" and inflate the survived count with a non-mutation.
		if strings.TrimSpace(f.Slice(start, end)) == "list" {
			return true
		}
		out = append(out, Candidate{
			Mutator:     IDRangeEmpty,
			Start:       start,
			End:         end,
			Replacement: "list",
		})
		return true
	})
	return out
}
```

- [ ] **Step 5: Run the new tests to verify they pass**

Run: `go test ./internal/mutator/ -run TestRangeEmpty -v`
Expected: PASS — all eight `TestRangeEmpty` subtests plus the two standalone tests.

- [ ] **Step 6: Run the package and watch the registry count fail**

Run: `go test -race ./internal/mutator/`
Expected: exactly one failure:
```
--- FAIL: TestRegistryHasAllEightMutators
    mutator_test.go:57: registry has 9 mutators ([bool-flip comparison-swap cond-negate default-drop num-literal range-empty required-drop str-literal yaml-key-delete]), want 8
```

- [ ] **Step 7: Update the registry count test**

In `internal/mutator/mutator_test.go`, rename the function and add the ID:

```go
func TestRegistryHasAllNineMutators(t *testing.T) {
	want := []string{
		IDBoolFlip, IDComparisonSwap, IDCondNegate, IDDefaultDrop,
		IDNumLiteral, IDRangeEmpty, IDRequiredDrop, IDStrLiteral, IDYAMLKeyDelete,
	}
```

Leave the body below it unchanged — it derives its counts from `want`.

- [ ] **Step 8: Run the whole mutator package**

Run: `go test -race ./internal/mutator/`
Expected: `ok`.

- [ ] **Step 9: Confirm the sample chart's numbers did not move**

`range-empty` must yield zero candidates on `testdata/charts/sample`, which has no `range`. This is what keeps every figure in `README.md` and `docs/` valid.

```bash
make build
./bin/helm-mutation-test testdata/charts/sample -f 'tests/strong_test.yaml' | tail -3
```

Expected: `458 mutants in …`, and a `Score 100.0%` line reading `(417 killed / 0 survived)`. If the mutant count changed, a `range` has appeared in `sample` — stop and report it.

- [ ] **Step 10: Run the runner package**

Run: `go test -race ./internal/runner/`
Expected: `ok`, in roughly 3 minutes. This is the slowest gate and covers `TestWeakSuiteScoresLowAndStrongScoresHigh`, the exact-100% assertion, and `TestInvalidMutantRateIsLow`. It was **not** run during planning, so treat a failure here as real.

- [ ] **Step 11: Commit**

```bash
gofmt -l internal/mutator/
make lint
git add internal/mutator/range_empty.go internal/mutator/mutator.go internal/mutator/mutator_test.go
git commit -m "feat(mutator): add range-empty

No mutator targeted *parse.RangeNode, so every loop in every chart was
mutation-invisible: a suite could assert nothing about env vars, ports or
ingress hosts and still score 100%."
```

---

### Task 6: The `rangeloop` fixture and its end-to-end tests

**Files:**
- Create: `testdata/charts/rangeloop/Chart.yaml`
- Create: `testdata/charts/rangeloop/values.yaml`
- Create: `testdata/charts/rangeloop/templates/configmap.yaml` *(heredoc only — the Write hook rejects templates)*
- Create: `testdata/charts/rangeloop/tests/weak_test.yaml`
- Create: `testdata/charts/rangeloop/tests/strong_test.yaml`
- Modify: `internal/runner/equivalence_test.go`

**Interfaces:**
- Consumes: `mutator.IDRangeEmpty` from Task 5; the existing `copyTree` and `runSession` test helpers.
- Produces: `func rangeLoopFixture(t *testing.T) string` in `internal/runner/equivalence_test.go`.

- [ ] **Step 1: Create the fixture, template via heredoc**

```bash
mkdir -p testdata/charts/rangeloop/templates testdata/charts/rangeloop/tests

cat > testdata/charts/rangeloop/Chart.yaml <<'EOF'
apiVersion: v2
name: rangeloop
description: |
  A minimal chart whose only interesting content is three range loops. It exists
  so range-empty has end-to-end evidence without adding a template to
  testdata/charts/sample, whose mutant count and scores are quoted throughout
  README.md and docs/.

  ports is a non-empty list: the base case, survived by weak_test.yaml and
  killed by strong_test.yaml. labels is a non-empty map ranged with
  "$k, $v :=": the declaration case, which must survive both the mutation and
  the equivalence probe. extras is empty under every test job, so forcing its
  loop to zero iterations provably changes nothing and is correctly Equivalent.
type: application
version: 0.1.0
appVersion: "1.0.0"
EOF

cat > testdata/charts/rangeloop/values.yaml <<'EOF'
name: app

ports:
  - 8080
  - 9090

labels:
  tier: backend
  region: eu

# Empty under every test job, and no job sets it. A loop over an empty list
# cannot change what renders, so range-empty on it is genuinely Equivalent
# rather than a survivor blaming the tests.
extras: []
EOF

cat > testdata/charts/rangeloop/templates/configmap.yaml <<'EOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Values.name }}
data:
  static: "always"
  {{- range .Values.ports }}
  port-{{ . }}: "open"
  {{- end }}
  {{- range $k, $v := .Values.labels }}
  label-{{ $k }}: {{ $v | quote }}
  {{- end }}
  {{- range .Values.extras }}
  extra-{{ . }}: "yes"
  {{- end }}
EOF

cat > testdata/charts/rangeloop/tests/weak_test.yaml <<'EOF'
suite: configmap-weak
templates:
  - templates/configmap.yaml
tests:
  # Deliberately asserts nothing that any loop produces, so forcing a loop to
  # zero iterations goes unnoticed. That is the weakness range-empty exposes.
  - it: renders a ConfigMap with its static key
    asserts:
      - isKind:
          of: ConfigMap
      - equal:
          path: metadata.name
          value: app
      - equal:
          path: data.static
          value: always
EOF

cat > testdata/charts/rangeloop/tests/strong_test.yaml <<'EOF'
suite: configmap-strong
templates:
  - templates/configmap.yaml
tests:
  # Asserts the loop output itself, so a range forced to zero iterations makes
  # these keys vanish and the assertions fail.
  - it: renders one key per port
    asserts:
      - equal:
          path: data.port-8080
          value: open
      - equal:
          path: data.port-9090
          value: open
  - it: renders one key per label, which requires the $k, $v declarations
    asserts:
      - equal:
          path: data.label-tier
          value: backend
      - equal:
          path: data.label-region
          value: eu
  - it: still renders the static key
    asserts:
      - equal:
          path: data.static
          value: always
EOF
```

- [ ] **Step 2: Verify both suites pass against the unmutated chart**

A fixture with a red baseline is a hard stop (exit code 2), so this must be green before anything else.

```bash
helm unittest -f 'tests/weak_test.yaml'   testdata/charts/rangeloop
helm unittest -f 'tests/strong_test.yaml' testdata/charts/rangeloop
```

Expected: both report all tests passed. If `data.label-tier` is missing, check the rendered output with `helm template testdata/charts/rangeloop` — map iteration is sorted by key, so `label-region` precedes `label-tier`.

- [ ] **Step 3: Write the failing end-to-end tests**

Add to `internal/runner/equivalence_test.go`, after the existing `shortCircuitFixture` helper. Add `"github.com/mmpyro/helm-mutation-test/internal/mutator"` to the file's imports.

```go
// rangeLoopFixture copies the range chart into a temp dir. It is a chart of its
// own rather than another template in testdata/charts/sample so that adding it
// does not move the fixture's scores.
func rangeLoopFixture(t *testing.T) string {
	t.Helper()
	src, err := filepath.Abs("../../testdata/charts/rangeloop")
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "rangeloop")
	if err := copyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	return dst
}

// rangeEmptyByStatus counts range-empty mutants in run by status.
func rangeEmptyByStatus(run *model.Run) map[model.Status]int {
	out := map[model.Status]int{}
	for _, m := range run.Mutants {
		if m.Mutator == mutator.IDRangeEmpty {
			out[m.Status]++
		}
	}
	return out
}

func scoreRangeLoop(t *testing.T, suite string) *model.Run {
	t.Helper()
	cfg := config.Defaults()
	cfg.ChartPath = rangeLoopFixture(t)
	cfg.TestFiles = []string{suite}
	cfg.Parallel = 4
	return runSession(t, cfg)
}

// TestRangeMutantSurvivesAWeakSuiteAndIsKilledByAStrongOne is the evidence that
// range-empty is a useful mutant rather than a guard-worthy one: it must separate
// a suite that only checks the ConfigMap exists from one that asserts what the
// loops produced. A mutation neither suite can tell apart would be noise.
func TestRangeMutantSurvivesAWeakSuiteAndIsKilledByAStrongOne(t *testing.T) {
	weak := rangeEmptyByStatus(scoreRangeLoop(t, "tests/weak_test.yaml"))
	strong := rangeEmptyByStatus(scoreRangeLoop(t, "tests/strong_test.yaml"))

	if weak[model.StatusKilled] != 0 {
		t.Errorf("the weak suite killed %d range mutants; it asserts nothing any loop produces",
			weak[model.StatusKilled])
	}
	if weak[model.StatusSurvived] < 2 {
		t.Errorf("the weak suite left %d range survivors, want at least 2 (ports and labels)",
			weak[model.StatusSurvived])
	}
	if strong[model.StatusKilled] < 2 {
		t.Errorf("the strong suite killed %d range mutants, want at least 2 (ports and labels): %v",
			strong[model.StatusKilled], strong)
	}
	if strong[model.StatusSurvived] != 0 {
		t.Errorf("the strong suite left %d range survivors, want 0", strong[model.StatusSurvived])
	}
}

// TestEmptyRangeIsJudgedEquivalent: extras is empty under every covering context,
// so forcing its loop to zero iterations cannot change any rendered manifest and
// no assertion could ever catch it. range evaluates its pipeline even when the
// result is empty, so the probe legitimately proves execution and the verdict is
// Equivalent — excluded from the score and named in the report, not hidden.
//
// This is also the end-to-end proof that the declaration-carrying probe parses:
// if it did not, the checker would read the parse failure as proof of execution
// and reach this verdict for the wrong reason, so the count below would be too
// high rather than too low.
func TestEmptyRangeIsJudgedEquivalent(t *testing.T) {
	got := rangeEmptyByStatus(scoreRangeLoop(t, "tests/strong_test.yaml"))
	if got[model.StatusEquivalent] != 1 {
		t.Errorf("range-empty statuses = %v, want exactly 1 equivalent (the extras loop)", got)
	}
}
```

- [ ] **Step 4: Run them**

Run: `go test -race ./internal/runner/ -run 'TestRangeMutantSurvives|TestEmptyRangeIsJudgedEquivalent' -v`
Expected: PASS. Diagnosis if not:
- `equivalent = 0` and `survived = 3` → the `extras` probe was refused. Print `m.Detail` for the survivor; `EquivalenceUnchecked` means no probe could be built.
- `equivalent = 3` → the probe is proving execution for something it should not. Check that Task 2 landed and that the mutated span still parses.
- `killed = 0` under the strong suite → the strong suite is not actually covering `templates/configmap.yaml`; confirm its `templates:` list.

- [ ] **Step 5: Run the whole runner package**

Run: `go test -race ./internal/runner/`
Expected: `ok`, ~3 minutes plus the two new scored runs.

- [ ] **Step 6: Commit**

```bash
git add testdata/charts/rangeloop internal/runner/equivalence_test.go
git commit -m "test(runner): add the rangeloop fixture and range-empty evidence

A chart of its own rather than a template in sample, whose mutant count and
scores are quoted throughout the docs. Three loops: a list and a map that
separate a weak suite from a strong one, and an empty list whose mutant is
genuinely equivalent."
```

---

### Task 7: Documentation

**Files:**
- Modify: `docs/mutators.md`
- Modify: `docs/README.md:16`
- Modify: `README.md:223`
- Modify: `docs/cli-reference.md:85`
- Modify: `CLAUDE.md`

**Interfaces:**
- Consumes: everything above. No code changes.

- [ ] **Step 1: Gather the real counts for the new docs**

Numbers in `docs/mutators.md` are measured, not estimated. Collect them:

```bash
./bin/helm-mutation-test testdata/charts/rangeloop -f 'tests/weak_test.yaml'   | grep -iE "range-empty|score|not scored"
./bin/helm-mutation-test testdata/charts/rangeloop -f 'tests/strong_test.yaml' | grep -iE "range-empty|score|not scored"
```

Use these for the section written in Step 3.

- [ ] **Step 2: Fix the counts and the "eight" claims**

`docs/mutators.md:3` — "Eight mutators, each producing" → "Nine mutators, each producing".

`docs/mutators.md:16-25` — add a row to the "At a glance" table, in alphabetical position between `num-literal` and `required-drop`:

```markdown
| [`range-empty`](#range-empty) | `range` loop expressions | 0 | 0 | — | — |
```

Then add this immediately below the table, before the "`k/s` is killed/survived" paragraph:

```markdown
`range-empty` shows zero because `testdata/charts/sample` contains no `range` at all. It is measured
against [`testdata/charts/rangeloop`](#range-empty) instead. A mutator that generated nothing is as
worth naming as a status that scored nothing — a blank row would read as "ran and found no weakness".
```

`docs/mutators.md:29` — "**six of the eight mutators score 0.0% against it**" is a claim about the mutators that actually fire on `sample`. Reword so the count stays true:

```markdown
headline: **six of the eight mutators that fire on this chart score 0.0% against it**, because a suite
```

`docs/README.md:16` — "All eight mutators, with a real before/after" → "All nine mutators, …".

`README.md:223` — "all eight mutators, with before/after examples" → "all nine mutators, …".

`docs/cli-reference.md:85` — the `--mutators` default cell, "*all eight*" → "*all nine*".

- [ ] **Step 3: Add the `range-empty` section**

Insert into `docs/mutators.md` between the `num-literal` section and the `required-drop` section (matching the file's alphabetical order), following the shape of the existing sections — bold one-line summary, **Before / after**, **Why it matters**, **The weak test it exposes**, then a `---` separator:

```markdown
## `range-empty`

**Force a `range` loop to iterate zero times: `range X` → `range list`.**

`list` is sprig's empty-list constructor, so the loop body never runs and everything it produced
disappears from the manifest. Variable declarations are preserved: `range $k, $v := X` becomes
`range $k, $v := list`, because dropping them leaves the body's `$k` and `$v` undeclared and the
template no longer parses — an unparseable mutant is `Invalid`, caught by every test regardless of
what it asserts, and grades nothing.

**Before / after.** A list, `testdata/charts/rangeloop/templates/configmap.yaml:7`:

```diff
-   {{- range .Values.ports }}
+   {{- range list }}
```

A map, where the declarations survive — `:10`:

```diff
-   {{- range $k, $v := .Values.labels }}
+   {{- range $k, $v := list }}
```

The whole expression is replaced, not just its first term, so a piped source collapses too:

```diff
- {{- range .Values.hosts | sortAlpha }}
+ {{- range list }}
```

Where the `range` has an `{{ else }}`, the mutant renders the **else** branch rather than nothing.
That is still a change to the manifest, so it is still a useful mutant — but "the block disappears"
is the wrong intuition for that shape.

**Why it matters.** Before this mutator, loops were completely unmutated: `cond-negate` covers `if`
and `with`, and nothing targeted `range`. Charts are full of them — env vars, ports, ingress hosts,
volume mounts, image pull secrets — so a suite could assert nothing whatsoever about any loop's
output and still score 100%. This is to `range` what `cond-negate` is to `if`.

**The weak test it exposes.** A suite that checks a resource exists but never what its loops
produced. `isKind` and a single `equal` on a static key cannot notice every port, label or host
vanishing at once.

**When it is correctly excluded.** A loop over a collection that is empty under every covering test
context cannot change what renders, so no assertion could catch it either. Those are reported as
[`Equivalent`](concepts.md#equivalent-mutants) and leave the score's denominator, like any other
unkillable mutant. `range` evaluates its pipeline even when the result is empty, so the execution
probe can prove the loop was reached and the verdict rests on evidence rather than on a guess.

---
```

- [ ] **Step 4: Update `CLAUDE.md`**

Three edits.

The architecture table's `internal/mutator` row — "The 8 mutators + registry + deterministic plan generation" → "The 9 mutators + registry + deterministic plan generation".

Add to "Gotchas that have already cost time":

```markdown
**A pipeline's variable declarations are part of its `Position()`.** `RangeNode`'s
`Pipe.Position()` for `{{ range $k, $v := .Values.m }}` is the offset of `$k`, not
of `.Values.m`. Replacing the whole pipeline leaves the body's `$k` and `$v`
undeclared, so the mutant fails to *parse* — becoming `Invalid`, which grades
nothing. The same rewrite in the equivalence probe is worse than useless: an
unparseable probe fails for a reason unrelated to execution, and `sameOutcome`
reads any difference from the original as proof the span ran, so it promotes
killable mutants to `Equivalent` and raises the score. It fired on the
`{{- $fullName := include "chart.fullname" . -}}` that `helm create` scaffolds
into every chart. `source.cutDeclarations` is the single definition both
`SpanOfRangedExpression` and `EnclosingPipelineSpan` use; `ProbeBytes` additionally
refuses any probe that will not parse. Pinned by
`TestProbeSpanPreservesVariableDeclarations` and `TestCutDeclarations`.
```

In the testing-conventions bullet about `testdata/charts/shortcircuit`, extend it to name the second single-purpose fixture:

```markdown
- **`testdata/charts/shortcircuit` and `testdata/charts/rangeloop` are separate
  fixtures, deliberately.** The first exercises the short-circuited-operand case
  above; the second exercises `range`, which `sample` contains none of. Keep new
  single-purpose fixtures out of `sample`: adding a template there changes the
  measured scores quoted in `README.md`, `docs/` and the pinned session tests.
```

- [ ] **Step 5: Verify every quoted figure still holds**

The docs claim `sample` yields 458 mutants and the strong suite scores 100.0%. Confirm rather than assume:

```bash
./bin/helm-mutation-test testdata/charts/sample -f 'tests/strong_test.yaml' | tail -3
grep -rn "eight" README.md docs/*.md CLAUDE.md | grep -v superpowers
```

Expected: `458 mutants`, `Score 100.0%`, and the `grep` returning nothing (every "eight" either updated or made explicit about the eight that fire).

- [ ] **Step 6: Full verification**

```bash
make lint
make test
make integration-tests
```

Expected: all green. `make test` includes the ~3min runner package; `make integration-tests` builds its own binary and checks exit codes and all five report formats.

- [ ] **Step 7: Commit**

```bash
git add docs/ README.md CLAUDE.md
git commit -m "docs: document range-empty and the declaration gotcha

Nine mutators now. range-empty is measured against rangeloop rather than
sample, which contains no range — named explicitly, because a blank row
would read as 'ran and found no weakness'."
```

---

## Self-Review

**Spec coverage.** Every section of the spec maps to a task: the `internal/source` helpers → Task 1; the `EnclosingPipelineSpan` fix → Task 2; the `ProbeBytes` parse guard → Task 3; the shared-invariant corpora → Task 4; the mutator, its ID, and the registry-count rename → Task 5; the `rangeloop` fixture and both runner tests → Task 6; every doc edit → Task 7. The spec's "`cond-negate` deliberately does not change" note needs no task, which is the point of it.

**Type consistency.** `cutDeclarations(string) (string, bool)` is defined in Task 1 and consumed in Task 2 with the same signature. `SpanOfRangedExpression(*File, *parse.PipeNode) (int, int, bool)` is defined in Task 1 and consumed in Task 5. `probeParses(*source.File, []byte) bool` is defined and consumed within Task 3. `IDRangeEmpty` is added in Task 5 and consumed in Task 6. `ProbeSpan` and `EnclosingPipelineSpan` keep their existing signatures throughout.

**Known gap, stated rather than papered over.** The parse guard in Task 3 has no naturally reachable failing input once declarations are preserved, so it is tested by calling `probeParses` directly with broken bytes. It is a safety net for future constructs and is labelled as one in both the test comment and the spec.
