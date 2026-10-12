# ADR-0004: Incremental mode (`--since <ref>`)

**Date:** 2026-10-10  
**Status:** Accepted  
**Deciders:** Project maintainers

---

## Context

Every run evaluates the full mutant set: 458 for the `sample` fixture in under two seconds, but 2–30
minutes on a production chart. That keeps the tool in nightly CI, out of per-PR checks — where a
missing assertion is cheapest to fix. Both `README.md`'s Known limits and `CLAUDE.md`'s Out of scope
named it.

Mutant generation already works per file, and `internal/coverage` already knows which suites render
which file, so narrowing is structurally cheap. The risk is the score: a run that silently evaluates
a fraction of the chart reads as full coverage, the failure `--max-mutants` was built to avoid.

## Decision

Add `--since <ref>`. The diff is `git merge-base <ref> HEAD` → working tree, plus untracked files,
restricted to the chart directory (`git diff --relative`).

### Selection

| changed path | runs |
|---|---|
| non-partial template | that file's mutants |
| baseline suite file, or its `__snapshot__/*.snap` | every file the suite covers, via `coverage.Index.For` |
| anything else | everything, with the triggering path recorded as `fullRunReason` |

"Anything else" is deliberately broad: `values.yaml` and partials affect every template; `Chart.yaml`
feeds `.Chart.*`; subcharts, `.helmignore` and arbitrary files can reach rendering through
`.Files.Get`; a deleted suite or a values file a suite reads cannot be mapped to the mutants whose
verdict it moved. Guessing any of these harmless would let the score flatter itself, so they all
fall back to a full run.

### Reporting

`model.Run.Incremental` carries base, merge-base commit, changed files, files inspected/skipped,
mutants skipped and the full-run reason. Every format names it next to the `--max-mutants` notice,
using one shared wording (`report.incrementalNotices`). `generated` keeps meaning "before any
narrowing", so `generated − mutantsSkipped − capped == len(mutants)`.

### Empty diff

A diff with no scored mutant exits 0 and says `nothing in the diff to score; threshold not applied`.
Failing a PR that touches no chart file on a 0% that graded nothing would make every unrelated change
pay for an empty check. A full run that scores nothing still fails.

## Alternatives rejected

| alternative | why not |
|---|---|
| A separate `--changed-only` flag | Equivalent to `--since HEAD`. |
| Committed changes only (`ref...HEAD`) | Local runs would miss the edits being made. |
| Re-scoring templates that include a changed partial | Needs an include graph; a full run is the simple honest answer until a chart shows the cost matters. |
| Silently falling back to a full run on a git error | A bad ref or shallow clone would hide behind a slow, apparently-normal run. It exits 2 instead and names `fetch-depth: 0`. |

## Consequences

- Per-PR CI becomes viable for PRs that touch templates or tests.
- PRs touching `values.yaml` or a partial — common — gain nothing; the reason is printed.
- `git` must be on `PATH` for `--since`; full runs are unaffected.
