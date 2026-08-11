# Equivalent-mutant detection

Design, 2026-08-02. Feature #1 from `features.md`.

## The problem, measured

Running the fixture chart's strong suite today:

```
killed 417 · survived 21 · invalid 20 · no-coverage 0   → score 95.2%
```

All 21 survivors are equivalent mutants — 14 are `nindent N → N+1` (different bytes,
identical parsed YAML) and 7 are `yaml-key-delete` on keys whose value is already the
zero value for its type, where deletion leaves `nil` and Go templates treat `nil` and
the zero value identically for truthiness and for `| default`.

So on a well-tested chart the tool's headline actionable output is **100% false
positives**, and the score is held down by an amount no assertion can raise.
`docs/concepts.md` already describes the phenomenon and even documents the detection
recipe by hand; the tool just does not do it. `config.SkipEquivalent` exists as a
struct field wired to nothing.

## What we are building

A new mutant status, `Equivalent`, detected automatically on survivors, excluded from
the score's denominator exactly as `NoCoverage` and `Invalid` are, and named
explicitly in all five report formats.

Excluding a mutant from the denominator *raises* the score, so the burden of proof is
high: a wrong `Equivalent` verdict deletes a real missing assertion from the report
and flatters the number. Every design decision below resolves toward `Survived` when
the evidence is not conclusive.

### Why parsed-render comparison is sufficient proof

`snapshot.Cache.Compare` computes its snapshot as
`common.TrustedMarshalYAML(content)`, where `content` is the **parsed** manifest, not
the rendered text. Every other helm-unittest assertion type over a YAML manifest also
reads values out of the parsed document tree.

That does not extend to `.txt` templates such as `NOTES.txt`. `TestJob.parseTextFile`
(`pkg/unittest/test_job.go`) stores a `.txt` template's output **verbatim** under
`common.RAW`, never YAML-parsed, and three validators assert against that raw string:
`equal_raw_validator.go`, `match_regex_raw_validator.go` and
`snapshot_raw_validator.go`. A byte difference in a `.txt` render is therefore
observable even when nothing would change about a parsed document.

So the proof has two cases: for a structured YAML manifest, if the original and the
mutant parse to identical documents, no assertion in any suite can distinguish them —
that is a proof, not a heuristic. For a `.txt` template, the render must match
byte-for-byte instead, because raw text validators read the rendered string directly.
`equivalence.Compare` implements exactly this split: exact string equality for `.txt`
outputs, parsed comparison for everything else.

### Why "identical renders" alone is not enough

Two very different situations produce identical renders:

1. The mutation genuinely cannot change output — unkillable by anyone.
2. No covering test's values reach the mutated line — killable, by a test nobody
   wrote yet.

The second is a real missing assertion and the most valuable thing the tool can
report. Excluding it would be precisely the score flattery this project's rules
forbid. The two are separated by the execution probe described below.

## Architecture

The pipeline gains one phase:

```
discover → baseline → coverage → generate → evaluate (workers) → equivalence → report
```

Equivalence runs as a post-pass in `Session.Run`, after `Execute` returns and before
`ComputeTally`. It reads only mutants with status `Survived`, and the only status
transition it can make is `Survived → Equivalent`.

### Why a post-pass and not part of the worker

Workers are subprocesses for exactly one reason: helm-unittest's
`TestJob.capabilitiesV3` writes through Helm's package-level `DefaultCapabilities`
pointer, which races. Our renderer is our own code — it constructs a fresh
`chartutil.Capabilities` per render and never touches that global — so it is
race-free in-process and needs no subprocess. Running equivalence inside the worker
would buy nothing and would drag a verdict, its evidence, and a new class of failure
through the worker wire protocol.

The pass runs its own in-process goroutine pool sized by `--parallel`.

### No chart copies on disk

`internal/workspace` exists because helm-unittest needs a real chart directory.
Rendering does not. The chart is loaded once with `v3loader.Load`; each mutant is
rendered from a shallow clone of that chart with a single `chart.File.Data` replaced,
or — for a `values.yaml` mutant — with the mutated bytes re-parsed into
`chart.Values`. No temp directories, no copying, no cleanup. This is most of why the
phase is cheap.

### New package: `internal/equivalence`

Pure, and imports nothing from `internal/runner` — the same posture
`internal/coverage` holds, for the same reason. It owns:

- `RenderContext`, an opaque bundle of values, release options, capabilities and
  chart metadata, which it consumes but does not know how to build
- rendering a chart plus a context into parsed documents
- comparing two renders
- applying and interpreting the execution probe
- the verdict

`internal/runner/unittest.go` — the existing and only helm-unittest seam — gains a
function that extracts one `RenderContext` per test job.

`internal/source` gains one span function, `EnclosingPipelineSpan`, a natural
neighbour to the existing `CommandArgSpan`, used to place the template probe.

Both load-bearing boundaries in CLAUDE.md survive: helm-unittest imports stay in
`unittest.go`, and `equivalence` imports Helm but never `runner`.

## The algorithm

### Contexts

For a given survivor, the contexts are the test jobs of its **covering suites**, with
skipped jobs excluded. Not every job in the chart: the covering set is what ran, so it
is what "no test noticed" refers to.

