# ADR-0003: `range-empty` Mutator

**Date:** 2026-08-03  
**Status:** Accepted  
**Deciders:** Project maintainers  
**Spec:** `2026-08-03-range-empty-mutator-design.md` (not in the repository)  
**Plan:** `2026-08-03-range-empty-mutator.md` (not in the repository)

---

## Context

`internal/source/tmplast.go` walks `*parse.RangeNode`, but no mutator targets it. `cond-negate` covers only `if` and `with`. Charts contain a lot of `range`: env vars, ports, ingress hosts, volume mounts and image pull secrets. Mutation testing cannot see any of these loops. A suite can assert nothing at all about the output of a loop and still score 100%.

During this investigation, we found a live bug in the equivalence probe that inflated the score. `EnclosingPipelineSpan` cuts the leading control keyword, but it does not cut the variable declarations (`$k, $v :=`). The probe then fails at *parse* time, not at *render* time. The checker reads any difference as proof of execution. So an unparseable probe always claims execution, and the checker promotes killable mutants to `Equivalent` with no evidence.

## Decision

Add `range-empty` as a ninth mutator. It forces `{{ range }}` loops to iterate zero times. Also fix the equivalence probe bug that the work on this mutator found.

### Mutation strategy

`{{ range X }}` → `{{ range list }}`. The sprig function `list` with no arguments is a real empty list. The loop body never runs, and everything that it produced disappears from the manifest. `range-empty` does to `range` what `cond-negate` does to `if`.

### Key design choices

| Decision | Choice | Rationale |
|---|---|---|
| **Variants** | Zero iterations only | One candidate per site. A one-iteration variant (`first 1 X`) would catch suites that assert presence but not count. But `first` accepts only a list and errors on a map. This would inflate the invalid rate, which `TestInvalidMutantRateIsLow` limits to 20%. |
| **Replacement text** | `list` | The sprig function `list` with no arguments is a real empty list. `text/template` does not treat `nil` as a command argument consistently. We verified that `{{ range list }}`, `{{ range $k, $v := list }}` and `range`/`else` all parse. |
| **Declaration preservation** | Replace only the expression, not the `$k, $v :=` prefix | For a `RangeNode`, `Pipe.Position()` is the offset of `$k`, not of the expression. If the mutator replaces the whole pipeline, `$k` and `$v` are no longer declared and the template fails to parse. The mutant is then `Invalid` and grades nothing. |
| **Declaration logic location** | One lexical helper `cutDeclarations` in `internal/source` | The mutator (`SpanOfRangedExpression`) and the probe fix (`EnclosingPipelineSpan`) both use it, so they cannot drift apart. |
| **End-to-end fixture** | New `testdata/charts/rangeloop` | `CLAUDE.md` forbids new templates in `sample`. Its mutant count (458) and exact tally appear in ~35 places in `README.md` and `docs/`, and `session_test.go` asserts that the strong suite scores exactly 100.0%. This follows the `shortcircuit` precedent. |
| **Probe fix scope** | Preserve declarations **and** add a generic parse guard | When the probe keeps the declarations, probe coverage comes back for `range`, `with` and declaration actions. The guard closes the whole class of bug: a probe that cannot parse proves nothing, whatever the cause. |

### The declaration bug (fixed alongside)

`source.EnclosingPipelineSpan` cuts the leading control keyword, but not the declarations. The probe makes these rewrites:

```
{{- with $cfg := .Values.config }}   →   {{- with fail "canary" }}
{{- range $k, $v := .Values.x }}     →   {{- range fail "canary" }}
```

The probed template fails to parse (`undefined variable "$cfg"`). In `equivalence/checker.go`, `sameOutcome` reports this as a difference, and any difference sets `executed = true`. So the probe claims execution **unconditionally**. When the mutant renders are also identical, the verdict is `Equivalent` with no evidence, and killable mutants leave the score.

The bug also fires on bare declaration actions that have no keyword, for example `{{- $fullName := include "chart.fullname" . -}}`. `helm create` puts this line into every chart.

**Fix:** `cutDeclarations` removes the `$k, $v :=` prefix from the span, so the probe keeps it. A second guard, `probeParses`, refuses any probe whose bytes do not parse as a valid template.

### Architecture

`range-empty` is a ninth `Mutator` in the existing registry. It therefore inherits generation, coverage, evaluation, classification, scoring and all five report formats, and the pipeline does not change.

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

The fixture has three `range` sites. Each one proves something different:

| Range site | Values | What it proves |
|---|---|---|
| `range .Values.ports` | non-empty list | Base case: survives the weak suite, killed by the strong one |
| `range $k, $v := .Values.labels` | non-empty map | Declarations survive both the mutation and the probe |
| `range .Values.extras` | `[]`, never set by any job | Genuinely `Equivalent`: no assertion can catch it |

## Consequences

### Positive

- Mutation testing can now see loops. If a suite asserts nothing about env vars, ports or ingress hosts, surviving `range-empty` mutants now show the gap.
- The probe bug fix makes the probe correct again for `with $x :=`, `range $k, $v :=` and bare declaration actions. Every `helm create` chart contains these shapes.
- The existing figures for `sample` (458 mutants, 100.0% strong score) do not change, because `sample` contains no `range`.

### Negative / Risks

- Existing charts with uncovered `range` loops will get new survivors, and their mutation score can go down. The feature works as intended here.
- For the `range`/`else` shape, the mutant renders the else branch, not nothing. This is still a change to the manifest, and a test can still observe it. But most people expect "the block disappears", and that is wrong for this shape.
- The one-iteration variant (`range (first 1 X)`) is deferred. It would catch suites that assert presence but not count, but it would cause a high invalid rate on maps.

### Testing strategy

- **Shared invariants extended first.** `TestMutantsKeepTemplatesParseable`, `TestCandidatesAreAlwaysChanges` and `TestNoMutatorPanicsOnUnparseableTemplate` get `range` sources before the mutator exists. So they cannot pass vacuously.
- **`TestRangeEmpty`**: a table that covers a plain range, `$v :=`, `$k, $v :=`, `range`/`else`, a piped expression, a `define` block, trim-marker preservation, a string literal that contains `:=`, and a range over a declared variable.
- **`TestRangeEmptyMutantsParseWithDeclarations`**: the main property. If the mutator drops the declarations, the mutant does not parse.
- **`TestProbeSpanPreservesVariableDeclarations`**: for `range`, `with` and bare declaration actions.
- **`TestProbeParsesRejectsBytesThatDoNotParse`**: tests the generic guard with bytes that are broken on purpose. When declarations are preserved, we found no naturally reachable input that the guard rejects.
- **End-to-end**: `TestRangeMutantSurvivesAWeakSuiteAndIsKilledByAStrongOne` and `TestEmptyRangeIsJudgedEquivalent`.
- Everything runs under `-race`.

## Out of Scope

- The one-iteration variant (`range (first 1 X)`).
- A `range` in `sample`, and a refresh of all ~35 quoted figures.
- Other `features.md` items: values-key reachability, `--compare`, live progress.
