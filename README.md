# helm-mutation-test

A Helm plugin that measures how much your [helm-unittest](https://github.com/helm-unittest/helm-unittest)
suites actually assert.

`helm unittest` tells you whether your chart tests **pass**. It cannot tell you whether they are
**worth anything**. This suite is green, and always will be:

```yaml
tests:
  - it: renders a Deployment
    asserts:
      - isKind:
          of: Deployment
```

Meanwhile the chart can regress `replicas`, lose `imagePullPolicy`, or stop rendering its whole
`{{- if .Values.ingress.enabled }}` block, and nothing turns red.

This plugin breaks the chart on purpose — one small change at a time — and re-runs your existing
suites against each one. A change that no test objects to is a **survived mutant**: a precise,
actionable pointer at a missing assertion.

## The fixture chart, as a demonstration

`testdata/charts/sample` ships two suites over the same templates. Both pass. `helm unittest` reports
them as equally green:

```console
$ helm unittest -f 'tests/weak_test.yaml'   testdata/charts/sample   # PASS, 4 tests
$ helm unittest -f 'tests/strong_test.yaml' testdata/charts/sample   # PASS, 43 tests
```

Mutation testing separates them:

| suite | killed | survived | equivalent | no-coverage | invalid | score |
|---|---:|---:|---:|---:|---:|---:|
| `weak_test.yaml` | 6 | 255 | 20 | 163 | 14 | **2.3%** |
| `strong_test.yaml` | 417 | 0 | 21 | 0 | 20 | **100.0%** |

Neither suite's score is measured against a denominator that includes something it never had a chance
to catch. `no-coverage` counts mutants in the five templates the weak suite never renders at all, and
`equivalent` counts mutants that changed the chart's source but provably cannot change any rendered
manifest, so no assertion anywhere could have caught them either. Both are excluded from the score and
named explicitly — see [equivalent mutants](docs/concepts.md#equivalent-mutants) and
[why Invalid and NoCoverage are excluded](docs/concepts.md#why-invalid-and-nocoverage-are-excluded).

Run `make demo` to reproduce both.

## Install

```console
helm plugin install https://github.com/mmpyro/helm-mutation-test

# Helm 4 refuses unsigned plugin sources unless you say so:
helm plugin install --verify=false https://github.com/mmpyro/helm-mutation-test
```

This downloads a prebuilt binary for linux and macOS on amd64 and arm64, verified against the
release's `SHA256SUMS`. No Go toolchain needed. The binaries are static, so the linux asset also
works on musl images such as `alpine/helm`.

On any other platform, or if the download fails, the install falls back to building from source and
then does need Go (https://go.dev/dl/).

Either way the chart needs `helm-unittest`-style test suites. Tested against helm-unittest v1.0.3.

From a checkout:

```console
make install
```

A checkout always builds from source rather than downloading, so `make install` runs the tree you
are editing. `HELM_MUTATION_TEST_BUILD=1` forces that behaviour anywhere.

## Usage

```console
helm mutation-test ./my-chart
```

```console
# Fail CI below 70%, and write reports the job can publish
helm mutation-test ./my-chart --threshold 70 \
  --report console,markdown,html,junit --report-dir ./mutation

# A fast subset while iterating on tests
helm mutation-test ./my-chart --mutators cond-negate,str-literal --max-mutants 50
```

### Reading the output

```
Mutation testing sample  (1 suite, 4 tests, baseline 3ms)

  Score    2.3%  █░░░░░░░░░░░░░░░░░░░░░░░░░   (6 killed / 255 survived)
  not scored: 163 no-coverage · 20 equivalent · 14 invalid
  8 survivors could not be checked for equivalence and may be unkillable; each one's reason is in its detail

  By mutator                          killed  survived    score
    bool-flip                              0        14    0.0%
    num-literal                            0        47    0.0%
    ...

  SURVIVED (255)

  templates/deployment.yaml:9  yaml-key-delete
    -   replicas: {{ .Values.replicaCount }}
    + (line removed)
    ran 4 tests in tests/weak_test.yaml — all passed
```

Each survivor is a concrete instruction: add an assertion that would tell those two lines apart.

### Exit codes

| code | meaning |
|---|---|
| `0` | ran successfully (and met `--threshold`, if set) |
| `1` | the mutation score is below `--threshold` |
| `2` | could not run — most often the chart's own tests do not pass |

The last case is deliberate. A mutation score measured against a failing suite is meaningless, so a
red baseline is a hard stop rather than a warning.

## Mutators

| ID | Mutation |
|---|---|
| `cond-negate` | Negate `if` / `else if` / `with` conditions: `COND` → `not (COND)` |
| `bool-flip` | `true` ↔ `false` |
| `num-literal` | Perturb numbers: `n` → `n+1`, and `n` → `0` |
| `range-empty` | Force a `range` loop to iterate zero times: `range X` → `range list` |
| `str-literal` | Replace a string with a sentinel |
| `yaml-key-delete` | Delete a key and its nested block |
| `default-drop` | Remove a `\| default X` pipeline segment |
| `comparison-swap` | `eq`↔`ne`, `lt`↔`ge`, `gt`↔`le`, `and`↔`or` |
| `required-drop` | Strip a `required` guard down to its value |

Templates (including `_helpers.tpl` partials) and `values.yaml` are both mutated. Test files never
are — mutating the tests would measure nothing about the chart.

## What the score does and does not count

```
score = killed / (killed + survived)
```

Only those two outcomes grade your assertions. Everything else is reported separately, because
folding it in would flatter the number:

| status | why it is excluded |
|---|---|
| **No coverage** | No suite renders the mutated template, so no test ever had the chance to catch it. This is a finding in its own right. |
| **Invalid** | The mutation stopped the chart rendering, so *every* test "caught" it regardless of what it asserts. That grades nothing. A high invalid rate is a bug in this tool, not in your suite. |
| **Equivalent** | The mutation provably cannot change any rendered manifest — checked automatically on every survivor, on by default. No assertion could ever have caught it either, so it is excluded the same way. |
| **Timeout** | Evaluation exceeded the per-mutant timeout. |
| **Error** | The tool itself failed on that mutant. |

`--max-mutants` also reports how many mutants it dropped. A silently truncated run would read as full
coverage. `--no-equivalence-check` disables the equivalence pass; every format states when that
happened, the same way it states a truncation.

## Reports

`--report` accepts any combination of:

- **console** *(default)* — score, per-mutator and per-file breakdowns, then every survivor as a diff
  at a `file:line`.
- **json** — the canonical machine-readable model: every mutant, with which assertion in which test
  killed it. Parse this if you want to build something on top.
- **html** — a self-contained page. Renders the interactive
  [mutation-testing-elements](https://github.com/stryker-mutator/mutation-testing-elements) viewer
  for annotated source, and carries a plain-HTML summary so the page still says something useful when
  opened out of a CI artifact with no network. Also writes the standard-schema JSON alongside.
- **markdown** — sized for `$GITHUB_STEP_SUMMARY` or a PR comment.
- **junit** — one `testcase` per mutant. **Survived becomes a `failure`**, since a survivor is the
  defect worth failing a build over; killed mutants pass, and the non-grading statuses are skipped.

## How it works

```
discover → baseline → coverage index → generate mutants → evaluate (workers) → report
```

**Mutations are located with a parser but applied as byte edits.** Templates are parsed with Go's
`text/template/parse` (using `SkipFuncCheck`, so no sprig or Helm funcmap is needed) and `values.yaml`
with `yaml.v3` node positions. The edit then replaces an exact byte range in the original file, so
formatting — including Helm's whitespace-sensitive `{{-` trim markers — survives untouched.

**Suites are selected by coverage.** helm-unittest suites declare which templates they render, so a
mutant in `ingress.yaml` only runs the suites that render `ingress.yaml`. That is both faster and more
honest: it turns "no suite renders this template at all" into a reportable finding instead of a
survivor.

**Kill attribution comes from driving `TestSuite.RunV3` directly** rather than helm-unittest's
top-level runner, which returns only a bool and prints. The lower-level call returns the full result
tree, which is what lets the tool name the exact assertion that caught each mutation — and distinguish
a failed assertion (a real kill) from a render error (which every test "catches", and so grades
nothing).

### Why workers are separate processes

helm-unittest cannot safely render concurrently within one process. `TestJob.capabilitiesV3` does:

```go
capabilities := v3util.DefaultCapabilities   // a *Capabilities POINTER
capabilities.KubeVersion = ...               // writes through the shared pointer
```

`chartutil.DefaultCapabilities` is a package-level pointer in Helm, so every test job mutates
process-global state. That is a data race, and a correctness bug too: a suite declaring a custom
`capabilities.majorVersion` leaks that version into suites rendering concurrently. The same code is
present in the latest release (v1.1.2), so upgrading is not a fix.

Each worker therefore runs in its own process, where that global is private. Workers are long-lived
and stream jobs, so process startup is paid once per worker rather than once per mutant. A single
worker (`-p 1`) still runs in-process, where no concurrency exists. `-p 1` and `-p 8` are verified to
produce identical results.

## Development

```console
make test    # full suite with -race
make lint
make demo    # score the fixture chart with both suites
```

## Documentation

| page | covers |
|---|---|
| [Quickstart and index](docs/README.md) | installation, a first run, where to go next |
| [CLI reference](docs/cli-reference.md) | every flag, with defaults and exit codes |
| [Mutators](docs/mutators.md) | all nine mutators, with before/after examples |
| [Reports](docs/reports.md) | all five output formats, with samples |
| [Concepts](docs/concepts.md) | the score, the six statuses, the baseline gate |

## Known limits

- Test suites must be discoverable by `-f`; charts nesting suites in subdirectories need an explicit
  glob (`-f 'tests/**/*_test.yaml'`).
- A suite file that fails to parse aborts the run, matching helm-unittest's own behaviour.
- Mutation is one change at a time; no higher-order mutants.
- No incremental mode yet: every run evaluates the full mutant set unless `--max-mutants` is given.
