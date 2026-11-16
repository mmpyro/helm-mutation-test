# Getting started

`helm unittest` tells you whether your chart tests pass. It cannot tell you whether they are
worth anything. This tool answers the second question. It breaks the chart on purpose, one small
change at a time. After each change, it runs your existing suites again. Then it reports every
change that no test objected to.

A change that survives is a **survived mutant**. It points at one specific missing assertion.

## Contents

| page | what is in it |
|---|---|
| [concepts.md](concepts.md) | The mutation score, the seven statuses, why `Invalid`, `NoCoverage` and `Equivalent` are excluded, the baseline gate, coverage-aware suite selection, and why workers are subprocesses |
| [cli-reference.md](cli-reference.md) | Every flag, grouped, with defaults, validation rules and exit codes |
| [mutators.md](mutators.md) | All ten mutators, with a real before/after and the weak assertion each one exposes |
| [reports.md](reports.md) | All six report formats, with real sample output and when to use each |

## Why it matters: one chart, two green suites

`testdata/charts/sample` ships two helm-unittest suites over the *same* chart. Both pass.
`helm unittest` reports them as equally green:

```console
$ helm unittest -f 'tests/weak_test.yaml'   testdata/charts/sample   # PASS,  4 tests
$ helm unittest -f 'tests/strong_test.yaml' testdata/charts/sample   # PASS, 43 tests
```

Mutation testing separates them completely. Here are the same 472 mutants, run against each suite:

| suite | score | killed | survived | equivalent | no coverage | invalid |
|---|---:|---:|---:|---:|---:|---:|
| `tests/weak_test.yaml` | **2.3%** | 6 | 255 | 24 | 173 | 14 |
| `tests/strong_test.yaml` | **100.0%** | 426 | 0 | 26 | 0 | 20 |

The weak suite has four tests, and they assert only `isKind` and `exists`. The suite is green. It
stays green while the chart loses `imagePullPolicy`, changes `containerPort`, inverts every feature
toggle, or drops its `required` guards. The 2.3% puts that fact into a number.

The strong suite's 100.0% is a perfect score, and an honest one. Its 21 remaining mutants are
[equivalent](concepts.md#equivalent-mutants). These mutations change the chart's source but cannot
change its rendered meaning, so no assertion could ever catch them. 14 are `nindent` indentation
widths, and 7 are `values.yaml` keys that already hold their type's zero value.

The tool detects equivalent mutants automatically. The check is on by default, and
`--no-equivalence-check` turns it off. The tool renders every survivor again, with and without the
mutation. Then it reclassifies the provably unkillable ones to `Equivalent`, which takes them out of
the score's denominator. See [concepts.md](concepts.md#equivalent-mutants) for how this works. Its
[by-hand recipe](concepts.md#how-to-recognise-one-by-hand) shows how to verify a verdict yourself.

Reproduce both with `make demo`.

## Five-minute quickstart

### 1. Install

=== "Helm 4"

    ```console
    # Helm 4 declines unsigned plugin sources by default
    helm plugin install https://github.com/mmpyro/helm-mutation-test --verify=false
    ```

=== "Helm 3"

    ```console
    helm plugin install https://github.com/mmpyro/helm-mutation-test
    ```

=== "From a checkout"

    ```console
    make install
    ```

    A checkout always builds from source, so `make install` installs the tree that you edit, not
    the last release.

`helm plugin install` downloads a checksum-verified prebuilt binary (linux and macOS, amd64 and
arm64), so you do not need a Go toolchain. On any other platform, or if the download fails, it
builds from source and then needs Go.

The latest release is [v1.2.0](https://github.com/mmpyro/helm-mutation-test/releases/tag/v1.2.0).
To pin it, for example in CI, add `--version v1.2.0` to either `helm plugin install` command above.

??? note "Downloading the binaries directly"

    Each archive holds one `helm-mutation-test` executable. The executable also runs on its own,
    outside Helm.

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

The chart that you point it at needs helm-unittest-style suites. The tool is pinned to
helm-unittest v1.1.2.

### 2. Score a chart

```console
helm mutation-test ./my-chart
```

This command first runs the chart's own suites without mutations. If any of them fails, the run
stops with exit code 2, because a score against a red suite is a fiction. See
[the baseline gate](concepts.md#the-baseline-gate-is-a-hard-stop).

### 3. Read the output

```
Mutation testing sample  (1 suite, 4 tests, baseline 3ms)

  Score    2.3%  █░░░░░░░░░░░░░░░░░░░░░░░░░   (6 killed / 255 survived)
  not scored: 173 no-coverage · 24 equivalent · 14 invalid
  8 survivors could not be checked for equivalence and may be unkillable; each one's reason is in its detail

  By mutator                          killed  survived    score
    bool-flip                              0        14    0.0%
    comparison-swap                        0         3    0.0%
    cond-negate                            0        10    0.0%
    default-drop                           0         4    0.0%
    num-literal                            0        47    0.0%
    pipe-drop                              0         0       —  not scored
    required-drop                          0         0       —  not scored
    yaml-key-delete                        4       131    3.0%
    str-literal                            2        46    4.2%

  SURVIVED (255)

  templates/deployment.yaml:34  yaml-key-delete
    -           imagePullPolicy: {{ .Values.image.pullPolicy }}
    + (line removed)
    ran 4 tests in tests/weak_test.yaml — all passed
```

Every survivor is an instruction: *add an assertion that would tell those two lines apart.* Here,
`imagePullPolicy` can disappear from the rendered Deployment, and all four tests stay green.

### 4. Fix one, and watch the number move

Add the missing assertion to your suite:

```yaml
- it: pins the image pull policy
  template: deployment.yaml
  asserts:
    - equal:
        path: spec.template.spec.containers[0].imagePullPolicy
        value: IfNotPresent
```

Run the tool again. That mutant is now `Killed`, and so are the other mutants at the same site.

### 5. Wire it into CI

```console
helm mutation-test ./my-chart \
  --threshold 70 \
  --report markdown,junit,html --report-dir ./mutation
```

The exit code is 1 below the threshold, and 2 if the tool could not run at all. The markdown file
goes straight into `$GITHUB_STEP_SUMMARY`. The JUnit XML turns each survivor into a `<failure>`,
which any CI system shows natively. See [reports.md](reports.md).

### 6. Iterate quickly

The full mutant set gives the honest score. While you write tests, a subset is faster:

```console
helm mutation-test ./my-chart --mutators cond-negate,str-literal --max-mutants 50
```

A capped run always says how many mutants it dropped, in every format. So nobody can mistake it
for full coverage.

## What gets mutated

The tool mutates every file under `templates/` with a `.yaml`, `.yml`, `.tpl` or `.txt` extension,
including partials such as `_helpers.tpl`. It also mutates `values.yaml` (or `values.yml`). It skips
`__snapshot__` directories.

The tool never mutates test files. Mutating the tests would measure nothing about the chart.

Use `--include` and `--exclude` to narrow or widen the set. See
[cli-reference.md](cli-reference.md#mutation-control).

## A note on runtime

The fixture chart yields **472 mutants**. A full strong-suite run over it takes about 2.5 seconds
wall-clock with 12 workers. That is the tool scoring one small chart.

The repository's own test suite is much slower. It scores the fixture several times and spawns real
worker subprocesses. **`make test` takes about 3m15s under `-race`** (measured: 3m13s, of which
`internal/runner` alone is 192s). That is expected, not a hang.

`make test-short` drops the race detector and is much faster. But the race detector found the
helm-unittest global-state bug in the first place. A plain `go test` passed cleanly while that bug
was live. Prefer `make test`.
