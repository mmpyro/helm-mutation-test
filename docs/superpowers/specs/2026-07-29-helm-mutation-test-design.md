# helm-mutation-test — Mutation Testing for helm-unittest Suites

## Context

`helm-unittest` tells you whether your chart tests **pass**. It cannot tell you whether they are
**worth anything**. A suite of `isKind: Deployment` assertions passes green forever while the chart
silently regresses `replicas`, `imagePullPolicy`, or an entire `{{- if .Values.ingress.enabled }}`
branch.

Mutation testing closes that gap: deliberately break the chart, re-run the tests, and see whether
the tests notice. A mutant the tests *don't* notice ("survived") is a precise, actionable pointer at
a missing assertion.

This project builds `helm mutation-test` — a Helm plugin that mutates a chart's templates and
`values.yaml`, re-runs the chart's existing `helm-unittest` suites against each mutant, and reports a
mutation score plus the exact list of survived mutants with `file:line` and a diff.

**Iteration 1 scope:** 8 basic mutators, 5 report formats, in-process parallel execution.
Greenfield — the working directory is empty.

**Decisions already made with the user:**

| Decision | Choice |
| --- | --- |
| Mutation target | Chart templates (`templates/**`, incl. `_helpers.tpl`) **and** `values.yaml` |
| Execution | Go binary importing helm-unittest as a **library** |
| Mutators | All 8 (see catalog) |
| Reports | All 5: console, JSON, HTML, Markdown, JUnit XML |
| Command | `helm mutation-test` |
| Module path | `github.com/mmpyro/helm-mutation-test` |
| Validation | Built-in fixture chart with a deliberately **weak** and a **strong** suite |

## Environment (verified)

- Helm `v3.17.3`, Go `1.24.4` (darwin/arm64).
- `helm-unittest` `v1.0.3` installed; full source at `~/Library/helm/plugins/helm-unittest.git`
  (useful as a local reference while implementing).
- `parse.SkipFuncCheck` exists in this Go version
  (`$(go env GOROOT)/src/text/template/parse/parse.go:42`) — we can parse Helm templates without
  supplying the sprig/Helm funcmap.
- helm-unittest has **no** `os.Chdir` / `os.Setenv` and no mutable package-level state in
  `pkg/unittest` — in-process goroutine parallelism is viable.

## Approach

Six stages, each an independently testable package.

```
discover ─▶ baseline ─▶ coverage index ─▶ generate mutants ─▶ execute (pool) ─▶ report
```

1. **Discover** mutable files: `templates/**` + `values.yaml`, filtered by `--include`/`--exclude`.
2. **Baseline**: run the unmutated suites. Any failure aborts with exit 2 — mutation scores are
   meaningless over a red suite. Records baseline wall time (feeds the per-mutant timeout).
3. **Coverage index**: map each template file → the suites that exercise it, from each suite's
   `Templates` / `ExcludeTemplates` and each job's `Template` / `Templates` fields (all exported).
   A suite with no template filter covers everything. `_helpers.tpl` and `values.yaml` conservatively
   cover **all** suites. This both cuts runtime substantially and makes `NoCoverage` a real finding.
4. **Generate mutants**: per file, per enabled mutator, produce `Mutant{ByteRange, Replacement, …}`.
   Deterministic order: file path → byte offset → mutator ID.
5. **Execute**: worker pool, each worker owning a private chart copy. Per mutant: apply the byte-range
   edit, `loader.Load`, re-parse suites, run **only covering suites** with `failFast=true`, classify,
   revert.
6. **Report**: every format serializes from one canonical `model.Run`.

### The helm-unittest integration seam

We deliberately bypass `TestRunner.RunV3` (returns only `bool`, prints to a `Printer`) and use the
exported layer beneath it, which hands back full structured results:

```go
// internal/runner/unittest.go — the ONLY file that imports helm-unittest.
suites, err := unittest.ParseTestSuiteFile(suitePath, chartRoute, cfg.Strict, cfg.ValuesFiles)
chart, err := loader.Load(workerChartDir)                       // helm.sh/helm/v3/pkg/chart/loader
cache, err := snapshot.CreateSnapshotOfSuite(suite.SnapshotFileUrl(), false)
res := suite.RunV3(chart, cache, /*failFast=*/ true, /*renderPath=*/ "", &results.TestSuiteResult{})
// Deliberately DO NOT call cache.StoreToFileIfNeeded() — never rewrite snapshots during a mutant run.
```

