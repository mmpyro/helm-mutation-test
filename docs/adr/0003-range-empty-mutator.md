# ADR-0003: `range-empty` Mutator

**Date:** 2026-08-03  
**Status:** Accepted  
**Deciders:** Project maintainers  
**Spec:** [2026-08-03-range-empty-mutator-design.md](../specs/2026-08-03-range-empty-mutator-design.md)  
**Plan:** [2026-08-03-range-empty-mutator.md](../plans/2026-08-03-range-empty-mutator.md)

---

## Context

`internal/source/tmplast.go` walks `*parse.RangeNode`, but no mutator targets it. `cond-negate` covers `if` and `with` only. Charts are full of `range` — env vars, ports, ingress hosts, volume mounts, image pull secrets — and every one of those loops is mutation-invisible: a suite can assert nothing whatsoever about a loop's output and still score 100%.

While investigating this, a live score-inflating bug was discovered in the equivalence probe: `EnclosingPipelineSpan` cuts the leading control keyword but not variable declarations (`$k, $v :=`), causing the probe to fail at *parse* time rather than *render* time. Since the checker reads any difference as proof of execution, an unparseable probe claims execution unconditionally, promoting killable mutants to `Equivalent` on no evidence.

## Decision

Add `range-empty` as a ninth mutator that forces `{{ range }}` loops to iterate zero times, and fix the equivalence probe bug that discovering it uncovered.

### Mutation strategy

`{{ range X }}` → `{{ range list }}` — sprig's `list` with no arguments is a real empty list. The loop body never runs and everything it produced disappears from the manifest. This is to `range` what `cond-negate` is to `if`.

### Key design choices

| Decision | Choice | Rationale |
|---|---|---|
| **Variants** | Zero iterations only | One candidate per site. A one-iteration variant (`first 1 X`) would catch suites that assert presence but not count, but `first` is list-only and errors on a map, inflating the invalid rate policed by `TestInvalidMutantRateIsLow` at 20%. |
| **Replacement text** | `list` | sprig's `list` with no args is a real empty list. `nil` as a command argument is a shape `text/template` treats inconsistently. `{{ range list }}`, `{{ range $k, $v := list }}`, and `range`/`else` were all verified to parse. |
| **Declaration preservation** | Replace only the expression, not the `$k, $v :=` prefix | `RangeNode`'s `Pipe.Position()` is the offset of `$k`, not of the expression. Replacing the whole pipeline leaves `$k` and `$v` undeclared, and the template fails to parse — making the mutant `Invalid` (grades nothing). |
| **Declaration logic location** | One lexical helper `cutDeclarations` in `internal/source` | Shared by both the mutator (`SpanOfRangedExpression`) and the probe fix (`EnclosingPipelineSpan`), so they cannot drift. |
| **End-to-end fixture** | New `testdata/charts/rangeloop` | `CLAUDE.md` forbids adding templates to `sample` — its mutant count (458) and exact tally are quoted in ~35 places across `README.md` and `docs/`, and `session_test.go` asserts the strong suite scores exactly 100.0%. Follows the `shortcircuit` precedent. |
| **Probe fix scope** | Preserve declarations **and** add a generic parse guard | Preserving declarations restores probe coverage for `range`, `with`, and declaration actions. The guard closes the whole class: a probe that cannot parse proves nothing, whatever the cause. |

### The declaration bug (fixed alongside)

`source.EnclosingPipelineSpan` cuts the leading control keyword but not the declarations. The probe rewrites:

```
{{- with $cfg := .Values.config }}   →   {{- with fail "canary" }}
{{- range $k, $v := .Values.x }}     →   {{- range fail "canary" }}
```

The probed template fails to parse (`undefined variable "$cfg"`). In `equivalence/checker.go`, `sameOutcome` reports this as a difference, and any difference sets `executed = true`. The probe claims execution **unconditionally**, combined with identical mutant renders yields `Equivalent` on no evidence — removing killable mutants from the score.

This also fires on bare declaration actions with no keyword: `{{- $fullName := include "chart.fullname" . -}}`, which `helm create` scaffolds into every chart.

**Fix:** `cutDeclarations` strips the `$k, $v :=` prefix from the span so the probe preserves it. A second guard, `probeParses`, refuses any probe whose bytes do not parse as a valid template.

### Architecture

`range-empty` is a ninth `Mutator` in the existing registry, so it inherits generation, coverage, evaluation, classification, scoring and all five report formats without touching the pipeline.

```
internal/source/tmplast.go
  ├── cutDeclarations(s string) (rest string, cut bool)      — unexported
  ├── isIdentByte(c byte) bool                                — unexported
  └── SpanOfRangedExpression(f *File, pipe *PipeNode)         — exported
       └── used by: range_empty.go (mutator) and EnclosingPipelineSpan (probe)

internal/mutator/range_empty.go                               — new
internal/mutator/mutator.go                                   — IDRangeEmpty added

internal/equivalence/probe.go
  └── probeParses(f *source.File, probed []byte) bool         — new guard
```

### Fixture design (`testdata/charts/rangeloop`)

Three `range` sites, each earning its place:

| Range site | Values | What it proves |
|---|---|---|
| `range .Values.ports` | non-empty list | Base case: survives weak suite, killed by strong one |
| `range $k, $v := .Values.labels` | non-empty map | Declarations survive both the mutation and the probe |
| `range .Values.extras` | `[]`, never set by any job | Genuinely `Equivalent` — no assertion can catch it |

## Consequences

### Positive

- Loops are no longer mutation-invisible. A suite that asserts nothing about env vars, ports, or ingress hosts will now have surviving `range-empty` mutants exposing the gap.
- The probe bug fix restores correctness for `with $x :=`, `range $k, $v :=`, and bare declaration actions — shapes present in every `helm create` chart.
- `sample`'s existing figures (458 mutants, 100.0% strong score) remain untouched because `sample` contains no `range`.

### Negative / Risks

- Existing charts with uncovered `range` loops will see new survivors, which may lower their mutation score. This is the feature working as intended.
- The `range`/`else` shape renders the else branch rather than nothing. Still a manifest change, still observable — but "the block disappears" is the natural intuition and it is wrong for that shape.
- The one-iteration variant (`range (first 1 X)`) is deferred — it would catch suites that assert presence but not count, at the cost of a high invalid rate on maps.

### Testing strategy

- **Shared invariants extended first** — `TestMutantsKeepTemplatesParseable`, `TestCandidatesAreAlwaysChanges`, and `TestNoMutatorPanicsOnUnparseableTemplate` gain `range` sources before the mutator exists, so they cannot pass vacuously.
- **`TestRangeEmpty`** — table over plain range, `$v :=`, `$k, $v :=`, `range`/`else`, piped expression, `define` block, trim-marker preservation, string-literal `:=`, and range over a declared variable.
- **`TestRangeEmptyMutantsParseWithDeclarations`** — the headline property: dropping declarations makes the mutant unparseable.
- **`TestProbeSpanPreservesVariableDeclarations`** — for `range`, `with`, and bare declaration actions.
- **`TestProbeParsesRejectsBytesThatDoNotParse`** — the generic guard, tested with deliberately broken bytes (no naturally reachable failing input was found after declarations are preserved).
- **End-to-end** — `TestRangeMutantSurvivesAWeakSuiteAndIsKilledByAStrongOne` and `TestEmptyRangeIsJudgedEquivalent`.
- Everything runs under `-race`.

## Out of Scope

- The one-iteration variant (`range (first 1 X)`).
- Giving `sample` a `range` and refreshing all ~35 quoted figures.
- Other `features.md` items: values-key reachability, `--compare`, live progress.
