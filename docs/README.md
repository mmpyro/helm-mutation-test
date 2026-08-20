# helm-mutation-test documentation

`helm unittest` tells you whether your chart tests pass. It cannot tell you whether they are
worth anything. This tool answers the second question: it breaks the chart on purpose, one small
change at a time, re-runs your existing suites against each change, and reports every change no
test objected to.

A change that survives is a **survived mutant** — a precise pointer at a missing assertion.

## Contents

| page | what is in it |
|---|---|
| [concepts.md](concepts.md) | The mutation score, the seven statuses, why `Invalid`, `NoCoverage` and `Equivalent` are excluded, the baseline gate, coverage-aware suite selection, and why workers are subprocesses |
| [cli-reference.md](cli-reference.md) | Every flag, grouped, with defaults, validation rules and exit codes |
| [mutators.md](mutators.md) | All nine mutators, with a real before/after and the weak assertion each one exposes |
| [reports.md](reports.md) | All five report formats, with real sample output and when to use each |

## Why it matters: one chart, two green suites

`testdata/charts/sample` ships two helm-unittest suites over the *same* chart. Both pass.
`helm unittest` reports them as equally green:

```console
$ helm unittest -f 'tests/weak_test.yaml'   testdata/charts/sample   # PASS,  4 tests
$ helm unittest -f 'tests/strong_test.yaml' testdata/charts/sample   # PASS, 43 tests
```

Mutation testing separates them completely. The same 458 mutants, run against each suite:

| suite | score | killed | survived | equivalent | no coverage | invalid |
|---|---:|---:|---:|---:|---:|---:|
| `tests/weak_test.yaml` | **2.3%** | 6 | 255 | 20 | 163 | 14 |
| `tests/strong_test.yaml` | **100.0%** | 417 | 0 | 21 | 0 | 20 |

The weak suite has four tests, asserting only `isKind` and `exists`. It is green, and it will stay
green while the chart loses `imagePullPolicy`, changes `containerPort`, inverts every feature toggle,
or drops its `required` guards. The 2.3% is that fact made into a number.

The strong suite's 100.0% is a perfect score, honestly measured: its 21 remaining mutants are
[equivalent](concepts.md#equivalent-mutants) — mutations that change the chart's source but cannot
change its rendered meaning, so no assertion could ever catch them. 14 are `nindent` indentation
widths and 7 are `values.yaml` keys already holding their type's zero value.

The tool detects equivalent mutants automatically, on by default (`--no-equivalence-check` turns it
off): it re-renders every survivor with and without the mutation and reclassifies the provably
unkillable ones to `Equivalent`, out of the score's denominator. See
[concepts.md](concepts.md#equivalent-mutants) for how, and its
[by-hand recipe](concepts.md#how-to-recognise-one-by-hand) for verifying a verdict yourself.

Reproduce both with `make demo`.

## Five-minute quickstart

### 1. Install

```console
helm plugin install https://github.com/mmpyro/helm-mutation-test
```

On Helm 4, add `--verify=false`: it declines unsigned plugin sources by default.

This downloads a checksum-verified prebuilt binary (linux and macOS, amd64 and arm64), so no Go
toolchain is needed. Anywhere else, or if the download fails, it builds from source and does need
Go.

Or from a checkout of this repository:

```console
make install
```

A checkout always builds from source, so `make install` installs the tree you are editing rather
than the last release.

The chart you point it at needs helm-unittest-style suites; the tool is pinned to
helm-unittest v1.1.2.

### 2. Score a chart

```console
helm mutation-test ./my-chart
```

That runs the chart's own suites unmutated first. If they do not all pass, the run stops with
exit code 2 — a score measured against a red suite is a fiction. See
[the baseline gate](concepts.md#the-baseline-gate-is-a-hard-stop).

### 3. Read the output

```
Mutation testing sample  (1 suite, 4 tests, baseline 3ms)

  Score    2.3%  █░░░░░░░░░░░░░░░░░░░░░░░░░   (6 killed / 255 survived)
  not scored: 163 no-coverage · 20 equivalent · 14 invalid
  8 survivors could not be checked for equivalence and may be unkillable; each one's reason is in its detail

  By mutator                          killed  survived    score
    bool-flip                              0        14    0.0%
    comparison-swap                        0         3    0.0%
    cond-negate                            0        10    0.0%
    default-drop                           0         4    0.0%
    num-literal                            0        47    0.0%
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
`imagePullPolicy` can vanish from the rendered Deployment with all four tests still green.

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

Re-run. That mutant is now `Killed`, and so are the other mutants at the same site.

### 5. Wire it into CI

```console
helm mutation-test ./my-chart \
  --threshold 70 \
  --report markdown,junit,html --report-dir ./mutation
```

Exit code 1 below the threshold, 2 if the tool could not run at all. The markdown file drops
straight into `$GITHUB_STEP_SUMMARY`; the JUnit XML turns each survivor into a `<failure>` that any
CI system displays natively. See [reports.md](reports.md).

### 6. Iterate quickly

The full mutant set is the honest one, but while writing tests a subset is faster:

```console
helm mutation-test ./my-chart --mutators cond-negate,str-literal --max-mutants 50
```

A capped run always says how much it dropped, in every format, so it can never be mistaken for
full coverage.

## What gets mutated

Everything under `templates/` with a `.yaml`, `.yml`, `.tpl` or `.txt` extension — partials such as
`_helpers.tpl` included — plus `values.yaml` (or `values.yml`). `__snapshot__` directories are
skipped.

Test files are never mutated. Mutating the tests would measure nothing about the chart.

Narrow or widen the set with `--include` and `--exclude`; see
[cli-reference.md](cli-reference.md#mutation-control).

## A note on runtime

The fixture chart yields **458 mutants**, and a full strong-suite run over it takes about 1.7
seconds wall-clock with 12 workers. That is the tool scoring one small chart.

The repository's own test suite is much slower, because it scores the fixture several times over and
spawns real worker subprocesses: **`make test` takes about 3m15s under `-race`** (measured: 3m13s,
of which `internal/runner` alone is 192s). That is expected, not a hang.

`make test-short` drops the race detector and is far quicker, but the race detector is how the
helm-unittest global-state bug was found in the first place, and a plain `go test` passed cleanly
while that bug was live. Prefer `make test`.
