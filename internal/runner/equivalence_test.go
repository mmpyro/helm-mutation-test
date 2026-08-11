package runner

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmpyro/helm-mutation-test/internal/config"
	"github.com/mmpyro/helm-mutation-test/internal/model"
	"github.com/mmpyro/helm-mutation-test/internal/mutator"
	"github.com/mmpyro/helm-mutation-test/internal/source"
)

func TestCheckEquivalenceOnlyTouchesSurvivors(t *testing.T) {
	// The pass may only ever turn Survived into Equivalent. Anything else would
	// let it rewrite a kill, an invalid or a no-coverage verdict.
	mutants := []model.Mutant{
		{ID: "k", Status: model.StatusKilled, File: "templates/deployment.yaml"},
		{ID: "n", Status: model.StatusNoCoverage, File: "templates/deployment.yaml"},
		{ID: "i", Status: model.StatusInvalid, File: "templates/deployment.yaml"},
	}
	before := make([]model.Status, len(mutants))
	for i, m := range mutants {
		before[i] = m.Status
	}

	CheckEquivalence(context.Background(), mutants, EquivalenceInput{
		ChartDir: fixtureChart,
		Parallel: 2,
	})

	for i, m := range mutants {
		if m.Status != before[i] {
			t.Errorf("%s: status changed from %s to %s", m.ID, before[i], m.Status)
		}
	}
}

// loadDeploymentSource re-reads the fixture's deployment.yaml from a chart
// copy, for tests that need real byte offsets into a real template rather
// than a synthetic one.
func loadDeploymentSource(t *testing.T, dir string) *source.File {
	t.Helper()
	f, err := source.Load(filepath.Join(dir, "templates/deployment.yaml"), "templates/deployment.yaml", source.KindTemplate)
	if err != nil {
		t.Fatalf("source.Load: %v", err)
	}
	return f
}

// nindentMutant builds a Survived mutant that bumps the deployment's resources
// nindent by one — the canonical equivalent mutation, since YAML block
// indentation depth carries no meaning as long as it stays deeper than its
// parent.
func nindentMutant(t *testing.T, f *source.File, coveringSuite string) model.Mutant {
	t.Helper()
	start := strings.Index(f.Text(), "nindent 12")
	if start < 0 {
		t.Fatal("fixture no longer contains \"nindent 12\" in deployment.yaml; update this test")
	}
	return model.Mutant{
		ID:             "nindent",
		Status:         model.StatusSurvived,
		File:           "templates/deployment.yaml",
		StartByte:      start,
		EndByte:        start + len("nindent 12"),
		Mutated:        "nindent 13",
		CoveringSuites: []string{coveringSuite},
	}
}

func TestCheckEquivalencePromotesAProvableSurvivorToEquivalent(t *testing.T) {
	// The end-to-end positive path: a real suite, a real chart, a mutation that
	// is genuinely unkillable. TestCheckEquivalenceOnlyTouchesSurvivors alone
	// never exercises a real promotion because it constructs zero survivors.
	dir := fixture(t)
	suites := writeSuite(t, dir, "equiv_promotion_test.yaml", `
suite: covers deployment with default values
tests:
  - it: renders
    asserts:
      - isKind:
          of: Deployment
`)
	f := loadDeploymentSource(t, dir)
	mutants := []model.Mutant{nindentMutant(t, f, suites[0].Key.File)}

	res := CheckEquivalence(context.Background(), mutants, EquivalenceInput{
		ChartDir: dir,
		Suites:   suites,
		Files:    map[string]*source.File{"templates/deployment.yaml": f},
		Parallel: 1,
	})

	if res.Equivalent != 1 {
		t.Fatalf("want 1 promotion, got %d", res.Equivalent)
	}
	if res.Unchecked != 0 {
		t.Fatalf("a decided verdict must not count as unchecked, got %d", res.Unchecked)
	}
	if mutants[0].Status != model.StatusEquivalent {
		t.Fatalf("want Equivalent, got %s: %s", mutants[0].Status, mutants[0].Detail)
	}
	if mutants[0].Detail == "" {
		t.Fatal("a promoted mutant must still explain the verdict")
	}
}

