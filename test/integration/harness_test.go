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
	Equivalent int `json:"equivalent"`
	Timeout    int `json:"timeout"`
	Errored    int `json:"error"`
}

func (t tally) total() int {
	return t.Killed + t.Survived + t.NoCoverage + t.Invalid + t.Equivalent + t.Timeout + t.Errored
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