Coverage is tracked at suite **file** granularity (`Mutant.CoveringSuites`), not per
`suite:` document, so a file holding several suites contributes every job in all of
them, not only the jobs of the suite that actually renders the mutated template. That
is coarser than "exactly what ran," but it errs in the safe direction: extra contexts
can only make an `Equivalent` verdict harder to earn, never easier, since every
context added is one more chance for a difference to show up.

Each `RenderContext` is reconstructed from exported fields on
`unittest.TestSuite` and `unittest.TestJob` — `Values`, `Set`, `Release`, `Chart`,
`CapabilitiesFields` — replicating the suite-to-job merge that helm-unittest performs
internally in the unexported `polishTestJobsPathInfo`: suite `Values` files are
prepended to the job's, suite `Set` becomes the job's global set and is merged before
the job's own `Set`, and suite-level release, chart and capabilities settings fill in
where the job leaves them empty.

This reconstruction is the single riskiest part of the feature. The testing strategy
below is built around detecting it going wrong.

### Per-context comparison

Compare rendered bytes first: cheap, and byte equality is unambiguous. On a byte
difference, parse both sides into document trees and deep-compare, preserving
document order — `documentIndex` assertions can observe it.

### The execution probe

A mutation is believed equivalent only if its span provably executed. The probe is a
maximal perturbation at the same span. It is independent of the mutation, so it is
computed once per span and cached.

**Template span.** Replace the pipeline of the *innermost enclosing action* — the
`{{ ... }}` that contains the span — with `fail "canary"`, preserving the action
keyword and trim markers:

| original | probe |
|---|---|
| `{{ .Values.x }}` | `{{ fail "canary" }}` |
| `{{ include "c.labels" . \| nindent 4 }}` | `{{ fail "canary" }}` |
| `{{- if .Values.a.b }}` | `{{- if fail "canary" }}` |
| `{{- with .Values.x }}` | `{{- with fail "canary" }}` |
| `{{- range .Values.list }}` | `{{- range fail "canary" }}` |

Always syntactically valid, never disturbs block structure, and sprig's `fail`
returns an error only when it is evaluated. Go templates evaluate arguments eagerly
and do no parse-time type checking, so a render error is proof the span executed.

**`values.yaml` span.** Substitute a distinctive sentinel string — a fixed literal
unlikely to occur in a chart, and non-empty so that it is truthy wherever the
original value was `false`, `0` or `""` — for the value. If
any template reads the key, the output changes or the render errors; either is proof.
A key that survives both its own deletion and the sentinel is read by nothing — that
is dead configuration, not a missing assertion, and it stays `Survived` here rather
than being silently excluded.

### Verdict

Contexts are visited in a fixed order — by suite key, then by job index within the
suite — short-circuiting on the first difference.

A render error is itself a comparable outcome, not an automatic non-match. Suites
legitimately contain jobs that render with a required value missing and assert the
failure (`matchFailedTemplate`), and the fixture chart has one: treating any render
failure as inconclusive poisoned every mutant that job covered, because a mutant and
the original both fail the same way and that agreement is real evidence, not
uncertainty.

| condition | verdict |
|---|---|
| mutant and original disagree in any context — either renders where the other errors, both error with different messages, or both render but the parsed output (or `.txt` bytes) differ | `Survived` |
| mutant and original agree in every context (identical render, or identical error message), and the probe's outcome differs from the original's in some context | `Equivalent` |
| mutant and original agree in every context, and the probe's outcome never differs from the original's | `Survived`, detail `not exercised by any covering test` |
| our render of either side cannot be attempted at all — the chart cannot be loaded, the mutated file cannot be re-parsed, or a probe cannot be built for the span | `Survived`, detail records the reason as inconclusive |
| the survivor has no usable contexts at all, because every covering job is skipped | `Survived`, detail records it as inconclusive |

Every uncertain path lands on `Survived`. Only positive evidence removes a mutant
from the denominator.

The probe rule follows the same logic: **proof of execution is that the probe's
outcome differs from the original's outcome** under some context — one rule that
covers both a probe-induced render error (the `fail "canary"` substitution only
evaluates if the span is reached) and a probe-induced output change (the
`values.yaml` sentinel). This subsumes the simpler "a probe error proves execution"
rule and is also correct when the original already errors: if the original errors
and the probe produces the *same* error, that error pre-exists the probe and proves
nothing about this span; only a probe outcome that differs from the original's is
evidence the span ran.

Against the fixture this produces the intended answers. The 14 `nindent N → N+1`
sites probe as executed — the `fail` substitution makes the render error — and
compare parsed-equal, so they become `Equivalent`. The 7 zero-valued `values.yaml`
keys probe as read, because a non-empty sentinel string is truthy where `false` was,
and compare equal, so they become `Equivalent`.

### Cost

Detection touches survivors only, and short-circuits at the first context whose
renders differ. A non-equivalent survivor therefore costs one render pair; only
genuine equivalents pay for every covering context plus the probe. Original renders
are cached per context and shared across all survivors. On the weak fixture — the
worst case in the repository, at 275 survivors — that is roughly 550 renders.

