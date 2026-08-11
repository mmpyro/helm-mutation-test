# Concepts

How the score is defined, what the six statuses mean, and the design decisions behind them.

- [The mutation score](#the-mutation-score)
- [The six statuses](#the-six-statuses)
- [Why Invalid and NoCoverage are excluded](#why-invalid-and-nocoverage-are-excluded)
- [Equivalent mutants](#equivalent-mutants)
- [Status precedence](#status-precedence)
- [The baseline gate is a hard stop](#the-baseline-gate-is-a-hard-stop)
- [Coverage-aware suite selection](#coverage-aware-suite-selection)
- [Truncation is always reported](#truncation-is-always-reported)
- [Determinism](#determinism)
- [Operational note: why workers are subprocesses](#operational-note-why-workers-are-subprocesses)
- [Pipeline](#pipeline)

## The mutation score

```
score = killed / (killed + survived)
```

Reported as a percentage in `[0, 100]`. When nothing was scored, the score is `0`.

That denominator is the whole point. It counts only the mutants that actually grade your
assertions:

- **Killed** — a test looked at the rendered output and disagreed with it. Your suite caught the
  change.
- **Survived** — the mutation changed the chart and every covering test still passed. A missing
  assertion.

Everything else is counted, named and displayed, but kept out of the fraction. Folding any of it in
would flatter the number.

The score is banded for colour and for the HTML/Stryker `thresholds` field: **≥ 80%** good,
**≥ 50%** middling, below that poor.

## The six statuses

| status | meaning | in the score? |
|---|---|---|
| `Killed` | At least one assertion failed. The tests noticed. | **yes** |
| `Survived` | Every covering test still passed. A missing assertion. | **yes** |
| `NoCoverage` | No test suite exercises the mutated file at all, so the mutation was never run. | no |
| `Invalid` | The mutation stopped the chart rendering. | no |
| `Timeout` | Evaluation exceeded the per-mutant timeout. | no |
| `Error` | The tool itself failed on this mutant (chart copy, load, suite setup). | no |

`Killed` and `Survived` are the only two statuses for which `Status.CountsTowardScore()` returns
true. Every report format lists the non-scoring counts explicitly alongside the score — see
[reports.md](reports.md).

## Why Invalid and NoCoverage are excluded

**`Invalid` is not a kill.** A mutation that breaks template rendering is "caught" by every test in
the suite, no matter what those tests assert. A suite of four `isKind` assertions catches it just as
reliably as a suite of forty `equal` assertions. It therefore says nothing about assertion quality,
and counting it as a kill would inflate the score in proportion to how badly the mutator behaves —
exactly the wrong incentive. So it is excluded and shown separately.

Two real examples from the fixture chart. `yaml-key-delete` removing the whole `image:` block from
`values.yaml`, which leaves the Deployment dereferencing nil:

```
values.yaml:3  yaml-key-delete   →  Invalid
  detail: template: sample/templates/deployment.yaml:33:28: executing
          "sample/templates/deployment.yaml" at <.Values.image.repository>:
          nil pointer evaluating interface {}.repository
```

And `num-literal` collapsing an `nindent` width to zero, which merges an included block into its
parent and produces YAML that no longer parses:

```
templates/deployment.yaml:19  num-literal  (nindent 8 → 0)  →  Invalid
  detail: yaml: line 22: did not find expected key
```

In both cases nothing rendered, so nothing was graded.

A high invalid rate is a bug in this tool, not in your suite. The repository's
`TestInvalidMutantRateIsLow` fails above 20%; the fixture chart sits at 20 invalid out of 458 — 4.4%.

**`NoCoverage` is not a survivor.** If no suite renders a template, its mutants never run. Calling
them "survived" would blame your tests for something they never had the chance to catch; calling them
"killed" would be a lie. So they get their own status — and it is a finding in its own right, reported
per file.

The fixture chart's weak suite illustrates this at scale. It declares only two of the chart's seven
templates, so **163 of its 458 mutants — 36% — never run at all**:

```
  NO COVERAGE (163)
    templates/configmap.yaml — no suite renders this template (56 mutants never run)
    templates/hpa.yaml — no suite renders this template (39 mutants never run)
    templates/ingress.yaml — no suite renders this template (35 mutants never run)
    templates/poddisruptionbudget.yaml — no suite renders this template (20 mutants never run)
    templates/serviceaccount.yaml — no suite renders this template (13 mutants never run)
```

Those five lines are a different and more serious finding than a low score: five entire templates are
untested. Fix them by adding suites, not by adding assertions to the suites you have. Note the two
gaps compound — the weak suite scores 2.1% on the 281 mutants it *did* run, on top of never running
the other 163.

The strong suite has zero no-coverage mutants over the same chart, because it declares all seven
templates across its seven suites.

`Timeout` and `Error` are excluded for the same reason: neither observed whether a test would have
noticed the change.

## Equivalent mutants

An **equivalent mutant** is a mutation that changes the chart's source but *cannot* change its
rendered meaning. No assertion can distinguish the original from the mutant, because there is nothing
to distinguish: the manifest parses to identical data.

This is the same underlying idea as `Invalid` — a mutant that grades nothing — but it arrives from the
opposite direction. An `Invalid` mutant is caught by every test; an equivalent mutant can be caught by
none. Both tell you nothing about your assertions.

**The tool does not detect or exclude them.** Equivalent mutants are reported as `Survived` and counted
in the score's denominator, which drags the score down by an amount nobody can fix. Recognising them
is left to the reader. There is no `--skip-equivalent` flag: `config.SkipEquivalent` exists as a struct
field but is wired to nothing.

### How to recognise one

Render the chart before and after the mutation and compare the **parsed** documents, not the text:

```console
$ helm template t ./chart               > a.yaml     # original
$ helm template t ./mutated-chart       > b.yaml     # mutation applied by hand
$ python3 -c "
import yaml
a=[d for d in yaml.safe_load_all(open('a.yaml')) if d]
b=[d for d in yaml.safe_load_all(open('b.yaml')) if d]
print('parsed-equal:', a==b)"
parsed-equal: True
```

`parsed-equal: True` means equivalent: give up on it, and move to the next survivor. `False` means the
mutant is genuinely killable and the survivor is a real missing assertion.

Render under whatever values the mutated code path actually needs. A mutation inside
`{{- if .Values.ingress.enabled }}` compares equal under default values only because neither side
renders anything — that is a vacuous pass, not equivalence.

### The two patterns that show up in Helm charts

Both are visible in the fixture chart, whose strong suite leaves 21 survivors — and **all 21 are
equivalent**, verified by the comparison above under both default values and with every feature toggle
enabled:

**1. An `indent` / `nindent` width incremented by one.** YAML does not care about a block mapping's
absolute indentation, only that it is deeper than its parent. So `nindent 4 → 5` emits different bytes
and identical data. All 14 of the fixture's `num-literal` survivors are this.

The `→ 0` variant at the same sites is a different matter, and the contrast is worth internalising —
measured across those same 14 sites:

| variant | render comparison | tool status |
|---|---|---|
| `nindent N → N+1` | `parsed-equal: True` (14/14) | `Survived` — equivalent, unkillable |
| `nindent N → 0` | parsed data differs (9/14) | `Killed` |
| `nindent N → 0` | manifest no longer parses (5/14) | `Invalid` |

Collapsing the indentation to zero merges a block into its parent, which either changes data a test
pins or breaks the YAML outright. The tool's status matched the independent render comparison at all
14 sites.

**2. A `values.yaml` key deleted whose value is already its type's zero value.** Removing the key
leaves the value nil, and Go templates treat nil and the zero value identically both for truthiness
and for `| default`. All 7 of the fixture's `yaml-key-delete` survivors are this, and they are
*byte-identical* renders rather than merely parsed-equal:

| survivor | zero value |
|---|---|
| `values.yaml:6` `tag: ""` | empty string |
| `values.yaml:14` `name: ""` | empty string |
| `values.yaml:44` `enabled: false` (autoscaling) | false |
| `values.yaml:49` `targetMemoryUtilizationPercentage: 0` | 0 |
| `values.yaml:66` `enabled: false` (ingress) | false |
| `values.yaml:73` `enabled: false` (ingress TLS) | false |
| `values.yaml:83` `debug: false` | false |

This one generalises past the default values too: a suite that overrides the key supplies its own value
regardless of whether the key exists in `values.yaml`, so the deletion is invisible under every value
set, not just the default one.

### What this means for a score

The fixture chart's strong suite scores **95.2%** — 417 killed, 21 survived. Since all 21 survivors are
unkillable, it killed **417 of 417 killable mutants**. The missing 4.8% is not a gap; it is the floor
this chart imposes on any suite.

So a high score with a handful of survivors deserves a check before it deserves work. Chase a survivor
only after the parsed-render comparison says it is real.

## Status precedence

`internal/runner/classify.go` resolves a mutant's suite outcomes in a fixed order. The ordering is
load-bearing and pinned by `TestClassify`:

1. **Any failed assertion → `Killed`.** A real disagreement beats everything else, including a
   render error elsewhere in the same run.
2. **Otherwise, any render error → `Invalid`.**
3. **Otherwise, any suite-level setup error → `Error`.** That is our failure, not the chart's.
4. **Otherwise, zero tests actually ran → `NoCoverage`.**
5. **Otherwise → `Survived`.**

## The baseline gate is a hard stop

Before any mutant is generated, the chart's own suites are run unmutated. If anything fails, the
run stops:

```console
$ helm mutation-test ./my-chart

Cannot measure mutation score:   weak assertions > renders a Deployment
      failed assert isKind: Template:	sample/templates/deployment.yaml

A score measured against failing tests is meaningless.
Fix the suite first, then re-run.
Error: the chart's tests do not pass before mutation:
  weak assertions > renders a Deployment
      failed assert isKind: Template:	sample/templates/deployment.yaml

$ echo $?
2
```

This is a hard stop rather than a warning, deliberately. Over a red suite, every mutant looks
"killed" by the pre-existing failure, and the resulting number is meaningless. It is better to
refuse than to publish a fiction.

The gate also fires, with the same exit code 2, when:

- no suite file matches `-f` at all, or
- the matching suites contain no test jobs.

With nothing to measure, a score would be equally fictional. The baseline run never fails fast
(unlike mutant runs under the default `--kill-attribution=first`), so the error message names every
broken test at once.

## Coverage-aware suite selection

helm-unittest suites declare which templates they render, via a suite-level `templates:` list or a
per-test `template:`. That declaration is free information, and it is used for two things:

**Speed.** A mutant in `ingress.yaml` runs only the suites that render `ingress.yaml`, not the whole
suite set. Measured on the fixture chart's strong run, which has 7 suites and 43 tests, the number of
tests a mutant actually runs tracks its file:

| mutated file | tests run |
|---|---:|
| `templates/hpa.yaml` | 3 |
| `templates/poddisruptionbudget.yaml` | 3 |
| `templates/serviceaccount.yaml` | 3 |
| `templates/ingress.yaml` | 4 |
| `templates/service.yaml` | 4 |
| `templates/configmap.yaml` | 9 |
| `templates/deployment.yaml` | 17 |
| `templates/_helpers.tpl` | 43 |
| `values.yaml` | 43 |

A `poddisruptionbudget.yaml` mutant costs 3 tests instead of 43 — a 14× saving, repeated across all 20
of that file's mutants.

**Honesty.** If no suite renders a template at all, its mutants become `NoCoverage` instead of
`Survived`.

Two file kinds are treated conservatively as covering everything, because a change to either can
affect any rendered template:

- `values.yaml` / `values.yml` — any template may read any value.
- Any file whose basename starts with `_` (a Helm partial such as `_helpers.tpl`) — any template may
  `include` any define in it.

Being conservative costs runtime but never misattributes a survivor.

A suite that declares no `templates:` and no per-test `template:` renders whatever the chart
produces, so it is indexed as global and no template can be `NoCoverage`.

Declared paths are normalised before matching: `deployment.yaml` and `templates/deployment.yaml`
are the same file, and backslashes are folded to forward slashes.

`internal/coverage` deliberately imports nothing else in the module — it works with opaque suite
IDs, and `runner.CoverageRefs` adapts. Keep it that way; the naive version was an import cycle.

## Truncation is always reported

`--max-mutants` samples deterministically from `--seed` and reports the drop count in **every**
format. A silently truncated run would read as full coverage, which is precisely the failure mode
this tool exists to prevent.

```console
$ ./bin/helm-mutation-test testdata/charts/sample -f 'tests/strong_test.yaml' \
    --no-color --max-mutants 20

Mutation testing sample  (7 suites, 43 tests, baseline 41ms)

  Score   94.7%  █████████████████████████░   (18 killed / 1 survived)
  not scored: 1 invalid
  only 20 of 458 generated mutants were run (--max-mutants); 438 not evaluated
```

Note that 94.7% is *not* comparable to the full run's 95.2%. It is a sample of 20 out of 458 — under
5% of the population — and the warning line is there so nobody quotes it as the score.

The same applies to the markdown format's survivor list, which caps at 40 entries and then says
`_N further survivors omitted; see the JSON or HTML report._`

## Determinism

Two runs over the same chart produce identical mutant lists and identical mutant IDs. Mutants are
ordered by file path, then byte offset, then mutator ID, then variant note. Mutator selection is
sorted by ID regardless of the order they were given on the command line. Suite file globs are
sorted, since glob order is filesystem-dependent.

A mutant ID looks like `deployment.yaml:2827:required-drop` — basename, byte offset, mutator, plus a
variant suffix where one mutator produces several mutants at one site
(`_helpers.tpl:140:num-literal:increment` and `_helpers.tpl:140:num-literal:zero`).

Results do not depend on `--parallel`. `-p 1` and `-p 8` are verified to produce identical output.

## Operational note: why workers are subprocesses

When `--parallel` is greater than 1, each worker runs in its own OS process rather than a goroutine.
This is not a style choice.

helm-unittest's `TestJob.capabilitiesV3` does:

```go
capabilities := v3util.DefaultCapabilities   // a *Capabilities POINTER
capabilities.KubeVersion = ...               // writes through the shared pointer
```

`chartutil.DefaultCapabilities` is a package-level pointer in Helm, so every test job mutates
process-global state. `go test -race` flags it, and it is a genuine correctness bug rather than
untidiness: a suite declaring a custom `capabilities.majorVersion` leaks that version into suites
rendering concurrently. The same code is present in helm-unittest v1.1.2, so upgrading is not a fix.

Each worker therefore gets its own process, where that global is private. Practical consequences:

- **Workers are long-lived and stream jobs**, so process startup is paid once per worker rather than
  once per mutant.
- **The per-mutant timeout is genuinely enforceable.** A hung worker process can be killed; an
  abandoned goroutine cannot.
- **`--parallel 1` runs in-process**, since with one worker there is no concurrency to race. The
  same happens when there are fewer mutants than workers: the worker count is clamped to the number
  of jobs.
- **Each worker gets its own temporary chart copy**, reused across all the mutants it handles. The
  parent writes the mutation into the worker's copy and restores it afterwards. `--keep-workdir`
  leaves those copies behind for inspection.

Two related invariants worth knowing about:

- The tool never calls helm-unittest's `cache.StoreToFileIfNeeded()`. A mutant run must not rewrite
  your chart's committed `__snapshot__` files.
- Suite keys are chart-relative. Each worker re-parses suites from its own temp copy, so an absolute
  path would never match the baseline's key and every mutant would silently report `NoCoverage`.

## Pipeline

```
discover → baseline → coverage index → generate mutants → evaluate (workers) → report
```

**Mutations are located with a parser but applied as byte edits.** Templates are parsed with Go's
`text/template/parse` (with `SkipFuncCheck`, so no sprig or Helm funcmap is needed) and `values.yaml`
with `yaml.v3` node positions. The edit then replaces an exact byte range in the original file, so
formatting survives untouched — including Helm's whitespace-sensitive `{{-` trim markers.

**Kill attribution comes from driving `TestSuite.RunV3` directly**, rather than helm-unittest's
top-level runner which returns only a bool and prints. The lower-level call returns the full result
tree, which is what makes it possible to name the exact assertion that caught each mutation — and to
distinguish a failed assertion (a real kill) from a render error (which every test "catches", and so
grades nothing).

Package responsibilities are listed in the repository's [`CLAUDE.md`](../CLAUDE.md#architecture).
