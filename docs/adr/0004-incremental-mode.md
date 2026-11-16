# ADR-0004: Incremental mode (`--since <ref>`)

**Date:** 2026-10-10  
**Status:** Accepted  
**Deciders:** Project maintainers

---

## Context

Every run evaluates the full set of mutants. For the `sample` fixture, that is 458 mutants in under two
seconds. On a production chart, it takes 2 to 30 minutes. So the tool stays in nightly CI and out of
per-PR checks, but a per-PR check is where a missing assertion costs least to fix. The Known limits
section of `README.md` and the Out of scope section of `CLAUDE.md` both named this problem.

Mutant generation already works per file, and `internal/coverage` already knows which suites render
which file. The structure therefore makes narrowing cheap. The risk is the score. If a run silently
evaluates a part of the chart, the result reads as full coverage. `--max-mutants` was built to avoid
this failure.

## Decision

Add `--since <ref>`. The diff goes from `git merge-base <ref> HEAD` to the working tree, and it
includes untracked files. It is restricted to the chart directory (`git diff --relative`).

### Selection

| changed path | runs |
|---|---|
| non-partial template | that file's mutants |
| baseline suite file, or its `__snapshot__/*.snap` | every file the suite covers, via `coverage.Index.For` |
| anything else | everything, with the triggering path recorded as `fullRunReason` |

"Anything else" is broad on purpose:

- `values.yaml` and partials affect every template.
- `Chart.yaml` feeds `.Chart.*`.
- Subcharts, `.helmignore` and arbitrary files can reach rendering through `.Files.Get`.
- The tool cannot map a deleted suite, or a values file that a suite reads, to the mutants whose
  verdict it moved.

If the tool guessed that any of these is harmless, the score could flatter itself. So all of them
fall back to a full run.

### Reporting

`model.Run.Incremental` holds the base, the merge-base commit, the changed files, the files inspected
and skipped, the mutants skipped and the full-run reason. Every format names it next to the
`--max-mutants` notice, with one shared wording (`report.incrementalNotices`). `generated` still means
"before any narrowing", so `generated − mutantsSkipped − capped == len(mutants)`.

### Empty diff

If a diff contains no scored mutant, the run exits 0 and says `nothing in the diff to score; threshold not applied`.
Consider a PR that touches no chart file. If the tool failed it on a 0% that graded nothing, every
unrelated change would pay for an empty check. A full run that scores nothing still fails.

## Alternatives rejected

| alternative | why not |
|---|---|
| A separate `--changed-only` flag | Equivalent to `--since HEAD`. |
| Committed changes only (`ref...HEAD`) | Local runs would miss the edits in progress. |
| Re-scoring templates that include a changed partial | This needs an include graph. A full run is the simple, honest answer until a chart shows that the cost matters. |
| Silently falling back to a full run on a git error | A bad ref or a shallow clone would hide behind a slow run that looks normal. Instead, the run exits 2 and names `fetch-depth: 0`. |

## Consequences

- Per-PR CI becomes practical for PRs that touch templates or tests.
- PRs that touch `values.yaml` or a partial get no benefit, and these PRs are common. The run prints
  the reason.
- `--since` needs `git` on `PATH`. Full runs do not change.
