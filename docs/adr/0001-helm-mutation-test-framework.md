# ADR-0001: Helm Mutation Test Framework Architecture

**Date:** 2026-07-29  
**Status:** Accepted  
**Deciders:** Project maintainers  
**Spec:** `docs/specs/2026-07-29-helm-mutation-test-design.md` (historical specification)

---

## Context

Helm chart developers use `helm-unittest` to verify that their Go templates (`text/template`) and `values.yaml` generate expected Kubernetes resource manifests. However, traditional unit test suites can easily pass 100% of their test cases while failing to assert critical configuration details (e.g., ports, environment variables, ingress annotations, or replica counts).

Mutation testing evaluates unit test suite quality by systematically introducing small artificial defects ("mutants") into source files and checking whether the unit test suite catches (kills) them. Prior to this framework, no automated mutation testing tool existed specifically designed for Helm charts and `helm-unittest`.

Key technical challenges in building a Helm mutation testing tool:
1. **Whitespace & Trim Markers:** Helm template parsing depends on whitespace-sensitive trim markers (`{{-` and `-}}`). Naive AST pretty-printing destroys formatting and trim semantics.
2. **Global State & Concurrency in `helm-unittest`:** `helm-unittest` mutates Helm's package-level global variables (such as `chartutil.DefaultCapabilities`) during test execution, causing severe data races and cross-test pollution when executed concurrently in Go goroutines.
3. **AST Quirks:** Go's `text/template/parse` AST has non-obvious quirks (e.g. `FieldNode.Position()` offset shifts, typed-nil `ElseList` on `IfNode` without `else`).

## Decision

We designed and built `helm-mutation-test`, a CLI tool and Helm plugin that parses Helm charts, generates deterministic mutations, executes `helm-unittest` suites against mutated charts, and reports test suite coverage and quality.

```
discover → baseline gate → coverage indexing → candidate generation → worker subprocess execution → classification → reporting
```

### Key Architectural Decisions

| Component / Layer | Choice / Design | Rationale |
|---|---|---|
| **Source Mutation (`internal/source`)** | Parse with AST, apply via byte-range edits | Parses files using Go's `text/template/parse` and `gopkg.in/yaml.v3` AST to accurately identify target nodes and spans, but applies modifications as byte-range replacements directly on original source bytes. This preserves all trim markers (`{{- ... -}}`), comments, and raw formatting. |
| **Worker Subprocesses (`internal/runner/worker.go`)** | Long-lived worker subprocesses over IPC | `helm-unittest` mutates global state (`chartutil.DefaultCapabilities`) during execution. Running tests in worker subprocesses isolates global state across concurrent workers while avoiding worker startup overhead per mutant via streaming IPC. |
| **Workspace Isolation (`internal/workspace`)** | Isolated temp directory per worker | Each worker process operates on an isolated directory copy of the chart under test, preventing write collisions and template state contamination. |
| **`helm-unittest` Seam (`internal/runner/unittest.go`)** | Invoke `TestSuite.RunV3` directly | Uses `TestSuite.RunV3` instead of high-level CLI wrappers to retrieve the detailed test assertion tree. This enables attributing kills to specific assertions and distinguishing failed assertions (`Killed`) from template render errors (`Invalid`). |
| **Baseline Gate (`internal/runner/baseline.go`)** | mandatory pre-flight baseline check | Before mutating, runs tests on the unmutated chart. If baseline tests fail, execution aborts immediately with exit code 2, preventing false-positive kills from a broken baseline. |
| **Coverage Indexing (`internal/coverage`)** | Pre-compute template-to-suite mapping | Maps each template file to the test suite files that explicitly reference it. Mutants are executed only against covering suites, drastically reducing execution time. |
| **Mutator Registry (`internal/mutator`)** | Extensible registry of AST mutators | Implements eight core initial mutators (`bool-flip`, `comparison-swap`, `cond-negate`, `default-drop`, `num-literal`, `required-drop`, `str-literal`, `yaml-key-delete`). Each mutator includes guard rules to minimize `Invalid` mutants (mutants that fail at render time and grade nothing). |
| **Classification & Scoring (`internal/model`, `internal/runner/classify.go`)** | Standardized statuses and score formula | Classifies mutants into `Killed`, `Survived`, `NoCoverage`, `Invalid`, and `Timeout`. Score is calculated strictly as `Score = Killed / (Killed + Survived) * 100%`. Excluded statuses (`NoCoverage`, `Invalid`, `Capped`) do not depress the denominator. |
| **Multi-Format Reporting (`internal/report`)** | Pure rendering functions over `model.Run` | Supports Console (colored unified diffs), JSON (canonical representation), HTML (Stryker `mutation-testing-elements` schema with offline fallback), Markdown (`GITHUB_STEP_SUMMARY`), and JUnit XML. Exclusions are explicitly named across all formats. |

### Source AST Handling Quirks

The `internal/source` package explicitly handles two critical `text/template/parse` quirks:
- `FieldNode.Position()` points past the field's leading dot/identifier rather than its start offset. `CommandArgSpan` guards against this to prevent shifted byte replacement spans.
- `IfNode` without an `else` branch contains a typed-nil `ElseList` pointer, which standard nil checks miss and would cause nil-pointer dereference panics during AST traversal.

## Consequences

### Positive

- **Deterministic & Format-Preserving Mutations:** Byte-range edits on raw source preserve Helm trim markers and formatting without template corruption.
- **Race-Free Parallelism:** Subprocess worker architecture bypasses `helm-unittest` global state pollution and enables safe `-p` parallel execution.
- **Accurate Fault Attribution:** Direct integration with `TestSuite.RunV3` distinguishes assertion failures (`Killed`) from parse/render failures (`Invalid`).
- **Performance:** Coverage indexing ensures mutants execute only against relevant test suites rather than running all test suites for every mutant.
- **Standardized Reporting:** Supports native CI integration (`GITHUB_STEP_SUMMARY`, JUnit XML) and standard mutation web UI standards (Stryker format).

### Negative / Risks

- Worker subprocesses introduce IPC overhead and process management complexity compared to pure goroutines.
- Dependency on `helm-unittest` internals requires matching `go.mod` replace directives (`yaml-jsonpath`).

## Out of Scope (Initial Version)

- In-process execution of `helm-unittest` for parallel workers (blocked by `helm-unittest` global state mutations).
- Equivalent mutant detection (addressed in ADR-0002).
- `range` loop mutations (addressed in ADR-0003).
