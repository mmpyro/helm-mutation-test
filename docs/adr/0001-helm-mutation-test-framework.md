# ADR-0001: Helm Mutation Test Framework Architecture

**Date:** 2026-07-29  
**Status:** Accepted  
**Deciders:** Project maintainers  
**Spec:** `docs/specs/2026-07-29-helm-mutation-test-design.md` (historical specification)

---

## Context

Helm chart developers use `helm-unittest` to check that their Go templates (`text/template`) and `values.yaml` generate the expected Kubernetes resource manifests. A unit test suite can pass 100% of its test cases and still assert nothing about important configuration details. Examples are ports, environment variables, ingress annotations and replica counts.

Mutation testing measures the quality of a unit test suite. It puts small artificial defects ("mutants") into the source files, one at a time. Then it checks whether the suite catches (kills) each one. Before this framework, no automated mutation testing tool existed for Helm charts and `helm-unittest`.

A Helm mutation testing tool must solve three technical problems:
1. **Whitespace & Trim Markers:** Helm template parsing depends on trim markers (`{{-` and `-}}`), which are sensitive to whitespace. A simple AST pretty-printer destroys the formatting and changes what the trim markers do.
2. **Global State & Concurrency in `helm-unittest`:** During test execution, `helm-unittest` changes package-level global variables in Helm, such as `chartutil.DefaultCapabilities`. When tests run concurrently in Go goroutines, this causes serious data races, and one test's state leaks into another.
3. **AST Quirks:** The `text/template/parse` AST in Go has quirks that are not obvious. For example, `FieldNode.Position()` gives a shifted offset, and an `IfNode` without `else` has a typed-nil `ElseList`.

## Decision

We designed and built `helm-mutation-test`, a CLI tool and Helm plugin. It parses Helm charts and generates deterministic mutations. It runs the `helm-unittest` suites against the mutated charts. Then it reports the coverage and quality of the test suites.

```
discover → baseline gate → coverage indexing → candidate generation → worker subprocess execution → classification → reporting
```

### Key Architectural Decisions

| Component / Layer | Choice / Design | Rationale |
|---|---|---|
| **Source Mutation (`internal/source`)** | Parse with AST, apply via byte-range edits | The tool parses files with the `text/template/parse` and `gopkg.in/yaml.v3` ASTs to find the exact target nodes and spans. It applies each change as a byte-range replacement on the original source bytes. This keeps all trim markers (`{{- ... -}}`), comments and raw formatting. |
| **Worker Subprocesses (`internal/runner/worker.go`)** | Long-lived worker subprocesses over IPC | `helm-unittest` changes global state (`chartutil.DefaultCapabilities`) during execution. Worker subprocesses keep this global state separate for each concurrent worker. Streaming IPC removes the cost of starting a worker for each mutant. |
| **Workspace Isolation (`internal/workspace`)** | Isolated temp directory per worker | Each worker process uses its own copy of the chart directory. This prevents write collisions, and one worker cannot contaminate the template state of another. |
| **`helm-unittest` Seam (`internal/runner/unittest.go`)** | Invoke `TestSuite.RunV3` directly | The tool calls `TestSuite.RunV3`, not the high-level CLI wrappers, so it gets the full tree of test assertions. With this tree, the tool can attribute a kill to a specific assertion. It can also tell a failed assertion (`Killed`) from a template render error (`Invalid`). |
| **Baseline Gate (`internal/runner/baseline.go`)** | mandatory pre-flight baseline check | Before it mutates anything, the tool runs the tests on the unmutated chart. If a baseline test fails, execution stops immediately with exit code 2. A broken baseline would otherwise produce false-positive kills. |
| **Coverage Indexing (`internal/coverage`)** | Pre-compute template-to-suite mapping | The index maps each template file to the test suite files that reference it explicitly. The tool runs a mutant only against the suites that cover it, which cuts execution time by a large amount. |
| **Mutator Registry (`internal/mutator`)** | Extensible registry of AST mutators | The first version has eight core mutators (`bool-flip`, `comparison-swap`, `cond-negate`, `default-drop`, `num-literal`, `required-drop`, `str-literal`, `yaml-key-delete`). Each mutator has guard rules that keep the number of `Invalid` mutants low. An `Invalid` mutant fails at render time and grades nothing. |
| **Classification & Scoring (`internal/model`, `internal/runner/classify.go`)** | Standardized statuses and score formula | The tool classifies each mutant as `Killed`, `Survived`, `NoCoverage`, `Invalid` or `Timeout`. The score formula is strictly `Score = Killed / (Killed + Survived) * 100%`. The excluded statuses (`NoCoverage`, `Invalid`, `Capped`) do not depress the denominator. |
| **Multi-Format Reporting (`internal/report`)** | Pure rendering functions over `model.Run` | The supported formats are Console (colored unified diffs), JSON (the canonical form), HTML (the Stryker `mutation-testing-elements` schema, with an offline fallback), Markdown (`GITHUB_STEP_SUMMARY`) and JUnit XML. All formats name the exclusions explicitly. |

### Source AST Handling Quirks

The `internal/source` package handles two important `text/template/parse` quirks explicitly:
- `FieldNode.Position()` points past the leading dot or identifier of the field, not to its start offset. `CommandArgSpan` guards against this, so the byte replacement spans do not shift.
- An `IfNode` without an `else` branch has a typed-nil `ElseList` pointer. A standard nil check misses it, and the AST traversal then panics with a nil-pointer dereference.

## Consequences

### Positive

- Deterministic & Format-Preserving Mutations: Byte-range edits on the raw source keep the Helm trim markers and the formatting. They do not corrupt the template.
- Race-Free Parallelism: Worker subprocesses avoid the global state pollution in `helm-unittest`. This makes parallel execution with `-p` safe.
- Accurate Fault Attribution: The tool calls `TestSuite.RunV3` directly, so it can tell assertion failures (`Killed`) from parse or render failures (`Invalid`).
- Performance: Because of the coverage index, the tool runs each mutant only against the relevant test suites. It does not run all test suites for every mutant.
- Standardized Reporting: The tool supports native CI integration (`GITHUB_STEP_SUMMARY`, JUnit XML) and the standard format for mutation web UIs (Stryker format).

### Negative / Risks

- Worker subprocesses add IPC overhead. Process management is also more complex than with pure goroutines.
- Because the tool depends on `helm-unittest` internals, it needs matching `go.mod` replace directives (`yaml-jsonpath`).

## Out of Scope (Initial Version)

- In-process execution of `helm-unittest` for parallel workers. The global state mutations in `helm-unittest` block this.
- Equivalent mutant detection (addressed in ADR-0002).
- `range` loop mutations (addressed in ADR-0003).