### Two deliberate divergences from helm-unittest's renderer

We render with `helm.sh/helm/v3/pkg/engine` rather than through the plugin.

**Post-renderers are ignored.** This is unsafe in only one direction, and it is the
harmless one: a deterministic post-renderer can collapse two different inputs into
one output, but it cannot split two identical inputs. So ignoring it can only ever
make us report *not* equivalent.

**`kubernetesProvider` fakes are not ignored.** A suite declaring a fake provider
makes `lookup` return objects our render will not see, which could make two renders
agree where helm-unittest would find them different. Mutants whose covering suites
include such a suite skip detection entirely and remain `Survived`.

## Model changes

```go
StatusEquivalent Status = "Equivalent"   // CountsTowardScore() == false
Tally.Equivalent int                     // plus Add() case, plus Total()
Run.Equivalent() []Mutant                // alongside Survived() and NoCoverage()
Run.EquivalenceChecked bool
```

Evidence rides on the existing `Mutant.Detail` field, whose documented meaning gains
a third clause. Typical contents: `identical under 12 test-job value sets; span
proven executed`, or `not exercised by any covering test`, or an inconclusive reason.
No new struct.

`EquivalenceChecked` is not optional polish. Without it, `equivalent 0` is ambiguous
between *checked and found none* and *never looked*, and the second reads as the
first. That is the same hazard `Run.Capped` exists to prevent.

## Report changes

`Equivalent` is named in all five formats alongside no-coverage and invalid, and
every format states when the check was disabled. Two formats need a mapping into a
fixed vocabulary, each following the precedent already set there:

- **Stryker / HTML** (`internal/report/stryker.go`) — `Ignored`, the schema's term
  for a deliberately excluded mutant, beside the existing `Invalid → CompileError`.
- **JUnit** — `<skipped>`, joining the four statuses already handled that way.

## Config and CLI

`config.SkipEquivalent`, dead since it was written, becomes `EquivalenceCheck bool`,
defaulting to `true`. The flag is `--no-equivalence-check`. No new validation rules.

## Out of scope

**The `num-literal` guard.** `features.md` proposes, as a cheap down payment,
suppressing the `N → N+1` variant on `indent`/`nindent` arguments. We are not doing
it. A guard deletes the mutant before it is ever counted, so it disappears from
`Generated` with no line in any report — the opposite of this project's rule that
every exclusion is named. Detection reports the same 14 mutants as `Equivalent` with
their evidence attached. CLAUDE.md's stated bar for a guard is also that the mutation
must break rendering, which these do not. Keeping them additionally gives the
detector 14 known-equivalent cases to test against.

**A separate `NotExercised` status.** The probe distinguishes unexercised spans and
says so in `Detail`, but they remain `Survived` and stay in the denominator, because
they are killable by a test somebody could write.

**Dead-configuration reporting.** The values sentinel probe incidentally identifies
values keys no template reads. Surfacing that as a finding belongs to feature #3.

## Testing

The strategy rests on one invariant that exercises the riskiest component for free.

**No killed mutant may be judged equivalent.** A `Killed` mutant demonstrably changed
something a test observed, so its render *must* differ under some covering context.
Running the equivalence check across the fixture's killed mutants and asserting zero
equivalent verdicts is the strongest available check on the `RenderContext`
reconstruction: if the suite-to-job value merge is wrong, we render the wrong branch,
and killed mutants begin to look identical. The test fails loudly and points straight
at the cause.

Around that:

- **Fixture expectations move.** The strong suite becomes 417 killed, 0 survived,
  21 equivalent — a score of 100%, which is the honest number for a chart whose
  remaining mutants are unkillable. `TestWeakSuiteScoresLowAndStrongScoresHigh` still
  holds at both ends: the weak suite's score rises slightly as some of its 275
  survivors reclassify, and stays far below the 45% assertion and the 50% CI gate.
- **Probe unit tests per span kind**, including the case that must *not* fire: a
  mutation inside a branch that no covering job's values reach stays `Survived`.
- **Parallelism invariance.** Verdicts do not depend on `--parallel`, pinned the way
  the existing invariance test does it.
- **Report tests** extend the existing "no two formats disagree" and "every format
  names its exclusions" checks to cover `Equivalent` and the disabled case.
- **Integration tests** cover a default run and a `--no-equivalence-check` run. Exit
  codes are unchanged.
- Everything runs under `-race`, as always.

## Documentation

- `docs/concepts.md` — the section headed "The tool does not detect or exclude them"
  is rewritten as the feature's description, keeping the by-hand recipe as background.
- `docs/cli-reference.md` — `--no-equivalence-check`.
- `docs/reports.md` — the new status and its per-format rendering.
- `README.md` — the demonstration table is already stale (it claims weak = 6.6%
  against an actual 2.1%, and has no no-coverage column at all, hiding the larger of
  the two findings). Its numbers change again here, so it is corrected in the same
  pass.

## Behaviour change to call out

Scores go up for every existing user, so `--threshold` verdicts can flip from fail to
pass. That is the feature working as intended, but it is a behaviour change rather
than a pure addition, and it belongs in the release notes.
