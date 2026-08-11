# ADR-0002: Equivalent-Mutant Detection

**Date:** 2026-08-02  
**Status:** Accepted  
**Deciders:** Project maintainers  
**Spec:** [2026-08-02-equivalent-mutant-detection-design.md](../superpowers/specs/2026-08-02-equivalent-mutant-detection-design.md)  
**Plan:** [2026-08-02-equivalent-mutant-detection.md](../superpowers/plans/2026-08-02-equivalent-mutant-detection.md)

---

## Context

Running the fixture chart's strong suite, all 21 survivors are equivalent mutants — 14 are `nindent N → N+1` (different bytes, identical parsed YAML) and 7 are `yaml-key-delete` on keys whose value is already the zero value for its type. On a well-tested chart, the tool's headline actionable output is 100% false positives, and the score is held down by an amount no assertion can raise.

`docs/concepts.md` already describes the phenomenon and documents the detection recipe by hand; the tool just does not do it. `config.SkipEquivalent` exists as a struct field wired to nothing.

## Decision

Add a new mutant status — `Equivalent` — detected automatically on survivors, excluded from the mutation score's denominator (just as `NoCoverage` and `Invalid` are), and named explicitly in all five report formats.

### Core algorithm

A post-pass in `Session.Run` re-renders each survivor with Helm's own engine under every covering test job's value set. If the parsed documents are identical everywhere **and** a maximal-perturbation probe proves the mutated span actually executed, the mutant becomes `Equivalent`. Every other path leaves it `Survived`.

### Key design choices

| Decision | Choice | Rationale |
|---|---|---|
| **Proof method** | Parsed-render comparison | helm-unittest computes every assertion from the parsed manifest tree, never from rendered text. Two renders that parse identically cannot be told apart by any assertion. |
| **Execution probe** | Replace the innermost enclosing action's pipeline with `fail "canary"` | `fail` errors only when evaluated, so a render error is proof the span executed. This separates "unkillable by anyone" from "no test reaches this line". |
| **Short-circuit awareness** | Narrow probe to the mutated sub-expression inside `and`/`or` | Go templates stop evaluating `and`/`or` operands at the one that decides the result. A wider probe would prove the action was reached, not that the mutated operand was evaluated. |
| **Burden of proof** | Every uncertain path resolves to `Survived` | Excluding a mutant from the denominator raises the score, so a wrong `Equivalent` verdict deletes a real missing assertion from the report and flatters the number. |
| **Phase placement** | Post-pass after evaluation, before `ComputeTally` | Workers are subprocesses only because helm-unittest writes through Helm's package-level `DefaultCapabilities`. Our renderer copies that struct instead of aliasing it, so it is race-free in-process. |
| **Chart loading** | In-memory clone, no disk copies | The chart is loaded once; each mutant gets a shallow clone with a single `chart.File.Data` replaced. No temp directories, no copying, no cleanup. |
| **Values.yaml probe** | Sentinel string substitution | A fixed, non-empty sentinel is truthy wherever `false`, `0` or `""` was, so any template reading the key sees a different value. |
| **`kubernetesProvider` suites** | Skip detection entirely | A suite declaring a fake provider makes `lookup` return objects our render will not see, which could make two renders agree where helm-unittest finds them different. |
| **Config** | `EquivalenceCheck bool`, default `true`; `--no-equivalence-check` flag | Detection is on by default because on a well-tested chart most survivors are equivalent. The dead `SkipEquivalent` field is replaced. |

### Architecture

```
discover → baseline → coverage → generate → evaluate (workers) → equivalence → report
```

New package `internal/equivalence` imports Helm but nothing from `internal/runner` — the same posture `internal/coverage` holds. It owns rendering, comparing, probing, and the verdict. `internal/runner/unittest.go` — the only helm-unittest seam — gains a function to extract one `RenderContext` per test job. `internal/source` gains `EnclosingPipelineSpan` and `ProbeSpan`.

### Model changes

- `StatusEquivalent Status = "Equivalent"` — `CountsTowardScore() == false`
- `Tally.Equivalent int`
- `Run.Equivalent() []Mutant`
- `Run.EquivalenceChecked bool` — disambiguates "checked, found none" from "never looked"
- `Run.EquivalenceUnchecked int` — survivors the pass could not decide

Evidence rides on the existing `Mutant.Detail` field. Typical contents: `identical under 12 test-job value sets; span proven executed`.

### Report changes

`Equivalent` is named in all five formats alongside no-coverage and invalid:

- **Console** — section listing equivalent mutants; score line includes the count
- **Markdown** — row in the summary table
- **HTML** — row in the summary table
- **Stryker** — mapped to `Ignored` (the schema's term for a deliberately excluded mutant)
- **JUnit** — mapped to `<skipped>`

Every format states when the check was disabled.

## Consequences

### Positive

- The strong suite's score rises from 95.2% to 100%, which is the honest number for a chart whose remaining mutants are unkillable.
- Actionable output on a well-tested chart drops from 21 false positives to zero.
- Unexercised branches remain `Survived` and stay in the denominator — the tool still catches missing tests.

### Negative / Risks

- `RenderContexts` reproduces helm-unittest's unexported value merge (`polishTestJobsPathInfo`, `getUserValues`). This is the single riskiest component and could drift with helm-unittest upgrades.
- Post-renderers are ignored. This is safe in only one direction: a post-renderer can collapse two different inputs into one output but cannot split identical inputs, so ignoring it can only make us report *not* equivalent.
- Scores go up for every existing user, so `--threshold` verdicts can flip from fail to pass. This is the feature working as intended, but it is a behaviour change.

### Testing strategy

- **No killed mutant may be judged equivalent** — the strongest available check on `RenderContext` reconstruction.
- **Fixture expectations move** — strong suite: 417 killed, 0 survived, 21 equivalent (score 100%).
- **Short-circuit fixture chart** (`testdata/charts/shortcircuit`) — mutants behind an `and` with no enabling job stay `Survived`.
- **Parallelism invariance** — verdicts do not depend on `--parallel`.
- **Report tests** — "every format names its exclusions" extended to `Equivalent`.
- Everything runs under `-race`.

## Out of Scope

- The `num-literal` guard (suppressing `N → N+1` on `indent`/`nindent`). Detection already handles these 14 mutants as `Equivalent` with evidence attached.
- A separate `NotExercised` status. The probe distinguishes unexercised spans and says so in `Detail`, but they remain `Survived`.
- Dead-configuration reporting (values keys no template reads).
