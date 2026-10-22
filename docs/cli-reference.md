# CLI reference

```
helm mutation-test [flags] CHART
```

Exactly one positional argument: the path to the chart directory. Anything else is a usage error.

Verify this page against your build with `helm mutation-test --help`.

- [Pass-through to helm-unittest](#pass-through-to-helm-unittest)
- [Mutation control](#mutation-control)
- [Execution](#execution)
- [Output](#output)
- [Flag value syntax](#flag-value-syntax)
- [Exit codes](#exit-codes)
- [Validation errors](#validation-errors)
- [Hidden and generated subcommands](#hidden-and-generated-subcommands)

## Pass-through to helm-unittest

These mirror `helm unittest`'s own flags and control how suites are discovered and parsed.

| flag | type | default | purpose |
|---|---|---|---|
| `-f`, `--file` | repeatable string | `tests/*_test.yaml` | Glob paths of test suite files, relative to the chart |
| `-v`, `--values` | repeatable string | *(none)* | Absolute or glob paths of values files that override chart values |
| `-s`, `--with-subchart` | bool | `true` | Include tests of subcharts *(see note below)* |
| `--strict` | bool | `false` | Strictly parse the test suites |
| `--chart-tests-path` | string | *(empty)* | Folder, relative to the chart, holding a chart that renders test suites *(see note below)* |

### `-f`, `--file`

The first `-f` **replaces** the default; further `-f` flags append. So this runs one suite, not the
default glob plus one:

```console
helm mutation-test ./my-chart -f 'tests/deployment_test.yaml'
```

Quote the glob so your shell does not expand it. Suites nested in subdirectories need an explicit
recursive glob:

```console
helm mutation-test ./my-chart -f 'tests/**/*_test.yaml'
```

Suite files are resolved relative to the chart directory, then sorted — glob order is
filesystem-dependent, and sorting keeps runs reproducible.

If no file matches, the run stops with exit code 2. There is nothing to measure.

### `-v`, `--values`

Values files applied on top of the chart's `values.yaml` when suites are parsed, exactly as
`helm unittest -v` does. Repeatable.

```console
helm mutation-test ./my-chart -v ./ci/prod-values.yaml
```

Note this does not change what gets mutated: `values.yaml` in the chart is still the mutation target.

### `--strict`

Passed through to helm-unittest's suite parser. With it on, unknown keys in a suite file are an
error rather than being ignored.

### Notes on `--with-subchart` and `--chart-tests-path`

Both flags are accepted, validated and plumbed through to the runner's options struct, but **no code
currently reads either value**, so setting them has no observable effect in this version. They are
documented here because the CLI accepts them; do not rely on them changing behaviour.

Subchart suites can still be reached with an explicit glob:

```console
helm mutation-test ./my-chart -f 'tests/*_test.yaml' -f 'charts/*/tests/*_test.yaml'
```

## Mutation control

| flag | type | default | purpose |
|---|---|---|---|
| `--mutators` | comma-separated list | *all ten* | Which mutators to run |
| `--exclude-mutators` | comma-separated list | *(none)* | Mutators to skip |
| `--include` | comma-separated globs | `templates/**,values.yaml` | Chart files to mutate |
| `--exclude` | comma-separated globs | *(none)* | Chart files to skip |
| `--max-mutants` | int | `0` (no limit) | Evaluate at most this many mutants, sampled deterministically |
| `--seed` | int | `1` | Sampling seed, for a reproducible `--max-mutants` subset and shard split |
| `--shard-index` | int | `0` | Evaluate only this shard (1-based) of `--shard-count` |
| `--shard-count` | int | `0` (no sharding) | Split the mutants into this many disjoint shards, one per CI job |
| `--since` | git ref | *(none)* | Score only the mutants the changes since this ref could affect |
| `--no-equivalence-check` | bool | `false` | Skip the post-pass that reclassifies unkillable survivors as `Equivalent` |
| `--timeout` | duration | `0` → 10× the baseline, at least 30s | Per-mutant timeout |

### `--mutators`, `--exclude-mutators`

Valid IDs: `bool-flip`, `comparison-swap`, `cond-negate`, `default-drop`, `num-literal`,
`pipe-drop`, `range-empty`, `required-drop`, `str-literal`, `yaml-key-delete`. See [mutators.md](mutators.md).

An unknown ID is rejected before the run starts, rather than silently running fewer mutators than
you asked for:

```console
$ helm mutation-test ./my-chart --mutators cond-negat
Error: --mutators: unknown mutator "cond-negat" (valid: bool-flip, comparison-swap, cond-negate, default-drop, num-literal, pipe-drop, range-empty, required-drop, str-literal, yaml-key-delete)
```

An empty `--mutators` means "all". `--exclude-mutators` is subtracted afterwards, so the two combine:
`--mutators cond-negate,str-literal --exclude-mutators str-literal` runs only `cond-negate`.

Selection order does not affect results — mutators are sorted by ID before generation.

```console
# Only the highest-signal structural mutators, while iterating on tests
helm mutation-test ./my-chart --mutators cond-negate,comparison-swap,default-drop

# Everything except the noisiest one on a chart with large nested blocks
helm mutation-test ./my-chart --exclude-mutators yaml-key-delete
```

### `--include`, `--exclude`

Globs relative to the chart root. `--exclude` wins over `--include`.

A trailing `/**` means "this directory and everything beneath it", which `filepath.Match` alone
cannot express. A pattern with no separator also matches against a path's basename, so `*.tpl`
matches `templates/_helpers.tpl`.

```console
# Templates only; leave values.yaml alone
helm mutation-test ./my-chart --include 'templates/**'

# Everything except the partials
helm mutation-test ./my-chart --exclude '_helpers.tpl'

# One template, for a tight feedback loop
helm mutation-test ./my-chart --include 'templates/deployment.yaml'
```

Only files that are discovered in the first place can be included: everything under `templates/`
with a `.yaml`, `.yml`, `.tpl` or `.txt` extension (`__snapshot__` directories excluded), plus
`values.yaml` or `values.yml` at the chart root. Test files are never mutated.

An empty `--include` falls back to the default list rather than mutating nothing.

### `--max-mutants`, `--seed`

`--max-mutants` samples by shuffling the full plan list with `--seed`, so the subset spans the whole
chart rather than stopping at the first *n* mutants of the first file. Report order is then restored.

The drop count is surfaced in every format — a silently truncated run would read as full coverage:

```console
$ helm mutation-test ./my-chart --max-mutants 20
  Score  100.0%  ██████████████████████████   (19 killed / 0 survived)
  not scored: 1 equivalent
  only 20 of 472 generated mutants were run (--max-mutants); 452 not evaluated
```

A sampled score is not comparable to a full one, even when — as above — the two numbers happen to
match. Use `--max-mutants` for iteration, not for the number you publish.

Negative values are rejected. `0` means no limit.

### `--since`

Incremental mode, for per-PR CI. The diff runs from `git merge-base <ref> HEAD` to the **working
tree**, plus untracked files — so in CI it is exactly the branch's commits, and locally it also sees
the edits you have not committed yet. `--since HEAD` scores only your uncommitted work. Paths outside
the chart directory are ignored.

Each changed chart-relative path is classified:

| changed path | what runs |
|---|---|
| a template under `templates/` (not a `_` partial) | that template's mutants |
| a suite file the baseline discovered (`-f`) | every file that suite covers — including `values.yaml` and partials, which every suite covers |
| `tests/__snapshot__/<suite>.snap` | as if `<suite>` changed |
| anything else: `values.yaml`, a `_*` partial, `Chart.yaml`, `Chart.lock`, `values.schema.json`, `charts/**`, `.helmignore`, a deleted suite, a values file a suite reads, any file a template might read via `.Files.Get` | **a full run**, naming the path that forced it |

Anything not provably attributable falls back to a full run rather than being ignored: guessing that
a change is harmless would let the score flatter itself.

What was left out is named in every format, the same way `--max-mutants` is:

```console
$ helm mutation-test ./my-chart --since origin/main
  changed-files mode (--since origin/main): 1 files inspected, 7 untouched files skipped, 412 mutants not evaluated; score covers the diff only
```

A fallback says why:

```console
  --since origin/main: full run, values.yaml changed, and any template may read any value
```

A diff that touches no mutable chart file scores nothing. Rather than fail `--threshold` on a 0% that
graded nothing, the run exits 0 and says `nothing in the diff to score; threshold not applied`. A
full run that scores nothing still fails the threshold.

`--max-mutants` and `--shard-count` compose: they sample and split the mutants the diff selected.
Every shard must use the same `--since` ref, and `merge` refuses shards whose diffs differ.

**Shallow clones.** `actions/checkout` fetches one commit by default, which has no merge-base with
`origin/main`. The run exits 2 and says so; fetch history first:

```yaml
- uses: actions/checkout@v4
  with:
    fetch-depth: 0
- run: helm mutation-test ./chart --since origin/${{ github.base_ref }} --threshold 70
```

### `--shard-index`, `--shard-count`

Split one run across CI jobs. Each job evaluates a disjoint slice of the mutants and writes its own
JSON report; [`merge`](#merge) recombines them into the run one machine would have produced.

```console
# job k of 4 (GitLab: --shard-index $CI_NODE_INDEX --shard-count $CI_NODE_TOTAL)
$ helm mutation-test ./my-chart --shard-index 2 --shard-count 4 --report json --report-dir shards
  Slice  100.0%  ██████████████████████████   (18 killed / 0 survived)
  shard 2 of 4: 20 of 80 selected mutants evaluated here, 60 in other shards; this is a slice, not a score — merge every shard's JSON report with 'helm mutation-test merge' to grade
```

Shards use the same `--seed` shuffle as `--max-mutants`: the cap is applied first, then shard *k* takes
every *M*-th entry of the shuffled list. So the shards together cover exactly the mutants an unsharded
run would evaluate, each one samples the whole chart rather than a run of files, and their sizes differ
by at most one. Every shard of a run must be given the same chart, flags and `--seed`.

A shard's score covers its slice only, and every format labels it so. **`--threshold` is refused
together with `--shard-count`**: gate on `merge --threshold` instead. Shards exit 0 unless they cannot
run (2).

A shard names its report files for its position — `mutation-report.shard-2-of-4.json` rather than
`mutation-report.json` — so every shard can write into the same directory, and CI can unpack all jobs'
artifacts into one folder without one shard overwriting another. Unsharded and merged runs keep the
plain names.

Indices are 1-based. `--shard-count 1`, or neither flag, means an ordinary unsharded run.

### `merge`

```console
$ helm mutation-test merge shards/mutation-report.shard-*.json --threshold 70 \
    --report console,markdown,html --report-dir ./mutation
```

Reads every shard's JSON report, then reports and grades the union exactly as a single run would:
same mutants, same order, same tally. It takes `--report`, `--report-dir`, `--threshold`,
`--color`/`--no-color`, and `--chart` (where to read chart source for the HTML report; defaults to the
path the shards recorded, which may not exist on the merging machine).

It refuses, with exit 2, anything that would make the merged report cover less than it claims: a
missing or repeated shard, a report that is not a shard, shards whose `--shard-count`, `--seed`, chart,
mutators, `--max-mutants` or `--since` diff differ, a mutant in two shards, or shards whose mutants do not add up to
what the run selected. Exit codes are otherwise those of a normal run.

### `--no-equivalence-check`

By default, after evaluation the tool re-renders every remaining `Survived` mutant to find the ones
that provably cannot change any rendered manifest — see
[equivalent mutants](concepts.md#equivalent-mutants). Those are reclassified `Equivalent` and dropped
from the score's denominator, which raises the score for any chart that has them.

The check costs one extra render pair per non-equivalent survivor, and a render per covering test job
plus a probe for each genuine equivalent — cheap relative to the worker pass, but not free on a chart
with hundreds of survivors. `--no-equivalence-check` skips it, leaving every survivor as `Survived`
exactly as before this feature existed.

The default is on because an undetected equivalent mutant is a false positive in the report: it reads
as a missing assertion that no assertion could ever supply, and it depresses the score by an amount
nobody can fix by writing tests. Disable it only when the render cost matters more than that accuracy,
for example a tight iteration loop with `--max-mutants`.

Every report format states when the check was skipped, the same way every format states a
`--max-mutants` truncation — see [reports.md](reports.md). It also states how many survivors the check
ran over but could not decide, because "the check ran" and "the check concluded" are different claims:
an unloadable chart, a covering suite it had to skip, or a span it could not probe all leave a survivor
unexamined, and each one's reason lands in its own detail text.

### `--timeout`

A Go duration string: `45s`, `2m`, `1m30s`.

Left at `0`, the timeout is derived from the baseline run: **ten times the baseline duration, with a
floor of 30 seconds**. Scaling off the baseline means a slow chart is not cut off spuriously, and the
floor keeps a fast chart from getting a uselessly tight budget.

Set it explicitly when a mutant can plausibly hang — for example a chart with a `range` whose bound
comes from a mutated number:

```console
helm mutation-test ./my-chart --timeout 90s
```

A mutant that exceeds it becomes `Timeout` and is excluded from the score. Negative values are
rejected.

## Execution

| flag | type | default | purpose |
|---|---|---|---|
| `-p`, `--parallel` | int | number of CPUs | Number of concurrent workers |
| `--kill-attribution` | `first` \| `all` | `first` | How much killing-test detail to collect |

### `-p`, `--parallel`

Must be at least 1. Above 1, each worker is a separate OS process — see
[why workers are subprocesses](concepts.md#operational-note-why-workers-are-subprocesses). At
exactly 1, evaluation runs in-process. The worker count is also clamped to the number of mutants,
so a tiny run never spawns idle processes.

Each worker keeps its own temporary chart copy for the life of the run. Results are identical at any
`--parallel` value.

```console
helm mutation-test ./my-chart -p 4
```

### `--kill-attribution`

- **`first`** (default) — stop each mutant's run at the first failing test. One kill is enough to
  classify a mutant, so this is the fast path.
- **`all`** — run every covering test and record the complete set of killing tests in
  `mutant.killedBy`.

Use `all` when you want to know which assertions overlap — for example to find redundant tests. It
costs real time. On the fixture chart's three `required-drop` mutants, tests run per mutant:

| mutant | `first` | `all` |
|---|---:|---:|
| `templates/deployment.yaml:74` | 9 | 17 |
| `templates/deployment.yaml:76` | 10 | 17 |
| `templates/ingress.yaml:21` | 3 | 4 |

`all` runs every covering test in the mutant's suites; `first` stops the moment one fails.

The baseline run never fails fast regardless of this flag; it needs the complete picture to name
every broken test at once.

Any value other than `first` or `all` is rejected.

## Output

| flag | type | default | purpose |
|---|---|---|---|
| `--report` | comma-separated list | `console` | Report formats: `console`, `json`, `html`, `markdown`, `junit`, `sarif` |
| `--report-dir` | string | `.helm-mutation-test` | Directory for file reports |
| `--threshold` | float | `0` (disabled) | Exit 1 if the mutation score is below this percentage |
| `--color` | bool | `false` | Force coloured output even when stdout is not a terminal |
| `--no-color` | bool | `false` | Disable coloured output |
| `-d`, `--debug` | bool | `false` | Verbose output, including worker stderr |
| `--keep-workdir` | bool | `false` | Keep the temporary chart copies for inspection |

### `--report`, `--report-dir`

Any combination, comma-separated. Values are lower-cased, so `--report JSON` works. Each format is
documented with real sample output in [reports.md](reports.md).

`console` writes to stdout and never touches disk. Omit it and you get no terminal summary at all:

```console
$ helm mutation-test ./my-chart --report json --report-dir ./out
  json report: out/mutation-report.json
```

`--report-dir` is created if missing, and **only** when a non-console format was requested — a plain
`helm mutation-test ./my-chart` leaves no `.helm-mutation-test` directory behind. Filenames are
fixed, except that a shard inserts its position after the stem
(`mutation-report.shard-2-of-4.json`; see [`--shard-index`](#--shard-index---shard-count)):

| format | file(s) written |
|---|---|
| `console` | *(stdout only)* |
| `json` | `mutation-report.json` |
| `markdown` | `mutation-report.md` |
| `junit` | `mutation-report.junit.xml` |
| `sarif` | `mutation-report.sarif` |
| `html` | `mutation-report.html` **and** `mutation-report.stryker.json` |

`html` writes two files on purpose: the standard-schema JSON is useful on its own, since that is what
other mutation-testing tooling consumes.

An unknown format is rejected before the run:

```console
$ helm mutation-test ./my-chart --report bogus
Error: unknown --report format "bogus" (valid: console, json, html, markdown, junit, sarif)
```

### `--threshold`

A percentage in `[0, 100]`. `0` disables the check. Below the threshold, the run still completes and
writes every report, then exits 1:

```console
$ helm mutation-test ./my-chart --threshold 70
...
Error: mutation score 2.3% is below the 70.0% threshold
$ echo $?
1
```

The console and markdown formats also state pass or fail against the threshold explicitly, and the
JSON report carries `"threshold"` and `"passed"` fields.

Values outside `[0, 100]` are rejected.

### `--color`, `--no-color`

Three-state. Without either flag, colour is auto-detected from whether stdout is a character device.
`--color` forces it on, `--no-color` forces it off, and **`--no-color` wins if both are given**.

`--color` is for piping into something that renders ANSI (a CI log viewer); `--no-color` is for
capturing clean text.

### `-d`, `--debug`

Prints each phase to stderr as it starts, and surfaces worker stderr instead of discarding it. It
also stops the tool silencing the Helm and helm-unittest libraries' own logging, so expect a lot of
`level=debug` lines interleaved with the phases:

```console
$ helm mutation-test ./my-chart -d 2>&1 >/dev/null | grep '^==>'
==> Running the chart's tests unmutated
==> Planning mutants
==> Evaluating 472 mutants across 12 workers
```

A `--max-mutants` run adds a phase line naming the cap and the drop count; a `--since` run adds one
listing the diff and one naming what it skipped, or why it fell back to a full run.

### `--keep-workdir`

Leaves each worker's temporary chart copy on disk instead of deleting it. Useful when a mutant comes
back `Invalid` or `Error` and you want to run `helm template` or `helm unittest` against the exact
tree the tool used.

## Flag value syntax

Two list styles, and the difference matters:

- **Repeatable arrays** — `-f`/`--file` and `-v`/`--values`. Give the flag once per value. Commas are
  *not* split, so a comma is a literal part of the glob.
- **Comma-separated slices** — `--mutators`, `--exclude-mutators`, `--include`, `--exclude`,
  `--report`. `--report console,json` and `--report console --report json` are equivalent.

Boolean flags with a `true` default need explicit `=false` to turn off: `--with-subchart=false`, not
`--with-subchart false`.

## Exit codes

| code | meaning |
|---|---|
| `0` | Ran successfully, and met `--threshold` if one was set |
| `1` | The mutation score is below `--threshold` |
| `2` | Could not run: the chart's own tests do not pass, or a configuration/usage error, or the tool failed |

Code 2 covers, verified:

- the chart's tests failing before mutation (the [baseline gate](concepts.md#the-baseline-gate-is-a-hard-stop));
- no suite matching `-f`, or matching suites with no test jobs;
- a chart path that does not exist or does not load;
- any flag validation failure;
- `merge` given an incomplete, inconsistent or unreadable set of shard reports;
- wrong number of positional arguments;
- a `--since` ref with no merge-base with `HEAD` (unknown ref, shallow clone, not a git checkout);
- a report file that could not be written.

The split between 1 and 2 is what lets CI distinguish "your tests are weak" from "the tool could not
run". Only code 1 is a finding about your suite.

## Validation errors

Configuration is validated before the baseline runs, so a typo costs no time. All of these exit 2.

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
| bad `--exclude-mutators` | as above, prefixed `--exclude-mutators:` |

Rejecting an unknown mutator ID rather than ignoring it is deliberate: silently running fewer
mutators than asked for would produce a score that looks complete and is not.

Failures found after validation — a chart that does not load, a baseline that is red — also exit 2,
but only after the relevant work has been attempted:

```console
$ helm mutation-test ./nope
Error: loading chart ./nope: stat ./nope: no such file or directory
```

## Hidden and generated subcommands

- `merge`: not hidden; see [`merge`](#merge).

- `__worker` — hidden. The parent process re-executes the binary with this argument to spawn an
  evaluation worker. Not for direct use.
- `completion` — cobra's generated shell-completion command
  (`helm mutation-test completion bash|zsh|fish|powershell`).
- `help` — cobra's generated help command.
