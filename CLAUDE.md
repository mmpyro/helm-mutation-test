# CLAUDE.md

Guidance for working in this repository. See `README.md` for what the tool does.
## What this is

A Helm plugin that mutates a chart's templates and `values.yaml`, re-runs the chart's
existing helm-unittest suites against each mutation, and reports which mutations no
test noticed. A survived mutant is a missing assertion.

Module: `github.com/mmpyro/helm-mutation-test`. Go 1.24. Pinned to helm-unittest
v1.0.3 and helm.sh/helm/v3 v3.19.0.

## Commands

```console
make build       # bin/helm-mutation-test
make test        # go test ./... -race    <- use this; see "Always run -race" below
make test-short  # without the race detector
make lint        # go vet + gofmt check
make demo        # score the fixture chart with both suites
make install     # install as a real Helm plugin from this directory
```

The `runner` package tests take ~3min under `-race` because they spawn worker
subprocesses and run real chart renders — the fixture chart yields ~460 mutants
and several tests score it end to end. That is expected, not a hang.

## Architecture

```
discover → baseline → coverage index → generate mutants → evaluate (workers) → report
```

| package | responsibility |
|---|---|
| `internal/source` | Load chart files; locate mutations via parser, apply as byte edits |
| `internal/mutator` | The 8 mutators + registry + deterministic plan generation |
| `internal/coverage` | Map a chart file to the suites that render it. Dependency-free by design |
| `internal/workspace` | Per-worker chart copies; write/restore a mutation |
| `internal/runner` | helm-unittest seam, baseline gate, executor, classification, session |
| `internal/report` | Five output formats, each a pure function of `model.Run` |
| `internal/model` | `Run`, `Mutant`, `Status`, `Tally` — the canonical model |

Two boundaries are load-bearing and should stay intact:

- **`internal/runner/unittest.go` is the only file that imports helm-unittest.**
  Everything downstream speaks in local `Suite`/`Outcome` types, so a version bump
  touches one file.
- **`internal/coverage` imports nothing from this module.** It works with opaque
  suite IDs; `runner.CoverageRefs` adapts. Do not reintroduce a `runner` import —
  that was an import cycle the first time.

## Core design rules

**The score must never flatter itself.** `score = killed / (killed + survived)`.
Everything else — no-coverage, invalid, timeout, error, and anything dropped by
`--max-mutants` — is excluded from the score *and named explicitly in every report
format*. A silently truncated or filtered run would read as full coverage. If you
add a status or a cap, surface it in all five formats.

**A render error is not a kill.** A mutation that stops the chart rendering is
"caught" by every test regardless of what it asserts, so it grades nothing. It
becomes `Invalid` and leaves the denominator. `internal/runner/classify.go` encodes
this ordering; `TestClassify` pins it.

**Mutator guards need evidence, not intuition.** Four guards were removed in
commit `a3c752c` because measurement disproved their premise: deleting `apiVersion`,
`kind` or `metadata`, altering a `nindent` width, and mutating a `required` message
all render fine and cleanly separate a weak suite from a strong one. Before adding a
guard, verify the mutation actually breaks rendering:

```console
# copy the fixture, apply the mutation by hand, then:
helm unittest -f 'tests/weak_test.yaml'   /tmp/probe   # should survive
helm unittest -f 'tests/strong_test.yaml' /tmp/probe   # should be killed
```

If it survives one and is killed by the other, it is a useful mutant — do not guard
it. The only justified guard is `include`/`template` target strings, which genuinely
error. Watch the invalid rate: `TestInvalidMutantRateIsLow` fails above 20%.

**Baseline failure is a hard stop, not a warning.** A score over a red suite is a
fiction. Exit code 2.

## Gotchas that have already cost time

**`FieldNode.Position()` is not the field's start.** For `.Values.deep.x` it reports
the offset of `.deep.x` — just past the first segment. Treating it as a start offset
shifts the span into the middle of the expression. `source.CommandArgSpan`
deliberately refuses `FieldNode`; anchor on the enclosing `CommandNode` or on a
neighbouring argument's end instead. Pinned by
`TestFieldNodePositionIsNotTheFieldStart`.