func TestCheckEquivalenceLeavesAnUnprovenSurvivorAlone(t *testing.T) {
	// autoscaling.enabled defaults to false, so hpa.yaml's entire body sits
	// behind one outer {{- if .Values.autoscaling.enabled }} and never renders.
	// A mutation to a pipeline expression inside it produces identical output
	// under every covering context, but the probe cannot prove the span itself
	// ever ran — that must not be promoted, or a missing test (no job ever
	// enables autoscaling) would be scored as an unkillable mutant instead of a
	// coverage gap.
	dir := fixture(t)
	suites := writeSuite(t, dir, "equiv_unproven_test.yaml", `
suite: covers hpa with default values
tests:
  - it: renders
    asserts:
      - hasDocuments:
          count: 0
`)
	f, err := source.Load(filepath.Join(dir, "templates/hpa.yaml"), "templates/hpa.yaml", source.KindTemplate)
	if err != nil {
		t.Fatalf("source.Load: %v", err)
	}
	needle := ".Values.autoscaling.minReplicas"
	start := strings.Index(f.Text(), needle)
	if start < 0 {
		t.Fatal("fixture no longer contains autoscaling.minReplicas in hpa.yaml; update this test")
	}
	mutants := []model.Mutant{{
		ID:             "hpa",
		Status:         model.StatusSurvived,
		File:           "templates/hpa.yaml",
		StartByte:      start,
		EndByte:        start + len(needle),
		Mutated:        "99",
		CoveringSuites: []string{suites[0].Key.File},
	}}

	res := CheckEquivalence(context.Background(), mutants, EquivalenceInput{
		ChartDir: dir,
		Suites:   suites,
		Files:    map[string]*source.File{"templates/hpa.yaml": f},
		Parallel: 1,
	})

	if res.Equivalent != 0 {
		t.Fatalf("an unreached mutation must not be promoted, got %d promotions", res.Equivalent)
	}
	if res.Unchecked != 0 {
		t.Fatalf("\"not exercised\" is a verdict, not a failure to check; got %d unchecked", res.Unchecked)
	}
	if mutants[0].Status != model.StatusSurvived {
		t.Fatalf("want Survived, got %s", mutants[0].Status)
	}
	if !strings.Contains(mutants[0].Detail, "not exercised") {
		t.Fatalf("detail should say the span never ran: %q", mutants[0].Detail)
	}
}

func TestCheckEquivalenceRecordsWhyEachSkippedSuiteWasSkipped(t *testing.T) {
	// contextsBySuiteFile skips a suite for two unrelated reasons: a fake
	// Kubernetes provider, or a failure to extract its render contexts. Both
	// used to report the same "fake Kubernetes provider" text regardless of
	// cause, which would misreport a broken values file as the provider guard.
	dir := fixture(t)
	kubeSuites := writeSuite(t, dir, "equiv_skip_kube_test.yaml", `
suite: uses a fake kubernetes client
kubernetesProvider:
  objects:
    - apiVersion: v1
      kind: ConfigMap
      metadata:
        name: foo
tests:
  - it: renders
    asserts:
      - isKind:
          of: Deployment
`)
	badValuesSuites := writeSuite(t, dir, "equiv_skip_badvalues_test.yaml", `
suite: references a missing values file
tests:
  - it: renders
    values:
      - does-not-exist.yaml
    asserts:
      - isKind:
          of: Deployment
`)

	f := loadDeploymentSource(t, dir)
	mutants := []model.Mutant{
		nindentMutant(t, f, kubeSuites[0].Key.File),
		nindentMutant(t, f, badValuesSuites[0].Key.File),
	}
	mutants[0].ID, mutants[1].ID = "kube", "badvalues"

	res := CheckEquivalence(context.Background(), mutants, EquivalenceInput{
		ChartDir: dir,
		Suites:   append(kubeSuites, badValuesSuites...),
		Files:    map[string]*source.File{"templates/deployment.yaml": f},
		Parallel: 1,
	})

	if res.Equivalent != 0 {
		t.Fatalf("a skipped suite must never promote a survivor, got %d promotions", res.Equivalent)
	}
	// Both survivors went unexamined. A report that showed only "0 equivalent"
	// would read as a verified run.
	if res.Unchecked != 2 {
		t.Errorf("want both survivors counted as unchecked, got %d", res.Unchecked)
	}
	for _, m := range mutants {
		if m.Status != model.StatusSurvived {
			t.Errorf("%s: want Survived, got %s", m.ID, m.Status)
		}
	}
	if !strings.Contains(mutants[0].Detail, "fake Kubernetes provider") {
		t.Fatalf("kube-provider skip should say so: %q", mutants[0].Detail)
	}
	if strings.Contains(mutants[1].Detail, "fake Kubernetes provider") {
		t.Fatalf("a values-file failure must not be blamed on the Kubernetes-provider guard: %q", mutants[1].Detail)
	}
	if mutants[0].Detail == mutants[1].Detail {
		t.Fatal("the two skip reasons must be distinguishable")
	}
}

