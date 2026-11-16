# Report formats

There are six formats. Select any combination of them with `--report`. Each format is a pure
function of the run model, so the formats always agree with each other.

```console
helm mutation-test ./my-chart --report console,json,markdown,junit,html,sarif --report-dir ./mutation
```

| format | destination | filename | use it for |
|---|---|---|---|
| [`console`](#console) *(default)* | stdout | — | Reading the result yourself, right now |
| [`json`](#json) | file | `mutation-report.json` | Building something on top (it is the canonical model) |
| [`markdown`](#markdown) | file | `mutation-report.md` | `$GITHUB_STEP_SUMMARY`, PR comments |
| [`junit`](#junit) | file | `mutation-report.junit.xml` | CI systems that already display test results |
| [`sarif`](#sarif) | file | `mutation-report.sarif` | GitHub code scanning, inline PR annotations, the editor's Problems panel |
| [`html`](#html) | file | `mutation-report.html` **+** `mutation-report.stryker.json` | A publishable artifact with annotated source |

The tool creates `--report-dir` (default `.helm-mutation-test`) only when you request a format other
than `console`.

**Every format names what it excluded.** All six formats show the score, the counts that are not
scored, and any `--max-mutants` truncation. This rule is deliberate. A report that showed only the
score could make a truncated or partly covered run look like full coverage. See
[concepts.md](concepts.md#truncation-is-always-reported).

**Every format also states when the equivalence check did not run.** With `--no-equivalence-check`,
every survivor stays `Survived`, exactly as before this feature existed (see
[concepts.md](concepts.md#equivalent-mutants)). Each format states this explicitly, the same way it
states a `--max-mutants` truncation. Without that statement, a report could silently look like a run
where none of the survivors were equivalent.

All samples on this page are real output from `testdata/charts/sample` (472 mutants). Truncated
samples are marked.

---

## `console`

This is the summary for people to read, and it is the default format. The order is deliberate. First
comes the score, then where the gaps are, then each survivor as a diff at a `file:line` that you can
fix.

The sections, in order, are: header, score, `By mutator`, `By file`, `SURVIVED`, `NO COVERAGE`,
`Equivalent`, `INVALID`, `PARTIALLY ANALYSED`, footer. The tool omits empty sections entirely.

**Use it for** interactive work. It is the only format that shows the survivor list, both breakdown
tables and the coverage note on every survivor.

### Sample — the weak suite (truncated)

```console
$ ./bin/helm-mutation-test testdata/charts/sample -f 'tests/weak_test.yaml' --no-color

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

  By file                             killed  survived    score
    templates/_helpers.tpl                 0        12    0.0%
    templates/configmap.yaml               0         0       —  not scored
    templates/hpa.yaml                     0         0       —  not scored
    templates/ingress.yaml                 0         0       —  not scored
    …plates/poddisruptionbudget.yaml       0         0       —  not scored
    templates/serviceaccount.yaml          0         0       —  not scored
    values.yaml                            0       115    0.0%
    templates/deployment.yaml              2       100    2.0%
    templates/service.yaml                 4        28   12.5%

  SURVIVED (255)

  templates/deployment.yaml:9  yaml-key-delete
    -   replicas: {{ .Values.replicaCount }}
    + (line removed)
    ran 4 tests in tests/weak_test.yaml — all passed

  templates/deployment.yaml:34  yaml-key-delete
    -           imagePullPolicy: {{ .Values.image.pullPolicy }}
    + (line removed)
    ran 4 tests in tests/weak_test.yaml — all passed

  values.yaml:89  str-literal
    - adminEmail: ops@example.com
    + adminEmail: "helm-mutation-test"
    ran 4 tests in tests/weak_test.yaml — all passed

  ... 252 further survivors, one block each ...

  NO COVERAGE (173)
    templates/configmap.yaml — no suite renders this template (66 mutants never run)
    templates/hpa.yaml — no suite renders this template (39 mutants never run)
    templates/ingress.yaml — no suite renders this template (35 mutants never run)
    templates/poddisruptionbudget.yaml — no suite renders this template (20 mutants never run)
    templates/serviceaccount.yaml — no suite renders this template (13 mutants never run)

  Equivalent  cannot change any rendered manifest; not scored
    templates/_helpers.tpl:3:39  pipe-drop
    templates/_helpers.tpl:3:48  num-literal
    templates/_helpers.tpl:3:50  pipe-drop
    ... 20 further equivalent mutants ...
    values.yaml:66:1  yaml-key-delete

  INVALID (14)
    these mutations stopped the chart rendering, so every test caught them
    they measure nothing about test quality and are excluded from the score

  472 mutants in 551ms
```

*(The sample shows three of the 255 survivor blocks and an abridged, non-contiguous `Equivalent`
list. The real output prints all of them.)*

### How to read it

- **The bar and the score have one colour per band**: green at ≥ 80%, yellow at ≥ 50%, red below.
- **`not scored:`** lists every excluded status with its count. The tool prints it only when the run
  excluded something.
- **A `--no-equivalence-check` run prints an extra line** directly under `not scored:`:
  `equivalence check skipped
  (--no-equivalence-check): some survivors may be unkillable`. Thus a report never looks like a run
  where the check found nothing.
- **Breakdowns are sorted by ascending score**, so the most under-tested mutator and file are at the
  top. A row with nothing scored shows `—  not scored`, not a meaningless `0.0%`. The five uncovered
  templates in the sample show this.
- **Long file names are truncated from the left** with a leading `…`, as
  `…plates/poddisruptionbudget.yaml` shows. The end of the path is the useful part.
- **The tool groups survivors by file**, in sorted order, so you can fix one file at a time.
- **A deletion has no "after" text**, so it renders as `(line removed)` or `(N lines removed)`. The
  diff shows only the first deleted line.
- **The note under each diff says what actually ran**: `ran 4 tests in tests/weak_test.yaml — all
  passed`, or `ran N tests across M suite files — all passed` when several suite files covered the
  mutant. This note separates a real survivor from one that no test exercised.
- **`NO COVERAGE` has one count per file**, because "no suite renders this template" is a finding
  about a file. A list of all 173 mutants would hide that finding.
- **`Equivalent` lists every mutant the check reclassified**, each at its `file:line:column`. Unlike
  `SURVIVED` entries, these have no diff, because there is nothing to fix. No assertion could ever
  have caught them. For some mutators, every survivor turns out equivalent (`pipe-drop` and
  `required-drop` in the sample). Their breakdown rows show `—  not scored`, not a `0.0%` that would
  look like a weak suite.
- **`PARTIALLY ANALYSED`** appears when the tool could not fully analyse a file. Most often this is a
  template that Go's parser rejected. In that case the line-based mutators still ran, but the AST
  mutators did not.

### The same chart, the strong suite

```console
$ ./bin/helm-mutation-test testdata/charts/sample -f 'tests/strong_test.yaml' --no-color

Mutation testing sample  (7 suites, 43 tests, baseline 44ms)

  Score  100.0%  ██████████████████████████   (426 killed / 0 survived)
  not scored: 26 equivalent · 20 invalid

  By mutator                          killed  survived    score
    bool-flip                             15         0  100.0%
    comparison-swap                       13         0  100.0%
    cond-negate                           19         0  100.0%
    default-drop                           7         0  100.0%
    num-literal                           68         0  100.0%
    pipe-drop                              9         0  100.0%
    required-drop                          3         0  100.0%
    str-literal                           83         0  100.0%
    yaml-key-delete                      209         0  100.0%

  By file                             killed  survived    score
    templates/_helpers.tpl                16         0  100.0%
    templates/configmap.yaml              62         0  100.0%
    templates/deployment.yaml            108         0  100.0%
    templates/hpa.yaml                    38         0  100.0%
    templates/ingress.yaml                32         0  100.0%
    …plates/poddisruptionbudget.yaml      18         0  100.0%
    templates/service.yaml                33         0  100.0%
    templates/serviceaccount.yaml         12         0  100.0%
    values.yaml                          107         0  100.0%

  Equivalent  cannot change any rendered manifest; not scored
    templates/_helpers.tpl:15:48  pipe-drop
    templates/configmap.yaml:6:45  num-literal
    ... 23 further equivalent mutants ...
    values.yaml:83:1  yaml-key-delete

  INVALID (20)
    these mutations stopped the chart rendering, so every test caught them
    they measure nothing about test quality and are excluded from the score

  472 mutants in 2.5s
```

*(The `Equivalent` list is abridged. The real output prints all 26.)*

This output has no `SURVIVED` section, because the count is zero. The suite killed every mutant that it
could kill. Of the remaining 26, 21 match the two patterns in
[equivalent mutants](concepts.md#equivalent-mutants):

- `configmap.yaml:6` is an `nindent 4 → 5`. It reindents a block but does not change the parsed data.
- `values.yaml:83` deletes `debug: false`. This leaves the value nil, and Go templates treat nil
  exactly like `false`.

The other 5 are `pipe-drop` mutants. Each one removes a `| quote` from a value that is already a plain
string, such as `.Chart.AppVersion` in `_helpers.tpl:15`. YAML parses that value to the same string
with or without the quotes.

Each `Equivalent` entry carries its evidence in the `detail` field of the JSON report, for example
`identical under 43 test-job value sets; span proven executed`. Thus a reader does not have to take the
verdict on faith. See [concepts.md](concepts.md#the-parsed-render-comparison) for what that evidence
means and how to check it by hand.

With a `--threshold`, the footer adds an explicit verdict line:

```
  472 mutants in 2.5s
  score 100.0% meets the 70.0% threshold
```

The tool turns colour on when stdout is a terminal. Force colour with `--color`, or turn it off with
`--no-color`.

---

## `json`

This is the canonical machine-readable report. It holds the full model and every mutant, with kill
attribution. The other file formats are summaries. Parse this one if you want to build something on
top.

**Use it for** custom dashboards, score trends over time, scripts ("which survivors are new since
`main`?"), or to find the exact assertion that killed a given mutation.

The wire shape is `"schema": "helm-mutation-test/v1"`. It is deliberately a separate type from the
internal model, so a refactor does not silently change the published format.

It is also the input to `helm mutation-test merge`. A shard's report has one extra top-level object
that unsharded and merged reports do not have. Its `score` covers that slice only:

```json
"shard": { "index": 2, "count": 4, "otherShards": 60, "seed": 1 }
```

A shard writes its report as `mutation-report.shard-K-of-N.json`, so shards that share a directory do
not overwrite each other.

### Top level (real, from the strong-suite run)

!!! tip "Interactive"
    On the docs site, click the :material-plus-circle: markers to see what each field means.

```json
{
  "schema": "helm-mutation-test/v1",
  "chartName": "sample",
  "chartPath": "testdata/charts/sample",
  "score": 100, // (1)!
  "threshold": 0,
  "passed": true, // (2)!
  "tally": { // (3)!
    "killed": 426,
    "survived": 0,
    "noCoverage": 0,
    "invalid": 20,
    "equivalent": 26,
    "timeout": 0,
    "error": 0
  },
  "equivalenceChecked": true, // (4)!
  "equivalenceUnchecked": 0, // (5)!
  "generated": 472, // (6)!
  "capped": 0, // (7)!
  "mutators": [
    "bool-flip", "comparison-swap", "cond-negate", "default-drop", "num-literal",
    "pipe-drop", "range-empty", "required-drop", "str-literal", "yaml-key-delete"
  ],
  "suites": [ // (8)!
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
  "byMutator": [ { "name": "num-literal", "score": 100, "tally": { "...": 0 } } ],
  "byFile":    [ { "name": "templates/poddisruptionbudget.yaml", "score": 100, "tally": { "...": 0 } } ],
  "mutants":   [ "..." ],
  "timing": { "baselineMillis": 44, "totalMillis": 2534 }
}
```

1.  `killed / (killed + survived)` as a percentage, rounded to two places. Nothing else is in the
    denominator.
2.  Reflects `--threshold`. With no threshold it is `true`. A shard refuses `--threshold`, because
    only `merge` grades.
3.  One count per status. Only `killed` and `survived` grade the suite. `score` excludes every
    other status, and this object names each one.
4.  `false` when `--no-equivalence-check` skipped the pass, so `tally.equivalent == 0` is not
    ambiguous.
5.  Survivors that the pass ran over but could not decide. Each one's `detail` says why.
6.  The count **before** any `--max-mutants` cap or `--since` narrowing.
7.  How many mutants `--max-mutants` dropped. This is never silent. Every format names it.
8.  One entry per `suite:` document, not per suite file.

*(The suite test lists, `byMutator`, `byFile` and `mutants` are truncated above. The real file is
complete. Note that all seven suites live in one suite *file*: `suites` has one entry per `suite:`
document. Every `byMutator`/`byFile` row ties at 100.0% here. The reason is that the equivalence pass
already removed every survivor that these breakdowns could otherwise show.)*

Field notes:

- `score` is rounded to two places. `passed` reflects `--threshold`. With no threshold it is `true`.
- `generated` is the count **before** any `--max-mutants` cap or `--since` narrowing. `capped` is how
  many mutants the cap dropped. `generated - incremental.mutantsSkipped - capped == len(mutants)`
  (`mutantsSkipped` is 0 without `--since`).
- `incremental` is present only for a `--since` run:

  ```json
  "incremental": {
    "base": "origin/main",
    "baseCommit": "fd15dca…",
    "filesChanged": ["templates/service.yaml"],
    "filesInspected": 1,
    "filesSkipped": 7,
    "mutantsSkipped": 412,
    "fullRunReason": ""
  }
  ```

  `filesChanged` is every chart-relative path in the diff. `filesInspected`/`filesSkipped` count
  mutable files. `fullRunReason` (omitted when empty) names the change that forced a full run. In
  that case `mutantsSkipped` is 0. The other formats print the same facts as one line. JUnit prints
  them as a prepended `--since` skipped testcase.
- `byMutator` and `byFile` are sorted ascending by score.
- `equivalenceChecked` separates a run where the pass found no equivalent mutants from a run where
  `--no-equivalence-check` skipped the pass entirely. On its own, `tally.equivalent == 0` is
  ambiguous.
- `equivalenceUnchecked` counts survivors that the pass ran over but could not decide. The cause can
  be an unloadable chart, a covering suite that the pass had to skip, or a span that it could not
  probe. If `equivalenceChecked` is `true` and `equivalenceUnchecked` is not zero, the check ran but
  proved nothing about that many survivors. Each one's `detail` says why.
- `skippedFiles` is present only when the tool could not fully analyse a file.
- `timing` is in milliseconds. Each mutant also has its own `durationNanos`.

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

`failInfo` names the exact JSONPath that the assertion checked. This is usually enough to see why the
assertion caught the mutation. Under the default `--kill-attribution=first`, `killedBy` holds a single
entry. Under `--kill-attribution=all`, it holds every assertion that killed the mutant.

### An equivalent mutant, verbatim

```json
{
  "id": "configmap.yaml:144:num-literal:increment",
  "mutator": "num-literal",
  "file": "templates/configmap.yaml",
  "line": 6,
  "column": 45,
  "startByte": 144,
  "endByte": 145,
  "originalLine": "    {{- include \"sample.labels\" . | nindent 4 }}",
  "mutatedLine": "    {{- include \"sample.labels\" . | nindent 5 }}",
  "original": "4",
  "mutated": "5",
  "status": "Equivalent",
  "coveringSuites": ["tests/strong_test.yaml"],
  "testsRun": 9,
  "detail": "identical under 43 test-job value sets; span proven executed",
  "durationNanos": 18943833
}
```

This mutant has no `killedBy`, because nothing killed it. Instead, `detail` carries the evidence from
the equivalence pass. It gives the number of test-job value sets under which the mutant and the
original rendered identically. It also says that the execution probe proved that at least one of
those value sets actually reached the span.

`testsRun` is unrelated and is left over from evaluation. It counts the 9 tests that ran in the
*worker* pass, before the tool knew that the mutant survived. It does not count the 43 render contexts
that the *equivalence* pass then checked.

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

For `Invalid`, `detail` carries the render error. For `Error`, it carries the tool error. An invalid
`num-literal` mutant looks different: `"detail": "yaml: line 22: did not find expected key"`. The
reason is that a collapsed `nindent` width breaks the YAML, not the template.

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

# Equivalent mutants, with the evidence for each
jq -r '.mutants[] | select(.status=="Equivalent") | "\(.file):\(.line)\t\(.detail)"' mutation-report.json

# Confirm nothing was silently dropped (on a shard, other shards hold the rest)
jq '{generated, capped, diffSkipped: (.incremental.mutantsSkipped // 0), otherShards: (.shard.otherShards // 0), evaluated: (.mutants|length)}' mutation-report.json

# Confirm the equivalence pass actually ran, rather than being skipped, and that
# it reached a verdict on every survivor
jq '{equivalenceChecked, equivalenceUnchecked}' mutation-report.json
```

---

## `markdown`

This format is a summary sized for a CI job summary or a PR comment.

**Use it for** `$GITHUB_STEP_SUMMARY`, a bot comment, or anywhere a reviewer will read the result
without opening an artifact.

```yaml
- name: Mutation test
  run: |
    helm mutation-test ./chart --threshold 70 \
      --report markdown --report-dir ./mutation
    cat ./mutation/mutation-report.md >> "$GITHUB_STEP_SUMMARY"
```

The survivor list is inside `<details>`, so it does not take over the page. The list is **capped at 40
entries**, with an explicit note when the tool truncates it. GitHub's step summary has a size limit,
and a silent cut would misrepresent the run. The 255 survivors of the weak run do not fit. The strong
run has no survivors to show, because its equivalence pass reclassifies every one.

### Sample — the strong suite

````markdown
## Mutation score: 100.0%

`sample` — 7 suites, 43 tests

| Outcome | Count | |
|---|---:|---|
| Killed | 426 | a test caught the mutation |
| Survived | 0 | **no test noticed** — a missing assertion |
| Equivalent | 26 | cannot change any rendered manifest — unkillable |
| Invalid | 20 | broke rendering, so it grades nothing |

Score counts only killed and survived mutants: 426 of 472.

### Score by mutator

| Name | Killed | Survived | Score |
|---|---:|---:|---:|
| `bool-flip` | 15 | 0 | 100.0% |
| `comparison-swap` | 13 | 0 | 100.0% |
| `cond-negate` | 19 | 0 | 100.0% |
| `default-drop` | 7 | 0 | 100.0% |
| `num-literal` | 68 | 0 | 100.0% |
| `pipe-drop` | 9 | 0 | 100.0% |
| `required-drop` | 3 | 0 | 100.0% |
| `str-literal` | 83 | 0 | 100.0% |
| `yaml-key-delete` | 209 | 0 | 100.0% |

### Score by file

| Name | Killed | Survived | Score |
|---|---:|---:|---:|
| `templates/_helpers.tpl` | 16 | 0 | 100.0% |
| `templates/configmap.yaml` | 62 | 0 | 100.0% |
| `templates/deployment.yaml` | 108 | 0 | 100.0% |
| `templates/hpa.yaml` | 38 | 0 | 100.0% |
| `templates/ingress.yaml` | 32 | 0 | 100.0% |
| `templates/poddisruptionbudget.yaml` | 18 | 0 | 100.0% |
| `templates/service.yaml` | 33 | 0 | 100.0% |
| `templates/serviceaccount.yaml` | 12 | 0 | 100.0% |
| `values.yaml` | 107 | 0 | 100.0% |

### Survived mutants

None — every covered mutation was caught.

<sub>472 mutants in 2.5s · baseline 44ms</sub>
````

Differences from `console`:

- To stay short, the outcome table hides non-scoring rows with a zero count. But it **always** shows
  `Killed` and `Survived`, and it always states `Score counts only killed and survived mutants: N of M`.
- With a `--threshold` set, a `✅ Meets the 70.0% threshold.` or
  `❌ **Below the 70.0% threshold.**` line appears directly under the header.
- With `--no-equivalence-check`, a `> The equivalence check was skipped
  (--no-equivalence-check); some survivors may be unkillable.` line appears directly after the
  outcome table.
- A capped run adds `> ⚠️ --max-mutants ran N of M generated mutants; K were not evaluated.`
- With no survivors, the section reads `### Survived mutants` / `None — every covered mutation was caught.`
- Templates that no suite renders get their own `### Templates no suite renders` section. For the weak
  suite, this section lists all five files with their mutant counts.
- **Unlike `console`, this format has no per-mutant `Equivalent` list.** It shows only the count in
  the Outcome table. It also has no per-survivor coverage note. Use `console` or `json` for either.

In the weak run, the `Survived mutants` section still shows a capped list of 40 of its 255 survivors.
Its `Outcome` table's `Equivalent` row reads 24. See [concepts.md](concepts.md#equivalent-mutants) for
what separates those 24 from the 255 that remain.

---

## `junit`

This format writes one `<testcase>` per mutant. Any CI system then displays mutation results natively,
and it does not need to understand mutation testing.

**The mapping inverts the usual sense on purpose:**

| status | JUnit |
|---|---|
| `Survived` | `<failure type="SurvivedMutant">`. The survivor is the defect you can act on, so it is what should turn the job red. |
| `Killed` | a passing testcase. The tests did their job. |
| `NoCoverage`, `Invalid`, `Equivalent`, `Timeout`, `Error` | `<skipped>` with a reason. None of them says anything about assertion quality, and none should fail a build. |

There is one `<testsuite>` per chart file, in order of first appearance. Each testcase name is
`MUTATOR at FILE:LINE [ID]`, `classname` is the file, and `time` is the duration of that mutant.

**Use it for** making a weak suite visible in the CI UI you already have, or gating a build without
`--threshold` (fail on any survivor).

### Sample — the strong suite (truncated)

```xml
<?xml version="1.0" encoding="UTF-8"?>
<testsuites name="helm-mutation-test:sample" tests="472" failures="0" skipped="46" time="2.5348">
  <testsuite name="templates/_helpers.tpl" tests="17" failures="0" skipped="1" hostname="localhost">
    <testcase name="default-drop at templates/_helpers.tpl:2 [_helpers.tpl:66:default-drop]"
              classname="templates/_helpers.tpl" time="0.0829"></testcase>
    <testcase name="pipe-drop at templates/_helpers.tpl:3 [_helpers.tpl:131:pipe-drop]"
              classname="templates/_helpers.tpl" time="0.2109"></testcase>
    ... 15 more testcases, one skipped as equivalent ...
  </testsuite>
  <testsuite name="templates/configmap.yaml" tests="66" failures="0" skipped="4" hostname="localhost">
    ... 7 passing testcases for lines 1-5 ...
    <testcase name="num-literal at templates/configmap.yaml:6 [configmap.yaml:144:num-literal:increment]"
              classname="templates/configmap.yaml" time="0.0189">
      <skipped message="equivalent: the mutation cannot change any rendered manifest, so no assertion could catch it"></skipped>
    </testcase>
    ... 58 more testcases ...
  </testsuite>
  ... 7 more testsuites ...
</testsuites>
```

*(Long attributes are wrapped for readability. `_helpers.tpl` has 17 testcases, zero failures and one
skip. The strong suite kills every mutant in it except a `| quote` drop that cannot change the
rendered string. This run has no `<failure>` elements anywhere. The equivalence pass reclassified all
26 of the strong suite's would-be survivors, so `failures="0"` across the whole file.)*

A `<failure>` still looks exactly as it always did. This is real output from the weak suite, which
does have real survivors:

```xml
<testcase name="yaml-key-delete at templates/deployment.yaml:9 [deployment.yaml:198:yaml-key-delete]"
          classname="templates/deployment.yaml" time="0.0069">
  <failure message="no test noticed this change to templates/deployment.yaml:9" type="SurvivedMutant">mutator: yaml-key-delete
location: templates/deployment.yaml:9:1

-   replicas: {{ .Values.replicaCount }}
+ (line removed)

ran 4 tests in tests/weak_test.yaml — all passed
suites run: tests/weak_test.yaml

Add an assertion that distinguishes the original from the mutation.
</failure>
</testcase>
```

*(The real file escapes newlines inside `<failure>` as `&#xA;`. They are expanded above for
readability.)*

Each failure body carries the mutator, the exact `file:line:column`, the diff, what ran, and the
one-line instruction `Add an assertion that distinguishes the original from the mutation.`

Skipped reasons are specific, so a skip is never a mystery. These reasons are real, from the
weak-suite run:

```xml
<skipped message="no suite renders templates/configmap.yaml, so this mutation was never run"/>
<skipped message="no suite renders templates/hpa.yaml, so this mutation was never run"/>
<skipped message="equivalent: the mutation cannot change any rendered manifest, so no assertion could catch it"/>
<skipped message="the mutation stopped the chart rendering, so it grades nothing: yaml: line 18: mapping values are not allowed in this context"/>
```

`Timeout` and `Error` use the same shape (`evaluation timed out: …`, `the tool failed on this
mutant: …`). The fixture chart produces neither.

With `--no-equivalence-check`, the tool prepends a `<testsuite name="--no-equivalence-check">`. This
makes the skip visible, the same way as for the `--max-mutants` truncation further down:

```xml
<testsuite name="--no-equivalence-check" tests="1" failures="0" skipped="1" hostname="localhost">
  <testcase name="equivalence check skipped" classname="--no-equivalence-check" time="0.0000">
    <skipped message="the equivalence check did not run (--no-equivalence-check); some survivors may be unkillable"/>
  </testcase>
</testsuite>
```

A truncated run gets one extra `<testsuite>`, so the cap is visible here as in every other format:

```xml
<testsuite name="--max-mutants" tests="1" failures="0" skipped="1" hostname="localhost">
  <testcase name="truncated run" classname="--max-mutants" time="0.0000">
    <skipped message="452 of 472 generated mutants were not evaluated (--max-mutants); this run is a sample, not full coverage"/>
  </testcase>
</testsuite>
```

The tool prepends it to the real testsuites and counts it in the top-level `tests` and `skipped`
attributes. These attributes therefore stay equal to the sum over the child suites.

```yaml
- name: Publish mutation results
  if: always()
  uses: mikepenz/action-junit-report@v4
  with:
    report_paths: ./mutation/mutation-report.junit.xml
```

---

## `sarif`

A [SARIF 2.1.0](https://docs.oasis-open.org/sarif/sarif/v2.1.0/sarif-v2.1.0.html) log. SARIF is
the format behind GitHub's code-scanning alerts, inline PR annotations, and the Problems panel of any
editor with a SARIF viewer.

**Use it for** survivors annotated on the template line they mutated, in the PR diff or in the
editor. Nobody has to open the HTML artifact.

**Every result becomes an alert, so only findings you can act on become results:**

| status | SARIF |
|---|---|
| Survived | one `result` per mutant, rule = the mutator ID |
| NoCoverage | one `result` **per file**, rule `no-coverage`, at line 1 |
| Killed, Invalid, Equivalent, Timeout, Error | no result. The notifications further down count them. |

A survivor's `level` is `error` when the run misses `--threshold`. This is the same condition as exit
code 1, so red annotations match a red job. Otherwise the level is `warning`. A no-coverage result is
always `warning`.

The weak run (threshold 70) produces 255 survivor results and 5 no-coverage results:

```json
{
  "ruleId": "default-drop",
  "level": "error",
  "message": {
    "text": "no test noticed this default-drop mutation at templates/_helpers.tpl:2\n- {{- $name := .Values.nameOverride | default .Chart.Name -}}\n+ {{- $name := .Values.nameOverride -}}\nran 4 tests in tests/weak_test.yaml — all passed\nsuites run: tests/weak_test.yaml\nAdd an assertion that distinguishes the original from the mutation."
  },
  "locations": [{
    "physicalLocation": {
      "artifactLocation": { "uri": "testdata/charts/sample/templates/_helpers.tpl", "uriBaseId": "%SRCROOT%" },
      "region": { "startLine": 2, "startColumn": 34, "endColumn": 56 }
    }
  }],
  "partialFingerprints": { "mutantFingerprint/v1": "b2dd554b…f1e0:1" },
  "properties": { "mutantId": "_helpers.tpl:66:default-drop", "status": "Survived", "original": " | default .Chart.Name", "mutated": "", "coveringSuites": ["tests/weak_test.yaml"] }
}
```

**The invocation's `toolExecutionNotifications` name what the results leave out:**

```text
note     mutation score 2.3% (6 killed of 261 scored); excluded from the score: 173 no-coverage, 14 invalid, 24 equivalent, 0 timeout, 0 error
note     score 2.3% is below the 70.0% threshold
warning  8 survivors could not be checked for equivalence and may be unkillable; each one's reason is in its detail
```

A `--max-mutants` cap, a `--no-equivalence-check` run and partially analysed files each add a
`warning` notification, with the same words as in JUnit. `runs[0].properties` repeats the numbers:
`score`, `tally`, `generated`, `capped`, `equivalenceChecked`, `equivalenceUnchecked`, `threshold`.

**Locations:**
- `uri` is the chart path as you passed it, joined with the chart-relative file, relative to
  `%SRCROOT%`. GitHub resolves that against the checkout root. So **run from the repository root with
  a relative chart path**. An absolute chart path gives an absolute `file://` URI.
- Columns count Unicode code points (`columnKind: unicodeCodePoints`), not bytes. As in the Stryker
  report, the region covers only the first line of the mutation.
- `partialFingerprints` hashes the file, the mutator, the original and mutated text, and the trimmed
  source line. It does not hash the mutant ID, because the ID contains a byte offset. With the ID in
  the hash, an edit above a survivor would close its alert and open an identical new one.

```yaml
permissions:
  security-events: write   # upload-sarif needs this
steps:
  - run: helm mutation-test ./charts/my-chart --report sarif --report-dir ./mutation
  - name: Upload mutation survivors
    if: always()
    uses: github/codeql-action/upload-sarif@v3
    with:
      sarif_file: ./mutation/mutation-report.sarif
      category: helm-mutation-test
```

---

## `html`

This format is a self-contained page, plus the standard-schema JSON next to it. `--report html`
writes **two** files:

- `mutation-report.html`: 293 KB for the fixture chart
- `mutation-report.stryker.json`: 288 KB, most of it embedded chart source

**Use it for** a CI artifact that somebody will actually open, and for annotated source. Annotated
source shows each mutation highlighted in place, in the template it came from.

### The page

The page has four sections: `Mutation report — CHART`, `Outcomes`, `Survived mutants`,
`Annotated source`.

The big score at the top uses the same bands as the console output: `good` / `okay` / `poor` classes
at 80/50. The strong run's 100.0% renders as `good`. The page has a dark-mode stylesheet through
`prefers-color-scheme`.

The `Outcomes` table has a row per status, `Equivalent` included. It also has a `Scored` total row
that states how many of all mutants counted. With `--no-equivalence-check`, an extra warning row
states that the check did not run. An extra row states a `--max-mutants` truncation the same way.

`Annotated source` hosts the published
[mutation-testing-elements](https://github.com/stryker-mutator/mutation-testing-elements) web
component. The page loads it from jsDelivr and feeds it the report from an inline
`<script type="application/json">`.

**The plain-HTML summary above the viewer is the important part.** People very often open an artifact
from a CI run with no network access or with scripts blocked. In exactly that case, a viewer loaded
from a CDN shows nothing. The `Outcomes` table and the full `Survived mutants` list are static HTML and
are complete on their own. The fallback text on the page says so.

The survivor list here is **not** capped. Unlike markdown, it lists every survivor, each with its diff
and its coverage note. For the weak suite that means all 255. For the strong suite the list is empty,
because the equivalence pass leaves no survivors. The page then says
`None — every covered mutation was caught.`

The page does not list equivalent mutants at all. Only `console` has that list. But the `Outcomes`
table counts them, and the embedded
Stryker/mutation-testing-elements JSON further down names them, each with `status: "Ignored"`.

The embedded JSON is HTML-escaped, so a `</script>` in the chart source cannot end the element early.

### `mutation-report.stryker.json`

This file conforms to the mutation-testing-elements report schema v2. It embeds the full source of
each file, so the viewer can highlight mutations in place. That is why the file is large relative to
the mutant count.

The tool writes it whenever you request `html`, because the file is useful on its own. Other
mutation-testing tools consume it, and it is what you would hand to a hosted dashboard.

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

*(The sample shows one file and two of its mutants. The real report covers all nine files and 472
mutants.)*

The tool maps its statuses onto the schema's vocabulary:

| ours | schema |
|---|---|
| `Killed` | `Killed` |
| `Survived` | `Survived` |
| `NoCoverage` | `NoCoverage` |
| `Invalid` | `CompileError` |
| `Equivalent` | `Ignored` |
| `Timeout` | `Timeout` |
| `Error` | `RuntimeError` |

`Invalid` → `CompileError` is the right fit. `CompileError` is the schema's term for "the mutation made
the artefact unbuildable". Like this tool, the viewer excludes it from the score.

`Equivalent` → `Ignored` fits in the same way. `Ignored` is the schema's term for a mutant
deliberately excluded from scoring, and an equivalence-pass verdict is exactly that. The
`description` of an equivalent mutant also differs from that of every other status. It reads
`"num-literal: cannot change any rendered manifest"`, not the bare mutator name. Thus the
annotated-source view explains itself without a lookup.

The shape of the schema forces three compromises:

- **`killedBy` holds `"Suite > Test"` strings**, not test IDs, because this tool has no separate
  namespace for test IDs.
- **A mutation's end position is clamped to its start line.** A `yaml-key-delete` can span many lines,
  but the tool tracks only the start line. So the end column is `start.column + span width`. The viewer
  highlights the start of the mutation accurately, but it does not fully underline a multi-line
  deletion.
- **`.tpl` files are reported as `language: "html"`.** No highlighting mode exists for YAML with Go
  templates, and `html` at least highlights `{{ }}`. The report gives `.yaml` templates as `yaml`.
  That is why `templates/serviceaccount.yaml` above says `yaml`, though it opens with a Go template
  action.

If the tool cannot read back a chart file while it writes the report, it still lists the file's
mutants against an empty source. It does not drop them silently.
