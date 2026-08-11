package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture returns the path to the sample chart, copied into a temp dir so a test
// can mutate it without touching the repository.
func fixture(t *testing.T) string {
	t.Helper()
	src, err := filepath.Abs("../../testdata/charts/sample")
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "sample")
	if err := copyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	return dst
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
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
}

func weakOpts() Options {
	return Options{TestFiles: []string{"tests/weak_test.yaml"}, WithSubChart: true}
}

func strongOpts() Options {
	return Options{TestFiles: []string{"tests/strong_test.yaml"}, WithSubChart: true}
}

func TestMain(m *testing.M) {
	SilenceLibraryLogging(false)
	os.Exit(m.Run())
}

func TestRunBaselinePassesOnTheFixture(t *testing.T) {
	b, err := RunBaseline(fixture(t), strongOpts())
	if err != nil {
		t.Fatalf("the fixture's strong suite must pass unmutated: %v", err)
	}
	if b.ChartName != "sample" {
		t.Errorf("chart name = %q, want sample", b.ChartName)
	}
	// The strong suite file declares three suites separated by "---".
	if len(b.Suites) != 3 {
		t.Errorf("got %d suites, want 3: %+v", len(b.Suites), b.Suites)
	}
	if b.TestCount != 14 {
		t.Errorf("got %d tests, want 14", b.TestCount)
	}
	if b.Duration <= 0 {
		t.Error("baseline duration should be recorded, it seeds the per-mutant timeout")
	}
}

func TestRunBaselineCapturesSuiteMetadataForCoverage(t *testing.T) {
	b, err := RunBaseline(fixture(t), strongOpts())
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*Suite{}
	for _, s := range b.Suites {
		byName[s.Key.Name] = s
	}

	dep, ok := byName["strong assertions on the deployment"]
	if !ok {
		t.Fatalf("suite not found; got %v", byName)
	}
	if len(dep.Templates) != 1 || dep.Templates[0] != "deployment.yaml" {
		t.Errorf("Templates = %v, want [deployment.yaml]", dep.Templates)
	}
	if dep.CoversAllTemplates() {
		t.Error("a suite declaring templates does not cover everything")
	}
	if len(dep.TestNames) != 10 {
		t.Errorf("got %d test names, want 10: %v", len(dep.TestNames), dep.TestNames)
	}
}

