# helm-mutation-test

<p align="center">
  <img src="docs/images/logo.png" alt="helm-mutation-test logo">
</p>

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

The chart can still regress `replicas`, lose `imagePullPolicy`, or stop rendering its whole
`{{- if .Values.ingress.enabled }}` block. No test turns red.

This plugin breaks the chart on purpose, one small change at a time. After each change, it runs your
existing suites again. A change that no test objects to is a **survived mutant**. It points at one
specific missing assertion.

## The fixture chart, as a demonstration

`testdata/charts/sample` ships two suites over the same templates. Both pass, and `helm unittest`
reports them as equally green:

```console
$ helm unittest -f 'tests/weak_test.yaml'   testdata/charts/sample   # PASS, 4 tests
$ helm unittest -f 'tests/strong_test.yaml' testdata/charts/sample   # PASS, 43 tests
```

Mutation testing separates them:

| suite | killed | survived | equivalent | no-coverage | invalid | score |
|---|---:|---:|---:|---:|---:|---:|
| `weak_test.yaml` | 6 | 255 | 24 | 173 | 14 | **2.3%** |
| `strong_test.yaml` | 426 | 0 | 26 | 0 | 20 | **100.0%** |

Neither score has a mutant in its denominator that the suite had no chance to catch.
`no-coverage` counts mutants in the five templates that the weak suite never renders. `equivalent`
counts mutants that changed the chart's source but provably cannot change any rendered manifest. No
assertion anywhere could catch those either. The score excludes both, and the reports name them
explicitly. See [equivalent mutants](docs/concepts.md#equivalent-mutants) and
[why Invalid and NoCoverage are excluded](docs/concepts.md#why-invalid-and-nocoverage-are-excluded).

Run `make demo` to reproduce both.

## Install

The latest release is [**v1.2.0**](https://github.com/mmpyro/helm-mutation-test/releases/tag/v1.2.0),
which adds the [`pipe-drop`](docs/mutators.md#pipe-drop) mutator.

```console
helm plugin install https://github.com/mmpyro/helm-mutation-test

# Helm 4 refuses unsigned plugin sources unless you say so:
helm plugin install --verify=false https://github.com/mmpyro/helm-mutation-test

# Pin a release, e.g. in CI:
helm plugin install --verify=false https://github.com/mmpyro/helm-mutation-test --version v1.2.0
```

This command downloads a prebuilt binary for linux and macOS on amd64 and arm64. The installer
verifies it against the release's `SHA256SUMS`. You do not need a Go toolchain. The binaries are
static, so the linux asset also works on musl images such as `alpine/helm`.

On any other platform, or if the download fails, the installer builds from source. Then it needs Go
(https://go.dev/dl/).

### Release artifacts

You can also download the same binaries directly. Each archive holds one `helm-mutation-test`
executable. The executable also runs on its own, outside Helm.

| platform | v1.2.0 asset |
|---|---|
| linux, amd64 | [`helm-mutation-test_linux_amd64.tar.gz`](https://github.com/mmpyro/helm-mutation-test/releases/download/v1.2.0/helm-mutation-test_linux_amd64.tar.gz) |
| linux, arm64 | [`helm-mutation-test_linux_arm64.tar.gz`](https://github.com/mmpyro/helm-mutation-test/releases/download/v1.2.0/helm-mutation-test_linux_arm64.tar.gz) |
| macOS, amd64 | [`helm-mutation-test_darwin_amd64.tar.gz`](https://github.com/mmpyro/helm-mutation-test/releases/download/v1.2.0/helm-mutation-test_darwin_amd64.tar.gz) |
| macOS, arm64 | [`helm-mutation-test_darwin_arm64.tar.gz`](https://github.com/mmpyro/helm-mutation-test/releases/download/v1.2.0/helm-mutation-test_darwin_arm64.tar.gz) |
| checksums | [`SHA256SUMS`](https://github.com/mmpyro/helm-mutation-test/releases/download/v1.2.0/SHA256SUMS) |

```console
curl -fsSLO https://github.com/mmpyro/helm-mutation-test/releases/download/v1.2.0/helm-mutation-test_linux_amd64.tar.gz
curl -fsSLO https://github.com/mmpyro/helm-mutation-test/releases/download/v1.2.0/SHA256SUMS
sha256sum --check --ignore-missing SHA256SUMS    # macOS: shasum -a 256 --check --ignore-missing SHA256SUMS
tar -xzf helm-mutation-test_linux_amd64.tar.gz helm-mutation-test
```

Older versions are on the [releases page](https://github.com/mmpyro/helm-mutation-test/releases).

Either way, the chart needs `helm-unittest`-style test suites. The tool is tested against
helm-unittest v1.0.3.

From a checkout:

```console
make install
```

A checkout always builds from source and does not download, so `make install` runs the tree that
you edit. `HELM_MUTATION_TEST_BUILD=1` forces the source build anywhere.

## Usage

```console
helm mutation-test ./my-chart
```

```console
# Fail CI below 70%, and write reports the job can publish
helm mutation-test ./my-chart --threshold 70 \
  --report console,markdown,html,junit,sarif --report-dir ./mutation

# Per-PR: score only what the branch changed (needs fetch-depth: 0 in CI)
helm mutation-test ./my-chart --since origin/main --threshold 70

# A fast subset while iterating on tests
helm mutation-test ./my-chart --mutators cond-negate,str-literal --max-mutants 50

# Split a big chart across CI jobs, then grade the merged result
helm mutation-test ./my-chart --shard-index 2 --shard-count 4 --report json --report-dir shards
helm mutation-test merge shards/mutation-report.shard-*.json --threshold 70
```

### Reading the output

```
Mutation testing sample  (1 suite, 4 tests, baseline 3ms)

  Score    2.3%  █░░░░░░░░░░░░░░░░░░░░░░░░░   (6 killed / 255 survived)
  not scored: 173 no-coverage · 24 equivalent · 14 invalid
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
| `2` | could not run, most often because the chart's own tests do not pass |

The last case is deliberate. A mutation score against a failing suite has no meaning. So a red
baseline stops the run. It is not a warning.

## Mutators

| ID | Mutation |
|---|---|
| `cond-negate` | Negate `if` / `else if` / `with` conditions: `COND` → `not (COND)` |
| `bool-flip` | `true` ↔ `false` |
| `num-literal` | Perturb numbers: `n` → `n+1`, and `n` → `0` |
| `pipe-drop` | Remove a type- or format-changing function: `X \| quote` → `X`, `toYaml X` → `X`, `X \| trunc 63` → `X` |
| `range-empty` | Force a `range` loop to iterate zero times: `range X` → `range list` |
| `str-literal` | Replace a string with a sentinel |
| `yaml-key-delete` | Delete a key and its nested block |
| `default-drop` | Remove a `\| default X` pipeline segment |
| `comparison-swap` | `eq`↔`ne`, `lt`↔`ge`, `gt`↔`le`, `and`↔`or` |
| `required-drop` | Strip a `required` guard down to its value |

The tool mutates templates (including `_helpers.tpl` partials) and `values.yaml`. It never mutates
test files, because mutating the tests would measure nothing about the chart.

## What the score does and does not count

```
score = killed / (killed + survived)
```

Only those two outcomes grade your assertions. The tool reports everything else separately, because
it would flatter the number if the score included it:

| status | why it is excluded |
|---|---|
| **No coverage** | No suite renders the mutated template, so no test had a chance to catch it. This is a finding on its own. |
| **Invalid** | The mutation stopped the chart from rendering, so *every* test "caught" it, whatever the test asserts. That grades nothing. A high invalid rate is a bug in this tool, not in your suite. |
| **Equivalent** | The mutation provably cannot change any rendered manifest. The tool checks every survivor for this automatically, and the check is on by default. No assertion could catch such a mutant either, so the score excludes it the same way. |
| **Timeout** | The evaluation took longer than the per-mutant timeout. |
| **Error** | The tool itself failed on that mutant. |

`--max-mutants` also reports how many mutants it dropped, because a silently truncated run would read
as full coverage. `--since` does the same for the mutants outside the diff. Some changes, such as
`values.yaml` or a partial, cannot be attributed to particular templates. For those, `--since` falls
back to a full run and says why.

For the same reason, a `--shard-count` shard labels its score a slice
in every format and refuses `--threshold`. Only the `merge` of every shard gets a grade.
`--no-equivalence-check` disables the equivalence pass. Every format states when that happened, the
same way it states a truncation.

## Reports

`--report` accepts any combination of:

- **console** *(default)*: the score, per-mutator and per-file breakdowns, then every survivor as a
  diff at a `file:line`.
- **json**: the canonical machine-readable model. It holds every mutant, with the assertion and the
  test that killed it. Parse this format if you want to build something on top.
- **html**: a self-contained page. It renders the interactive
  [mutation-testing-elements](https://github.com/stryker-mutator/mutation-testing-elements) viewer
  for annotated source. It also carries a plain-HTML summary, so the page is still useful when you
  open it from a CI artifact with no network. This format also writes the standard-schema JSON next
  to the page.
- **markdown**: sized for `$GITHUB_STEP_SUMMARY` or a PR comment.
- **junit**: one `testcase` per mutant. **Survived becomes a `failure`**, because a survivor is the
  defect worth failing a build over. Killed mutants pass, and the non-grading statuses are skipped.
- **sarif**: SARIF 2.1.0 for GitHub code scanning and editor Problems panels. Each survivor gets an
  annotation on the line it mutated. Each untested template is one alert. The run's notifications
  name everything that the results leave out.

## How it works

```
discover → baseline → coverage index → generate mutants → evaluate (workers) → report
```

**A parser locates each mutation, and a byte edit applies it.** The tool parses templates with Go's
`text/template/parse`. It uses `SkipFuncCheck`, so it needs no sprig or Helm funcmap. It reads
`values.yaml` with `yaml.v3` node positions. The edit then replaces an exact byte range in the
original file. So the formatting survives untouched, including Helm's whitespace-sensitive `{{-` trim
markers.

**Coverage selects the suites.** helm-unittest suites declare which templates they render. So a
mutant in `ingress.yaml` runs only the suites that render `ingress.yaml`. This is faster, and it is
also more honest. It turns "no suite renders this template at all" into a finding that the tool can
report, instead of a survivor.

**Kill attribution comes from a direct call to `TestSuite.RunV3`.** The tool does not use
helm-unittest's top-level runner, because that runner only prints and returns a bool. The lower-level
call returns the full result tree. With that tree, the tool can name the exact assertion that caught
each mutation. It can also tell a failed assertion (a real kill) from a render error. Every test
"catches" a render error, so a render error grades nothing.

### Why workers are separate processes

helm-unittest cannot render safely in parallel inside one process. `TestJob.capabilitiesV3` does
this:

```go
capabilities := v3util.DefaultCapabilities   // a *Capabilities POINTER
capabilities.KubeVersion = ...               // writes through the shared pointer
```

`chartutil.DefaultCapabilities` is a package-level pointer in Helm, so every test job mutates
process-global state. That is a data race. It is also a correctness bug: a suite that declares a
custom `capabilities.majorVersion` leaks that version into suites that render at the same time. The
latest release (v1.1.2) has the same code, so an upgrade does not fix it.

So each worker runs in its own process, where that global is private. Workers are long-lived and
stream jobs. Each worker pays the process startup cost once, not once per mutant. A single worker
(`-p 1`) still runs in-process, where no concurrency exists. `-p 1` and `-p 8` are verified to
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
| [Quickstart and index](docs/getting-started.md) | installation, a first run, where to go next |
| [CLI reference](docs/cli-reference.md) | every flag, with defaults and exit codes |
| [Mutators](docs/mutators.md) | all ten mutators, with before/after examples |
| [Reports](docs/reports.md) | all six output formats, with samples |
| [Concepts](docs/concepts.md) | the score, the six statuses, the baseline gate |

## Known limits

- Test suites must be discoverable by `-f`. Charts that nest suites in subdirectories need an
  explicit glob (`-f 'tests/**/*_test.yaml'`).
- A suite file that fails to parse aborts the run, matching helm-unittest's own behaviour.
- Mutation is one change at a time. There are no higher-order mutants.
- `--since` treats any change that it cannot attribute (values, partials, `Chart.yaml`, subcharts,
  files read via `.Files.Get`) as a reason for a full run. So PRs that touch shared files get no
  speedup.
