package runner

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmpyro/helm-mutation-test/internal/model"
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

	n := CheckEquivalence(context.Background(), mutants, EquivalenceInput{
		ChartDir: dir,
		Suites:   suites,
		Files:    map[string]*source.File{"templates/deployment.yaml": f},
		Parallel: 1,
	})

	if n != 1 {
		t.Fatalf("want 1 promotion, got %d", n)
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

	n := CheckEquivalence(context.Background(), mutants, EquivalenceInput{
		ChartDir: dir,
		Suites:   suites,
		Files:    map[string]*source.File{"templates/hpa.yaml": f},
		Parallel: 1,
	})

	if n != 0 {
		t.Fatalf("an unreached mutation must not be promoted, got %d promotions", n)
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

	n := CheckEquivalence(context.Background(), mutants, EquivalenceInput{
		ChartDir: dir,
		Suites:   append(kubeSuites, badValuesSuites...),
		Files:    map[string]*source.File{"templates/deployment.yaml": f},
		Parallel: 1,
	})

	if n != 0 {
		t.Fatalf("a skipped suite must never promote a survivor, got %d promotions", n)
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
