# range-empty: a mutator for `{{ range }}`, and the probe bug it uncovers

Date: 2026-08-03
Status: approved, ready for an implementation plan

## Why

`internal/source/tmplast.go` walks `*parse.RangeNode`, but no mutator targets it.
`cond-negate` covers `if` and `with` only. Charts are full of `range` — env vars, ports,
ingress hosts, volume mounts, image pull secrets — and today every one of those loops is
mutation-invisible: a suite can assert nothing whatsoever about a loop's output and still
score 100%.

`{{ range X }}` → `{{ range list }}` forces zero iterations. It is to `range` what
`cond-negate` is to `if`, and `cond-negate` is described in its own source as the
highest-value mutator in the set.

## What the investigation found

Two facts came out of probing `text/template` directly, and both shape the design.

**1. `RangeNode`'s `Pipe.Position()` sits before the variable declarations.** For
`{{- range $k, $v := .Values.labels }}` it reports the offset of `$k`, not of
`.Values.labels`. So the obvious rewrite — reuse `source.SpanOfCondition` and replace the
whole pipeline — yields `{{- range list }}` with the body still referring to `$k` and `$v`,
and `text/template` rejects that at *parse* time:

```
template: x:1: undefined variable "$v"
```

An unparseable mutant is `Invalid`: caught by every test regardless of what it asserts, so
it grades nothing and leaves the denominator. It would also fail
`TestMutantsKeepTemplatesParseable`.

**2. The same span bug is already live in the equivalence probe, and it inflates the
score.** `source.EnclosingPipelineSpan` cuts the leading control keyword but not the
declarations, so the probe rewrites

```
{{- with $cfg := .Values.config }}   ->   {{- with fail "canary" }}
{{- range $k, $v := .Values.x }}     ->   {{- range fail "canary" }}
```

and the probed template fails to parse — `undefined variable "$cfg"`. In
`equivalence/checker.go`, `renderOutcome` folds that failure into `outcome.errText`,
`sameOutcome` reports "one errored and the other did not" as a difference, and any
difference sets `executed = true`. So the probe claims the span executed **unconditionally**,
whether or not the branch ever runs. Combined with identical mutant renders that yields
`Equivalent` on no evidence at all, which removes a killable mutant from the denominator
and raises the score — precisely what the burden-of-proof rule in `CLAUDE.md` exists to
prevent.

This is not exotic. The keyword cut and the declaration cut are independent, so the bug
also fires on a bare declaration action with no keyword at all:

```
{{- $fullName := include "chart.fullname" . -}}
```

which `helm create` scaffolds into every chart. Any mutation inside such an action — a
`default` drop, a string literal — gets a probe that deletes the declaration and
parse-errors. Neither fixture chart uses `with $x :=` or a declaration action, which is why
no test caught it.

Fixing the probe is therefore a prerequisite for `range-empty` being sound, not adjacent
scope: without it, every mutant in a `{{ range $k, $v := … }}` that renders identically
would be excluded from the score on a false proof of execution.

## Decisions

| Decision | Choice | Why |
|---|---|---|
| Variants | Zero iterations only | One candidate per site. A one-iteration variant (`first 1 X`) would catch suites that assert presence but not count, but `first` is list-only and errors on a map, inflating the invalid rate that `TestInvalidMutantRateIsLow` polices at 20%. Not worth it in v1. |
| Replacement text | `list` | sprig's `list` with no arguments is a real empty list. `nil` as a command argument is a shape `text/template` treats inconsistently. `{{ range list }}`, `{{ range $k, $v := list }}` and `range`/`else` were all verified to parse. |
| End-to-end fixture | New `testdata/charts/rangeloop` | `CLAUDE.md` forbids adding single-purpose templates to `sample`, and measurement backs it: `458` and the exact tally are quoted in ~35 places across `README.md` and four files in `docs/`, and `session_test.go:185` asserts the strong suite scores *exactly* 100.0%, so a new `range` in `sample` would also force new assertions in `strong_test.yaml`. Follows the `shortcircuit` precedent. |
| Probe fix scope | Preserve declarations **and** add a generic parse guard | Preserving declarations restores full probe coverage for `range`, `with` and declaration actions. The guard closes the whole class: a probe that cannot parse proves nothing, whatever the cause. |
| Where the declaration logic lives | One lexical helper in `internal/source` | `EnclosingPipelineSpan` takes an offset and has no parse tree, so a lexical helper drops in with no signature churn along the probe path. Declarations have a tight lexical shape, and the file already works this way (`FindAction`, `cutKeyword`, `barBefore`). One definition consumed by both the mutator and the probe, so they cannot drift. |