// shortCircuitFixture copies the short-circuit chart into a temp dir. It is a
// chart of its own rather than another template in testdata/charts/sample so
// that adding it does not move the fixture's scores.
func shortCircuitFixture(t *testing.T) string {
	t.Helper()
	src, err := filepath.Abs("../../testdata/charts/shortcircuit")
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "shortcircuit")
	if err := copyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	return dst
}

// TestShortCircuitedOperandIsNotJudgedEquivalent guards the line between "no test
// reaches this" and "no test can catch this", at the one place the two are easiest
// to confuse.
//
// The chart's only branch is `{{- if and .Values.ingress.enabled (eq
// .Values.ingress.className "nginx") }}`, with ingress.enabled false under every
// covering job. Go templates short-circuit `and`, so the `eq` never evaluates and
// mutating it changes nothing — but an assertion that enables the branch kills
// those mutants outright, so they are missing tests, not unkillable mutants.
//
// A probe over the whole `and ...` pipeline errors regardless (it is reached), and
// reading that as proof of execution promoted both of them to Equivalent, deleting
// two real findings and raising the score. The probe must cover no more than the
// mutated operand.
func TestShortCircuitedOperandIsNotJudgedEquivalent(t *testing.T) {
	dir := shortCircuitFixture(t)
	cfg := config.Defaults()
	cfg.ChartPath = dir
	cfg.TestFiles = []string{"tests/configmap_test.yaml"}
	cfg.Parallel = 4
	run := runSession(t, cfg)

	f, err := source.Load(
		filepath.Join(dir, "templates/configmap.yaml"), "templates/configmap.yaml", source.KindTemplate)
	if err != nil {
		t.Fatalf("source.Load: %v", err)
	}
	guarded := `(eq .Values.ingress.className "nginx")`
	lo := strings.Index(f.Text(), guarded)
	if lo < 0 {
		t.Fatalf("fixture no longer contains %s; update this test", guarded)
	}
	hi := lo + len(guarded)

	var inside int
	for _, m := range run.Mutants {
		if m.File != "templates/configmap.yaml" || m.StartByte < lo || m.EndByte > hi {
			continue
		}
		inside++
		if m.Status != model.StatusEquivalent {
			continue
		}
		t.Errorf("%s at %d:%d (%q -> %q) was judged equivalent, but enabling the branch kills it: %s",
			m.Mutator, m.StartByte, m.EndByte, m.Original, m.Mutated, m.Detail)
	}
	if inside < 2 {
		t.Fatalf("expected mutants inside the short-circuited operand, found %d", inside)
	}
	// Without this the test would also pass with equivalence detection switched
	// off entirely, which is not the property being protected.
	if run.Tally.Equivalent == 0 {
		t.Error("the pass proved nothing equivalent anywhere in this chart, so it was not really exercised")
	}
}

// rangeLoopFixture copies the range chart into a temp dir. It is a chart of its
// own rather than another template in testdata/charts/sample so that adding it
// does not move the fixture's scores.
func rangeLoopFixture(t *testing.T) string {
	t.Helper()
	src, err := filepath.Abs("../../testdata/charts/rangeloop")
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "rangeloop")
	if err := copyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	return dst
}

