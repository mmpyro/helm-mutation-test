# Black-Box Integration Tests Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A black-box integration suite that runs the built plugin binary against the fixture chart and asserts every report format and the CLI's documented behaviour, plus a `make integration-tests` target.

**Architecture:** A new `test/integration` package, guarded by `//go:build integration` so `go test ./...` and `make test` are unchanged. `TestMain` builds the binary from source into a temp dir; a single `run()` helper invokes it and returns stdout, stderr, exit code and the report directory, with typed accessors that parse each artifact. Two full-fixture runs (strong and weak suite, all five formats) are computed lazily and shared read-only; everything else uses a cheap `--max-mutants` run. Assertions are structural and cross-format — no golden files, no hardcoded mutant counts.

**Tech Stack:** Go 1.24 standard library only (`os/exec`, `encoding/json`, `encoding/xml`, `sync.OnceValues`). No new dependencies.

## Global Constraints

- Module is `github.com/mmpyro/helm-mutation-test`. Go 1.24.
- **No new dependencies.** Standard library only.
- Every file in `test/integration/` starts with `//go:build integration` followed by a blank line, then `package integration`.
- **Never hardcode fixture counts** (458 mutants, 95.2%, 275 survivors). Derive every expected value from the JSON report of the same run. The one permitted exception is the count of registered mutators (8), which is a registry invariant with its own unit test.
- **No `t.Parallel()`** anywhere in this package. The tool defaults `--parallel` to `NumCPU` and saturates the machine; overlapping runs trade a modest wall-clock win for flaky timing.
- Tests are behaviour-first and named for the property they protect, with a comment saying *why* when it is not obvious. Match the existing style in `internal/report/report_test.go`.
- `gofmt` clean and `go vet` clean: `make lint` must pass. Note `make lint` only checks `cmd` and `internal`, so run `gofmt -l test` by hand.
- Run the integration suite with `-tags=integration -count=1`. Without `-count=1` Go caches the result, which is misleading for tests that exec a binary.
- The fixture chart is `testdata/charts/sample`, passed as a **relative** path because `cmd.Dir` is the repo root. Its `chartPath` in reports is therefore exactly `testdata/charts/sample`.
- **A test that calls `r.JSON(t)` must pass `--report json`** (or a list containing it). `--report` defaults to `console` only, which writes no files, so the accessor fails with "no such file or directory". The same applies to `Markdown`, `JUnit`, `Stryker` and `HTML`. The shared `strong(t)` / `weak(t)` runs already request every format. This was the single most common mistake when the plan's code was first validated.

## Verified Reference Data

Every string below was measured against the current build on 2026-07-30. Do not
paraphrase them in assertions.

**Report filenames** (in `--report-dir`, from `internal/report/write.go`):
`mutation-report.json`, `mutation-report.md`, `mutation-report.junit.xml`,
`mutation-report.html`, `mutation-report.stryker.json`. `--report html` writes
**both** the HTML and the stryker JSON.

**Exit codes and messages:**

| invocation | exit | where | text |
|---|---|---|---|
| `./does-not-exist` | 2 | stderr | `Error: loading chart ./does-not-exist: stat ./does-not-exist: no such file or directory` |
| `-f 'tests/nope_*.yaml'` | 2 | stderr | `no test suites matched [tests/nope_*.yaml] under testdata/charts/sample` — routed through the baseline-failure path, so the "meaningless" guidance also prints |
| `--mutators cond-negat` | 2 | stderr | `Error: --mutators: unknown mutator "cond-negat" (valid: bool-flip, comparison-swap, cond-negate, default-drop, num-literal, required-drop, str-literal, yaml-key-delete)` |
| `--threshold 150` | 2 | stderr | `Error: --threshold must be between 0 and 100, got 150` |
| `--report yaml` | 2 | stderr | `Error: unknown --report format "yaml" (valid: console, json, html, markdown, junit)` |
| `--max-mutants -1` | 2 | stderr | `Error: --max-mutants cannot be negative, got -1` |
| `--kill-attribution some` | 2 | stderr | `Error: --kill-attribution must be "first" or "all", got "some"` |
| no chart argument | 2 | stderr | `Error: accepts 1 arg(s), received 0` |
| two chart arguments | 2 | stderr | `Error: accepts 1 arg(s), received 2` |
| weak `--threshold 50` | 1 | stdout + stderr | stdout `score 2.1% is below the 50.0% threshold`; stderr `Error: mutation score 2.1% is below the 50.0% threshold` |
| strong `--threshold 70` | 0 | stdout | `score 94.7% meets the 70.0% threshold` (value varies with `--max-mutants`) |
| red baseline | 2 | stderr | `Cannot measure mutation score:` … `A score measured against failing tests is meaningless.` … `Fix the suite first, then re-run.` — and **no report directory is created** |

**Breaking the baseline:** copy `testdata/charts/sample`, then in `tests/weak_test.yaml`
replace the first `of: Deployment` with `of: StatefulSet`. Verified to produce the
hard stop above.

**Cap disclosure**, for `--max-mutants 20` on the 458-mutant fixture:

- console: `only 20 of 458 generated mutants were run (--max-mutants); 438 not evaluated`
- markdown: `` > ⚠️ `--max-mutants` ran 20 of 458 generated mutants; 438 were not evaluated. ``
- html: `--max-mutants ran 20 of 458 generated`
- json: `"generated": 458, "capped": 438`
- junit: **nothing** — the gap Task 1 fixes.

**Other verified behaviour:** `--color` emits ANSI escapes through a pipe; the
default through a pipe emits none. `--exclude 'templates/**'` leaves only
`values.yaml` mutants, and `--include templates/service.yaml` leaves only that
file. `--kill-attribution=first` gives every killed mutant exactly one `killedBy`;
`all` reaches 15 on the fixture. `--seed 1` twice gives an identical subset;
`--seed 99` gives a different one. `--parallel 1` and `--parallel 4` agree on
every mutant ID and status. The HTML survivor list is uncapped: 21 `<li>` entries
for the strong run's 21 survivors.

**`mutators` in the JSON preserves command-line order**, because
`config.EnabledMutators` does. `--mutators cond-negate,comparison-swap` reports
`["cond-negate","comparison-swap"]`, while `byMutator` is sorted ascending by
score then name, giving `["comparison-swap","cond-negate"]`. Do not assert a
sorted `mutators` array.

**The weak run's markdown truncation note** is `_235 further survivors omitted`
alongside 40 `` ```diff `` blocks, for 275 survivors. Parse it with a regexp: the
first `_` in the file belongs to `` `templates/_helpers.tpl` `` in the byFile table.

**Wall clock:** full strong run with all five formats 2.8s; `--max-mutants 40` run
0.12s. Whole suite should land under 20s, versus ~3min for `make test`.

**Markdown survivor cap:** `maxMarkdownSurvivors = 40` in `internal/report/markdown.go`.
Assert the *relationship* (`shown + omitted == total`), not the number.

---

### Task 1: JUnit discloses the `--max-mutants` cap

`internal/report/junit.go` is the only format that stays silent about truncation,
which contradicts CLAUDE.md ("If you add a status or a cap, surface it in all five
formats") and `docs/reports.md` ("any `--max-mutants` truncation appear[s] in all
five"). A CI UI fed only this XML would read a 20-mutant sample as full coverage.
Fix it first, so Task 4's cross-format assertion can cover all five.

**Files:**
- Modify: `internal/report/junit.go` — add `cappedNotice`, call it in `JUnit`
- Modify: `internal/report/report_test.go:398-406` — the `tests` and `skipped` totals shift by one, because `sampleRun()` already sets `Capped: 2`
- Modify: `docs/reports.md` — document the new element in the `junit` section

**Interfaces:**
- Consumes: nothing.
- Produces: `cappedNotice(run *model.Run) junitSuite` (package-private). Task 4 relies on the observable contract: when `run.Capped > 0` the XML gains one extra `<testsuite name="--max-mutants">` holding one skipped `<testcase name="truncated run">`, and the top-level `tests` and `skipped` attributes each grow by exactly 1.

- [ ] **Step 1: Write the failing unit test**

Add to `internal/report/report_test.go`, directly after `TestJUnitHasXMLHeader`:

```go
// TestJUnitNamesTheCap: every other format says what --max-mutants dropped. A
// CI UI fed only this XML would otherwise read a truncated sample as a complete
// run, which is the exact misreading the reports are designed to prevent.
func TestJUnitNamesTheCap(t *testing.T) {
	run := sampleRun() // Capped: 2, Generated: 9
	b, err := JUnit(run)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, `name="--max-mutants"`) {
		t.Errorf("no --max-mutants testsuite in:\n%s", got)
	}
	if !strings.Contains(got, "2 of 9 generated mutants were not evaluated") {
		t.Errorf("cap notice does not state the numbers:\n%s", got)
	}
	if !strings.Contains(got, "not full coverage") {
		t.Errorf("cap notice does not say the run is a sample:\n%s", got)
	}
}