## Design

### `internal/source/tmplast.go`

One unexported helper and one exported span function:

```go
// cutDeclarations strips a pipeline's leading variable declarations —
// "$x := " or "$k, $v := " — reporting whether one was there.
func cutDeclarations(s string) (rest string, cut bool)

// SpanOfRangedExpression returns the span of the expression a range iterates
// over, excluding any declaration prefix.
func SpanOfRangedExpression(f *File, pipe *parse.PipeNode) (start, end int, ok bool)
```

`SpanOfRangedExpression` is `SpanOfCondition` plus the cut, recovering the start by suffix
length rather than index arithmetic — `start = end - len(rest)` — so there is no offset to
get wrong.

`cutDeclarations` scans lexically and bails at the first byte that cannot appear in a
declaration, so a `:=` inside a string literal is never mistaken for one. It correctly
declines `{{ range $items }}` and `{{ range $.Values.list }}`, where the `$` begins the
expression rather than a declaration.

`EnclosingPipelineSpan` gains the cut after its existing keyword cut, so the probe span
starts *after* the declarations and therefore preserves them:

```go
// A declaration must survive the probe. Overwriting `$k, $v :=` leaves the
// body's variables undeclared, so the probed template no longer parses — and a
// parse failure is a difference the checker reads as proof of execution, which
// would exclude a killable mutant from the score.
if rest, cut := cutDeclarations(inner[lead:]); cut {
    lead = len(inner) - len(rest)
}
```

Because the keyword cut is independent, this also covers the no-keyword case
(`{{- $fullName := include … -}}`).

**`cond-negate` deliberately does not change.** On `{{- with $cfg := .Values.config }}` it
produces `{{- with not ($cfg := .Values.config) }}`, which was verified to parse and keeps
`$cfg` bound for the body. It is a valid mutant, so do not route `cond-negate` through
`SpanOfRangedExpression` — only the new mutator and the probe need the cut.

### `internal/mutator/range_empty.go` (new)

Mirrors `cond_negate.go`: walk the tree, match `*parse.RangeNode`, take
`SpanOfRangedExpression(f, rng.Pipe)`, emit one candidate replacing that span with `list`.
Single variant, no `Note`. `IDRangeEmpty = "range-empty"` joins the ID block in
`mutator.go`; registering it makes it default, since `config.EnabledMutators` falls back to
`mutator.IDs()` when `--mutators` is unset.

Two guards:

- **No-op guard** — skip when the span already is `list`, or `TestCandidatesAreAlwaysChanges` fails.
- **`ok == false` means skip** — an undelimitable pipeline yields no candidate rather than a guessed span.

The doc comment must state that where the `range` has an `{{ else }}`, the mutant renders
the *else* branch rather than nothing. Still a manifest change, still observable, so no
special case — but "the block disappears" is the natural intuition and it is wrong for that
shape.

### `internal/equivalence/probe.go`

A generic guard after building the probe bytes:

```go
probed := f.Apply(ps, pe, `fail "`+Canary+`"`)
if _, err := source.ParseTemplate(source.New(probed, f.AbsPath, f.Path, f.Kind)); err != nil {
    return nil, false // a probe that cannot parse fails for reasons unrelated to execution
}
return probed, true
```

One extra parse per *span*, not per mutant — `Checker.probed` already caches by span key,
and a parse is negligible against a chart render. Refusing yields no probe, hence
`EquivalenceUnchecked` and `Survived`, which is the direction the burden-of-proof rule
demands.

### `testdata/charts/rangeloop` (new)

Mirrors `shortcircuit`: a `Chart.yaml` whose description states why the fixture exists
standalone, a `values.yaml`, one `templates/configmap.yaml`, and two suites —
`tests/weak_test.yaml`, which asserts only that the ConfigMap renders, and
`tests/strong_test.yaml`, which asserts the loop output itself. One `data:` block carrying
three ranges, each earning its place:

| range site | values | what it proves |
|---|---|---|
| `range .Values.ports` | non-empty list | the base case: survives the weak suite, killed by the strong one |
| `range $k, $v := .Values.labels` | non-empty map | declarations survive both the mutation and the probe |
| `range .Values.extras` | `[]`, never set by any job | genuinely `Equivalent` — no assertion can catch it |

The third site is the honest counterpart to the second. `range` always evaluates its
pipeline even when the result is empty, so the probe legitimately proves execution, the
renders are identical, and `Equivalent` is the correct verdict — reported in every format,
not hidden.

Both suites must pass `helm unittest`, per the fixture convention.

## Testing

### Shared invariants first

Neither corpus in `TestMutantsKeepTemplatesParseable` nor
`TestCandidatesAreAlwaysChanges` contains a single `range`, and
`TestNoMutatorPanicsOnUnparseableTemplate`'s broken inputs contain none either. All three
would pass **vacuously** for `range-empty`. Adding the mutator without extending these
corpora buys zero protection, so this comes first:

- add `{{- range $k, $v := .Values.labels }}{{ $k }}: {{ $v }}{{- end }}` and a plain
  `{{- range .Values.ports }}` to both corpora
- add a truncated `{{ range $k, $v := }}` to the panic corpus

### `internal/mutator/mutator_test.go`

- `TestRegistryHasAllEightMutators` → `TestRegistryHasAllNineMutators`, plus `IDRangeEmpty`
- `TestRangeEmpty` — table over plain range, `$v :=`, `$k, $v :=`, `range`/`else`, a piped
  expression (`.Values.x | sortAlpha`), a range inside a `define` block, and trim-marker
  preservation. Asserts on resulting source via the `applied`/`spans` helpers.
- `TestRangeEmptyPreservesVariableDeclarations` — the headline property, commented with
  *why*: drop them and the mutant is unparseable, so it becomes `Invalid` and grades
  nothing.

### `internal/source/tmplast_test.go`

- `TestSpanOfRangedExpressionExcludesDeclarations`
- `TestCutDeclarationsIgnoresAssignmentInsideAStringLiteral` — the adversarial
  `{{ range .Values.x | replace ":=" "-" }}`
- `TestProbeSpanPreservesVariableDeclarations` — for `range`, `with`, and the bare
  `{{- $fullName := include … -}}` action

### `internal/equivalence/probe_test.go`

- `TestProbeBytesKeepsDeclarationsSoTheProbeParses` — the reachable regression, all three shapes
- A direct unit test of the parse guard. Once declarations are preserved, no *naturally
  reachable* input was found that still produces an unparseable probe, so the guard is
  tested by calling it with deliberately broken bytes rather than through `ProbeBytes` end
  to end. It is a safety net for future constructs and is labelled as such rather than
  dressed up as a reachable path.

### `internal/runner/equivalence_test.go`

Mirroring the existing `shortCircuitFixture` helper:

- `TestRangeMutantSurvivesAWeakSuiteAndIsKilledByAStrongOne` — filters the scored run to
  `Mutator == IDRangeEmpty`
- `TestEmptyRangeIsJudgedEquivalent` — the `extras: []` site

Run everything with `-race`, per `CLAUDE.md`.

## Docs

`sample`'s `458` and every tally stay untouched. That is the dividend from choosing a
separate fixture, and it means the doc work is small:

- **`docs/mutators.md`** — a new `range-empty` section with before/after and counts taken
  from `rangeloop`, stating explicitly that `sample` contains no `range` and therefore
  yields zero. The "name every exclusion" rule applies to a mutator that scores nothing as
  much as to a status.
- **"eight" → "nine"** at `docs/mutators.md:3`, `docs/README.md:16`, `README.md:223`,
  `docs/cli-reference.md:85`.
- **`docs/mutators.md:29`** — "six of the eight mutators score 0.0% against it" is a claim
  about the eight that fire on `sample`. It must not silently become "six of the nine";
  reword so the count stays true.
- **`CLAUDE.md`** — add the declaration gotcha to "Gotchas that have already cost time",
  add `rangeloop` to the fixture conventions beside `shortcircuit`, and update "The 8
  mutators" in the architecture table.

## Out of scope

- The one-iteration variant (`range (first 1 X)`).
- Giving `sample` a `range` and refreshing all quoted figures. `features.md` argues for it
  and notes the README table may already be stale; that is its own change.
- The other `features.md` items: values-key reachability, `--compare`, live progress.
