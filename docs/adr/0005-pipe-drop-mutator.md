# ADR-0005: `pipe-drop` Mutator

**Date:** 2026-10-21  
**Status:** Accepted  
**Deciders:** Project maintainers

---

## Context

The nine existing mutators change literals, conditions, keys and the `default` and `required` calls.
None of them touches the functions that decide the *type* or *shape* of a rendered value: `quote`,
`toYaml`, `trunc`, `trimSuffix` and similar functions. A chart's most serious bugs hide in these
functions. Two examples:

- An unquoted `"8080"` renders the integer `8080`. The API server rejects an integer on string fields
  such as ConfigMap `data` and `env[].value`.
- Without `trunc 63`, a generated name can exceed the DNS label limit.

A suite that asserts presence, or compares a value without its type, never notices these bugs.

`default-drop` already removes one such function, so the mechanism exists. The sample fixture has 12
`quote`, 4 `int`, 3 `toYaml`, one `trunc` and one `trimSuffix`.

## Decision

Add `pipe-drop`. Like every other mutator, it is on by default. It removes one droppable function from
a pipeline and keeps the value that the function was applied to. It works on both call forms:

| form | before | after |
|---|---|---|
| piped | `{{ .Values.port \| quote }}` | `{{ .Values.port }}` |
| piped, with a leading argument | `{{ .Release.Name \| trunc 63 }}` | `{{ .Release.Name }}` |
| prefix | `{{ quote .Values.deep.x }}` | `{{ .Values.deep.x }}` |
| prefix, with a leading argument | `{{ trunc 63 .Values.name }}` | `{{ .Values.name }}` |

### Allowlist

The allowlist is an explicit map from a function name to the number of arguments that the function
takes before its value:

- No arguments: `quote`, `squote`, `toString`, `toYaml`, `toJson`, `toPrettyJson`, `trim`, `lower`,
  `upper`, `title`, `b64enc`.
- One argument: `trunc`, `trimPrefix`, `trimSuffix`.

The mutator leaves a call alone if its argument count does not match this shape. `quote` is variadic,
and if the mutator dropped it from `quote .a .b`, only one of the values would remain.

These functions are left out by design:

- `default` and `required`, because they have their own mutators.
- `include`/`tpl`/`printf`, because they produce the value and do not transform it.
- `indent`/`nindent`, because `num-literal` already changes their widths.

### Key design choices

- **Classify by pipeline index, not by command shape.** The walk visits `PipeNode`s. It tries command 0
  as a prefix call and commands 1+ as piped segments. A piped `| trunc 63` is a command with two
  arguments. A check based on shape would also read it as a prefix call and leave `| 63` behind.
- **Prefix spans anchor on the argument before the value**, through `source.CommandArgSpan` and then
  `SkipSpaceForward`. `required-drop` does exactly the same. For a `FieldNode`, the value's own
  `Position()` is wrong (`TestFieldNodePositionIsNotTheFieldStart`).
- **Piped spans come from `SegmentsOf`**, which `default-drop` also uses. The work on `pipe-drop`
  found a bug in it. The last segment of a pipeline ended at the end of the *enclosing action*. Inside
  a parenthesised pipeline, for example `{{ if gt (.Values.n | default 3) 1 }}`, the drop therefore
  also deleted `) 1`. The result was an unparseable `{{ if gt (.Values.n }}`, graded `Invalid`. The
  segment now ends at the pipeline's own unmatched `)` (`pipelineEnd`).
  `TestSegmentsOfEndsANestedPipelineAtItsParenthesis` pins this. The fixture has no parenthesised
  `| default`, so no measured figure changed.

### Evidence, and the two exclusions it produced

With the full starting allowlist (including `int`, `int64` and `float64`), the fixture gave 21
mutants. 7 of them, a third, were `Invalid`. Per site:

| function | sites | weak suite | strong suite | outcome |
|---|---:|---|---|---|
| `quote` | 12 | 2 equivalent, 10 no-coverage | 7 killed (`equal`), 5 equivalent | kept |
| `trunc 63` | 1 | equivalent | killed (`equal`) | kept |
| `trimSuffix "-"` | 1 | equivalent | killed (`equal`) | kept |
| `int` | 4 | 4 no-coverage | 4 invalid: `incompatible types for comparison: float64 and int` | **excluded** |
| `toYaml` before `nindent` | 3 | 2 invalid, 1 no-coverage | 3 invalid: `wrong type for value; expected string; got map[string]interface {}` | **excluded** |

Both exclusions cause an error whatever the values are. This project sets that bar for a guard
(`CLAUDE.md`, "Mutator guards need evidence"):

- **`int`, `int64`, `float64`.** Helm decodes every number in the values as float64. These
  conversions therefore exist to feed a comparison against an integer literal
  (`gt (int .Values.replicaCount) 1`), and Go templates refuse to compare float64 with int. They are
  removed from the allowlist.
- **A serialiser feeding an indenter.** `indent` and `nindent` accept only a string. If the mutator
  drops `toYaml`/`toJson`/`toPrettyJson` in front of one, the indenter gets a map. The guard is
  narrow: the mutator still mutates a serialiser that no indenter follows.

After both exclusions, the fixture gives 14 mutants, and none is `Invalid`. One probe by hand
confirmed the verdict of the tool independently. When `| quote` is dropped from
`configmap.yaml:10`, `helm unittest` passes on the weak suite, and two strong-suite tests on
`data['max-connections']` fail.

The five equivalents in the strong suite are all `| quote` on values that are already plain strings:
`.Chart.AppVersion`, `config.logLevel`, `config.logFormat`, a `ternary` of two string literals, and
`apiToken`. YAML parses `info` and `"info"` identically, so no assertion can tell them apart. The
equivalence pass removes them from the denominator and does not count them as survivors.

## Consequences

### Positive

- The tool finds a new class of bug: type and shape regressions. These bugs pass `helm template` and
  fail at `kubectl apply`.
- The score of the weak suite stays at 2.3%. Every `pipe-drop` mutant that it reaches is equivalent
  or no-coverage, so the end-to-end separation guard is not affected.
- The strong suite stays at 100.0% (426 killed, 26 equivalent, 20 invalid). The invalid rate goes
  from 4.4% to 4.2%.

### Negative / Risks

- Every quoted fixture figure changed. The docs were refreshed from regenerated output.
  - Mutants: 458 → 472.
  - Strong suite: 417 → 426 killed, 21 → 26 equivalent.
  - Weak suite: 163 → 173 no-coverage, 20 → 24 equivalent.
- `| quote` on a string is frequently equivalent. Each such survivor costs one render in the
  equivalence pass. It never costs score honesty, because the pass proves each one and does not
  assume it.
- The allowlist is a closed set. The tool does not mutate a chart's own type-shaping function (for
  example, a custom formatter based on `tpl`) until someone adds it.

### Testing strategy

- `TestPipeDrop`: both forms, middle segments, leading arguments, a deep field, a parenthesised
  pipeline, a `quote` after an `include`.
- `TestPipeDropIgnoresOtherFunctions`: `default`, `required`, `nindent`, a field named `quote`, a bare
  `quote`, a variadic `quote`.
- `TestPipeDropSkipsMutantsThatCanOnlyBeInvalid` pins both exclusions.
- `TestPipeDropDoesNotTreatAPipedSegmentAsPrefix` pins the classification by index.
- The shared `IDs()` invariant loops cover it automatically.

## Out of Scope

- Removal of `indent`/`nindent`. This was not measured. `num-literal` already changes their widths,
  and this addition needs the same per-site evidence as above.
- Custom formatting functions that a chart defines.