// TestJUnitOmitsTheCapNoticeWhenNothingWasDropped keeps a complete run's XML
// free of a testsuite that would only ever say "nothing was dropped".
func TestJUnitOmitsTheCapNoticeWhenNothingWasDropped(t *testing.T) {
	run := sampleRun()
	run.Capped = 0
	b, err := JUnit(run)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "--max-mutants") {
		t.Errorf("uncapped run should not mention --max-mutants:\n%s", b)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/report/ -run 'TestJUnit(NamesTheCap|OmitsTheCapNotice)' -v`

Expected: `TestJUnitNamesTheCap` FAILS with "no --max-mutants testsuite in:".
`TestJUnitOmitsTheCapNoticeWhenNothingWasDropped` PASSES already (nothing mentions
the flag yet) — that is fine, it is a regression guard for Step 3.

- [ ] **Step 3: Emit the notice**

In `internal/report/junit.go`, inside `JUnit`, immediately after the
`for _, file := range files { ... }` loop closes and before
`suites.Time = fmt.Sprintf(...)`, insert:

```go
	// Truncation has to be visible here too. Prepended rather than appended so a
	// CI UI that lists suites in order shows it before the results it qualifies.
	if run.Capped > 0 {
		suites.Suites = append([]junitSuite{cappedNotice(run)}, suites.Suites...)
		suites.Tests++
		suites.Skipped++
	}
```

Then add, after the `skipReason` function:

```go
// cappedNotice makes --max-mutants truncation visible in the XML.
//
// Every other format names what it excluded. Without this, a capped run's JUnit
// output is indistinguishable from a complete one, so a CI UI showing only these
// results would present a sample as full coverage.
//
// It is counted in the top-level tests and skipped attributes, so those stay
// equal to the sum over child testsuites.
func cappedNotice(run *model.Run) junitSuite {
	return junitSuite{
		Name:     "--max-mutants",
		Tests:    1,
		Skipped:  1,
		Hostname: "localhost",
		Cases: []junitCase{{
			Name:      "truncated run",
			ClassName: "--max-mutants",
			Time:      "0.0000",
			Skipped: &junitSkipped{Message: fmt.Sprintf(
				"%d of %d generated mutants were not evaluated (--max-mutants); "+
					"this run is a sample, not full coverage",
				run.Capped, run.Generated)},
		}},
	}
}
```

- [ ] **Step 4: Fix the two shifted totals in the existing test**

`sampleRun()` sets `Capped: 2`, so `TestJUnitMapsSurvivedToFailure` now sees one
more testcase and one more skip. In `internal/report/report_test.go`, change:

```go
	if suites.Tests != 6 {
		t.Errorf("tests = %d, want 6", suites.Tests)
	}
```

to:

```go
	// 6 mutants + the --max-mutants notice, which sampleRun triggers with Capped: 2.
	if suites.Tests != 7 {
		t.Errorf("tests = %d, want 7", suites.Tests)
	}
```

and change:

```go
	// NoCoverage, Invalid, Timeout and Error all skip: none grades assertions.
	if suites.Skipped != 4 {
		t.Errorf("skipped = %d, want 4", suites.Skipped)
	}
```

to:

```go
	// NoCoverage, Invalid, Timeout and Error all skip: none grades assertions.
	// Plus the --max-mutants notice, which also reports as a skip.
	if suites.Skipped != 5 {
		t.Errorf("skipped = %d, want 5", suites.Skipped)
	}
```

- [ ] **Step 5: Run the report package tests**

Run: `go test ./internal/report/ -race`

Expected: PASS, all tests including the two new ones.

- [ ] **Step 6: Document the new element**

In `docs/reports.md`, in the `## junit` section, find the paragraph beginning
"Skipped reasons are specific, so a skip is never mysterious." and the XML block
after it. Immediately **after** that XML block's closing triple-backtick, insert:

````markdown
A truncated run gains one extra `<testsuite>`, so the cap is visible here as it is
in every other format:

```xml
<testsuite name="--max-mutants" tests="1" failures="0" skipped="1" hostname="localhost">
  <testcase name="truncated run" classname="--max-mutants" time="0.0000">
    <skipped message="438 of 458 generated mutants were not evaluated (--max-mutants); this run is a sample, not full coverage"/>
  </testcase>
</testsuite>
```

It is prepended to the real testsuites and counted in the top-level `tests` and
`skipped` attributes, which therefore stay equal to the sum over child suites.
````

- [ ] **Step 7: Verify against a real capped run**

Run:

```bash
make build
bin/helm-mutation-test testdata/charts/sample -f 'tests/strong_test.yaml' \
  --max-mutants 20 --no-color --report junit --report-dir /tmp/cap-check >/dev/null
grep -c "max-mutants" /tmp/cap-check/mutation-report.junit.xml
head -3 /tmp/cap-check/mutation-report.junit.xml
```

Expected: grep count is at least 2 (the suite name and the classname), and the
`<testsuites>` element shows `tests="21"` — 20 mutants plus the notice.

- [ ] **Step 8: Commit**

```bash
git add internal/report/junit.go internal/report/report_test.go docs/reports.md
git commit -m "fix(junit): name --max-mutants truncation, as every other format does"
```

---

### Task 2: The integration harness

One file that owns process invocation and artifact parsing, so no test hand-rolls
an `exec.Command` or a `json.Unmarshal`. Its deliverable is a smoke test proving
the harness works end to end.

**Files:**
- Create: `test/integration/harness_test.go`

**Interfaces:**
- Consumes: the built binary, and `testdata/charts/sample` relative to the repo root.
- Produces, for every later task:
  - `fixture` — package-level `string`, the relative fixture chart path
  - `allFormats` — package-level `const string = "console,json,markdown,junit,html"`
  - `run(t *testing.T, args ...string) result`
  - `strong(t *testing.T) result` and `weak(t *testing.T) result` — the shared full runs
  - `result` with fields `Stdout, Stderr string`, `ExitCode int`, `Dir string`, `Args []string`
  - methods `(result) JSON(*testing.T) jsonReport`, `JUnit(*testing.T) junitReport`, `Stryker(*testing.T) strykerReport`, `Markdown(*testing.T) string`, `HTML(*testing.T) string`, `Console() string`
  - types `jsonReport`, `tally` (with methods `total()`, `scored()`, `nonScoring()`), `suiteInfo`, `breakdown`, `mutant`, `junitReport`, `strykerReport`
  - `redBaselineChart(t *testing.T) string` — a temp chart copy whose weak suite fails

- [ ] **Step 1: Write the harness**

Create `test/integration/harness_test.go`:

```go
//go:build integration

// Package integration exercises the built plugin as a black box: it runs real
// commands against the fixture chart and asserts on stdout, exit codes, and the
// report files that land on disk.
//
// This is the only coverage of cmd/helm-mutation-test — flag wiring, the three
// exit codes, and each report format as an artifact of a real run rather than of
// a synthetic model. Everything here goes through the same path a user does.
//
// Build-tagged so `go test ./...` and `make test` stay fast; run it with
// `make integration-tests`.
package integration

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fixture is the chart every test scores, relative to the repo root because that
// is the working directory every invocation gets.
const fixture = "testdata/charts/sample"

// allFormats requests every format, which writes all five files.
const allFormats = "console,json,markdown,junit,html"

var (
	// bin is the freshly built plugin.
	bin string
	// repoRoot is the module root, and the working directory for every invocation.
	repoRoot string
	// sharedDir holds the shared runs' reports. It outlives any single test, so
	// it cannot be a t.TempDir(): a later test would find the files deleted.
	sharedDir string
)

func TestMain(m *testing.M) {
	code, err := setupAndRun(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, "integration setup:", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func setupAndRun(m *testing.M) (int, error) {
	root, err := findRepoRoot()
	if err != nil {
		return 0, err
	}
	repoRoot = root

	tmp, err := os.MkdirTemp("", "helm-mutation-test-integration-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(tmp)
	sharedDir = tmp

	// Built from source rather than taken from bin/: a stale binary quietly
	// testing yesterday's code is the failure mode worth designing out. The Go
	// build cache makes this cheap on a warm tree.
	bin = filepath.Join(tmp, "helm-mutation-test")
	build := exec.Command("go", "build", "-o", bin, "./cmd/helm-mutation-test")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("building the plugin: %w\n%s", err, out)
	}
	return m.Run(), nil
}

// findRepoRoot walks up from the test's working directory to the module root, so
// the fixture path does not depend on where `go test` was invoked from.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod found above %s", dir)
		}
		dir = parent
	}
}

// result is everything one invocation of the plugin lets a caller observe.
type result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	// Dir is this invocation's --report-dir. It may not exist: a run that fails
	// its baseline writes nothing.
	Dir  string
	Args []string
}

// Console is the human-facing report, which goes to stdout.
func (r result) Console() string { return r.Stdout }

// run invokes the built plugin and returns what it produced.
//
// A non-zero exit is data, not a failure: several tests assert on specific exit
// codes. Only failing to start the process at all fails the test.
//
// Each invocation gets its own --report-dir, and --no-color unless the caller set
// a colour flag, so assertions never have to strip ANSI escapes.
func run(t *testing.T, args ...string) result {
	t.Helper()
	res, err := invoke(filepath.Join(t.TempDir(), "reports"), args)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func invoke(dir string, args []string) (result, error) {
	full := append([]string{}, args...)
	if !hasFlag(args, "--report-dir") {
		full = append(full, "--report-dir", dir)
	}
	if !hasFlag(args, "--color") && !hasFlag(args, "--no-color") {
		full = append(full, "--no-color")
	}

	cmd := exec.Command(bin, full...)
	cmd.Dir = repoRoot
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	err := cmd.Run()
	res := result{
		Stdout: stdout.String(), Stderr: stderr.String(),
		Dir: dir, Args: full,
	}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		res.ExitCode = exitErr.ExitCode()
	default:
		return res, fmt.Errorf("running %s: %w", strings.Join(full, " "), err)
	}
	return res, nil
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag || strings.HasPrefix(a, flag+"=") {
			return true
		}
	}
	return false
}

// The two full-fixture runs. Scoring all of the fixture's mutants is the
// expensive part (~3s each), and many assertions read the same complete run, so
// each runs at most once per `go test` process. Lazy rather than eager in
// TestMain so a filtered -run does not pay for a run it never reads.
//
// The results are shared read-only. Do not mutate them.
var (
	strongOnce = sync.OnceValues(func() (result, error) {
		return invoke(filepath.Join(sharedDir, "strong"),
			[]string{fixture, "-f", "tests/strong_test.yaml", "--report", allFormats})
	})
	weakOnce = sync.OnceValues(func() (result, error) {
		return invoke(filepath.Join(sharedDir, "weak"),
			[]string{fixture, "-f", "tests/weak_test.yaml", "--report", allFormats})
	})
)

// strong is the shared full run of the thorough suite: every mutant, every format.
func strong(t *testing.T) result {
	t.Helper()
	return shared(t, strongOnce)
}

// weak is the shared full run of the deliberately weak suite. It is the only run
// that produces no-coverage mutants and a survivor list long enough to truncate.
func weak(t *testing.T) result {
	t.Helper()
	return shared(t, weakOnce)
}

func shared(t *testing.T, once func() (result, error)) result {
	t.Helper()
	r, err := once()
	if err != nil {
		t.Fatalf("shared run failed: %v", err)
	}
	return r
}

// ---------- artifact accessors ----------

// file reads one report artifact, failing the test with the run's output if it is
// missing — which is far more use than a bare "no such file".
func (r result) file(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.Dir, name))
	if err != nil {
		t.Fatalf("reading %s: %v\nargs: %s\nstdout:\n%s\nstderr:\n%s",
			name, err, strings.Join(r.Args, " "), r.Stdout, r.Stderr)
	}
	return b
}

func (r result) JSON(t *testing.T) jsonReport {
	t.Helper()
	var out jsonReport
	if err := json.Unmarshal(r.file(t, "mutation-report.json"), &out); err != nil {
		t.Fatalf("mutation-report.json is not valid JSON: %v", err)
	}
	return out
}

func (r result) JUnit(t *testing.T) junitReport {
	t.Helper()
	var out junitReport
	if err := xml.Unmarshal(r.file(t, "mutation-report.junit.xml"), &out); err != nil {
		t.Fatalf("mutation-report.junit.xml is not valid XML: %v", err)
	}
	return out
}

func (r result) Stryker(t *testing.T) strykerReport {
	t.Helper()
	var out strykerReport
	if err := json.Unmarshal(r.file(t, "mutation-report.stryker.json"), &out); err != nil {
		t.Fatalf("mutation-report.stryker.json is not valid JSON: %v", err)
	}
	return out
}

func (r result) Markdown(t *testing.T) string { return string(r.file(t, "mutation-report.md")) }
func (r result) HTML(t *testing.T) string     { return string(r.file(t, "mutation-report.html")) }

// ---------- the published wire shapes ----------
//
// These mirror the report formats rather than importing internal/report, on
// purpose: these tests assert on the published contract, so a rename inside the
// tool should break them.

type jsonReport struct {
	Schema    string      `json:"schema"`
	ChartName string      `json:"chartName"`
	ChartPath string      `json:"chartPath"`
	Score     float64     `json:"score"`
	Threshold float64     `json:"threshold"`
	Passed    bool        `json:"passed"`
	Tally     tally       `json:"tally"`
	Generated int         `json:"generated"`
	Capped    int         `json:"capped"`
	Mutators  []string    `json:"mutators"`
	Suites    []suiteInfo `json:"suites"`
	TestCount int         `json:"testCount"`
	ByMutator []breakdown `json:"byMutator"`
	ByFile    []breakdown `json:"byFile"`
	Mutants   []mutant    `json:"mutants"`
	Timing    struct {
		BaselineMillis int64 `json:"baselineMillis"`
		TotalMillis    int64 `json:"totalMillis"`
	} `json:"timing"`
}

type tally struct {
	Killed     int `json:"killed"`
	Survived   int `json:"survived"`
	NoCoverage int `json:"noCoverage"`
	Invalid    int `json:"invalid"`
	Timeout    int `json:"timeout"`
	Errored    int `json:"error"`
}

func (t tally) total() int {
	return t.Killed + t.Survived + t.NoCoverage + t.Invalid + t.Timeout + t.Errored
}
func (t tally) scored() int     { return t.Killed + t.Survived }
func (t tally) nonScoring() int { return t.total() - t.scored() }

type suiteInfo struct {
	Name  string   `json:"name"`
	File  string   `json:"file"`
	Tests []string `json:"tests"`
}

type breakdown struct {
	Name  string  `json:"name"`
	Score float64 `json:"score"`
	Tally tally   `json:"tally"`
}

type mutant struct {
	ID           string `json:"id"`
	Mutator      string `json:"mutator"`
	File         string `json:"file"`
	Line         int    `json:"line"`
	Column       int    `json:"column"`
	OriginalLine string `json:"originalLine"`
	Original     string `json:"original"`
	Mutated      string `json:"mutated"`
	Status       string `json:"status"`
	KilledBy     []struct {
		Suite      string `json:"suite"`
		SuiteFile  string `json:"suiteFile"`
		Test       string `json:"test"`
		AssertType string `json:"assertType"`
	} `json:"killedBy"`
	CoveringSuites []string `json:"coveringSuites"`
	TestsRun       int      `json:"testsRun"`
	Detail         string   `json:"detail"`
}

// byStatus counts the run's mutants by status string.
func (r jsonReport) byStatus() map[string]int {
	out := map[string]int{}
	for _, m := range r.Mutants {
		out[m.Status]++
	}
	return out
}

type junitReport struct {
	XMLName  xml.Name     `xml:"testsuites"`
	Name     string       `xml:"name,attr"`
	Tests    int          `xml:"tests,attr"`
	Failures int          `xml:"failures,attr"`
	Skipped  int          `xml:"skipped,attr"`
	Time     string       `xml:"time,attr"`
	Suites   []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name     string      `xml:"name,attr"`
	Tests    int         `xml:"tests,attr"`
	Failures int         `xml:"failures,attr"`
	Skipped  int         `xml:"skipped,attr"`
	Cases    []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name      string `xml:"name,attr"`
	ClassName string `xml:"classname,attr"`
	Time      string `xml:"time,attr"`
	Failure   *struct {
		Message string `xml:"message,attr"`
		Type    string `xml:"type,attr"`
		Text    string `xml:",chardata"`
	} `xml:"failure"`
	Skipped *struct {
		Message string `xml:"message,attr"`
	} `xml:"skipped"`
}

// cases flattens every testcase across every testsuite.
func (j junitReport) cases() []junitCase {
	var out []junitCase
	for _, ts := range j.Suites {
		out = append(out, ts.Cases...)
	}
	return out
}

type strykerReport struct {
	Schema        string `json:"$schema"`
	SchemaVersion string `json:"schemaVersion"`
	Thresholds    struct {
		High int `json:"high"`
		Low  int `json:"low"`
	} `json:"thresholds"`
	ProjectRoot string                 `json:"projectRoot"`
	Files       map[string]strykerFile `json:"files"`
}

type strykerFile struct {
	Source   string          `json:"source"`
	Language string          `json:"language"`
	Mutants  []strykerMutant `json:"mutants"`
}

type strykerMutant struct {
	ID          string   `json:"id"`
	MutatorName string   `json:"mutatorName"`
	Replacement string   `json:"replacement"`
	Status      string   `json:"status"`
	Description string   `json:"description"`
	KilledBy    []string `json:"killedBy"`
	TestsRun    int      `json:"testsCompleted"`
	Location    struct {
		Start struct {
			Line   int `json:"line"`
			Column int `json:"column"`
		} `json:"start"`
		End struct {
			Line   int `json:"line"`
			Column int `json:"column"`
		} `json:"end"`
	} `json:"location"`
}

// byStatus counts every mutant in the stryker report by its schema status.
func (s strykerReport) byStatus() map[string]int {
	out := map[string]int{}
	for _, f := range s.Files {
		for _, m := range f.Mutants {
			out[m.Status]++
		}
	}
	return out
}

// ---------- fixtures ----------

// redBaselineChart returns a copy of the fixture whose weak suite asserts the
// wrong kind, so the baseline gate must reject it. Returns an absolute path.
func redBaselineChart(t *testing.T) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "red")
	copyTree(t, filepath.Join(repoRoot, fixture), dst)

	suite := filepath.Join(dst, "tests", "weak_test.yaml")
	b, err := os.ReadFile(suite)
	if err != nil {
		t.Fatal(err)
	}
	const marker = "of: Deployment"
	if !strings.Contains(string(b), marker) {
		t.Fatalf("%s no longer contains %q; pick another assertion to break", suite, marker)
	}
	broken := strings.Replace(string(b), marker, "of: StatefulSet", 1)
	if err := os.WriteFile(suite, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	return dst
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatalf("copying %s to %s: %v", src, dst, err)
	}
}
```

- [ ] **Step 2: Write the smoke test**

Append to `test/integration/harness_test.go`:

```go
// TestTheFixtureChartScoresAndWritesEveryFormat is the harness's own smoke test:
// if this fails, nothing else in the package means anything.
func TestTheFixtureChartScoresAndWritesEveryFormat(t *testing.T) {
	r := strong(t)
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.ExitCode, r.Stdout, r.Stderr)
	}
	for _, name := range []string{
		"mutation-report.json",
		"mutation-report.md",
		"mutation-report.junit.xml",
		"mutation-report.html",
		"mutation-report.stryker.json",
	} {
		if _, err := os.Stat(filepath.Join(r.Dir, name)); err != nil {
			t.Errorf("%s was not written: %v", name, err)
		}
	}
	if j := r.JSON(t); len(j.Mutants) == 0 {
		t.Error("the run evaluated no mutants at all")
	}
}
```

- [ ] **Step 3: Run it**

Run: `go test -tags=integration -count=1 -v ./test/integration/ -run TestTheFixtureChartScoresAndWritesEveryFormat`

Expected: PASS in roughly 3-6s (a build plus one full run). If it fails with
"no go.mod found", the working directory assumption is wrong — check `findRepoRoot`.

- [ ] **Step 4: Confirm the tag actually excludes the package**

Run: `go test ./... 2>&1 | grep -c integration`

Expected: `0` — the untagged build must not see this package, so `make test` is
unaffected.

- [ ] **Step 5: Check formatting and vet**

Run: `gofmt -l test && go vet -tags=integration ./test/integration/`

Expected: no output from either.

- [ ] **Step 6: Commit**

```bash
git add test/integration/harness_test.go
git commit -m "test: add the black-box integration harness and its smoke test"
```

---

### Task 3: Per-format assertions

Each of the five formats, checked as a real artifact: parsed properly, internally
consistent, and carrying what its documentation promises.

**Files:**
- Create: `test/integration/reports_test.go`

**Interfaces:**
- Consumes: everything Task 2 produces.
- Produces: nothing other tasks depend on.

- [ ] **Step 1: Write the JSON and JUnit tests**

Create `test/integration/reports_test.go`:

```go
//go:build integration

package integration

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// knownStatuses is the complete status vocabulary. A status outside it means the
// tool grew one without the reports being taught about it.
var knownStatuses = map[string]bool{
	"Killed": true, "Survived": true, "NoCoverage": true,
	"Invalid": true, "Timeout": true, "Error": true,
}

// TestJSONReportIsCompleteAndSelfConsistent: the JSON report is the canonical
// model other tooling parses, so its internal arithmetic has to hold exactly.
func TestJSONReportIsCompleteAndSelfConsistent(t *testing.T) {
	j := strong(t).JSON(t)

	if j.Schema != "helm-mutation-test/v1" {
		t.Errorf("schema = %q, want helm-mutation-test/v1", j.Schema)
	}
	if j.ChartName != "sample" {
		t.Errorf("chartName = %q, want sample", j.ChartName)
	}
	if j.ChartPath != fixture {
		t.Errorf("chartPath = %q, want %q", j.ChartPath, fixture)
	}
	// The documented invariant: nothing is dropped without being counted.
	if j.Generated-j.Capped != len(j.Mutants) {
		t.Errorf("generated(%d) - capped(%d) = %d, but %d mutants were reported",
			j.Generated, j.Capped, j.Generated-j.Capped, len(j.Mutants))
	}
	if j.Tally.total() != len(j.Mutants) {
		t.Errorf("tally totals %d but %d mutants were reported", j.Tally.total(), len(j.Mutants))
	}
	// score = killed / (killed + survived), and nothing else.
	want := float64(j.Tally.Killed) / float64(j.Tally.scored()) * 100
	if diff := j.Score - want; diff > 0.01 || diff < -0.01 {
		t.Errorf("score = %v, but killed/scored = %v", j.Score, want)
	}
	if len(j.Mutators) != 8 {
		t.Errorf("mutators = %v, want all 8 by default", j.Mutators)
	}
	if len(j.Suites) == 0 || j.TestCount == 0 {
		t.Errorf("suites = %d, testCount = %d, want both non-zero", len(j.Suites), j.TestCount)
	}
	if j.Timing.BaselineMillis < 0 || j.Timing.TotalMillis <= 0 {
		t.Errorf("timing = %+v, want a positive total", j.Timing)
	}

	for _, m := range j.Mutants {
		if m.ID == "" || m.Mutator == "" || m.File == "" || m.Line == 0 {
			t.Errorf("mutant is missing identity fields: %+v", m)
			break
		}
		if !knownStatuses[m.Status] {
			t.Errorf("mutant %s has unknown status %q", m.ID, m.Status)
			break
		}
	}
}

// TestJSONAttributesEveryKillToAnAssertion: "which test caught this" is the
// report's most useful field, and a kill with no attribution is a silent gap.
func TestJSONAttributesEveryKillToAnAssertion(t *testing.T) {
	j := strong(t).JSON(t)

	var killed, survived int
	for _, m := range j.Mutants {
		switch m.Status {
		case "Killed":
			killed++
			if len(m.KilledBy) == 0 {
				t.Errorf("killed mutant %s has no killedBy entry", m.ID)
				continue
			}
			k := m.KilledBy[0]
			if k.Test == "" || k.AssertType == "" || k.SuiteFile == "" {
				t.Errorf("mutant %s attribution is incomplete: %+v", m.ID, k)
			}
		case "Survived":
			survived++
			if len(m.KilledBy) != 0 {
				t.Errorf("survived mutant %s claims to have been killed by %+v", m.ID, m.KilledBy)
			}
		}
	}
	if killed == 0 || survived == 0 {
		t.Fatalf("killed = %d, survived = %d; the strong suite should produce both",
			killed, survived)
	}
}

// TestJUnitInvertsTheUsualSense: a survivor is the actionable defect, so it is
// the survivor -- not the killed mutant -- that must turn a CI job red.
func TestJUnitInvertsTheUsualSense(t *testing.T) {
	r := strong(t)
	j, x := r.JSON(t), r.JUnit(t)

	if x.Name != "helm-mutation-test:sample" {
		t.Errorf("testsuites name = %q", x.Name)
	}
	if x.Failures != j.Tally.Survived {
		t.Errorf("failures = %d, want %d (the survivors)", x.Failures, j.Tally.Survived)
	}
	if x.Skipped != j.Tally.nonScoring() {
		t.Errorf("skipped = %d, want %d (everything that grades nothing)",
			x.Skipped, j.Tally.nonScoring())
	}
	if x.Tests != len(j.Mutants) {
		t.Errorf("tests = %d, want %d (one per mutant, this run is uncapped)",
			x.Tests, len(j.Mutants))
	}

	// The top-level attributes must equal the sum over child suites, or a CI UI
	// that recomputes them will disagree with one that trusts them.
	var tests, failures, skipped int
	for _, ts := range x.Suites {
		tests += ts.Tests
		failures += ts.Failures
		skipped += ts.Skipped
	}
	if tests != x.Tests || failures != x.Failures || skipped != x.Skipped {
		t.Errorf("children sum to tests=%d failures=%d skipped=%d, but the root says %d/%d/%d",
			tests, failures, skipped, x.Tests, x.Failures, x.Skipped)
	}

	var sawFailure, sawPass bool
	for _, c := range x.cases() {
		switch {
		case c.Failure != nil:
			sawFailure = true
			if c.Failure.Type != "SurvivedMutant" {
				t.Errorf("failure type = %q, want SurvivedMutant", c.Failure.Type)
			}
			if !strings.Contains(c.Failure.Text, "Add an assertion") {
				t.Errorf("failure body does not say what to do:\n%s", c.Failure.Text)
			}
			if !strings.Contains(c.Failure.Text, "mutator:") ||
				!strings.Contains(c.Failure.Text, "location:") {
				t.Errorf("failure body lacks mutator/location:\n%s", c.Failure.Text)
			}
		case c.Skipped == nil:
			sawPass = true // a killed mutant: a plain passing testcase
		}
	}
	if !sawFailure || !sawPass {
		t.Errorf("sawFailure = %v, sawPass = %v; expected both", sawFailure, sawPass)
	}
}

// TestJUnitSkipReasonsAreNeverMysterious: a skip that does not say why reads as
// "the tool gave up". The weak run produces no-coverage and invalid mutants.
func TestJUnitSkipReasonsAreNeverMysterious(t *testing.T) {
	for _, c := range weak(t).JUnit(t).cases() {
		if c.Skipped == nil {
			continue
		}
		msg := c.Skipped.Message
		switch {
		case strings.Contains(msg, "no suite renders"),
			strings.Contains(msg, "stopped the chart rendering"),
			strings.Contains(msg, "timed out"),
			strings.Contains(msg, "the tool failed"),
			strings.Contains(msg, "--max-mutants"):
		default:
			t.Errorf("skip reason is not one of the documented explanations: %q", msg)
		}
	}
}
```

- [ ] **Step 2: Run the JSON and JUnit tests**

Run: `go test -tags=integration -count=1 -v ./test/integration/ -run 'TestJSON|TestJUnit'`

Expected: all four PASS.

- [ ] **Step 3: Add the markdown, HTML and stryker tests**

Append to `test/integration/reports_test.go` (this block is fenced with four
backticks because the code contains a triple-backtick string literal):

````go
// TestMarkdownCarriesTheWholeSummary: this format is what a reviewer reads in a
// job summary, so the score, both breakdowns, and the exclusion arithmetic all
// have to be present without opening anything else.
func TestMarkdownCarriesTheWholeSummary(t *testing.T) {
	r := strong(t)
	j, md := r.JSON(t), r.Markdown(t)

	for _, want := range []string{
		fmt.Sprintf("## Mutation score: %.1f%%", j.Score),
		"| Outcome | Count | |",
		"### Score by mutator",
		"### Score by file",
		fmt.Sprintf("Score counts only killed and survived mutants: %d of %d.",
			j.Tally.scored(), j.Tally.total()),
		fmt.Sprintf("| Killed | %d |", j.Tally.Killed),
		fmt.Sprintf("| Survived | %d |", j.Tally.Survived),
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown is missing %q", want)
		}
	}
	// Every mutator that ran should appear in the breakdown table.
	for _, b := range j.ByMutator {
		if !strings.Contains(md, "| `"+b.Name+"` |") {
			t.Errorf("markdown byMutator table omits %q", b.Name)
		}
	}
}