// rangeEmptyByStatus counts range-empty mutants in run by status.
func rangeEmptyByStatus(run *model.Run) map[model.Status]int {
	out := map[model.Status]int{}
	for _, m := range run.Mutants {
		if m.Mutator == mutator.IDRangeEmpty {
			out[m.Status]++
		}
	}
	return out
}

func scoreRangeLoop(t *testing.T, suite string) *model.Run {
	t.Helper()
	cfg := config.Defaults()
	cfg.ChartPath = rangeLoopFixture(t)
	cfg.TestFiles = []string{suite}
	cfg.Parallel = 4
	return runSession(t, cfg)
}

// TestRangeMutantSurvivesAWeakSuiteAndIsKilledByAStrongOne is the evidence that
// range-empty is a useful mutant rather than a guard-worthy one: it must separate
// a suite that only checks the ConfigMap exists from one that asserts what the
// loops produced. A mutation neither suite can tell apart would be noise.
func TestRangeMutantSurvivesAWeakSuiteAndIsKilledByAStrongOne(t *testing.T) {
	weak := rangeEmptyByStatus(scoreRangeLoop(t, "tests/weak_test.yaml"))
	strong := rangeEmptyByStatus(scoreRangeLoop(t, "tests/strong_test.yaml"))

	if weak[model.StatusKilled] != 0 {
		t.Errorf("the weak suite killed %d range mutants; it asserts nothing any loop produces",
			weak[model.StatusKilled])
	}
	if weak[model.StatusSurvived] < 2 {
		t.Errorf("the weak suite left %d range survivors, want at least 2 (ports and labels)",
			weak[model.StatusSurvived])
	}
	if strong[model.StatusKilled] < 2 {
		t.Errorf("the strong suite killed %d range mutants, want at least 2 (ports and labels): %v",
			strong[model.StatusKilled], strong)
	}
	if strong[model.StatusSurvived] != 0 {
		t.Errorf("the strong suite left %d range survivors, want 0", strong[model.StatusSurvived])
	}
}

// TestEmptyRangeIsJudgedEquivalent: extras is empty under every covering context,
// so forcing its loop to zero iterations cannot change any rendered manifest and
// no assertion could ever catch it. range evaluates its pipeline even when the
// result is empty, so the probe legitimately proves execution and the verdict is
// Equivalent — excluded from the score and named in the report, not hidden.
//
// This is also the end-to-end proof that the declaration-carrying probe parses:
// if it did not, the checker would read the parse failure as proof of execution
// and reach this verdict for the wrong reason, so the count below would be too
// high rather than too low.
func TestEmptyRangeIsJudgedEquivalent(t *testing.T) {
	got := rangeEmptyByStatus(scoreRangeLoop(t, "tests/strong_test.yaml"))
	if got[model.StatusEquivalent] != 1 {
		t.Errorf("range-empty statuses = %v, want exactly 1 equivalent (the extras loop)", got)
	}
}

// TestCheckEquivalenceCountsEverySurvivorItCouldNotLoad: when the chart itself
// will not load the pass examines nothing, yet the run still records that the
// check ran. Without the Unchecked count that is indistinguishable from a clean
// "checked every survivor, none are equivalent" result.
func TestCheckEquivalenceCountsEverySurvivorItCouldNotLoad(t *testing.T) {
	mutants := []model.Mutant{
		{ID: "a", Status: model.StatusSurvived, File: "templates/deployment.yaml"},
		{ID: "b", Status: model.StatusSurvived, File: "templates/deployment.yaml"},
		{ID: "k", Status: model.StatusKilled, File: "templates/deployment.yaml"},
	}
	res := CheckEquivalence(context.Background(), mutants, EquivalenceInput{
		ChartDir: filepath.Join(t.TempDir(), "no-such-chart"),
		Parallel: 2,
	})

	if res.Equivalent != 0 {
		t.Fatalf("an unloadable chart cannot prove anything, got %d promotions", res.Equivalent)
	}
	if res.Unchecked != 2 {
		t.Errorf("want both survivors counted as unchecked, got %d", res.Unchecked)
	}
	for _, m := range mutants[:2] {
		if !strings.Contains(m.Detail, "inconclusive") {
			t.Errorf("%s: detail should say the check was inconclusive: %q", m.ID, m.Detail)
		}
	}
}
