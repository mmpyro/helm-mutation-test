# ADR-0005: `pipe-drop` Mutator

**Date:** 2026-10-21  
**Status:** Accepted  
**Deciders:** Project maintainers

---

## Context

The nine existing mutators perturb literals, conditions, keys and the `default` and `required` calls.
None of them touches the functions that decide the *type* or *shape* of a rendered value: `quote`,
`toYaml`, `trunc`, `trimSuffix` and the like. Those are where a chart's most consequential bugs hide —
an unquoted `"8080"` renders the integer `8080`, which the API server rejects on string fields such as
ConfigMap `data` and `env[].value`; a missing `trunc 63` lets a generated name exceed the DNS label
limit — and a suite that asserts presence, or compares a value without its type, never notices.

`default-drop` already removes one such function, so the mechanism exists. The sample fixture has 12
`quote`, 4 `int`, 3 `toYaml`, one `trunc` and one `trimSuffix`.

## Decision

Add `pipe-drop`, on by default like every other mutator. It removes one droppable function from a
pipeline and keeps the value it was applied to, in both call forms:

| form | before | after |
|---|---|---|
| piped | `{{ .Values.port \| quote }}` | `{{ .Values.port }}` |
| piped, with a leading argument | `{{ .Release.Name \| trunc 63 }}` | `{{ .Release.Name }}` |
| prefix | `{{ quote .Values.deep.x }}` | `{{ .Values.deep.x }}` |
| prefix, with a leading argument | `{{ trunc 63 .Values.name }}` | `{{ .Values.name }}` |

### Allowlist

An explicit map from function name to the number of arguments it takes before its value:
`quote`, `squote`, `toString`, `toYaml`, `toJson`, `toPrettyJson`, `trim`, `lower`, `upper`, `title`,
`b64enc` take none; `trunc`, `trimPrefix`, `trimSuffix` take one. A call whose argument count differs
from that shape is left alone — `quote` is variadic, and dropping it from `quote .a .b` would keep only
one of the values.

Left out by design: `default` and `required` (their own mutators), `include`/`tpl`/`printf` (they
produce the value rather than transform it), `indent`/`nindent` (`num-literal` already perturbs their
widths).

### Key design choices

- **Classify by pipeline index, not by command shape.** The walk visits `PipeNode`s; command 0 is
  tried as a prefix call, commands 1+ as piped segments. A piped `| trunc 63` is a two-argument
  command, so a shape-based check would also read it as a prefix call and leave `| 63` behind.
- **Prefix spans anchor on the argument before the value**, via `source.CommandArgSpan`, then
  `SkipSpaceForward` — exactly as `required-drop` does. The value's own `Position()` is wrong for a
  `FieldNode` (`TestFieldNodePositionIsNotTheFieldStart`).
- **Piped spans come from `SegmentsOf`**, shared with `default-drop`. Building `pipe-drop` exposed a
  bug in it: a pipeline's last segment ended at the *enclosing action's* end, so inside a
  parenthesised pipeline — `{{ if gt (.Values.n | default 3) 1 }}` — the drop also deleted `) 1` and
  left an unparseable `{{ if gt (.Values.n }}`, graded `Invalid`. It now ends at the pipeline's own
  unmatched `)` (`pipelineEnd`), pinned by `TestSegmentsOfEndsANestedPipelineAtItsParenthesis`. The
  fixture has no parenthesised `| default`, so no measured figure moved.

### Evidence, and the two exclusions it produced

With the full starting allowlist (including `int`, `int64` and `float64`), the fixture yielded 21
mutants, 7 of them `Invalid` — a third. Per site:

| function | sites | weak suite | strong suite | outcome |
|---|---:|---|---|---|
| `quote` | 12 | 2 equivalent, 10 no-coverage | 7 killed (`equal`), 5 equivalent | kept |
| `trunc 63` | 1 | equivalent | killed (`equal`) | kept |
| `trimSuffix "-"` | 1 | equivalent | killed (`equal`) | kept |
| `int` | 4 | 4 no-coverage | 4 invalid: `incompatible types for comparison: float64 and int` | **excluded** |
| `toYaml` before `nindent` | 3 | 2 invalid, 1 no-coverage | 3 invalid: `wrong type for value; expected string; got map[string]interface {}` | **excluded** |

Both exclusions error whatever the values, which is the bar this project sets for a guard
(`CLAUDE.md`, "Mutator guards need evidence"):

- **`int`, `int64`, `float64`.** Helm decodes every values number as float64, so these conversions
  exist to feed a comparison against an integer literal (`gt (int .Values.replicaCount) 1`), and Go
  templates refuse to compare float64 with int. Removed from the allowlist.
- **A serialiser feeding an indenter.** `indent` and `nindent` accept only a string, so dropping
  `toYaml`/`toJson`/`toPrettyJson` in front of one hands it a map. The guard is narrow: a serialiser
  not followed by an indenter is still mutated.

After both, the fixture yields 14 mutants and none is `Invalid`. One hand probe confirmed the tool's
verdict independently: dropping `| quote` from `configmap.yaml:10` passes `helm unittest` on the weak
suite and fails two strong-suite tests on `data['max-connections']`.

The five strong-suite equivalents are all `| quote` on values that are already plain strings
(`.Chart.AppVersion`, `config.logLevel`, `config.logFormat`, a `ternary` of two string literals,
`apiToken`): YAML parses `info` and `"info"` identically, so no assertion can tell them apart, and the
equivalence pass removes them from the denominator rather than counting them as survivors.

## Consequences

### Positive

- A new class of finding: type and shape regressions, the bugs that pass `helm template` and fail at
  `kubectl apply`.
- The weak suite's score is unchanged at 2.3%: every `pipe-drop` mutant it reaches is equivalent or
  no-coverage, so the end-to-end separation guard is unaffected.
- The strong suite stays at 100.0% (426 killed, 26 equivalent, 20 invalid); the invalid rate falls from
  4.4% to 4.2%.

### Negative / Risks

- Every quoted fixture figure moved (458 → 472 mutants; strong 417 → 426 killed, 21 → 26 equivalent;
  weak 163 → 173 no-coverage, 20 → 24 equivalent), and the docs were refreshed from regenerated output.
- `| quote` on a string is frequently equivalent. That costs an equivalence-pass render per such
  survivor; it never costs score honesty, since each one is proven rather than assumed.
- The allowlist is a closed set. A chart's own type-shaping function (say, a custom `tpl`-based
  formatter) is not mutated until added.

### Testing strategy

- `TestPipeDrop`: both forms, middle segments, leading arguments, a deep field, a parenthesised
  pipeline, a `quote` after an `include`.
- `TestPipeDropIgnoresOtherFunctions`: `default`, `required`, `nindent`, a field named `quote`, a bare
  `quote`, a variadic `quote`.
- `TestPipeDropSkipsMutantsThatCanOnlyBeInvalid` pins both exclusions.
- `TestPipeDropDoesNotTreatAPipedSegmentAsPrefix` pins the index-based classification.
- The shared `IDs()` invariant loops cover it automatically.

## Out of Scope

- `indent`/`nindent` removal. Not measured; `num-literal` already perturbs their widths, and adding it
  needs the same per-site evidence as above.
- Custom, chart-defined formatting functions.