// TestMarkdownTruncatesSurvivorsHonestly: GitHub's step summary has a size limit,
// so the list is capped -- but a silent cut would misrepresent the run. Asserts
// the relationship rather than the cap's value, which is free to change.
func TestMarkdownTruncatesSurvivorsHonestly(t *testing.T) {
	r := weak(t)
	j, md := r.JSON(t), r.Markdown(t)

	total := j.Tally.Survived
	if total < 50 {
		t.Fatalf("the weak suite produced only %d survivors; this test needs a long list", total)
	}
	if !strings.Contains(md, fmt.Sprintf("### Survived mutants (%d)", total)) {
		t.Errorf("markdown does not state the true survivor count %d", total)
	}

	// Parse the note with a regexp, not by finding the first "_": the byFile table
	// contains `templates/_helpers.tpl`, whose underscore comes first in the file.
	shown := strings.Count(md, "```diff")
	m := regexp.MustCompile(`_(\d+) further survivors omitted`).FindStringSubmatch(md)
	if m == nil {
		t.Fatalf("no truncation note in a report with %d survivors", total)
	}
	omitted, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatal(err)
	}
	if shown+omitted != total {
		t.Errorf("markdown shows %d and admits omitting %d, but there are %d survivors",
			shown, omitted, total)
	}
}