Three rules this seam must enforce:

- **Re-parse suites per mutant run.** `*TestSuite` carries mutable state (`polishTestJobsPathInfo`,
  `polishChartSettings`) and is not safe to share across goroutines. Re-parsing YAML is cheap next to
  rendering. Test files are never mutated, but parse from the *worker's* copy so paths resolve.
- **`failFast=true` for mutants, `false` for baseline.** One failing test is enough to kill a mutant.
  (`--kill-attribution all` overrides this to `false` when the user wants the *complete* set of killing
  tests rather than just the first — slower, off by default.)
- **Silence output**: `printer.NewPrinter(io.Discard, &noColor)`.

Isolating helm-unittest behind this one adapter means a version bump touches exactly one file.

## Repository layout

```
helm-mutation-test/
├── plugin.yaml                     name: mutation-test, command: $HELM_PLUGIN_DIR/bin/helm-mutation-test
├── install-binary.sh               model on ~/Library/helm/plugins/helm-diff/install-binary.sh
├── Makefile                        build / test / lint / install
├── go.mod                          module github.com/mmpyro/helm-mutation-test  (go 1.24)
├── cmd/helm-mutation-test/main.go  cobra CLI; flags → config.Config; exit codes
├── internal/
│   ├── config/config.go            flag struct + validation + defaults
│   ├── model/model.go              Mutant, MutantStatus, KilledBy, Run, Score  ← canonical model
│   ├── source/
│   │   ├── source.go               File: path, bytes, line index; ApplyEdit(byteRange, repl)
│   │   ├── tmplast.go              parse.Tree walk (SkipFuncCheck) → node positions
│   │   ├── span.go                 AST pos → exact source byte span (delimiter-anchored surgery)
│   │   └── yamlnode.go             yaml.v3 Node walk → line/col spans (values.yaml)
│   ├── mutator/
│   │   ├── mutator.go              Mutator interface + registry
│   │   ├── cond_negate.go   bool_flip.go        num_literal.go     str_literal.go
│   │   └── yaml_key_delete.go  default_drop.go  comparison_swap.go required_drop.go
│   ├── coverage/coverage.go        template file → covering suites
│   ├── workspace/workspace.go      per-worker chart copy; apply/revert; cleanup
│   ├── runner/
│   │   ├── unittest.go             the helm-unittest adapter (above)
│   │   ├── baseline.go             baseline run + gate
│   │   ├── executor.go             worker pool, timeout, classification
│   │   └── classify.go             results → MutantStatus
│   └── report/
│       ├── console.go  json.go  markdown.go  junit.go
│       ├── stryker.go              mutation-testing-elements schema JSON
│       └── html.go                 HTML shell embedding the stryker JSON
└── testdata/charts/sample/         fixture chart (see below)
```

Each mutator is ~30–60 lines plus a table-driven test. `report/*.go` are pure functions
`func(model.Run) ([]byte, error)` — trivially golden-testable.

## Component detail

### `source` — precise, formatting-preserving edits

The core technique: **use the AST for positions, but edit the original bytes.**

```go
tree := parse.New(filename)
tree.Mode = parse.SkipFuncCheck            // no sprig/Helm funcmap needed
trees := map[string]*parse.Tree{}
_, err := tree.Parse(string(src), "{{", "}}", trees)
```

Notes that matter:

- Walk **every** tree in `trees`, not just the root — `_helpers.tpl` `{{ define }}` blocks become
  separate trees, with `Pos` still offsets into the same original source.
- `Node.Position()` is a byte offset into the original text. Text nodes are trim-normalised by the
  parser, so never reconstruct source from `Node.String()` — always slice the original bytes.
- For nodes whose exact source span isn't directly available (an `IfNode`'s condition pipeline),
  `span.go` anchors on the node offset, scans to the enclosing `{{`/`}}` (respecting `-` trim
  markers), and locates the keyword — robust text surgery anchored on AST truth.
- A parse failure on a file is a **warning, not a fatal**: skip that file's AST mutators, still apply
  the line-based `yaml-key-delete`, and record it in the report.

`values.yaml` uses `gopkg.in/yaml.v3` `Node` (has `Line`/`Column`/`Tag`/`Style`) → line/col resolved
to byte offsets via the line index.

### `runner/classify.go` — mutant status

Order matters:

| Check | Status | In score? |
| --- | --- | --- |
| No covering suite for the mutated file | `NoCoverage` | no (reported as a finding) |
| Some `AssertsResult[j].Passed == false` | **`Killed`** | **yes** |
| All failures are `TestJobResult.ExecError` (render broke) | `Invalid` | no |
| Every covering test passed | **`Survived`** | **yes** |
| Exceeded per-mutant timeout | `Timeout` | no |
| Internal failure (copy / load / parse) | `Error` | no |

**Why `Invalid` is excluded:** a mutant that breaks rendering is killed by *every* test, so it carries
zero information about test quality. Counting it inflates the score. We report the count separately —
a high `Invalid` rate means our mutators are too blunt, which is our bug, not the user's.

```
score = Killed / (Killed + Survived)
```

`Timeout` handling: `TestSuite.RunV3` takes no `context.Context`, so the worker runs it in a goroutine
and abandons it on timeout. Acceptable for a short-lived CLI; documented in code.

### `workspace` — isolation

One chart copy per worker (not per mutant): copy once, then per mutant apply the byte-range edit,
run, and restore the original bytes. Each copy has its own `__snapshot__` dir, so snapshot assertions
can't cross-contaminate. `t.Cleanup` / `defer` removes copies; a `--keep-workdir` debug flag retains
them for inspection.

## Mutator catalog

| ID | Target node / pattern | Mutation | Guards (avoid `Invalid` noise) |
| --- | --- | --- | --- |
| `cond-negate` | `parse.IfNode`, `parse.WithNode` | wrap condition: `COND` → `not (COND)` | — |
| `bool-flip` | `parse.BoolNode`; YAML `!!bool` | `true` ↔ `false` | — |
| `num-literal` | `parse.NumberNode`; YAML `!!int`/`!!float` | two variants: `n`→`n+1`, and `n`→`0` (`1` when `n==0`) | **skip** numbers that are args to `indent`, `nindent`, `repeat`, `trunc`, `printf` — those break YAML structure |
| `str-literal` | `parse.StringNode`; YAML `!!str` | → `"helm-mutation-test"` | skip `apiVersion` values |
| `yaml-key-delete` | template/values lines matching `^\s*key:` | delete the line **and its more-indented block** | skip `apiVersion`; skip lines containing `{{`-control-flow (`if`/`range`/`end`) |
| `default-drop` | `parse.PipeNode` cmd with ident `default` | remove the `\| default X` segment | — |
| `comparison-swap` | `parse.IdentifierNode` | `eq`↔`ne`, `lt`↔`ge`, `gt`↔`le`, `and`↔`or` | — |
| `required-drop` | `parse.CommandNode` with ident `required` | `required "msg" ARG` → `ARG` | — |

`values.yaml` gets `bool-flip`, `num-literal`, `str-literal`, `yaml-key-delete` via the YAML node path.

Optional heuristic pre-filter, **off by default**: `--skip-equivalent` renders the mutated chart under
default values and skips the mutant if output is byte-identical to baseline. It is only a heuristic —
a test supplying non-default values could still kill such a mutant — so it stays opt-in to keep
default scores sound.

## Reports

All derive from `model.Run`. `--report console,json,html,markdown,junit`, written to `--report-dir`
(default `.helm-mutation-test/`). Console always prints.

**Console** (default):

```
Mutation testing my-chart  (14 suites, 61 tests, baseline 1.2s)

  Score  38.2%   ██████████░░░░░░░░░░░░░░░░   (13 killed / 21 survived)
  skipped: 4 no-coverage · 2 invalid · 0 timeout

  By mutator                      killed  survived   score
    cond-negate                        4         1   80.0%
    str-literal                        2         9   18.2%
    yaml-key-delete                    3         6   33.3%
    ...

  By file                         killed  survived   score
    templates/deployment.yaml          9         4   69.2%
    templates/service.yaml             0         8    0.0%
    values.yaml                        4         9   30.8%

  SURVIVED (21)

  templates/service.yaml:9  cond-negate
    - {{- if .Values.service.external }}
    + {{- if not (.Values.service.external) }}
    ran 3 tests in service_test.yaml — all passed

  templates/deployment.yaml:34  str-literal
    - imagePullPolicy: {{ .Values.image.pullPolicy }}
    + imagePullPolicy: helm-mutation-test
    ran 3 tests in deployment_test.yaml — all passed

  NO COVERAGE (4)
    templates/ingress.yaml — no suite lists this template (4 mutants never run)
```

