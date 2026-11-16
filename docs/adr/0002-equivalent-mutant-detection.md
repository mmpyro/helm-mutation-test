# ADR-0002: Equivalent-Mutant Detection

**Date:** 2026-08-02  
**Status:** Accepted  
**Deciders:** Project maintainers  
**Spec:** `2026-08-02-equivalent-mutant-detection-design.md` (not in the repository)  
**Plan:** `2026-08-02-equivalent-mutant-detection.md` (not in the repository)

---

## Context

When the strong suite of the fixture chart runs, all 21 survivors are equivalent mutants. 14 of them are `nindent N → N+1`: the bytes differ, but the parsed YAML is identical. The other 7 are `yaml-key-delete` on keys whose value is already the zero value for its type. So on a well-tested chart, 100% of the actionable output that the tool puts first is false positives. These mutants also hold the score down, and no assertion can raise it.

`docs/concepts.md` already describes this and gives the steps to detect it by hand. The tool does not do it. `config.SkipEquivalent` exists as a struct field, but nothing uses it.

## Decision

Add a new mutant status, `Equivalent`. The tool detects it automatically on survivors. It is excluded from the denominator of the mutation score, the same as `NoCoverage` and `Invalid`. All five report formats name it explicitly.

### Core algorithm

`Session.Run` gets a post-pass. The post-pass renders each survivor again with Helm's own engine, once under the value set of each covering test job. The mutant becomes `Equivalent` only if two conditions are true:

- The parsed documents are identical in all of these renders.
- A maximal-perturbation probe proves that the mutated span actually executed.

Every other path leaves the mutant `Survived`.

### Key design choices

| Decision | Choice | Rationale |
|---|---|---|
| **Proof method** | Parsed-render comparison | helm-unittest computes every assertion from the parsed manifest tree, never from the rendered text. If two renders parse identically, no assertion can tell them apart. |
| **Execution probe** | Replace the innermost enclosing action's pipeline with `fail "canary"` | `fail` causes an error only when Go evaluates it, so a render error proves that the span executed. This separates "nobody can kill this" from "no test reaches this line". |
| **Short-circuit awareness** | Narrow probe to the mutated sub-expression inside `and`/`or` | Go templates stop evaluating `and`/`or` operands at the operand that decides the result. A wider probe would prove that the action was reached. It would not prove that the mutated operand was evaluated. |
| **Burden of proof** | Every uncertain path resolves to `Survived` | When a mutant leaves the denominator, the score goes up. So a wrong `Equivalent` verdict deletes a real missing assertion from the report and flatters the number. |
| **Phase placement** | Post-pass after evaluation, before `ComputeTally` | The workers are subprocesses only because helm-unittest writes through the package-level `DefaultCapabilities` in Helm. Our renderer copies that struct and does not alias it, so it is race-free in-process. |
| **Chart loading** | In-memory clone, no disk copies | The tool loads the chart once. Each mutant gets a shallow clone in which only one `chart.File.Data` is replaced. There are no temp directories, no copies and no cleanup. |
| **Values.yaml probe** | Sentinel string substitution | The sentinel is fixed and not empty, so it is truthy wherever `false`, `0` or `""` was. Any template that reads the key therefore sees a different value. |
| **`kubernetesProvider` suites** | Skip detection entirely | When a suite declares a fake provider, `lookup` returns objects that our render does not see. Two renders could then agree in our check while helm-unittest finds them different. |
| **Config** | `EquivalenceCheck bool`, default `true`; `--no-equivalence-check` flag | Detection is on by default, because on a well-tested chart most survivors are equivalent. This replaces the dead `SkipEquivalent` field. |

### Architecture

```
discover → baseline → coverage → generate → evaluate (workers) → equivalence → report
```

The new package `internal/equivalence` imports Helm but nothing from `internal/runner`. `internal/coverage` follows the same rule. The new package owns rendering, comparison, probing and the verdict. `internal/runner/unittest.go` is the only seam to helm-unittest. It gets a function that extracts one `RenderContext` for each test job. `internal/source` gets `EnclosingPipelineSpan` and `ProbeSpan`.

### Model changes

- `StatusEquivalent Status = "Equivalent"`, with `CountsTowardScore() == false`
- `Tally.Equivalent int`
- `Run.Equivalent() []Mutant`
- `Run.EquivalenceChecked bool`: tells "checked, found none" apart from "never looked"
- `Run.EquivalenceUnchecked int`: survivors that the pass could not decide

The evidence goes in the existing `Mutant.Detail` field. A typical value is `identical under 12 test-job value sets; span proven executed`.

### Report changes

All five formats name `Equivalent`, next to no-coverage and invalid:

- **Console**: a section that lists the equivalent mutants. The score line includes the count.
- **Markdown**: a row in the summary table.
- **HTML**: a row in the summary table.
- **Stryker**: mapped to `Ignored`, the schema's term for a mutant that is excluded on purpose.
- **JUnit**: mapped to `<skipped>`.

Every format states when the check was disabled.

## Consequences

### Positive

- The score of the strong suite goes from 95.2% to 100%. This is the honest number for a chart whose remaining mutants nobody can kill.
- On a well-tested chart, the actionable output goes from 21 false positives to zero.
- Branches that no test exercises stay `Survived` and stay in the denominator. The tool still catches missing tests.

### Negative / Risks

- `RenderContexts` copies the unexported value merge of helm-unittest (`polishTestJobsPathInfo`, `getUserValues`). This is the component with the highest risk. It could drift when helm-unittest is upgraded.
- The tool ignores post-renderers. This is safe in only one direction. A post-renderer can turn two different inputs into one output, but it cannot split identical inputs. So ignoring post-renderers can only make us report *not* equivalent.
- Scores go up for every existing user, so a `--threshold` verdict can change from fail to pass. The feature works as intended here, but it is a change in behaviour.

### Testing strategy

- **No killed mutant may be judged equivalent.** This is the strongest check available on the `RenderContext` reconstruction.
- **Fixture expectations move.** Strong suite: 417 killed, 0 survived, 21 equivalent (score 100%).
- **Short-circuit fixture chart** (`testdata/charts/shortcircuit`): mutants behind an `and` with no job that enables it stay `Survived`.
- **Parallelism invariance**: the verdicts do not depend on `--parallel`.
- **Report tests**: the test "every format names its exclusions" now includes `Equivalent`.
- Everything runs under `-race`.

## Out of Scope

- The `num-literal` guard, which would suppress `N → N+1` on `indent`/`nindent`. Detection already handles these 14 mutants as `Equivalent` and attaches the evidence.
- A separate `NotExercised` status. The probe tells unexercised spans apart and says so in `Detail`, but these mutants stay `Survived`.
- Reports of dead configuration (values keys that no template reads).