// TestHTMLCommunicatesWithoutScripts: an artifact opened out of a CI run is
// routinely opened with the CDN unreachable or scripts blocked, which is exactly
// when the embedded viewer shows nothing. The static summary must stand alone.
func TestHTMLCommunicatesWithoutScripts(t *testing.T) {
	r := strong(t)
	j, h := r.JSON(t), r.HTML(t)

	for _, want := range []string{
		"Mutation report — sample",
		fmt.Sprintf("%.1f%%", j.Score),
		"<h2>Outcomes</h2>",
		"<h2>Survived mutants</h2>",
		fmt.Sprintf("<th>Killed</th><td>%d</td>", j.Tally.Killed),
		fmt.Sprintf("<th>Survived</th><td>%d</td>", j.Tally.Survived),
		`<script id="mutation-report" type="application/json">`,
	} {
		if !strings.Contains(h, want) {
			t.Errorf("HTML is missing %q", want)
		}
	}
	// Every survivor is listed here, uncapped, unlike markdown.
	if got := strings.Count(h, `<li><code class="loc">`); got != j.Tally.Survived {
		t.Errorf("HTML lists %d survivors, want all %d", got, j.Tally.Survived)
	}
	// The embedded payload must not be able to close its own script element.
	payload := h[strings.Index(h, `id="mutation-report"`):]
	payload = payload[:strings.Index(payload, "</script>")]
	if strings.Contains(payload, "</script") {
		t.Error("the embedded JSON contains an unescaped </script")
	}
}