func TestRunBaselineDetectsARedSuite(t *testing.T) {
	// The gate that protects the whole measurement: a mutation score over a
	// failing suite is meaningless, so this must be a hard stop.
	dir := fixture(t)
	broken := filepath.Join(dir, "tests", "broken_test.yaml")
	content := `suite: deliberately failing
templates:
  - deployment.yaml
tests:
  - it: asserts something false
    asserts:
      - equal:
          path: spec.replicas
          value: 999
`
	if err := os.WriteFile(broken, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := RunBaseline(dir, Options{TestFiles: []string{"tests/broken_test.yaml"}})
	if err == nil {
		t.Fatal("expected a BaselineFailure for a red suite")
	}
	var bf *BaselineFailure
	if !asBaselineFailure(err, &bf) {
		t.Fatalf("error type = %T, want *BaselineFailure", err)
	}
	if !strings.Contains(bf.Detail, "asserts something false") {
		t.Errorf("the failure should name the broken test, got:\n%s", bf.Detail)
	}
}

func TestRunBaselineRejectsAChartWithNoTests(t *testing.T) {
	_, err := RunBaseline(fixture(t), Options{TestFiles: []string{"tests/nonexistent_*.yaml"}})
	if err == nil {
		t.Fatal("expected a BaselineFailure when no suites match")
	}
	var bf *BaselineFailure
	if !asBaselineFailure(err, &bf) {
		t.Fatalf("error type = %T, want *BaselineFailure", err)
	}
	if !strings.Contains(bf.Detail, "no test suites matched") {
		t.Errorf("got: %s", bf.Detail)
	}
}

func TestRunBaselineRejectsAMissingChart(t *testing.T) {
	if _, err := RunBaseline(filepath.Join(t.TempDir(), "nope"), weakOpts()); err == nil {
		t.Fatal("expected an error for a nonexistent chart")
	}
}

// TestSeamProducesPerAssertionAttribution is the core reason this tool drives
// TestSuite.RunV3 instead of TestRunner.RunV3: we need to know which assertion
// in which test caught a change, not merely that something failed.
func TestSeamProducesPerAssertionAttribution(t *testing.T) {
	dir := fixture(t)
	// Break the chart the way a mutant would: change the replica count.
	dep := filepath.Join(dir, "templates", "deployment.yaml")
	b, err := os.ReadFile(dep)
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(string(b),
		"replicas: {{ .Values.replicaCount }}", "replicas: 999", 1)
	if mutated == string(b) {
		t.Fatal("test setup failed to alter the template")
	}
	if err := os.WriteFile(dep, []byte(mutated), 0o644); err != nil {
		t.Fatal(err)
	}

	chart, err := LoadChart(dir)
	if err != nil {
		t.Fatal(err)
	}
	suites, err := DiscoverSuites(dir, chart, strongOpts())
	if err != nil {
		t.Fatal(err)
	}
	outcomes := RunSuites(chart, suites, false)

	status, killedBy, testsRun := Classify(outcomes)
	if status != "Killed" {
		t.Fatalf("status = %q, want Killed", status)
	}
	if testsRun == 0 {
		t.Fatal("no tests ran")
	}
	var found bool
	for _, k := range killedBy {
		if k.AssertType == "equal" && strings.Contains(k.Test, "pins every deployment field") {
			found = true
			if k.FailInfo == "" {
				t.Error("attribution should carry the assertion's failure text")
			}
		}
	}
	if !found {
		t.Errorf("expected the 'equal' assertion on replicas to be named; got %+v", killedBy)
	}
}

// TestWeakSuiteFailsToNoticeTheSameChange is the fixture's whole purpose: the
// identical mutation that the strong suite catches slips past the weak one.
func TestWeakSuiteFailsToNoticeTheSameChange(t *testing.T) {
	dir := fixture(t)
	dep := filepath.Join(dir, "templates", "deployment.yaml")
	b, err := os.ReadFile(dep)
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(string(b),
		"replicas: {{ .Values.replicaCount }}", "replicas: 999", 1)
	if err := os.WriteFile(dep, []byte(mutated), 0o644); err != nil {
		t.Fatal(err)
	}

	chart, err := LoadChart(dir)
	if err != nil {
		t.Fatal(err)
	}
	suites, err := DiscoverSuites(dir, chart, weakOpts())
	if err != nil {
		t.Fatal(err)
	}
	status, _, testsRun := Classify(RunSuites(chart, suites, true))
	if status != "Survived" {
		t.Fatalf("status = %q, want Survived: the weak suite asserts nothing about replicas", status)
	}
	if testsRun == 0 {
		t.Fatal("the weak suite should still have run tests")
	}
}

// TestRenderBreakingChangeIsInvalidNotKilled proves the classification that keeps
// the score honest.
func TestRenderBreakingChangeIsInvalidNotKilled(t *testing.T) {
	dir := fixture(t)
	dep := filepath.Join(dir, "templates", "deployment.yaml")
	b, err := os.ReadFile(dep)
	if err != nil {
		t.Fatal(err)
	}
	// Point an include at a template that does not exist. Measurement showed this
	// is the mutation shape that genuinely errors at render time — notably, an
	// altered `nindent` width does NOT, which is why num-literal no longer
	// exempts layout arguments.
	mutated := strings.Replace(string(b),
		`{{- include "sample.labels" . | nindent 4 }}`,
		`{{- include "sample.nonexistent" . | nindent 4 }}`, 1)
	if mutated == string(b) {
		t.Fatal("test setup failed to alter the template")
	}
	if err := os.WriteFile(dep, []byte(mutated), 0o644); err != nil {
		t.Fatal(err)
	}

	chart, err := LoadChart(dir)
	if err != nil {
		t.Fatal(err)
	}
	suites, err := DiscoverSuites(dir, chart, weakOpts())
	if err != nil {
		t.Fatal(err)
	}
	status, killedBy, _ := Classify(RunSuites(chart, suites, true))
	if status != "Invalid" {
		t.Fatalf("status = %q, want Invalid: a render break is caught by every test and so measures nothing", status)
	}
	if len(killedBy) != 0 {
		t.Errorf("an Invalid mutant must not claim kill attribution: %+v", killedBy)
	}
}

// TestMutantRunsDoNotWriteSnapshots guards against a mutant run rewriting the
// chart's committed snapshot files.
func TestMutantRunsDoNotWriteSnapshots(t *testing.T) {
	dir := fixture(t)
	chart, err := LoadChart(dir)
	if err != nil {
		t.Fatal(err)
	}
	suites, err := DiscoverSuites(dir, chart, strongOpts())
	if err != nil {
		t.Fatal(err)
	}
	RunSuites(chart, suites, false)

	snapDir := filepath.Join(dir, "tests", "__snapshot__")
	entries, err := os.ReadDir(snapDir)
	if err != nil {
		return // no snapshot dir at all is the best outcome
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.Size() > 0 {
			t.Errorf("a mutant run wrote snapshot content to %s (%d bytes)", e.Name(), info.Size())
		}
	}
}

func asBaselineFailure(err error, target **BaselineFailure) bool {
	if bf, ok := err.(*BaselineFailure); ok {
		*target = bf
		return true
	}
	return false
}
