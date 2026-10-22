---
hide:
  - navigation
  - toc
---

<div class="hmt-hero" markdown>

![helm-mutation-test logo](images/logo.png){ width="160" }

# helm-mutation-test

**Mutation testing for Helm charts.** Measures how much your
[helm-unittest](https://github.com/helm-unittest/helm-unittest) suites actually assert.

[Get started](getting-started.md){ .md-button .md-button--primary }
[CLI reference](cli-reference.md){ .md-button }
[GitHub](https://github.com/mmpyro/helm-mutation-test){ .md-button }

</div>

`helm unittest -t XUnit -o '/Users/mmarszalek/playground/helm-mutation-test/reports/.heatmap-bash.xml'` tells you whether your chart tests **pass**. It cannot tell you whether they are
**worth anything**. This plugin breaks the chart on purpose — one small change at a time — and
re-runs your existing suites against each one. A change that no test objects to is a **survived
mutant**: a precise, actionable pointer at a missing assertion.

```sh
helm mutation-test ./my-chart                                   # score the chart's own suites
helm mutation-test ./my-chart --threshold 70 --report markdown  # gate CI on the score
helm mutation-test ./my-chart --since origin/main               # only what the branch changed
```

## One chart, two green suites

`testdata/charts/sample` ships two suites over the same templates. Both pass, and `helm unittest`
reports them as equally green. The same 472 mutants, run against each:

| suite | killed | survived | equivalent | no-coverage | invalid | score |
|---|---:|---:|---:|---:|---:|---:|
| `weak_test.yaml` | 6 | 255 | 24 | 173 | 14 | **2.3%** |
| `strong_test.yaml` | 426 | 0 | 26 | 0 | 20 | **100.0%** |

Run `make demo` to reproduce both. The walk-through is in [Getting started](getting-started.md).

## Why helm-mutation-test

<div class="grid cards" markdown>

-   :material-scale-balance:{ .lg .middle } **A score that never flatters itself**

    ---

    `killed / (killed + survived)`. Everything else — no coverage, invalid, timeouts, capped
    mutants — is excluded *and named* in every report.

    [:octicons-arrow-right-24: The mutation score](concepts.md#the-mutation-score)

-   :material-equal:{ .lg .middle } **Equivalent mutants detected**

    ---

    Survivors that provably cannot change any rendered manifest leave the denominator — and only
    on proof, never on a guess.

    [:octicons-arrow-right-24: Equivalent mutants](concepts.md#equivalent-mutants)

-   :material-target:{ .lg .middle } **Coverage-aware**

    ---

    Each mutant runs only the suites that render its file. A template no suite renders is a
    finding of its own.

    [:octicons-arrow-right-24: Suite selection](concepts.md#coverage-aware-suite-selection)

-   :material-source-branch:{ .lg .middle } **Incremental with `--since`**

    ---

    Score only the mutants a branch could have moved, falling back to a full run — saying why —
    when a change cannot be attributed.

    [:octicons-arrow-right-24: `--since`](cli-reference.md#--since)

-   :material-view-split-vertical:{ .lg .middle } **Sharded across CI jobs**

    ---

    Shards partition exactly the unsharded selection; `merge` refuses an incomplete set before it
    grades.

    [:octicons-arrow-right-24: Sharding](cli-reference.md#--shard-index---shard-count)

-   :material-file-chart:{ .lg .middle } **Six report formats**

    ---

    Console, JSON, HTML, Markdown for `$GITHUB_STEP_SUMMARY`, JUnit and SARIF for code scanning.

    [:octicons-arrow-right-24: Reports](reports.md)

</div>

## Install

Pick how you run Helm. The choice is remembered across the docs.

=== "Helm 4"

    ```sh
    # Helm 4 declines unsigned plugin sources by default
    helm plugin install https://github.com/mmpyro/helm-mutation-test --verify=false
    ```

=== "Helm 3"

    ```sh
    helm plugin install https://github.com/mmpyro/helm-mutation-test
    ```

=== "From a checkout"

    ```sh
    make install    # builds bin/helm-mutation-test and links this tree as a Helm plugin
    ```

Prebuilt, checksum-verified binaries cover linux and macOS on amd64 and arm64; anywhere else the
plugin builds from source and needs Go.

The latest release is [**v1.2.0**](https://github.com/mmpyro/helm-mutation-test/releases/tag/v1.2.0),
which adds the [`pipe-drop`](mutators.md#pipe-drop) mutator. Pin it with `--version v1.2.0`, or
[download the binaries directly](getting-started.md#1-install).

## Guides

| Guide | Read it when you want to… |
|---|---|
| [Getting started](getting-started.md) | install, score a chart, read the output, wire it into CI |
| [Concepts](concepts.md) | understand the score, the statuses, the baseline gate and equivalence |
| [CLI reference](cli-reference.md) | look up a flag, its default, or an exit code |
| [Mutators](mutators.md) | see what each of the ten mutators changes and which assertion it exposes |
| [Reports](reports.md) | pick an output format and parse it |
| [Decisions](adr/0001-helm-mutation-test-framework.md) | read why the tool is built the way it is |