// TestStrykerConformsToTheSchema: this file is what other mutation-testing
// tooling and the published viewer consume, so the schema's vocabulary and the
// embedded source both have to be right.
func TestStrykerConformsToTheSchema(t *testing.T) {
	r := strong(t)
	j, s := r.JSON(t), r.Stryker(t)

	if s.SchemaVersion != "2" {
		t.Errorf("schemaVersion = %q, want 2", s.SchemaVersion)
	}
	if !strings.Contains(s.Schema, "mutation-testing-report-schema.json") {
		t.Errorf("$schema = %q", s.Schema)
	}
	if s.ProjectRoot != fixture {
		t.Errorf("projectRoot = %q, want %q", s.ProjectRoot, fixture)
	}
	if s.Thresholds.High != 80 || s.Thresholds.Low != 50 {
		t.Errorf("thresholds = %+v, want high 80 / low 50", s.Thresholds)
	}
	if len(s.Files) == 0 {
		t.Fatal("no files in the stryker report")
	}

	// Our statuses map onto the schema's vocabulary; Invalid becomes CompileError.
	want := map[string]int{
		"Killed":       j.Tally.Killed,
		"Survived":     j.Tally.Survived,
		"NoCoverage":   j.Tally.NoCoverage,
		"CompileError": j.Tally.Invalid,
		"Timeout":      j.Tally.Timeout,
		"RuntimeError": j.Tally.Errored,
	}
	got := s.byStatus()
	for status, n := range want {
		if n == 0 {
			continue
		}
		if got[status] != n {
			t.Errorf("stryker has %d %s, want %d", got[status], status, n)
		}
	}

	for name, f := range s.Files {
		// The viewer needs source to annotate mutations in place.
		if f.Source == "" {
			t.Errorf("%s has no embedded source", name)
		}
		wantLang := "yaml"
		if strings.HasSuffix(name, ".tpl") {
			wantLang = "html" // no YAML-with-Go-template mode exists
		}
		if f.Language != wantLang {
			t.Errorf("%s language = %q, want %q", name, f.Language, wantLang)
		}
		for _, m := range f.Mutants {
			if m.Location.Start.Line == 0 || m.Location.End.Column <= m.Location.Start.Column {
				t.Errorf("%s mutant %s has a degenerate location %+v", name, m.ID, m.Location)
				break
			}
		}
	}
}

// TestEveryWrittenReportIsAnnounced: the CLI prints where it put each file, and a
// file written but not announced is a file nobody finds.
func TestEveryWrittenReportIsAnnounced(t *testing.T) {
	r := strong(t)
	for _, name := range []string{
		"mutation-report.json",
		"mutation-report.md",
		"mutation-report.junit.xml",
		"mutation-report.html",
		"mutation-report.stryker.json",
	} {
		if !strings.Contains(r.Stdout, name) {
			t.Errorf("stdout does not announce %s", name)
		}
	}
}

// TestOnlyRequestedFormatsAreWritten: --report is a whitelist, and writing an
// unasked-for file surprises anyone collecting a directory as a CI artifact.
func TestOnlyRequestedFormatsAreWritten(t *testing.T) {
	r := run(t, fixture, "-f", "tests/strong_test.yaml", "--max-mutants", "5",
		"--report", "json")
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d\n%s", r.ExitCode, r.Stderr)
	}
	entries, err := osReadDirNames(r.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0] != "mutation-report.json" {
		t.Errorf("report dir holds %v, want only mutation-report.json", entries)
	}
}
````

- [ ] **Step 4: Add the directory-listing helper to the harness**

Append to `test/integration/harness_test.go`:

```go
// osReadDirNames lists a directory's entry names, sorted.
func osReadDirNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}
```

`"sort"` is already in that file's import block from Task 2.

- [ ] **Step 5: Run the whole reports file**

Run: `go test -tags=integration -count=1 -v ./test/integration/ -run 'TestJSON|TestJUnit|TestMarkdown|TestHTML|TestStryker|TestEveryWritten|TestOnlyRequested'`

Expected: all PASS. The first weak-suite test pays ~1.5s for the shared weak run.

If `TestMarkdownTruncatesSurvivorsHonestly` fails to find the note, check whether
the weak suite still produces more than 40 survivors: `jq '.tally.survived'` on
the weak run's JSON.

- [ ] **Step 6: Commit**

```bash
git add test/integration/reports_test.go test/integration/harness_test.go
git commit -m "test: assert every report format as an artifact of a real run"
```

---

### Task 4: Cross-format agreement

The headline property. Five formats are pure functions of one model, so they must
never disagree — and every one must name what it excluded from the score.

**Files:**
- Create: `test/integration/crossformat_test.go`

**Interfaces:**
- Consumes: Task 2's harness; Task 1's JUnit cap notice.
- Produces: nothing.

- [ ] **Step 1: Write the agreement tests**

Create `test/integration/crossformat_test.go`:

```go
//go:build integration

package integration

import (
	"fmt"
	"strings"
	"testing"
)

// TestEveryFormatReportsTheSameScore: the five formats are pure functions of one
// run, so a disagreement between them means one of them is lying. JUnit carries
// no score, so it is held to the tallies instead.
func TestEveryFormatReportsTheSameScore(t *testing.T) {
	r := strong(t)
	j := r.JSON(t)
	score := fmt.Sprintf("%.1f%%", j.Score)

	for _, f := range []struct {
		name, body string
	}{
		{"console", r.Console()},
		{"markdown", r.Markdown(t)},
		{"html", r.HTML(t)},
	} {
		if !strings.Contains(f.body, score) {
			t.Errorf("%s does not report the score %s", f.name, score)
		}
	}

	x := r.JUnit(t)
	if x.Failures != j.Tally.Survived {
		t.Errorf("junit failures = %d but json says %d survived", x.Failures, j.Tally.Survived)
	}
	if got := r.Stryker(t).byStatus(); got["Survived"] != j.Tally.Survived ||
		got["Killed"] != j.Tally.Killed {
		t.Errorf("stryker has %d killed / %d survived, json says %d / %d",
			got["Killed"], got["Survived"], j.Tally.Killed, j.Tally.Survived)
	}
}

// TestEveryFormatNamesWhatItExcluded is the honesty requirement. The score counts
// only killed and survived; a format that showed the score without naming the
// no-coverage and invalid mutants would let a partially-analysed run read as full
// coverage. The weak run has both.
func TestEveryFormatNamesWhatItExcluded(t *testing.T) {
	r := weak(t)
	j := r.JSON(t)
	if j.Tally.NoCoverage == 0 || j.Tally.Invalid == 0 {
		t.Fatalf("weak run has %d no-coverage and %d invalid; this test needs both",
			j.Tally.NoCoverage, j.Tally.Invalid)
	}

	console, md, h := r.Console(), r.Markdown(t), r.HTML(t)

	// Console names each excluded status with its count, and gives them sections.
	for _, want := range []string{
		fmt.Sprintf("%d no-coverage", j.Tally.NoCoverage),
		fmt.Sprintf("%d invalid", j.Tally.Invalid),
		fmt.Sprintf("NO COVERAGE (%d)", j.Tally.NoCoverage),
		fmt.Sprintf("INVALID (%d)", j.Tally.Invalid),
		"excluded from the score",
	} {
		if !strings.Contains(console, want) {
			t.Errorf("console does not name %q", want)
		}
	}

	// Markdown states the arithmetic outright and lists the unrendered templates.
	for _, want := range []string{
		fmt.Sprintf("| No coverage | %d |", j.Tally.NoCoverage),
		fmt.Sprintf("| Invalid | %d |", j.Tally.Invalid),
		fmt.Sprintf("Score counts only killed and survived mutants: %d of %d.",
			j.Tally.scored(), j.Tally.total()),
		"### Templates no suite renders",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown does not name %q", want)
		}
	}

	// HTML's static table carries the same rows plus the scored-of-total line.
	for _, want := range []string{
		fmt.Sprintf("<th>No coverage</th><td>%d</td>", j.Tally.NoCoverage),
		fmt.Sprintf("<th>Invalid</th><td>%d</td>", j.Tally.Invalid),
		fmt.Sprintf("<th>Scored</th><td>%d</td><td>of %d mutants",
			j.Tally.scored(), j.Tally.total()),
	} {
		if !strings.Contains(h, want) {
			t.Errorf("HTML does not name %q", want)
		}
	}

	// JUnit turns each into a skip with a specific reason, never a pass.
	if got := r.JUnit(t).Skipped; got != j.Tally.nonScoring() {
		t.Errorf("junit skipped = %d, want %d", got, j.Tally.nonScoring())
	}
}

// TestEveryFormatDisclosesTruncation: a --max-mutants run is a sample. Every
// format has to say so, or a truncated run reads as full coverage -- the exact
// misreading CLAUDE.md forbids.
func TestEveryFormatDisclosesTruncation(t *testing.T) {
	const limit = 20 // not "cap": that shadows a builtin
	r := run(t, fixture, "-f", "tests/strong_test.yaml",
		"--max-mutants", fmt.Sprint(limit), "--report", allFormats)
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d\n%s", r.ExitCode, r.Stderr)
	}

	j := r.JSON(t)
	if len(j.Mutants) != limit {
		t.Fatalf("evaluated %d mutants, want %d", len(j.Mutants), limit)
	}
	if j.Capped != j.Generated-limit || j.Capped == 0 {
		t.Fatalf("capped = %d, generated = %d; want capped = generated - %d",
			j.Capped, j.Generated, limit)
	}
	dropped := j.Capped

	if !strings.Contains(r.Console(), fmt.Sprintf(
		"only %d of %d generated mutants were run (--max-mutants); %d not evaluated",
		limit, j.Generated, dropped)) {
		t.Errorf("console does not disclose the cap:\n%s", r.Console())
	}
	if !strings.Contains(r.Markdown(t), fmt.Sprintf(
		"`--max-mutants` ran %d of %d generated mutants; %d were not evaluated.",
		limit, j.Generated, dropped)) {
		t.Error("markdown does not disclose the cap")
	}
	if !strings.Contains(r.HTML(t), fmt.Sprintf(
		"--max-mutants ran %d of %d generated", limit, j.Generated)) {
		t.Error("HTML does not disclose the cap")
	}

	// JUnit says it with a dedicated testsuite, added in this branch.
	x := r.JUnit(t)
	var notice *junitSuite
	for i := range x.Suites {
		if x.Suites[i].Name == "--max-mutants" {
			notice = &x.Suites[i]
			break
		}
	}
	if notice == nil {
		t.Fatal("junit has no --max-mutants testsuite")
	}
	if len(notice.Cases) != 1 || notice.Cases[0].Skipped == nil {
		t.Fatalf("the cap notice should be one skipped case, got %+v", notice.Cases)
	}
	if msg := notice.Cases[0].Skipped.Message; !strings.Contains(msg, fmt.Sprintf(
		"%d of %d generated mutants were not evaluated", dropped, j.Generated)) {
		t.Errorf("cap notice message = %q", msg)
	}
	// One extra testcase for the notice, on top of one per mutant.
	if x.Tests != len(j.Mutants)+1 {
		t.Errorf("junit tests = %d, want %d mutants + 1 notice", x.Tests, len(j.Mutants))
	}
}

// TestBreakdownsAgreeAcrossFormats: the per-mutator and per-file tables drive
// where a reader looks first, so console, markdown and JSON must rank the same
// way -- weakest first.
func TestBreakdownsAgreeAcrossFormats(t *testing.T) {
	r := strong(t)
	j, md, console := r.JSON(t), r.Markdown(t), r.Console()

	if len(j.ByMutator) == 0 || len(j.ByFile) == 0 {
		t.Fatal("JSON carries no breakdowns")
	}
	// Ascending score, so the most under-tested rows surface first.
	for _, rows := range [][]breakdown{j.ByMutator, j.ByFile} {
		for i := 1; i < len(rows); i++ {
			if rows[i].Score < rows[i-1].Score {
				t.Errorf("breakdown is not ascending by score: %q(%v) before %q(%v)",
					rows[i-1].Name, rows[i-1].Score, rows[i].Name, rows[i].Score)
			}
		}
	}
	// Every row's tally has to match the mutants it claims to summarise.
	perFile := map[string]tally{}
	for _, m := range j.Mutants {
		x := perFile[m.File]
		switch m.Status {
		case "Killed":
			x.Killed++
		case "Survived":
			x.Survived++
		case "NoCoverage":
			x.NoCoverage++
		case "Invalid":
			x.Invalid++
		case "Timeout":
			x.Timeout++
		case "Error":
			x.Errored++
		}
		perFile[m.File] = x
	}
	for _, row := range j.ByFile {
		if perFile[row.Name] != row.Tally {
			t.Errorf("byFile[%s] = %+v, but its mutants tally %+v",
				row.Name, row.Tally, perFile[row.Name])
		}
		if !strings.Contains(md, "| `"+row.Name+"` |") {
			t.Errorf("markdown byFile table omits %s", row.Name)
		}
	}
	if !strings.Contains(console, "By mutator") || !strings.Contains(console, "By file") {
		t.Error("console is missing a breakdown table")
	}
}
```