**JSON** — canonical: every mutant with `id`, `mutator`, `file`, `line`, `column`, `original`,
`mutated`, `status`, `killedBy` (`{suite, test, assertType, assertIndex}`), `duration`. Report
timings are excluded from golden-test comparison for determinism.

**HTML** — emit the [mutation-testing-elements](https://github.com/stryker-mutator/mutation-testing-elements)
standard schema and ship a small `go:embed`ed HTML shell mounting `<mutation-test-report-app>`. Gives
an interactive source-annotated report (click a file, see mutants inline) for ~50 lines. Our statuses
map to the schema's: `Killed`→`Killed`, `Survived`→`Survived`, `NoCoverage`→`NoCoverage`,
`Invalid`→`CompileError`, `Timeout`→`Timeout`, `Error`→`RuntimeError`.

**Markdown** — score table + survived list, sized for `$GITHUB_STEP_SUMMARY` / PR comments.

**JUnit XML** — one `<testcase>` per mutant; `Survived` → `<failure>` with the diff; `NoCoverage`/
`Invalid` → `<skipped>`. Copy the shape of `pkg/unittest/formatter/junit_report_xml.go`.

## CLI

```
helm mutation-test [flags] CHART

Pass-through to helm-unittest:
  -f, --file stringArray         test file globs (default tests/*_test.yaml)
  -v, --values stringArray       values files
  -s, --with-subchart            include subchart tests (default true)
      --strict                   strict suite parsing
      --chart-tests-path string

Mutation control:
      --mutators strings         enabled (default: all 8)
      --exclude-mutators strings
      --include strings          files to mutate (default templates/**, values.yaml)
      --exclude strings
      --max-mutants int          cap (0 = uncapped)
      --seed int                 sampling seed (default 1) — reproducible caps
      --skip-equivalent          opt-in heuristic (see above)
      --timeout duration         per mutant (default max(30s, 10× baseline))

Execution:
  -p, --parallel int             workers (default NumCPU)
      --kill-attribution string  first|all (default first; `all` disables failFast)

Output:
      --report strings           console,json,html,markdown,junit (default console)
      --report-dir string        default .helm-mutation-test
      --threshold float          exit 1 if score below (0 = disabled)
      --color / -d, --debug / --keep-workdir
```

Exit codes: `0` ok · `1` score below `--threshold` · `2` baseline failed or config error.

## Fixture chart

`testdata/charts/sample/` — designed so scores are *predictable and assertable*, which turns the
tool's own correctness into a test.

- `templates/deployment.yaml` — `replicas: {{ .Values.replicaCount }}`;
  `image: "{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}"`;
  `imagePullPolicy`; `{{- if .Values.serviceAccount.create }}` block; `{{- if eq .Values.env "prod" }}`
  block; a `{{ required "..." .Values.mandatory }}`.
- `templates/service.yaml` — `type`, `port`.
- `templates/ingress.yaml` — entirely inside `{{- if .Values.ingress.enabled }}`.
- `templates/_helpers.tpl` — `fullname` / `labels` defines.
- `tests/weak_test.yaml` — `isKind` only. Expected to survive nearly everything.
- `tests/strong_test.yaml` — exact `equal` per field, `hasDocuments: 0/1` for the ingress toggle,
  `failedTemplate` for the `required` path, snapshot assertions.

## Testing strategy (TDD — write the test first)

| Level | What |
| --- | --- |
| Mutator units | Table-driven: template snippet → expected `[(line, original, mutated)]`. Covers every guard (e.g. `nindent 8` is **not** mutated by `num-literal`). |
| `source`/`span` | Trim markers (`{{-`, `-}}`), `{{ define }}` in `_helpers.tpl`, CRLF, unparseable file → warning not panic. |
| `coverage` | Suite with `templates:`, with `excludeTemplates:`, with neither; per-job `template:`; `_helpers.tpl` ⇒ covers all. |
| `classify` | Synthetic `TestSuiteResult` fixtures for each of the six statuses — esp. `ExecError` ⇒ `Invalid`, not `Killed`. |
| End-to-end | Fixture chart: `weak_test.yaml` score < 30%, `strong_test.yaml` score > 85%. Assert named mutants: `cond-negate` on `ingress.enabled` **survives** weak, is **killed** by strong. |
| Reports | Golden files for JSON/Markdown/JUnit; validate stryker JSON against the published schema. |
| Determinism | Same seed + chart ⇒ byte-identical JSON (durations excluded). |
| Parallel safety | `--parallel 1` vs `--parallel 8` ⇒ identical statuses for every mutant. |

Also smoke-test against real charts under
`~/Library/helm/plugins/helm-unittest.git/pkg/unittest/testdata/` to shake out template-parsing edge
cases on production-shaped input.

## Implementation phases

Each phase ends green and independently reviewable.

0. **Repo init** — `git init`, and write this design to
   `docs/superpowers/specs/2026-07-29-helm-mutation-test-design.md` as the committed spec of record
   (the brainstorming workflow's spec location; not created yet because the directory is neither a git
   repo nor writable during planning).
1. **Skeleton** — `go.mod`, `plugin.yaml`, `install-binary.sh`, `Makefile`, cobra CLI with flags
   parsed and validated, `model.Run` types. `helm mutation-test --help` works.
2. **helm-unittest seam + baseline** — `runner/unittest.go`, `runner/baseline.go`. Fixture chart with
   both suites. Verify baseline detects a red suite and aborts with exit 2.
3. **`source` layer** — AST walk, span resolution, YAML node walk, `ApplyEdit`. Heaviest test burden;
   everything downstream depends on its precision.
4. **Mutators** — all 8, one at a time, test-first, each landing with its guards.
5. **Coverage + executor** — coverage index, worker pool, timeout, classification. First real
   end-to-end score. Assert the weak/strong score bands here.
6. **Reports** — console first (it's the primary UX), then JSON, then the three serializers over it.
7. **Polish** — `--threshold` exit codes, README with a worked example, GitHub Actions workflow
   running the tool on its own fixture chart.

## Verification

```bash
# Unit + integration
make test                                   # go test ./... with -race

# End-to-end against the fixture: weak suite should score badly
go run ./cmd/helm-mutation-test \
  -f 'tests/weak_test.yaml' testdata/charts/sample \
  --report console,json,html,markdown,junit --report-dir /tmp/mt-weak
# expect: score < 30%, many survived, exit 0

# Same chart, strong suite: should score well and satisfy a threshold
go run ./cmd/helm-mutation-test \
  -f 'tests/strong_test.yaml' testdata/charts/sample \
  --threshold 85 --report console
# expect: score > 85%, exit 0

# Threshold enforcement actually fails the build
go run ./cmd/helm-mutation-test -f 'tests/weak_test.yaml' \
  testdata/charts/sample --threshold 90; echo "exit=$?"   # expect exit=1

# Baseline gate: break a test on purpose, confirm exit 2 and no mutants run

# Determinism and parallel safety
for p in 1 8; do
  go run ./cmd/helm-mutation-test testdata/charts/sample -p $p \
    --report json --report-dir /tmp/mt-p$p
done
# then diff the two JSON files ignoring duration fields — expect no differences

# Real-chart smoke test
go run ./cmd/helm-mutation-test \
  ~/Library/helm/plugins/helm-unittest.git/pkg/unittest/testdata/chart-snapshot

# Installed-plugin path
make install && helm plugin list | grep mutation-test
helm mutation-test testdata/charts/sample

# Inspect the HTML report
open /tmp/mt-weak/mutation-report.html    # survived mutants highlighted inline in source
```

## Risks and mitigations

| Risk | Mitigation |
| --- | --- |
| helm-unittest not actually goroutine-safe despite the static audit | All usage is behind `runner/unittest.go`. Fallback: process-level parallelism via a hidden `--worker` subcommand emitting JSON per mutant. The parallel-safety test catches this early. |
| Runtime = mutants × suite time | Coverage-aware suite selection, `failFast=true`, worker pool, `--max-mutants` with `--seed`. Report the count skipped by any cap explicitly — a silent truncation reads as full coverage. |
| High `Invalid` rate makes reports noisy | Per-mutator guards (table above); `Invalid` count surfaced in every report so we can tune. |
| helm-unittest API drift on version bump | One adapter file; pinned in `go.mod`. |
| `text/template/parse` rejects an exotic Helm template | Per-file warning, not fatal: skip AST mutators for that file, keep line-based ones, surface it in the report. |

## Out of scope (iteration 2+)

Incremental / changed-files-only mode; SARIF output; mutant caching across runs; higher-order
mutants; subchart-aware scoring; SLA-based mutant scheduling.