**An `{{if}}` without `{{else}}` has a typed-nil `ElseList`.** A bare `n == nil`
check misses it and every AST mutator panics on `Position()`. Use
`source.isNilNode`. Pinned by `TestWalkIsRobustAgainstTypedNilElseList`.

**`SuiteKey.File` must stay chart-relative.** Each worker re-parses suites from its
own temp chart copy, so an absolute path never matches the baseline's key — every
mutant silently runs zero suites and reports `NoCoverage`. That bug produced a
plausible-looking "134 mutants, all no-coverage" run.

**Workers must be subprocesses when parallel > 1.** helm-unittest's
`TestJob.capabilitiesV3` does `capabilities := v3util.DefaultCapabilities` — a
package-level pointer in Helm — then writes through it. That races, and leaks one
suite's custom `kubeVersion` into another's concurrent render. v1.1.2 has identical
code, so upgrading is not a fix. See the comment block at the top of
`internal/runner/worker.go` before touching the executor.

**`go.mod` needs the `yaml-jsonpath` replace directive.** Go ignores `replace` from
dependencies, so without redirecting to helm-unittest's fork the build fails on a
`gopkg.in/yaml.v3` vs `go.yaml.in/yaml/v3` type mismatch. Do not run
`go mod edit -dropreplace` on it. A helm-unittest bump may need this line updated to
match their `go.mod`.

**Never call `cache.StoreToFileIfNeeded()`.** A mutant run must not rewrite the
chart's committed `__snapshot__` files. Pinned by
`TestMutantRunsDoNotWriteSnapshots`.

**pflag's `UnquoteUsage` eats backticks.** The first backquoted string in a flag's
usage becomes its displayed value type, so a backtick in a `Describe()` string
turned `--mutators strings` into `--mutators | default X`. Keep mutator
descriptions backtick-free.

## Environment notes

- A project `Write` hook validates YAML. Helm templates are **not** valid YAML
  before rendering, so it rejects every file under `testdata/charts/*/templates/`.
  Write those with a shell heredoc, or exclude `templates/**` from the hook.
- `sed` on this machine is GNU sed, so BSD-style `sed -i ''` fails. Use `python3`
  for in-place edits in throwaway scripts.
- helm-unittest's full source is available locally at
  `~/Library/helm/plugins/helm-unittest.git` — useful for checking behaviour rather
  than guessing at it.

## Testing conventions

Tests are behaviour-first and named for the property they protect, with a comment
saying *why* the property matters when it is not obvious. Keep that style.

- **Always run `-race`.** The helm-unittest global-state race was invisible without
  it and passed a plain `go test` cleanly.
- **Mutator tests assert on resulting source**, not on byte offsets — use the
  `applied` and `spans` helpers in `mutator_test.go`.
- **Shared invariants belong in loops over `IDs()`**, so a new mutator inherits
  them automatically: no no-op candidates, no inverted spans, no panics on
  unparseable templates, mutants still parse.
- **The fixture chart is the tool's own test.** `testdata/charts/sample` ships a
  deliberately weak suite and a thorough one; both cover `deployment.yaml` and
  `service.yaml`, and the thorough one additionally covers the other five
  templates. Both must pass `helm unittest`; the weak one must score under 45%
  and the strong one over 70%. If you change the chart or a mutator, re-check
  `TestWeakSuiteScoresLowAndStrongScoresHigh` — it is the end-to-end guard, and CI
  also fails if the weak suite ever scores above 50%.
- `internal/runner/baseline_test.go`'s `TestMain` doubles as the worker entry point
  so tests exercise the real subprocess protocol rather than a stand-in. Do not
  remove `SetWorkerArgs`.

## Out of scope for now

Incremental / changed-files-only mode, SARIF output, mutant caching across runs,
higher-order mutants, subchart-aware scoring.