- [ ] **Step 2: Run them**

Run: `go test -tags=integration -count=1 -v ./test/integration/ -run 'TestEveryFormat|TestBreakdowns'`

Expected: all four PASS. `TestEveryFormatDisclosesTruncation` is the one that
depends on Task 1; if it fails at "junit has no --max-mutants testsuite", Task 1
was not completed.

- [ ] **Step 3: Commit**

```bash
git add test/integration/crossformat_test.go
git commit -m "test: assert the five formats never disagree and always name exclusions"
```

---

### Task 5: Exit codes, thresholds and the baseline gate

The CLI's contract with CI. Every string here was measured; see Verified
Reference Data.

**Files:**
- Create: `test/integration/exitcodes_test.go`

**Interfaces:**
- Consumes: Task 2's harness, including `redBaselineChart`.
- Produces: nothing.

- [ ] **Step 1: Write the tests**

Create `test/integration/exitcodes_test.go`:

```go
//go:build integration

package integration

import (
	"os"
	"strings"
	"testing"
)

// TestExitZeroWhenTheThresholdIsMet, and the verdict says so in the footer.
func TestExitZeroWhenTheThresholdIsMet(t *testing.T) {
	r := run(t, fixture, "-f", "tests/strong_test.yaml",
		"--max-mutants", "20", "--threshold", "70")
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.ExitCode, r.Stdout, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "meets the 70.0% threshold") {
		t.Errorf("no threshold verdict in:\n%s", r.Stdout)
	}
}

// TestExitOneWhenBelowTheThreshold: exit 1 means "your tests are weak", which CI
// must be able to tell apart from "the tool could not run".
func TestExitOneWhenBelowTheThreshold(t *testing.T) {
	// --report json,console: the JSON is what the score assertion below reads, and
	// console must stay on so the verdict line is checked too.
	r := run(t, fixture, "-f", "tests/weak_test.yaml", "--threshold", "50",
		"--report", "console,json")
	if r.ExitCode != 1 {
		t.Fatalf("exit = %d, want 1\nstdout:\n%s\nstderr:\n%s", r.ExitCode, r.Stdout, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "is below the 50.0% threshold") {
		t.Errorf("console does not state the verdict:\n%s", r.Stdout)
	}
	if !strings.Contains(r.Stderr, "mutation score") ||
		!strings.Contains(r.Stderr, "is below the 50.0% threshold") {
		t.Errorf("stderr does not state the failure:\n%s", r.Stderr)
	}
	// The deliberately weak suite scoring above 50% would mean a mutator regressed.
	if j := r.JSON(t); j.Score >= 50 {
		t.Errorf("the weak suite scored %.1f%%; a mutator has regressed", j.Score)
	}
}

// TestNoThresholdNeverFails: the default is to measure, not to gate.
func TestNoThresholdNeverFails(t *testing.T) {
	r := run(t, fixture, "-f", "tests/weak_test.yaml", "--max-mutants", "10",
		"--report", "json")
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0 with no --threshold\n%s", r.ExitCode, r.Stderr)
	}
	if j := r.JSON(t); !j.Passed || j.Threshold != 0 {
		t.Errorf("passed = %v, threshold = %v; want true and 0", j.Passed, j.Threshold)
	}
}

// TestExitTwoForEveryCannotRun: exit 2 is "the tool could not run", and the
// message has to name the actual problem rather than dumping usage.
func TestExitTwoForEveryCannotRun(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			"a chart path that does not exist",
			[]string{"./does-not-exist"},
			"loading chart ./does-not-exist",
		},
		{
			"a suite glob that matches nothing",
			[]string{fixture, "-f", "tests/nope_*.yaml"},
			"no test suites matched",
		},
		{
			"a misspelled mutator",
			[]string{fixture, "--mutators", "cond-negat"},
			`unknown mutator "cond-negat"`,
		},
		{
			"a threshold outside 0-100",
			[]string{fixture, "--threshold", "150"},
			"--threshold must be between 0 and 100, got 150",
		},
		{
			"an unknown report format",
			[]string{fixture, "--report", "yaml"},
			`unknown --report format "yaml"`,
		},
		{
			"a negative --max-mutants",
			[]string{fixture, "--max-mutants", "-1"},
			"--max-mutants cannot be negative",
		},
		{
			"an invalid --kill-attribution",
			[]string{fixture, "--kill-attribution", "some"},
			"--kill-attribution must be",
		},
		{
			"no chart argument at all",
			[]string{},
			"accepts 1 arg",
		},
		{
			"two chart arguments",
			[]string{fixture, fixture},
			"accepts 1 arg",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := run(t, tc.args...)
			if r.ExitCode != 2 {
				t.Errorf("exit = %d, want 2\nstdout:\n%s\nstderr:\n%s",
					r.ExitCode, r.Stdout, r.Stderr)
			}
			if !strings.Contains(r.Stderr, tc.want) {
				t.Errorf("stderr does not contain %q:\n%s", tc.want, r.Stderr)
			}
		})
	}
}

// TestAMisspelledMutatorListsTheValidOnes: rejecting a typo is only useful if the
// message says what to write instead.
func TestAMisspelledMutatorListsTheValidOnes(t *testing.T) {
	r := run(t, fixture, "--mutators", "cond-negat")
	for _, id := range []string{
		"bool-flip", "comparison-swap", "cond-negate", "default-drop",
		"num-literal", "required-drop", "str-literal", "yaml-key-delete",
	} {
		if !strings.Contains(r.Stderr, id) {
			t.Errorf("the error does not offer %q as a valid ID:\n%s", id, r.Stderr)
		}
	}
}

// TestARedBaselineIsAHardStop: a mutation score measured over failing tests is a
// fiction, so the run must refuse rather than warn -- and must not leave a report
// behind that could be published as if it meant something.
func TestARedBaselineIsAHardStop(t *testing.T) {
	chart := redBaselineChart(t)
	r := run(t, chart, "-f", "tests/weak_test.yaml", "--report", allFormats)

	if r.ExitCode != 2 {
		t.Fatalf("exit = %d, want 2\nstdout:\n%s\nstderr:\n%s", r.ExitCode, r.Stdout, r.Stderr)
	}
	for _, want := range []string{
		"Cannot measure mutation score:",
		"A score measured against failing tests is meaningless.",
		"Fix the suite first, then re-run.",
		"the chart's tests do not pass before mutation",
	} {
		if !strings.Contains(r.Stderr, want) {
			t.Errorf("stderr is missing %q:\n%s", want, r.Stderr)
		}
	}
	// The failing test has to be named, or the user cannot act on it.
	if !strings.Contains(r.Stderr, "renders a Deployment") {
		t.Errorf("stderr does not name the failing test:\n%s", r.Stderr)
	}
	if _, err := os.Stat(r.Dir); !os.IsNotExist(err) {
		names, _ := osReadDirNames(r.Dir)
		t.Errorf("a red baseline wrote reports anyway: %v", names)
	}
}

// TestTheFixtureChartItselfStaysGreen: the whole suite rests on the fixture's own
// tests passing, so a broken fixture should fail loudly here rather than as a
// confusing baseline error in every other test.
func TestTheFixtureChartItselfStaysGreen(t *testing.T) {
	for _, suite := range []string{"tests/weak_test.yaml", "tests/strong_test.yaml"} {
		r := run(t, fixture, "-f", suite, "--max-mutants", "1")
		if r.ExitCode != 0 {
			t.Errorf("%s does not pass its own baseline (exit %d):\n%s",
				suite, r.ExitCode, r.Stderr)
		}
	}
}
```

- [ ] **Step 2: Run them**

Run: `go test -tags=integration -count=1 -v ./test/integration/ -run 'TestExit|TestNoThreshold|TestAMisspelled|TestARedBaseline|TestTheFixtureChartItself'`

Expected: all PASS. If a subtest of `TestExitTwoForEveryCannotRun` fails on the
cobra arity message, check the exact wording with
`bin/helm-mutation-test 2>&1 | head -2` — cobra phrases it `accepts 1 arg(s), received 0`.

- [ ] **Step 3: Commit**

```bash
git add test/integration/exitcodes_test.go
git commit -m "test: pin the three exit codes, threshold verdicts and the baseline gate"
```

---

### Task 6: Mutation-control flags

