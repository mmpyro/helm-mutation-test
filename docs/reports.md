# Report formats

Five formats, selected with `--report` in any combination. Every one is a pure function of the run
model, so they always agree with each other.

```console
helm mutation-test ./my-chart --report console,json,markdown,junit,html --report-dir ./mutation
```

| format | destination | filename | use it for |
|---|---|---|---|
| [`console`](#console) *(default)* | stdout | — | Reading the result yourself, right now |
| [`json`](#json) | file | `mutation-report.json` | Building something on top; the canonical model |
| [`markdown`](#markdown) | file | `mutation-report.md` | `$GITHUB_STEP_SUMMARY`, PR comments |
| [`junit`](#junit) | file | `mutation-report.junit.xml` | CI systems that already display test results |
| [`html`](#html) | file | `mutation-report.html` **+** `mutation-report.stryker.json` | A publishable artifact with annotated source |

`--report-dir` (default `.helm-mutation-test`) is created only when a non-console format is requested.

**Every format names what it excluded.** The score, the non-scoring counts, and any `--max-mutants`
truncation appear in all five. That is a deliberate invariant: a report that showed only the score
could let a truncated or partially-covered run read as full coverage. See
[concepts.md](concepts.md#truncation-is-always-reported).

All samples below are real output from `testdata/charts/sample` (458 mutants), and are marked where
truncated.

---

## `console`

The human-facing summary, and the default. Ordered deliberately: the score first, then where the gaps
are, then each individual survivor as a diff at a `file:line` you can go and fix.

Sections, in order: header, score, `By mutator`, `By file`, `SURVIVED`, `NO COVERAGE`, `INVALID`,
`PARTIALLY ANALYSED`, footer. Empty sections are omitted entirely.

**Use it for** interactive work. It is the only format that gets the survivor list *and* both
breakdown tables *and* the coverage note on every survivor.

### Sample — the weak suite (truncated)

```console
$ ./bin/helm-mutation-test testdata/charts/sample -f 'tests/weak_test.yaml' --no-color

Mutation testing sample  (1 suite, 4 tests, baseline 3ms)

  Score    2.1%  █░░░░░░░░░░░░░░░░░░░░░░░░░   (6 killed / 275 survived)
  not scored: 163 no-coverage · 14 invalid

  By mutator                          killed  survived    score
    bool-flip                              0        14    0.0%
    comparison-swap                        0         3    0.0%
    cond-negate                            0        10    0.0%
    default-drop                           0         4    0.0%
    num-literal                            0        55    0.0%
    required-drop                          0         2    0.0%
    yaml-key-delete                        4       135    2.9%
    str-literal                            2        52    3.7%

  By file                             killed  survived    score
    templates/_helpers.tpl                 0        14    0.0%
    templates/configmap.yaml               0         0       —  not scored
    templates/hpa.yaml                     0         0       —  not scored
    templates/ingress.yaml                 0         0       —  not scored
    …plates/poddisruptionbudget.yaml       0         0       —  not scored
    templates/serviceaccount.yaml          0         0       —  not scored
    values.yaml                            0       119    0.0%
    templates/deployment.yaml              2       111    1.8%
    templates/service.yaml                 4        31   11.4%

  SURVIVED (275)

  templates/deployment.yaml:9  yaml-key-delete
    -   replicas: {{ .Values.replicaCount }}
    + (line removed)
    ran 4 tests in tests/weak_test.yaml — all passed

  templates/deployment.yaml:34  yaml-key-delete
    -           imagePullPolicy: {{ .Values.image.pullPolicy }}
    + (line removed)
    ran 4 tests in tests/weak_test.yaml — all passed

  templates/deployment.yaml:74  required-drop
    -               value: {{ required "apiToken is required" .Values.apiToken | quote }}
    +               value: {{ .Values.apiToken | quote }}
    ran 4 tests in tests/weak_test.yaml — all passed

  ... 272 further survivors, one block each ...

  NO COVERAGE (163)
    templates/configmap.yaml — no suite renders this template (56 mutants never run)
    templates/hpa.yaml — no suite renders this template (39 mutants never run)
    templates/ingress.yaml — no suite renders this template (35 mutants never run)
    templates/poddisruptionbudget.yaml — no suite renders this template (20 mutants never run)
    templates/serviceaccount.yaml — no suite renders this template (13 mutants never run)

  INVALID (14)
    these mutations stopped the chart rendering, so every test caught them
    they measure nothing about test quality and are excluded from the score

  458 mutants in 298ms
```

*(Three of the 275 survivor blocks shown, non-contiguous; the real output prints all of them.)*

### How to read it

- **The bar and the score are coloured by band**: green at ≥ 80%, yellow at ≥ 50%, red below.
- **`not scored:`** lists every excluded status with its count. It is only printed when something was
  excluded.
- **Breakdowns are sorted by ascending score**, so the most under-tested mutator and file are at the
  top. A row with nothing scored shows `—  not scored` rather than a meaningless `0.0%`, which is what
  the five uncovered templates show above.
- **Long file names are truncated from the left** with a leading `…`, as
  `…plates/poddisruptionbudget.yaml` shows — the tail is the useful part.
- **Survivors are grouped by file** in sorted order, so you fix one file at a time.
- **A deletion has no "after" text**, so it renders as `(line removed)` or `(N lines removed)` — only
  the first deleted line is shown.
- **The note under each diff says what actually ran**: `ran 4 tests in tests/weak_test.yaml — all
  passed`, or `ran N tests across M suite files — all passed` when several suite files covered it.
  That distinguishes a genuine survivor from an unexercised one.
- **`NO COVERAGE` is counted per file**, because "no suite renders this template" is a per-file
  finding; listing all 163 mutants would bury it.
- **`PARTIALLY ANALYSED`** appears when a file could not be fully analysed — most often a template
  that Go's parser rejected, in which case the line-based mutators still ran but the AST ones did not.

### The same chart, the strong suite

```console
$ ./bin/helm-mutation-test testdata/charts/sample -f 'tests/strong_test.yaml' --no-color

Mutation testing sample  (7 suites, 43 tests, baseline 40ms)

  Score   95.2%  █████████████████████████░   (417 killed / 21 survived)
  not scored: 20 invalid

  By mutator                          killed  survived    score
    num-literal                           68        14   82.9%
    yaml-key-delete                      209         7   96.8%
    bool-flip                             15         0  100.0%
    comparison-swap                       13         0  100.0%
    cond-negate                           19         0  100.0%
    default-drop                           7         0  100.0%
    required-drop                          3         0  100.0%
    str-literal                           83         0  100.0%

  By file                             killed  survived    score
    …plates/poddisruptionbudget.yaml      18         2   90.0%
    templates/serviceaccount.yaml         12         1   92.3%
    values.yaml                          107         7   93.9%
    templates/ingress.yaml                32         2   94.1%
    templates/service.yaml                33         2   94.3%
    templates/deployment.yaml            108         5   95.6%
    templates/hpa.yaml                    38         1   97.4%
    templates/configmap.yaml              55         1   98.2%
    templates/_helpers.tpl                14         0  100.0%

  SURVIVED (21)

  templates/configmap.yaml:6  num-literal
    -     {{- include "sample.labels" . | nindent 4 }}
    +     {{- include "sample.labels" . | nindent 5 }}
    ran 9 tests in tests/strong_test.yaml — all passed

  ... 19 further survivors ...

  values.yaml:83  yaml-key-delete
    -   debug: false
    + (line removed)
    ran 43 tests in tests/strong_test.yaml — all passed

  INVALID (20)
    these mutations stopped the chart rendering, so every test caught them
    they measure nothing about test quality and are excluded from the score

  458 mutants in 1.7s
```

Note the coverage notes differ: 9 tests for a `configmap.yaml` mutant, 43 for a `values.yaml` one.
That is [coverage-aware selection](concepts.md#coverage-aware-suite-selection) at work.

**Read that `SURVIVED (21)` list with care.** All 21 of these are
[equivalent mutants](concepts.md#equivalent-mutants) — mutations that cannot change the rendered
manifest's meaning, so no assertion could catch them. The two shown above are both examples: `nindent
4 → 5` reindents a block without changing the parsed data, and deleting `debug: false` leaves the value
nil, which Go templates treat exactly like `false`.

No report format marks these; the tool does not detect them. A reader who works through the list
looking for missing assertions will find none, so check a survivor with the
[parsed-render comparison](concepts.md#how-to-recognise-one) before spending time on it.

With a `--threshold`, the footer adds an explicit verdict line:

```
  458 mutants in 1.6s
  score 95.2% meets the 70.0% threshold
```

Colour is auto-detected from whether stdout is a terminal; force it with `--color`, suppress it with
`--no-color`.

---

## `json`

The canonical machine-readable report: the full model, every mutant, with kill attribution. The other
file formats are summaries — this is the one to parse if you want to build something on top.

**Use it for** custom dashboards, score trends over time, scripting ("which survivors are new since
`main`?"), or extracting the exact assertion that killed a given mutation.

The wire shape is `"schema": "helm-mutation-test/v1"` and is deliberately a separate type from the
internal model, so refactors do not silently change the published format.

### Top level (real, from the strong-suite run)

```json
{
  "schema": "helm-mutation-test/v1",
  "chartName": "sample",
  "chartPath": "testdata/charts/sample",
  "score": 95.21,
  "threshold": 0,
  "passed": true,
  "tally": {
    "killed": 417,
    "survived": 21,
    "noCoverage": 0,
    "invalid": 20,
    "timeout": 0,
    "error": 0
  },
  "generated": 458,
  "capped": 0,
  "mutators": [
    "bool-flip", "comparison-swap", "cond-negate", "default-drop",
    "num-literal", "required-drop", "str-literal", "yaml-key-delete"
  ],
  "suites": [
    {
      "name": "strong assertions on the deployment",
      "file": "tests/strong_test.yaml",
      "tests": ["pins every deployment field", "... 16 more ..."]
    },
    { "name": "strong assertions on the service",               "file": "tests/strong_test.yaml", "tests": ["... 4 ..."] },
    { "name": "strong assertions on the config map",            "file": "tests/strong_test.yaml", "tests": ["... 9 ..."] },
    { "name": "strong assertions on the service account",       "file": "tests/strong_test.yaml", "tests": ["... 3 ..."] },
    { "name": "strong assertions on the ingress",               "file": "tests/strong_test.yaml", "tests": ["... 4 ..."] },
    { "name": "strong assertions on the autoscaler",            "file": "tests/strong_test.yaml", "tests": ["... 3 ..."] },
    { "name": "strong assertions on the pod disruption budget", "file": "tests/strong_test.yaml", "tests": ["... 3 ..."] }
  ],
  "testCount": 43,
  "byMutator": [ { "name": "num-literal", "score": 82.93, "tally": { "...": 0 } } ],
  "byFile":    [ { "name": "templates/poddisruptionbudget.yaml", "score": 90, "tally": { "...": 0 } } ],
  "mutants":   [ "..." ],
  "timing": { "baselineMillis": 40, "totalMillis": 1669 }
}
```

*(Suite test lists, `byMutator`, `byFile` and `mutants` truncated above; the real file is complete.
Note seven suites all live in one suite *file* — `suites` is one entry per `suite:` document.)*

Field notes:

- `score` is rounded to two places. `passed` reflects `--threshold`; with no threshold it is `true`.
- `generated` is the count **before** any `--max-mutants` cap; `capped` is how many were dropped.
  `generated - capped == len(mutants)`.
- `byMutator` and `byFile` are sorted ascending by score.
- `skippedFiles` is present only when a file could not be fully analysed.
- `timing` is in milliseconds; each mutant additionally carries `durationNanos`.

### A killed mutant, verbatim

```json
{
  "id": "deployment.yaml:735:default-drop",
  "mutator": "default-drop",
  "file": "templates/deployment.yaml",
  "line": 25,
  "column": 78,
  "startByte": 735,
  "endByte": 748,
  "originalLine": "      terminationGracePeriodSeconds: {{ .Values.terminationGracePeriodSeconds | default 30 }}",
  "mutatedLine": "      terminationGracePeriodSeconds: {{ .Values.terminationGracePeriodSeconds }}",
  "original": " | default 30",
  "mutated": "",
  "status": "Killed",
  "killedBy": [
    {
      "suite": "strong assertions on the deployment",
      "suiteFile": "tests/strong_test.yaml",
      "test": "pins every deployment field",
      "assertType": "equal",
      "assertIndex": 17,
      "failInfo": "Template:\tsample/templates/deployment.yaml\nDocumentIndex:\t0\nValuesIndex:\t0\nPath:\tspec.template.spec.terminationGracePeriodSeconds\nExpected to equal:\n\t30\n..."
    }
  ],
  "coveringSuites": ["tests/strong_test.yaml"],
  "testsRun": 1,
  "durationNanos": 18245166
}
```

`failInfo` names the exact JSONPath the assertion was checking, which is usually enough to see why the
mutation was caught. `killedBy` holds a single entry under the default `--kill-attribution=first`, and
every killing assertion under `--kill-attribution=all`.

### An invalid mutant, verbatim

```json
{
  "id": "values.yaml:17:yaml-key-delete",
  "mutator": "yaml-key-delete",
  "file": "values.yaml",
  "line": 3,
  "column": 1,
  "startByte": 17,
  "endByte": 162,
  "originalLine": "image:",
  "mutatedLine": "",
  "original": "image:\n  repository: nginx\n  # tag is intentionally empty ...\n  tag: \"\"\n  pullPolicy: IfNotPresent\n",
  "mutated": "",
  "status": "Invalid",
  "coveringSuites": ["tests/strong_test.yaml"],
  "testsRun": 5,
  "detail": "template: sample/templates/deployment.yaml:33:28: executing \"sample/templates/deployment.yaml\" at <.Values.image.repository>: nil pointer evaluating interface {}.repository"
}
```

`detail` carries the render error for `Invalid`, and the tool error for `Error`. A `num-literal`
invalid looks different — `"detail": "yaml: line 22: did not find expected key"` — because collapsing
an `nindent` width breaks the YAML rather than the template.

### Useful queries

```console
# Every survivor as file:line + mutator
jq -r '.mutants[] | select(.status=="Survived") | "\(.file):\(.line)\t\(.mutator)"' mutation-report.json

# Fail a script if the score dropped
jq -e '.score >= 70' mutation-report.json

# Which tests are doing the killing?
jq -r '.mutants[].killedBy[]?.test' mutation-report.json | sort | uniq -c | sort -rn

# Which assertion types are doing the killing? (how the mutators.md table was built)
jq -r '.mutants[].killedBy[]?.assertType' mutation-report.json | sort | uniq -c | sort -rn

# Templates no suite renders, with mutant counts
jq -r '.mutants[] | select(.status=="NoCoverage") | .file' mutation-report.json | sort | uniq -c

# Confirm nothing was silently dropped
jq '{generated, capped, evaluated: (.mutants|length)}' mutation-report.json
```

---

## `markdown`

A summary sized for a CI job summary or a PR comment.

**Use it for** `$GITHUB_STEP_SUMMARY`, a bot comment, or anywhere a reviewer will read the result
without opening an artifact.

```yaml
- name: Mutation test
  run: |
    helm mutation-test ./chart --threshold 70 \
      --report markdown --report-dir ./mutation
    cat ./mutation/mutation-report.md >> "$GITHUB_STEP_SUMMARY"
```

The survivor list is wrapped in `<details>` so it does not dominate the page, and is **capped at 40
entries** with an explicit note when it truncates — GitHub's step summary has a size limit, and a
silent cut would misrepresent the run. The strong run's 21 survivors fit; the weak run's 275 do not.

### Sample — the strong suite (truncated)

````markdown
## Mutation score: 95.2%

`sample` — 7 suites, 43 tests

| Outcome | Count | |
|---|---:|---|
| Killed | 417 | a test caught the mutation |
| Survived | 21 | **no test noticed** — a missing assertion |
| Invalid | 20 | broke rendering, so it grades nothing |

Score counts only killed and survived mutants: 438 of 458.

### Score by mutator

| Name | Killed | Survived | Score |
|---|---:|---:|---:|
| `num-literal` | 68 | 14 | 82.9% |
| `yaml-key-delete` | 209 | 7 | 96.8% |
| `bool-flip` | 15 | 0 | 100.0% |
| `comparison-swap` | 13 | 0 | 100.0% |
| `cond-negate` | 19 | 0 | 100.0% |
| `default-drop` | 7 | 0 | 100.0% |
| `required-drop` | 3 | 0 | 100.0% |
| `str-literal` | 83 | 0 | 100.0% |

### Score by file

| Name | Killed | Survived | Score |
|---|---:|---:|---:|
| `templates/poddisruptionbudget.yaml` | 18 | 2 | 90.0% |
| `templates/serviceaccount.yaml` | 12 | 1 | 92.3% |
| `values.yaml` | 107 | 7 | 93.9% |
| `templates/ingress.yaml` | 32 | 2 | 94.1% |
| `templates/service.yaml` | 33 | 2 | 94.3% |
| `templates/deployment.yaml` | 108 | 5 | 95.6% |
| `templates/hpa.yaml` | 38 | 1 | 97.4% |
| `templates/configmap.yaml` | 55 | 1 | 98.2% |
| `templates/_helpers.tpl` | 14 | 0 | 100.0% |

### Survived mutants (21)

Each of these changed the chart without any test noticing.

<details>
<summary>Show survivors</summary>

**`templates/configmap.yaml:6`** · `num-literal`

```diff
-     {{- include "sample.labels" . | nindent 4 }}
+     {{- include "sample.labels" . | nindent 5 }}
```

**`templates/deployment.yaml:6`** · `num-literal`

```diff
-     {{- include "sample.labels" . | nindent 4 }}
+     {{- include "sample.labels" . | nindent 5 }}
```

... 18 further survivor blocks ...

**`values.yaml:83`** · `yaml-key-delete`

```diff
-   debug: false
+ (line removed)
```

</details>

<sub>458 mutants in 1.7s · baseline 40ms</sub>
````

Differences from `console`:

- The outcome table hides zero-count non-scoring rows to stay short, but **always** shows `Killed`
  and `Survived`, and always states `Score counts only killed and survived mutants: N of M`.
- With a `--threshold` set, a `✅ Meets the 70.0% threshold.` or
  `❌ **Below the 70.0% threshold.**` line appears right under the header.
- A capped run adds `> ⚠️ --max-mutants ran N of M generated mutants; K were not evaluated.`
- With no survivors: `### Survived mutants` / `None — every covered mutation was caught.`
- Templates no suite renders get their own `### Templates no suite renders` section, which for the
  weak suite lists all five files with their mutant counts.
- There is no per-survivor coverage note; use `console` or `json` for that.

One caveat that applies to this format and to every other: the line *"Each of these changed the chart
without any test noticing"* is generic, and in the sample above it overstates the case. All 21 of that
run's survivors are [equivalent mutants](concepts.md#equivalent-mutants), which no test *could* notice.
No format distinguishes them, because the tool does not detect them — see
[concepts.md](concepts.md#equivalent-mutants).

---

## `junit`

One `<testcase>` per mutant, so any CI system displays mutation results natively without needing to
understand mutation testing.

**The mapping inverts the usual sense on purpose:**

| status | JUnit |
|---|---|
| `Survived` | `<failure type="SurvivedMutant">` — the survivor is the actionable defect, so it is what should turn the job red |
| `Killed` | a passing testcase — the tests did their job |
| `NoCoverage`, `Invalid`, `Timeout`, `Error` | `<skipped>` with a reason — none of them says anything about assertion quality, and none should fail a build |

One `<testsuite>` per chart file, in first-appearance order. Each testcase name is
`MUTATOR at FILE:LINE [ID]`, `classname` is the file, and `time` is that mutant's own duration.

**Use it for** making a weak suite visible in the CI UI you already have, or gating a build without
`--threshold` (fail on any survivor).

### Sample — the strong suite (truncated)

```xml
<?xml version="1.0" encoding="UTF-8"?>
<testsuites name="helm-mutation-test:sample" tests="458" failures="21" skipped="20" time="1.6698">
  <testsuite name="templates/_helpers.tpl" tests="14" failures="0" skipped="0" hostname="localhost">
    <testcase name="default-drop at templates/_helpers.tpl:2 [_helpers.tpl:66:default-drop]"
              classname="templates/_helpers.tpl" time="0.0495"></testcase>
    <testcase name="num-literal at templates/_helpers.tpl:3 [_helpers.tpl:140:num-literal:increment]"
              classname="templates/_helpers.tpl" time="0.1133"></testcase>
    ... 12 more testcases, all passing ...
  </testsuite>
  <testsuite name="templates/configmap.yaml" tests="56" failures="1" skipped="0" hostname="localhost">
    ... 5 passing testcases for lines 1-4 ...
    <testcase name="num-literal at templates/configmap.yaml:6 [configmap.yaml:144:num-literal:increment]"
              classname="templates/configmap.yaml" time="0.0201">
      <failure message="no test noticed this change to templates/configmap.yaml:6" type="SurvivedMutant">mutator: num-literal
location: templates/configmap.yaml:6:45

-     {{- include "sample.labels" . | nindent 4 }}
+     {{- include "sample.labels" . | nindent 5 }}

ran 9 tests in tests/strong_test.yaml — all passed
suites run: tests/strong_test.yaml

Add an assertion that distinguishes the original from the mutation.
</failure>
    </testcase>
    ... 50 more testcases ...
  </testsuite>
  ... 7 more testsuites ...
</testsuites>
```

*(Newlines inside `<failure>` are XML-escaped as `&#xA;` in the real file; expanded above for
readability, and long attributes wrapped. `_helpers.tpl` has 14 testcases and zero failures, since the
strong suite kills every mutant in it.)*

Each failure body carries the mutator, the exact `file:line:column`, the diff, what ran, and the
one-line instruction `Add an assertion that distinguishes the original from the mutation.`

Skipped reasons are specific, so a skip is never mysterious. These are real, from the weak-suite run:

```xml
<skipped message="no suite renders templates/configmap.yaml, so this mutation was never run"/>
<skipped message="no suite renders templates/hpa.yaml, so this mutation was never run"/>
<skipped message="the mutation stopped the chart rendering, so it grades nothing: execution error at (sample/templates/deployment.yaml:74:24): apiToken is required"/>
```

`Timeout` and `Error` use the same shape (`evaluation timed out: …`, `the tool failed on this
mutant: …`); the fixture chart produces neither.

A truncated run gains one extra `<testsuite>`, so the cap is visible here as it is in every other
format:

```xml
<testsuite name="--max-mutants" tests="1" failures="0" skipped="1" hostname="localhost">
  <testcase name="truncated run" classname="--max-mutants" time="0.0000">
    <skipped message="438 of 458 generated mutants were not evaluated (--max-mutants); this run is a sample, not full coverage"/>
  </testcase>
</testsuite>
```

It is prepended to the real testsuites and counted in the top-level `tests` and `skipped`
attributes, which therefore stay equal to the sum over child suites.

```yaml
- name: Publish mutation results
  if: always()
  uses: mikepenz/action-junit-report@v4
  with:
    report_paths: ./mutation/mutation-report.junit.xml
```

---

## `html`

A self-contained page, plus the standard-schema JSON alongside it. `--report html` writes **two**
files:

- `mutation-report.html` — 290 KB for the fixture chart
- `mutation-report.stryker.json` — 278 KB, most of it embedded chart source

**Use it for** a CI artifact somebody will actually open, and for annotated source: seeing each
mutation highlighted in place in the template it came from.

### The page

Four sections: `Mutation report — CHART`, `Outcomes`, `Survived mutants`, `Annotated source`.

The big score at the top is banded like the console output (`good` / `okay` / `poor` classes at
80/50 — the strong run's 95.2% renders as `good`), and the page has a dark-mode stylesheet via
`prefers-color-scheme`.

`Annotated source` hosts the published
[mutation-testing-elements](https://github.com/stryker-mutator/mutation-testing-elements) web
component, loaded from jsDelivr, fed the report from an inline `<script type="application/json">`.

**The plain-HTML summary above it is the point.** An artifact opened out of a CI run is very often
opened with no network access or with scripts blocked, which is exactly when a CDN-loaded viewer shows
nothing. The `Outcomes` table and the full `Survived mutants` list are static HTML and complete on
their own; the page says so in the fallback text.

The survivor list here is **not** capped — unlike markdown, every survivor is listed, each with its
diff and its coverage note. For the weak suite that means all 275.

The embedded JSON is HTML-escaped, so a `</script>` appearing in chart source cannot terminate the
element early.

### `mutation-report.stryker.json`

Conforms to the mutation-testing-elements report schema v2. It embeds each file's full source so the
viewer can highlight mutations in place, which is why it is large relative to the mutant count.

Written whenever `html` is requested, because it is useful on its own: it is what other
mutation-testing tooling consumes, and what you would hand to a hosted dashboard.

```json
{
  "$schema": "https://raw.githubusercontent.com/stryker-mutator/mutation-testing-elements/master/packages/report-schema/src/mutation-testing-report-schema.json",
  "schemaVersion": "2",
  "thresholds": { "high": 80, "low": 50 },
  "projectRoot": "testdata/charts/sample",
  "files": {
    "templates/serviceaccount.yaml": {
      "source": "{{- if .Values.serviceAccount.create }}\napiVersion: v1\nkind: ServiceAc...",
      "language": "yaml",
      "mutants": [
        {
          "id": "serviceaccount.yaml:7:cond-negate",
          "mutatorName": "cond-negate",
          "replacement": "not (.Values.serviceAccount.create)",
          "status": "Killed",
          "description": "cond-negate",
          "location": { "start": { "line": 1, "column": 8 }, "end": { "line": 1, "column": 37 } },
          "killedBy": ["strong assertions on the service account > pins every service account field"],
          "testsCompleted": 1
        },
        {
          "id": "serviceaccount.yaml:40:yaml-key-delete",
          "mutatorName": "yaml-key-delete",
          "status": "Killed",
          "description": "yaml-key-delete",
          "location": { "start": { "line": 2, "column": 1 }, "end": { "line": 2, "column": 16 } },
          "killedBy": ["strong assertions on the service account > pins every service account field"],
          "testsCompleted": 1
        }
      ]
    }
  }
}
```

*(One file and two of its mutants shown; the real report covers all nine files and 458 mutants.)*

Status mapping onto the schema's vocabulary:

| ours | schema |
|---|---|
| `Killed` | `Killed` |
| `Survived` | `Survived` |
| `NoCoverage` | `NoCoverage` |
| `Invalid` | `CompileError` |
| `Timeout` | `Timeout` |
| `Error` | `RuntimeError` |

`Invalid` → `CompileError` is the right fit: that is the schema's term for "the mutation made the
artefact unbuildable", and like this tool, the viewer excludes it from the score.

Three schema-shaped compromises worth knowing:

- **`killedBy` holds `"Suite > Test"` strings**, not test IDs, since there is no separate test-id
  namespace here.
- **A mutation's end position is clamped to its start line.** A `yaml-key-delete` can span many lines,
  but only the start line is tracked, so the end column is `start.column + span width`. The viewer
  highlights the start of the mutation accurately; a multi-line deletion is not fully underlined.
- **`.tpl` files are reported as `language: "html"`.** No YAML-with-Go-template highlighting mode
  exists, and `html` at least highlights `{{ }}`. `.yaml` templates are reported as `yaml`, which is
  why `templates/serviceaccount.yaml` above says `yaml` despite opening with a Go template action.

If a chart file cannot be read back while writing the report, its mutants are still listed against an
empty source rather than being dropped silently.
