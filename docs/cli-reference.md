# CLI reference

```
helm mutation-test [flags] CHART
```

The command takes exactly one positional argument: the path to the chart directory. Any other
argument is a usage error.

To check this page against your build, run `helm mutation-test --help`.

- [Pass-through to helm-unittest](#pass-through-to-helm-unittest)
- [Mutation control](#mutation-control)
- [Execution](#execution)
- [Output](#output)
- [Flag value syntax](#flag-value-syntax)
- [Exit codes](#exit-codes)
- [Validation errors](#validation-errors)
- [Hidden and generated subcommands](#hidden-and-generated-subcommands)

## Pass-through to helm-unittest

These flags copy the flags of `helm unittest`. They control how the tool finds and parses suites.

| flag | type | default | purpose |
|---|---|---|---|
| `-f`, `--file` | repeatable string | `tests/*_test.yaml` | Glob paths of test suite files, relative to the chart |
| `-v`, `--values` | repeatable string | *(none)* | Absolute or glob paths of values files that override chart values |
| `-s`, `--with-subchart` | bool | `true` | Include tests of subcharts *(see note below)* |
| `--strict` | bool | `false` | Parse the test suites strictly |
| `--chart-tests-path` | string | *(empty)* | Folder, relative to the chart, that holds a chart that renders test suites *(see note below)* |

### `-f`, `--file`

The first `-f` **replaces** the default. Each further `-f` adds to the list. So this command runs
one suite, not the default glob plus one:

```console
helm mutation-test ./my-chart -f 'tests/deployment_test.yaml'
```

Put the glob in quotes so that your shell does not expand it. For suites in subdirectories, give a
recursive glob:

```console
helm mutation-test ./my-chart -f 'tests/**/*_test.yaml'
```

The tool resolves suite files relative to the chart directory and then sorts them. Glob order
depends on the filesystem, and the sort keeps runs reproducible.

If no file matches, the run stops with exit code 2. The tool has nothing to measure.

### `-v`, `--values`

The tool applies these values files on top of the chart's `values.yaml` when it parses suites.
This is the same as `helm unittest -v`. You can give the flag more than once.

```console
helm mutation-test ./my-chart -v ./ci/prod-values.yaml
```

This flag does not change what the tool mutates. The chart's `values.yaml` is still the mutation
target.

### `--strict`

The tool passes this flag to the suite parser of helm-unittest. When it is on, an unknown key in a
suite file is an error. When it is off, the parser ignores the key.

### Notes on `--with-subchart` and `--chart-tests-path`

The CLI accepts and validates both flags, and passes them to the options struct of the runner. But
**no code reads either value at this time**. Setting them has no visible effect in this version.
This page documents them because the CLI accepts them. Do not rely on them to change behaviour.

You can still reach subchart suites with an explicit glob:

```console
helm mutation-test ./my-chart -f 'tests/*_test.yaml' -f 'charts/*/tests/*_test.yaml'
```

## Mutation control

| flag | type | default | purpose |
|---|---|---|---|
| `--mutators` | comma-separated list | *all ten* | Mutators to run |
| `--exclude-mutators` | comma-separated list | *(none)* | Mutators to skip |
| `--include` | comma-separated globs | `templates/**,values.yaml` | Chart files to mutate |
| `--exclude` | comma-separated globs | *(none)* | Chart files to skip |
| `--max-mutants` | int | `0` (no limit) | Evaluate this many mutants at most, with a deterministic sample |
| `--seed` | int | `1` | Sampling seed, for a reproducible `--max-mutants` subset and shard split |
| `--shard-index` | int | `0` | Evaluate only this shard (1-based) of `--shard-count` |
| `--shard-count` | int | `0` (no sharding) | Split the mutants into this many disjoint shards, one for each CI job |
| `--since` | git ref | *(none)* | Score only the mutants that the changes since this ref can affect |
| `--no-equivalence-check` | bool | `false` | Skip the post-pass that changes unkillable survivors to `Equivalent` |
| `--timeout` | duration | `0` → 10× the baseline, at least 30s | Timeout for each mutant |

### `--mutators`, `--exclude-mutators`

Valid IDs: `bool-flip`, `comparison-swap`, `cond-negate`, `default-drop`, `num-literal`,
`pipe-drop`, `range-empty`, `required-drop`, `str-literal`, `yaml-key-delete`. See [mutators.md](mutators.md).

The tool rejects an unknown ID before the run starts. It does not silently run fewer mutators than
you asked for:

```console
$ helm mutation-test ./my-chart --mutators cond-negat
Error: --mutators: unknown mutator "cond-negat" (valid: bool-flip, comparison-swap, cond-negate, default-drop, num-literal, pipe-drop, range-empty, required-drop, str-literal, yaml-key-delete)
```

An empty `--mutators` means "all". The tool subtracts `--exclude-mutators` after that, so you can
combine the two. For example,
`--mutators cond-negate,str-literal --exclude-mutators str-literal` runs only `cond-negate`.

The order of your selection does not affect results. The tool sorts mutators by ID before it
generates mutants.

```console
# Only the highest-signal structural mutators, while iterating on tests
helm mutation-test ./my-chart --mutators cond-negate,comparison-swap,default-drop

# Everything except the noisiest one on a chart with large nested blocks
helm mutation-test ./my-chart --exclude-mutators yaml-key-delete
```

### `--include`, `--exclude`

These are globs relative to the chart root. When both match a file, `--exclude` wins.

A trailing `/**` means "this directory and everything under it". `filepath.Match` alone cannot
express that. A pattern with no separator also matches against the basename of a path, so `*.tpl`
matches `templates/_helpers.tpl`.

```console
# Templates only; leave values.yaml alone
helm mutation-test ./my-chart --include 'templates/**'

# Everything except the partials
helm mutation-test ./my-chart --exclude '_helpers.tpl'

# One template, for a tight feedback loop
helm mutation-test ./my-chart --include 'templates/deployment.yaml'
```

You can include only the files that the tool discovers:

- every file under `templates/` with a `.yaml`, `.yml`, `.tpl` or `.txt` extension, except in
  `__snapshot__` directories
- `values.yaml` or `values.yml` at the chart root

The tool never mutates test files.

An empty `--include` uses the default list. It does not mutate nothing.

### `--max-mutants`, `--seed`

`--max-mutants` shuffles the full plan list with `--seed` and takes a sample. So the subset covers
the whole chart, and does not stop at the first *n* mutants of the first file. The report then
shows the mutants in their original order.

Every format shows the count of dropped mutants, because a silently truncated run would look like
full coverage:

```console
$ helm mutation-test ./my-chart --max-mutants 20
  Score  100.0%  ██████████████████████████   (19 killed / 0 survived)
  not scored: 1 equivalent
  only 20 of 472 generated mutants were run (--max-mutants); 452 not evaluated
```

You cannot compare a sampled score with a full one. This is true even when the two numbers are the
same by chance, as in the example above. Use `--max-mutants` while you iterate, not for the number
you publish.

The tool rejects negative values. `0` means no limit.

### `--since`

`--since` turns on incremental mode, for CI on each PR. The diff runs from
`git merge-base <ref> HEAD` to the **working tree**, and includes untracked files. In CI, the diff
is exactly the commits of the branch. On your machine, it also includes the edits that you have
not committed yet. `--since HEAD` scores only your uncommitted work. The tool ignores paths outside
the chart directory.

The tool sorts each changed chart-relative path into one of these classes:

| changed path | what runs |
|---|---|
| a template under `templates/` (not a `_` partial) | the mutants of that template |
| a suite file that the baseline discovered (`-f`) | every file that the suite covers. This includes `values.yaml` and partials, because every suite covers them |
| `tests/__snapshot__/<suite>.snap` | the same as a change to `<suite>` |
| anything else: `values.yaml`, a `_*` partial, `Chart.yaml`, `Chart.lock`, `values.schema.json`, `charts/**`, `.helmignore`, a deleted suite, a values file that a suite reads, any file that a template can read through `.Files.Get` | **a full run**, which names the path that caused it |

If the tool cannot prove which mutants a change affects, it does a full run. It does not ignore the
change. A guess that a change is harmless would make the score look better than it is.

Every format names what the run left out, in the same way as for `--max-mutants`:

```console
$ helm mutation-test ./my-chart --since origin/main
  changed-files mode (--since origin/main): 1 files inspected, 7 untouched files skipped, 412 mutants not evaluated; score covers the diff only
```

A full run gives its reason:

```console
  --since origin/main: full run, values.yaml changed, and any template may read any value
```

A diff that touches no mutable chart file scores nothing. Such a run graded nothing, so its 0% does
not fail `--threshold`. The run exits 0 and says `nothing in the diff to score; threshold not applied`.
A full run that scores nothing still fails the threshold.

You can combine `--max-mutants` and `--shard-count` with `--since`. They sample and split the
mutants that the diff selected. Every shard must use the same `--since` ref. `merge` refuses shards
whose diffs are different.

**Shallow clones.** By default, `actions/checkout` fetches one commit. That commit has no
merge-base with `origin/main`. The run exits 2 and gives this reason. Fetch the history first:

```yaml
- uses: actions/checkout@v4
  with:
    fetch-depth: 0
- run: helm mutation-test ./chart --since origin/${{ github.base_ref }} --threshold 70
```

### `--shard-index`, `--shard-count`

These flags split one run across CI jobs. Each job evaluates a disjoint slice of the mutants and
writes its own JSON report. [`merge`](#merge) joins the reports into the run that one machine would
have produced.

```console
# job k of 4 (GitLab: --shard-index $CI_NODE_INDEX --shard-count $CI_NODE_TOTAL)
$ helm mutation-test ./my-chart --shard-index 2 --shard-count 4 --report json --report-dir shards
  Slice  100.0%  ██████████████████████████   (18 killed / 0 survived)
  shard 2 of 4: 20 of 80 selected mutants evaluated here, 60 in other shards; this is a slice, not a score — merge every shard's JSON report with 'helm mutation-test merge' to grade
```

Shards use the same `--seed` shuffle as `--max-mutants`. The tool applies the cap first. Then shard
*k* takes every *M*-th entry of the shuffled list. This has three results:

- Together, the shards cover exactly the mutants that an unsharded run would evaluate.
- Each shard samples the whole chart, not a run of files.
- The shard sizes differ by one at most.

Give every shard of a run the same chart, flags and `--seed`.

The score of a shard covers only its slice, and every format labels it as a slice. **The tool
refuses `--threshold` together with `--shard-count`**. Use `merge --threshold` as the gate. A shard
exits 0 unless it cannot run (2).

A shard puts its position in the names of its report files: `mutation-report.shard-2-of-4.json`,
not `mutation-report.json`. So every shard can write into the same directory. CI can unpack the
artifacts of all jobs into one folder, and no shard overwrites another. Unsharded and merged runs
keep the plain names.

Indices start at 1. `--shard-count 1`, or neither flag, gives a normal unsharded run.

### `merge`

```console
$ helm mutation-test merge shards/mutation-report.shard-*.json --threshold 70 \
    --report console,markdown,html --report-dir ./mutation
```

`merge` reads the JSON report of every shard. Then it reports and grades the union in the same way
as a single run: same mutants, same order, same tally. It takes these flags:

- `--report`
- `--report-dir`
- `--threshold`
- `--color`/`--no-color`
- `--chart`: where to read chart source for the HTML report. The default is the path that the
  shards recorded. That path may not exist on the merging machine.

`merge` exits 2 if the merged report would cover less than it claims. It refuses:

- a missing or repeated shard
- a report that is not a shard
- shards with a different `--shard-count`, `--seed`, chart, mutators, `--max-mutants` or `--since` diff
- a mutant in two shards
- shards whose mutants do not add up to what the run selected

In all other cases, the exit codes are the same as for a normal run.

### `--no-equivalence-check`

By default, the tool re-renders every remaining `Survived` mutant after evaluation. It looks for
the mutants that provably cannot change any rendered manifest. See
[equivalent mutants](concepts.md#equivalent-mutants). The tool changes those mutants to
`Equivalent` and removes them from the denominator of the score. This raises the score for any
chart that has them.

The check costs one extra render pair for each non-equivalent survivor. For each true equivalent,
it costs one render for each covering test job, plus a probe. This is cheap compared with the
worker pass, but it is not free on a chart with hundreds of survivors. `--no-equivalence-check`
skips the check. Every survivor then stays `Survived`, as before this feature existed.

The check is on by default because an undetected equivalent mutant is a false positive in the
report. It looks like a missing assertion, but no assertion can ever catch it. It also lowers the
score by an amount that no new test can fix. Turn the check off only when the render cost matters
more than that accuracy. An example is a tight iteration loop with `--max-mutants`.

Every report format states when the run skipped the check, in the same way that it states a
`--max-mutants` truncation. See [reports.md](reports.md). Every format also states how many
survivors the check examined but could not decide. "The check ran" and "the check concluded" are
different claims. Three things leave a survivor undecided:

- a chart that does not load
- a covering suite that the check had to skip
- a span that the check could not probe

The detail text of each survivor gives its reason.

### `--timeout`

The value is a Go duration string: `45s`, `2m`, `1m30s`.

If you leave it at `0`, the tool calculates the timeout from the baseline run. The timeout is
**ten times the baseline duration, with a minimum of 30 seconds**. Because the timeout scales with the baseline,
the tool does not cut off a slow chart without cause. The minimum gives a fast chart a usable
budget.

Set the timeout explicitly when a mutant can hang. An example is a chart with a `range` whose bound
comes from a mutated number:

```console
helm mutation-test ./my-chart --timeout 90s
```

A mutant that goes over the timeout becomes `Timeout`, and the score excludes it. The tool rejects
negative values.

## Execution

| flag | type | default | purpose |
|---|---|---|---|
| `-p`, `--parallel` | int | number of CPUs | Number of concurrent workers |
| `--kill-attribution` | `first` \| `all` | `first` | How much detail to collect about killing tests |

### `-p`, `--parallel`

The value must be 1 or more. Above 1, each worker is a separate OS process. See
[why workers are subprocesses](concepts.md#operational-note-why-workers-are-subprocesses). At
exactly 1, evaluation runs in the main process. The tool also limits the worker count to the number
of mutants, so a very small run does not start idle processes.

Each worker keeps its own temporary chart copy for the full run. Results are the same at every
`--parallel` value.

```console
helm mutation-test ./my-chart -p 4
```

### `--kill-attribution`

- `first` (default): stop the run of each mutant at the first failing test. One kill is enough to
  classify a mutant, so this is the fast path.
- `all`: run every covering test, and record the full set of killing tests in `mutant.killedBy`.

Use `all` when you want to know which assertions overlap, for example to find redundant tests. It
costs real time. This table shows the tests run for each of the three `required-drop` mutants in
the fixture chart:

| mutant | `first` | `all` |
|---|---:|---:|
| `templates/deployment.yaml:74` | 9 | 17 |
| `templates/deployment.yaml:76` | 10 | 17 |
| `templates/ingress.yaml:21` | 3 | 4 |

`all` runs every covering test in the suites of the mutant. `first` stops when one test fails.

The baseline run never stops at the first failure, whatever the value of this flag. It must see
all of the results so that it can name every broken test at once.

The tool rejects any value other than `first` or `all`.

## Output

| flag | type | default | purpose |
|---|---|---|---|
| `--report` | comma-separated list | `console` | Report formats: `console`, `json`, `html`, `markdown`, `junit`, `sarif` |
| `--report-dir` | string | `.helm-mutation-test` | Directory for file reports |
| `--threshold` | float | `0` (disabled) | Exit 1 if the mutation score is below this percentage |
| `--color` | bool | `false` | Force coloured output when stdout is not a terminal |
| `--no-color` | bool | `false` | Turn off coloured output |
| `-d`, `--debug` | bool | `false` | Verbose output, which includes worker stderr |
| `--keep-workdir` | bool | `false` | Keep the temporary chart copies so you can examine them |

### `--report`, `--report-dir`

You can give any combination, separated by commas. The tool changes values to lower case, so
`--report JSON` works. [reports.md](reports.md) documents each format with real sample output.

`console` writes to stdout and never writes to disk. If you leave it out, you get no terminal
summary:

```console
$ helm mutation-test ./my-chart --report json --report-dir ./out
  json report: out/mutation-report.json
```

The tool creates `--report-dir` if it is missing, but **only** when you ask for a format other than
console. A plain `helm mutation-test ./my-chart` leaves no `.helm-mutation-test` directory. The
filenames are fixed. The one exception is a shard, which puts its position after the stem
(`mutation-report.shard-2-of-4.json`). See [`--shard-index`](#--shard-index---shard-count).

| format | file(s) written |
|---|---|
| `console` | *(stdout only)* |
| `json` | `mutation-report.json` |
| `markdown` | `mutation-report.md` |
| `junit` | `mutation-report.junit.xml` |
| `sarif` | `mutation-report.sarif` |
| `html` | `mutation-report.html` **and** `mutation-report.stryker.json` |

`html` writes two files on purpose. The standard-schema JSON is useful by itself, because other
mutation-testing tools read that format.

The tool rejects an unknown format before the run:

```console
$ helm mutation-test ./my-chart --report bogus
Error: unknown --report format "bogus" (valid: console, json, html, markdown, junit, sarif)
```

### `--threshold`

The value is a percentage in `[0, 100]`. `0` turns off the check. If the score is below the
threshold, the run still completes and writes every report. Then it exits 1:

```console
$ helm mutation-test ./my-chart --threshold 70
...
Error: mutation score 2.3% is below the 70.0% threshold
$ echo $?
1
```

The console and markdown formats also state explicitly whether the run passed or failed the
threshold. The JSON report has `"threshold"` and `"passed"` fields.

The tool rejects values outside `[0, 100]`.

### `--color`, `--no-color`

Colour has three states. Without either flag, the tool turns colour on when stdout is a character
device. `--color` forces colour on. `--no-color` forces it off. **If you give both, `--no-color`
wins**.

Use `--color` when you pipe output into something that renders ANSI, such as a CI log viewer. Use
`--no-color` to capture clean text.

### `-d`, `--debug`

The tool prints each phase to stderr when the phase starts. It also shows worker stderr and does
not discard it. In addition, the tool stops silencing the logs of the Helm and helm-unittest
libraries. So expect many `level=debug` lines between the phase lines:

```console
$ helm mutation-test ./my-chart -d 2>&1 >/dev/null | grep '^==>'
==> Running the chart's tests unmutated
==> Planning mutants
==> Evaluating 472 mutants across 12 workers
```

A `--max-mutants` run adds a phase line that names the cap and the drop count. A `--since` run adds
one line that lists the diff, and one that names what it skipped or why it changed to a full run.

### `--keep-workdir`

The tool keeps the temporary chart copy of each worker on disk and does not delete it. This is
useful when a mutant comes back `Invalid` or `Error`. You can then run `helm template` or
`helm unittest` against the exact tree that the tool used.

## Flag value syntax

There are two list styles, and the difference is important:

- Repeatable arrays: `-f`/`--file` and `-v`/`--values`. Give the flag once for each value. The
  tool does *not* split on commas, so a comma is a literal part of the glob.
- Comma-separated slices: `--mutators`, `--exclude-mutators`, `--include`, `--exclude`,
  `--report`. `--report console,json` and `--report console --report json` are the same.

To turn off a boolean flag with a `true` default, give an explicit `=false`. Write
`--with-subchart=false`, not `--with-subchart false`.

## Exit codes

| code | meaning |
|---|---|
| `0` | The run completed, and met `--threshold` if you set one |
| `1` | The mutation score is below `--threshold` |
| `2` | The tool could not run: the chart's own tests fail, or there is a configuration or usage error, or the tool failed |

These are the verified causes of code 2:

- the chart's tests fail before mutation (the [baseline gate](concepts.md#the-baseline-gate-is-a-hard-stop))
- no suite matches `-f`, or the matching suites have no test jobs
- the chart path does not exist or does not load
- a flag fails validation
- `merge` gets an incomplete, inconsistent or unreadable set of shard reports
- the number of positional arguments is wrong
- a `--since` ref has no merge-base with `HEAD` (unknown ref, shallow clone, not a git checkout)
- the tool could not write a report file

Because of the split between 1 and 2, CI can tell "your tests are weak" apart from "the tool could
not run". Only code 1 is a finding about your suite.

## Validation errors

The tool validates the configuration before the baseline runs, so a typo costs no time. All of
these errors exit 2.

| condition | message |
|---|---|
| empty chart path | `a chart path is required` |
| `--parallel` < 1 | `--parallel must be at least 1, got N` |
| `--max-mutants` < 0 | `--max-mutants cannot be negative, got N` |
| `--threshold` outside `[0,100]` | `--threshold must be between 0 and 100, got N` |
| `--timeout` < 0 | `--timeout cannot be negative, got N` |
| `--shard-index` without `--shard-count` | `--shard-index needs --shard-count` |
| `--shard-count` > 1 without `--shard-index` | `--shard-count needs --shard-index (1 to M)` |
| `--shard-index` outside `1..M` | `--shard-index must be between 1 and M, got N` |
| `--threshold` with `--shard-count` > 1 | `--threshold cannot be used with --shard-count: a shard is a slice, not a score; ...` |
| bad `--kill-attribution` | `--kill-attribution must be "first" or "all", got "X"` |
| bad `--report` | `unknown --report format "X" (valid: console, json, html, markdown, junit, sarif)` |
| bad `--mutators` | `--mutators: unknown mutator "X" (valid: bool-flip, comparison-swap, cond-negate, default-drop, num-literal, pipe-drop, range-empty, required-drop, str-literal, yaml-key-delete)` |
| bad `--exclude-mutators` | as above, with the prefix `--exclude-mutators:` |

The tool rejects an unknown mutator ID on purpose and does not ignore it. If it silently ran fewer
mutators than you asked for, the score would look complete when it is not.

Some failures occur after validation, for example a chart that does not load or a red baseline.
These also exit 2, but only after the tool tries the related work:

```console
$ helm mutation-test ./nope
Error: loading chart ./nope: stat ./nope: no such file or directory
```

## Hidden and generated subcommands

- `merge`: not hidden. See [`merge`](#merge).

- `__worker`: hidden. The parent process runs the binary again with this argument to start an
  evaluation worker. It is not for direct use.
- `completion`: the shell-completion command that cobra generates
  (`helm mutation-test completion bash|zsh|fish|powershell`).
- `help`: the help command that cobra generates.