`--mutators`, `--exclude-mutators`, `--include`, `--exclude`, `--max-mutants` and
`--seed`, each asserted through the JSON report of the run it shaped.

**Files:**
- Create: `test/integration/flags_test.go`

**Interfaces:**
- Consumes: Task 2's harness.
- Produces: `mutantKeys(jsonReport) []string`, used by Task 7's parallelism test.

- [ ] **Step 1: Write the tests**

Create `test/integration/flags_test.go`:

```go
//go:build integration

package integration

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// mutantKeys is every mutant as "id=status", sorted: a run's complete fingerprint.
func mutantKeys(j jsonReport) []string {
	out := make([]string, 0, len(j.Mutants))
	for _, m := range j.Mutants {
		out = append(out, m.ID+"="+m.Status)
	}
	slices.Sort(out)
	return out
}

// TestMutatorSelectionIsExact: silently running fewer or more mutators than asked
// would make the score incomparable between runs.
func TestMutatorSelectionIsExact(t *testing.T) {
	r := run(t, fixture, "-f", "tests/strong_test.yaml",
		"--mutators", "cond-negate,comparison-swap", "--report", "json,markdown")
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d\n%s", r.ExitCode, r.Stderr)
	}
	j := r.JSON(t)

	// The reported list preserves the order given on the command line: it echoes
	// the request. Generation sorts internally, which TestMutatorIDsAreOrder-
	// Independent covers, so do not expect a sorted list here.
	want := []string{"cond-negate", "comparison-swap"}
	if !slices.Equal(j.Mutators, want) {
		t.Errorf("mutators = %v, want %v", j.Mutators, want)
	}
	for _, m := range j.Mutants {
		if m.Mutator != "cond-negate" && m.Mutator != "comparison-swap" {
			t.Fatalf("mutant %s ran an unrequested mutator %q", m.ID, m.Mutator)
		}
	}
	if len(j.Mutants) == 0 {
		t.Error("selecting two mutators produced no mutants at all")
	}
	// Both formats' breakdowns must agree with the selection.
	if len(j.ByMutator) > 2 {
		t.Errorf("byMutator has %d rows for a two-mutator run", len(j.ByMutator))
	}
	if md := r.Markdown(t); strings.Contains(md, "| `yaml-key-delete` |") {
		t.Error("markdown reports a mutator that was not selected")
	}
}

// TestExcludeMutatorsIsSubtractedFromTheSelection, so the two flags compose.
func TestExcludeMutatorsIsSubtractedFromTheSelection(t *testing.T) {
	r := run(t, fixture, "-f", "tests/strong_test.yaml",
		"--mutators", "cond-negate,str-literal", "--exclude-mutators", "str-literal",
		"--report", "json")
	j := r.JSON(t)
	if !slices.Equal(j.Mutators, []string{"cond-negate"}) {
		t.Errorf("mutators = %v, want only cond-negate", j.Mutators)
	}
	for _, m := range j.Mutants {
		if m.Mutator != "cond-negate" {
			t.Fatalf("mutant %s survived the exclusion: %q", m.ID, m.Mutator)
		}
	}
}

// TestExcludeSkipsFilesEntirely: --exclude is how a user quarantines a template,
// so a mutant leaking through would defeat the point.
func TestExcludeSkipsFilesEntirely(t *testing.T) {
	r := run(t, fixture, "-f", "tests/strong_test.yaml", "--exclude", "templates/**",
		"--report", "json")
	j := r.JSON(t)
	if len(j.Mutants) == 0 {
		t.Fatal("excluding templates left nothing to mutate")
	}
	for _, m := range j.Mutants {
		if m.File != "values.yaml" {
			t.Fatalf("mutant %s is in %s, which was excluded", m.ID, m.File)
		}
	}
}

// TestIncludeNarrowsToTheGivenGlobs, the positive form of the same control.
func TestIncludeNarrowsToTheGivenGlobs(t *testing.T) {
	r := run(t, fixture, "-f", "tests/strong_test.yaml",
		"--include", "templates/service.yaml", "--report", "json")
	j := r.JSON(t)
	if len(j.Mutants) == 0 {
		t.Fatal("including one template produced no mutants")
	}
	for _, m := range j.Mutants {
		if m.File != "templates/service.yaml" {
			t.Fatalf("mutant %s is in %s, outside the include glob", m.ID, m.File)
		}
	}
}

// TestMaxMutantsEvaluatesExactlyTheCap and accounts for everything it dropped.
func TestMaxMutantsEvaluatesExactlyTheCap(t *testing.T) {
	const limit = 15 // not "cap": that shadows a builtin
	r := run(t, fixture, "-f", "tests/strong_test.yaml",
		"--max-mutants", fmt.Sprint(limit), "--report", "json")
	j := r.JSON(t)

	if len(j.Mutants) != limit {
		t.Errorf("evaluated %d mutants, want %d", len(j.Mutants), limit)
	}
	if j.Generated <= limit {
		t.Fatalf("generated = %d; the fixture should generate far more than %d",
			j.Generated, limit)
	}
	if j.Generated-j.Capped != len(j.Mutants) {
		t.Errorf("generated(%d) - capped(%d) != %d evaluated",
			j.Generated, j.Capped, len(j.Mutants))
	}
}

// TestTheSampledSubsetIsSeedStable: a score that shifted between identical runs
// would be useless for trend tracking, and --seed is the documented knob.
func TestTheSampledSubsetIsSeedStable(t *testing.T) {
	args := func(seed string) []string {
		return []string{fixture, "-f", "tests/strong_test.yaml",
			"--max-mutants", "25", "--seed", seed, "--report", "json"}
	}
	first := mutantKeys(run(t, args("1")...).JSON(t))
	again := mutantKeys(run(t, args("1")...).JSON(t))
	other := mutantKeys(run(t, args("99")...).JSON(t))

	if !slices.Equal(first, again) {
		t.Error("two runs with --seed 1 disagreed on the sampled subset or its results")
	}
	if slices.Equal(first, other) {
		t.Error("--seed 99 sampled exactly the same subset as --seed 1, so the seed does nothing")
	}
	if len(first) != 25 {
		t.Errorf("sampled %d mutants, want 25", len(first))
	}
}

// TestMutatorIDsAreOrderIndependent: selection order must not change results, so
// a script assembling the flag from a set is safe.
func TestMutatorIDsAreOrderIndependent(t *testing.T) {
	a := run(t, fixture, "-f", "tests/strong_test.yaml",
		"--mutators", "cond-negate,bool-flip", "--report", "json").JSON(t)
	b := run(t, fixture, "-f", "tests/strong_test.yaml",
		"--mutators", "bool-flip,cond-negate", "--report", "json").JSON(t)
	if !slices.Equal(mutantKeys(a), mutantKeys(b)) {
		t.Error("reordering --mutators changed the run")
	}
}
```

- [ ] **Step 2: Run them**

Run: `go test -tags=integration -count=1 -v ./test/integration/ -run 'TestMutator|TestExclude|TestInclude|TestMaxMutants|TestTheSampled'`

Expected: all PASS.

- [ ] **Step 3: Commit**

```bash
git add test/integration/flags_test.go
git commit -m "test: pin the mutation-control flags through the JSON report"
```

---

### Task 7: Coverage reporting, execution flags and colour

The remaining behaviour: no-coverage reported per file across formats,
parallelism invariance, kill attribution, and the colour tri-state.

**Files:**
- Create: `test/integration/execution_test.go`

**Interfaces:**
- Consumes: Task 2's harness and Task 6's `mutantKeys`.
- Produces: nothing.

- [ ] **Step 1: Write the tests**

Create `test/integration/execution_test.go`:

```go
//go:build integration

package integration

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// TestNoCoverageIsReportedPerFile: "no suite renders this template" is a per-file
// finding. Listing every uncovered mutant individually would bury it, and calling
// them survivors would be a lie -- they never ran.
func TestNoCoverageIsReportedPerFile(t *testing.T) {
	r := weak(t)
	j := r.JSON(t)

	gaps := map[string]int{}
	for _, m := range j.Mutants {
		if m.Status == "NoCoverage" {
			gaps[m.File]++
		}
	}
	if len(gaps) == 0 {
		t.Fatal("the weak suite declares only two templates; the rest should be no-coverage")
	}
	// The weak suite declares deployment.yaml and service.yaml only.
	for _, covered := range []string{"templates/deployment.yaml", "templates/service.yaml"} {
		if gaps[covered] != 0 {
			t.Errorf("%s is declared by the weak suite but has %d no-coverage mutants",
				covered, gaps[covered])
		}
	}

	console, md := r.Console(), r.Markdown(t)
	if !strings.Contains(console, fmt.Sprintf("NO COVERAGE (%d)", j.Tally.NoCoverage)) {
		t.Errorf("console has no NO COVERAGE section:\n%s", console)
	}
	for file, n := range gaps {
		if !strings.Contains(console, fmt.Sprintf(
			"%s — no suite renders this template (%d mutants never run)", file, n)) {
			t.Errorf("console does not report %s with its %d mutants", file, n)
		}
		if !strings.Contains(md, fmt.Sprintf("- `%s` — %d mutants never run", file, n)) {
			t.Errorf("markdown does not report %s with its %d mutants", file, n)
		}
	}

	// A no-coverage mutant is a skip in JUnit, never a pass and never a failure.
	for _, c := range r.JUnit(t).cases() {
		if c.Skipped != nil && strings.Contains(c.Skipped.Message, "no suite renders") {
			return
		}
	}
	t.Error("junit has no skip explaining an unrendered template")
}

// TestParallelismDoesNotChangeResults: workers are subprocesses precisely because
// helm-unittest mutates package-level Helm state, and a leak between concurrent
// renders would show up here as a status that depends on --parallel.
func TestParallelismDoesNotChangeResults(t *testing.T) {
	args := func(p string) []string {
		return []string{fixture, "-f", "tests/strong_test.yaml",
			"--max-mutants", "30", "--parallel", p, "--report", "json"}
	}
	serial := mutantKeys(run(t, args("1")...).JSON(t))
	parallel := mutantKeys(run(t, args("4")...).JSON(t))

	if !slices.Equal(serial, parallel) {
		t.Errorf("--parallel changed the outcome.\nserial:   %v\nparallel: %v", serial, parallel)
	}
	if len(serial) == 0 {
		t.Fatal("no mutants were evaluated")
	}
}

// TestKillAttributionFirstStopsAtOne, which is what makes it the fast default.
func TestKillAttributionFirstStopsAtOne(t *testing.T) {
	j := run(t, fixture, "-f", "tests/strong_test.yaml",
		"--max-mutants", "30", "--kill-attribution", "first", "--report", "json").JSON(t)

	var killed int
	for _, m := range j.Mutants {
		if m.Status != "Killed" {
			continue
		}
		killed++
		if len(m.KilledBy) != 1 {
			t.Errorf("mutant %s has %d killers under --kill-attribution=first, want 1",
				m.ID, len(m.KilledBy))
		}
	}
	if killed == 0 {
		t.Fatal("no mutants were killed, so attribution was not exercised")
	}
}

// TestKillAttributionAllCollectsEveryKiller, which is the whole reason to pay for
// it: it answers "which assertions are doing the work".
func TestKillAttributionAllCollectsEveryKiller(t *testing.T) {
	j := run(t, fixture, "-f", "tests/strong_test.yaml",
		"--max-mutants", "30", "--kill-attribution", "all", "--report", "json").JSON(t)

	var most int
	for _, m := range j.Mutants {
		if m.Status == "Killed" && len(m.KilledBy) > most {
			most = len(m.KilledBy)
		}
	}
	if most < 2 {
		t.Errorf("the most-killed mutant has %d killers under --kill-attribution=all; "+
			"expected at least one mutant caught by several assertions", most)
	}
}

// TestColourIsATriState: auto-detected from whether stdout is a terminal, and
// forceable either way. Test output is always a pipe, so the default is plain.
func TestColourIsATriState(t *testing.T) {
	const esc = "\x1b["
	base := []string{fixture, "-f", "tests/strong_test.yaml", "--max-mutants", "5"}

	// run() only injects --no-color when the caller set neither flag, so each case
	// below controls colour itself. The auto-detect path is not exercised here:
	// stdout is always a pipe under `go test`, so auto and --no-color coincide.
	forced := run(t, append(slices.Clone(base), "--color")...)
	if !strings.Contains(forced.Stdout, esc) {
		t.Error("--color did not emit ANSI escapes through a pipe")
	}

	off := run(t, append(slices.Clone(base), "--no-color")...)
	if strings.Contains(off.Stdout, esc) {
		t.Error("--no-color still emitted ANSI escapes")
	}

	// Both flags together: off wins, because suppressing output is the safe default.
	both := run(t, append(slices.Clone(base), "--color", "--no-color")...)
	if strings.Contains(both.Stdout, esc) {
		t.Error("--color --no-color emitted escapes; --no-color should win")
	}
}

// TestConsoleIsTheDefaultAndWritesNoFiles: the default invocation must not
// scatter a report directory into the user's working tree.
func TestConsoleIsTheDefaultAndWritesNoFiles(t *testing.T) {
	r := run(t, fixture, "-f", "tests/strong_test.yaml", "--max-mutants", "5")
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d\n%s", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "Mutation testing sample") {
		t.Errorf("no console report on stdout:\n%s", r.Stdout)
	}
	if names, err := osReadDirNames(r.Dir); err == nil && len(names) > 0 {
		t.Errorf("a console-only run wrote %v", names)
	}
}

// TestSurvivorsPointAtSomethingActionable: a survivor's value is that a reader can
// go to the line and add the missing assertion, so location, diff and what-ran all
// have to be there.
func TestSurvivorsPointAtSomethingActionable(t *testing.T) {
	r := strong(t)
	j := r.JSON(t)
	console := r.Console()

	var checked int
	for _, m := range j.Mutants {
		if m.Status != "Survived" {
			continue
		}
		if m.OriginalLine == "" || m.Original == m.Mutated {
			t.Errorf("survivor %s carries no displayable change: %+v", m.ID, m)
		}
		if len(m.CoveringSuites) == 0 || m.TestsRun == 0 {
			t.Errorf("survivor %s claims no tests ran, so it is not a survivor", m.ID)
		}
		if !strings.Contains(console, fmt.Sprintf("%s:%d", m.File, m.Line)) {
			t.Errorf("console does not point at %s:%d", m.File, m.Line)
		}
		checked++
		if checked == 5 {
			break
		}
	}
	if checked == 0 {
		t.Fatal("the strong suite produced no survivors to check")
	}
	if !strings.Contains(console, "— all passed") {
		t.Error("console survivors do not say what ran and passed")
	}
}
```

- [ ] **Step 2: Run them**

Run: `go test -tags=integration -count=1 -v ./test/integration/ -run 'TestNoCoverage|TestParallelism|TestKillAttribution|TestColour|TestConsoleIsTheDefault|TestSurvivors'`

Expected: all PASS.

If `TestKillAttributionAllCollectsEveryKiller` reports `most = 1`, the 30-mutant
sample happened to miss multiply-killed mutants; raise `--max-mutants` to 60 and
re-run to confirm before assuming a regression.

- [ ] **Step 3: Run the entire integration suite**

Run: `go test -tags=integration -count=1 ./test/integration/`

Expected: PASS, in under ~25s.

- [ ] **Step 4: Commit**

```bash
git add test/integration/execution_test.go
git commit -m "test: pin coverage reporting, parallelism invariance, attribution and colour"
```

---

### Task 8: Wire it into the Makefile, CI and the docs

**Files:**
- Modify: `Makefile` — add the `integration-tests` target, widen the `help` pattern
- Modify: `.github/workflows/ci.yml` — replace the hand-rolled score check
- Modify: `CLAUDE.md` — the commands list and the package table

**Interfaces:**
- Consumes: the `test/integration` package from Tasks 2-7.
- Produces: `make integration-tests`.

- [ ] **Step 1: Add the Makefile target**

In `Makefile`, insert after the `test-short` target:

```make
.PHONY: integration-tests
integration-tests: ## Run the black-box CLI integration tests
	go test -tags=integration -count=1 -timeout 15m ./test/integration/...
```

`-count=1` because these tests exec a binary and touch the filesystem: a cached
PASS would be reporting on a run that never happened. The package builds the
binary itself, so this target deliberately does not depend on `build`.

- [ ] **Step 2: Fix the help target's pattern**

`help` greps `^[a-z-]+:.*?## `, which matches `integration-tests`, but the printf
pads to 12 characters and `integration-tests` is 17. Widen the column:

```make
.PHONY: help
help:
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "};{printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
```

- [ ] **Step 3: Verify both targets**

Run: `make help && make integration-tests`

Expected: `help` lists `integration-tests  Run the black-box CLI integration tests`
in an aligned column, and the suite passes.

- [ ] **Step 4: Replace the CI shell block**

In `.github/workflows/ci.yml`, delete the `Fixture chart scores as expected` step
entirely — `TestExitOneWhenBelowTheThreshold` now asserts the weak suite scores
below 50%, and `TestExitZeroWhenTheThresholdIsMet` covers the strong side, both
with better diagnostics. Replace it with two steps: one that runs the suite, and
one that only generates the artifacts the later steps publish.

```yaml
      - name: Integration tests
        run: make integration-tests

      # Not a check: the following steps publish this directory, and the weak/strong
      # score expectations are asserted by the integration suite above.
      - name: Generate the report for publishing
        run: |
          bin/helm-mutation-test -f 'tests/strong_test.yaml' testdata/charts/sample \
            --threshold 70 --report console,markdown,json --report-dir mutation-strong
```

Leave the `Build`, `Publish the mutation report to the job summary` and
`Upload reports` steps as they are. `Build` must stay: the publishing step uses
`bin/helm-mutation-test`.

- [ ] **Step 5: Check the workflow still parses and the ordering is right**

Run:

```bash
python3 -c "import yaml,sys; d=yaml.safe_load(open('.github/workflows/ci.yml')); print([s.get('name') for s in d['jobs']['test']['steps']])"
```

Expected: the step names in order, with `Build` before
`Generate the report for publishing`, and no `Fixture chart scores as expected`.

- [ ] **Step 6: Document the command and the package**

In `CLAUDE.md`, in the `## Commands` console block, add after the `test-short` line:

```
make integration-tests  # black-box CLI tests: exit codes, all five report formats
```

Then, immediately after that block's closing fence and before the paragraph
beginning "The `runner` package tests take ~3min", insert:

```markdown
`make test` does not include the integration suite: `test/integration` is behind a
`//go:build integration` tag so the inner loop stays fast. Run it with
`make integration-tests` (~20s), which builds its own binary from source rather
than trusting whatever is in `bin/`.
```

In the same file's package table, add a final row:

```markdown
| `test/integration` | Black-box tests of the built binary: exit codes, flags, every report format |
```

- [ ] **Step 7: Full verification**

Run each and confirm:

```bash
make lint              # go vet + gofmt on cmd and internal
gofmt -l test          # the target above does not cover test/
make test              # unchanged, still ~3min, includes Task 1's new unit tests
make integration-tests # the new suite
```

Expected: `make lint` and `gofmt -l test` produce no output; both test commands
pass.

- [ ] **Step 8: Commit**

```bash
git add Makefile .github/workflows/ci.yml CLAUDE.md
git commit -m "build: add make integration-tests and run it in CI"
```

---

## Self-Review Notes

Checked against the four decisions from brainstorming:

1. **Black-box, exec the built binary** — Task 2's harness runs the binary as a subprocess; no test imports `internal/`. The wire-shape structs are deliberate local mirrors.
2. **Structural + cross-format agreement** — Task 3 parses each artifact; Task 4 asserts the five formats agree on score, tallies, exclusions and truncation. No golden files. The only hardcoded fixture number is the mutator count (8), which is a registry invariant.
3. **All four behaviour groups** — exit codes and threshold (Task 5), red baseline (Task 5), mutation-control flags (Task 6), coverage and execution flags (Task 7).
4. **Tagged package, own target, replaces the CI shell step** — Task 8, with the artifact-publishing step preserved because deleting the whole block would break the job summary and upload.

Type consistency: `result`, `jsonReport`, `tally`, `junitReport`, `junitSuite`,
`junitCase`, `strykerReport`, `strykerFile`, `strykerMutant`, `breakdown`,
`mutant`, `suiteInfo` are declared once in Task 2 and only used afterwards.
`mutantKeys` is declared in Task 6 and reused in Task 7. `osReadDirNames` is added
in Task 3 Step 4 and reused in Tasks 5 and 7.

One coupling to note: Task 4's `TestEveryFormatDisclosesTruncation` fails unless
Task 1 is done, and Task 1 also shifts two assertions in the existing
`internal/report/report_test.go`. Do Task 1 first.
