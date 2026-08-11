package runner

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mmpyro/helm-mutation-test/internal/config"
	"github.com/mmpyro/helm-mutation-test/internal/model"
)

func sessionCfg(t *testing.T, testFile string) config.Config {
	t.Helper()
	c := config.Defaults()
	c.ChartPath = fixture(t)
	c.TestFiles = []string{testFile}
	c.Parallel = 4
	return c
}

func runSession(t *testing.T, cfg config.Config) *model.Run {
	t.Helper()
	s := &Session{Cfg: cfg}
	run, err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("session failed: %v", err)
	}
	return run
}

// TestWeakSuiteScoresLowAndStrongScoresHigh is the whole tool in one assertion.
// The two suites cover the same templates and both pass, so helm-unittest reports
// them as equally green; the mutation score is what separates them.
func TestWeakSuiteScoresLowAndStrongScoresHigh(t *testing.T) {
	weak := runSession(t, sessionCfg(t, "tests/weak_test.yaml"))
	strong := runSession(t, sessionCfg(t, "tests/strong_test.yaml"))

	t.Logf("weak:   score %.1f%%  killed %d survived %d  (invalid %d, no-coverage %d, generated %d)",
		weak.Score(), weak.Tally.Killed, weak.Tally.Survived,
		weak.Tally.Invalid, weak.Tally.NoCoverage, weak.Generated)
	t.Logf("strong: score %.1f%%  killed %d survived %d  (invalid %d, no-coverage %d, generated %d)",
		strong.Score(), strong.Tally.Killed, strong.Tally.Survived,
		strong.Tally.Invalid, strong.Tally.NoCoverage, strong.Generated)

	if weak.Generated == 0 {
		t.Fatal("no mutants were generated")
	}
	if weak.Generated != strong.Generated {
		t.Errorf("both runs mutate the same chart, so generation must match: %d vs %d",
			weak.Generated, strong.Generated)
	}
	if weak.Score() >= strong.Score() {
		t.Errorf("the strong suite must outscore the weak one: weak %.1f%%, strong %.1f%%",
			weak.Score(), strong.Score())
	}
	if weak.Score() > 45 {
		t.Errorf("the weak suite scored %.1f%%, expected well under 45%%", weak.Score())
	}
	if strong.Score() < 70 {
		t.Errorf("the strong suite scored %.1f%%, expected at least 70%%", strong.Score())
	}
}

// TestInvalidMutantRateIsLow guards mutator quality. A high Invalid rate means our
// mutations break rendering rather than probing assertions, which is our bug.
func TestInvalidMutantRateIsLow(t *testing.T) {
	run := runSession(t, sessionCfg(t, "tests/strong_test.yaml"))
	total := run.Tally.Total()
	if total == 0 {
		t.Fatal("no mutants evaluated")
	}
	rate := float64(run.Tally.Invalid) / float64(total) * 100
	t.Logf("invalid: %d of %d (%.1f%%)", run.Tally.Invalid, total, rate)
	if rate > 20 {
		t.Errorf("invalid mutant rate is %.1f%%, expected under 20%%; the mutators are too blunt", rate)
	}
}

func TestIngressIsNoCoverageForTheWeakSuite(t *testing.T) {
	// The weak suite declares only deployment.yaml and service.yaml, so every
	// ingress mutant should be NoCoverage rather than Survived. Calling it
	// "survived" would blame the tests for something they never ran.
	run := runSession(t, sessionCfg(t, "tests/weak_test.yaml"))

	var ingressTotal, ingressNoCoverage int
	for _, m := range run.Mutants {
		if m.File != "templates/ingress.yaml" {
			continue
		}
		ingressTotal++
		if m.Status == model.StatusNoCoverage {
			ingressNoCoverage++
		}
	}
	if ingressTotal == 0 {
		t.Fatal("no ingress mutants were generated")
	}
	if ingressNoCoverage != ingressTotal {
		t.Errorf("%d of %d ingress mutants are NoCoverage, want all of them",
			ingressNoCoverage, ingressTotal)
	}
}

func TestKilledMutantsCarryAttribution(t *testing.T) {
	run := runSession(t, sessionCfg(t, "tests/strong_test.yaml"))
	var killed int
	for _, m := range run.Mutants {
		if m.Status != model.StatusKilled {
			continue
		}
		killed++
		if len(m.KilledBy) == 0 {
			t.Errorf("mutant %s is Killed but names no test", m.ID)
			continue
		}
		k := m.KilledBy[0]
		if k.Suite == "" || k.Test == "" || k.AssertType == "" {
			t.Errorf("mutant %s has incomplete attribution: %+v", m.ID, k)
		}
	}
	if killed == 0 {
		t.Fatal("the strong suite killed nothing, which cannot be right")
	}
}

func TestSurvivedMutantsCarryDisplayableDiff(t *testing.T) {
	run := runSession(t, sessionCfg(t, "tests/weak_test.yaml"))
	survived := run.Survived()
	if len(survived) == 0 {
		t.Fatal("the weak suite should leave survivors")
	}
	for _, m := range survived {
		if m.OriginalLine == "" {
			t.Errorf("mutant %s has no original line to show", m.ID)
		}
		if m.OriginalLine == m.MutatedLine {
			t.Errorf("mutant %s reports an identical before/after line: %q", m.ID, m.OriginalLine)
		}
		if m.Line < 1 {
			t.Errorf("mutant %s has no line number", m.ID)
		}
	}
}

// TestRunIsDeterministic underpins reproducible reports and --max-mutants sampling.
func TestRunIsDeterministic(t *testing.T) {
	a := runSession(t, sessionCfg(t, "tests/weak_test.yaml"))
	b := runSession(t, sessionCfg(t, "tests/weak_test.yaml"))

	if len(a.Mutants) != len(b.Mutants) {
		t.Fatalf("mutant counts differ: %d vs %d", len(a.Mutants), len(b.Mutants))
	}
	for i := range a.Mutants {
		x, y := a.Mutants[i], b.Mutants[i]
		if x.ID != y.ID {
			t.Fatalf("mutant %d: ID %q vs %q", i, x.ID, y.ID)
		}
		if x.Status != y.Status {
			t.Errorf("mutant %s: status %q vs %q", x.ID, x.Status, y.Status)
		}
	}
	if a.Score() != b.Score() {
		t.Errorf("scores differ: %v vs %v", a.Score(), b.Score())
	}
}

// TestParallelismDoesNotChangeResults is the guard on the in-process concurrency
// decision. helm-unittest has no mutable package state and no os.Chdir, so
// per-worker chart copies should make parallel runs identical to serial ones.
func TestParallelismDoesNotChangeResults(t *testing.T) {
	serialCfg := sessionCfg(t, "tests/strong_test.yaml")
	serialCfg.Parallel = 1
	serial := runSession(t, serialCfg)

	parallelCfg := sessionCfg(t, "tests/strong_test.yaml")
	parallelCfg.Parallel = 8
	parallel := runSession(t, parallelCfg)

	if len(serial.Mutants) != len(parallel.Mutants) {
		t.Fatalf("mutant counts differ: %d vs %d", len(serial.Mutants), len(parallel.Mutants))
	}
	for i := range serial.Mutants {
		s, p := serial.Mutants[i], parallel.Mutants[i]
		if s.ID != p.ID {
			t.Fatalf("mutant %d: ID %q vs %q", i, s.ID, p.ID)
		}
		if s.Status != p.Status {
			t.Errorf("mutant %s: serial %q, parallel %q", s.ID, s.Status, p.Status)
		}
	}
	if serial.Score() != parallel.Score() {
		t.Errorf("scores differ between -p 1 (%.2f) and -p 8 (%.2f)", serial.Score(), parallel.Score())
	}
}

func TestMaxMutantsCapReportsWhatItDropped(t *testing.T) {
	cfg := sessionCfg(t, "tests/weak_test.yaml")
	cfg.MaxMutants = 5
	run := runSession(t, cfg)

	if len(run.Mutants) != 5 {
		t.Errorf("got %d mutants, want 5", len(run.Mutants))
	}
	// A silently truncated run would read as full coverage.
	if run.Capped <= 0 {
		t.Error("the cap must report how many mutants it dropped")
	}
	if run.Generated != len(run.Mutants)+run.Capped {
		t.Errorf("generated (%d) should equal kept (%d) + dropped (%d)",
			run.Generated, len(run.Mutants), run.Capped)
	}
}

func TestMaxMutantsSamplingIsSeedStable(t *testing.T) {
	ids := func(seed int64) []string {
		cfg := sessionCfg(t, "tests/weak_test.yaml")
		cfg.MaxMutants, cfg.Seed = 6, seed
		run := runSession(t, cfg)
		out := make([]string, len(run.Mutants))
		for i, m := range run.Mutants {
			out[i] = m.ID
		}
		return out
	}
	a, b := ids(1), ids(1)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("the same seed must select the same mutants: %v vs %v", a, b)
		}
	}
	if c := ids(99); equalStrings(a, c) {
		t.Error("a different seed should generally select a different sample")
	}
}

func TestMutatorSelectionIsHonoured(t *testing.T) {
	cfg := sessionCfg(t, "tests/weak_test.yaml")
	cfg.Mutators = []string{"cond-negate"}
	run := runSession(t, cfg)

	if len(run.Mutants) == 0 {
		t.Fatal("no cond-negate mutants generated")
	}
	for _, m := range run.Mutants {
		if m.Mutator != "cond-negate" {
			t.Errorf("mutant %s used %q despite --mutators=cond-negate", m.ID, m.Mutator)
		}
	}
}

func TestExcludeSkipsFiles(t *testing.T) {
	cfg := sessionCfg(t, "tests/weak_test.yaml")
	cfg.Exclude = []string{"values.yaml"}
	run := runSession(t, cfg)
	for _, m := range run.Mutants {
		if m.File == "values.yaml" {
			t.Errorf("values.yaml was excluded but mutant %s targets it", m.ID)
		}
	}
}

func TestSessionPropagatesBaselineFailure(t *testing.T) {
	cfg := sessionCfg(t, "tests/nonexistent_*.yaml")
	s := &Session{Cfg: cfg}
	_, err := s.Run(context.Background())
	if err == nil {
		t.Fatal("expected a baseline failure")
	}
	if _, ok := err.(*BaselineFailure); !ok {
		t.Fatalf("error type = %T, want *BaselineFailure", err)
	}
}

func TestSessionRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before any work starts

	s := &Session{Cfg: sessionCfg(t, "tests/weak_test.yaml")}
	run, err := s.Run(ctx)
	if err != nil {
		return // aborting outright is a fine outcome
	}
	// Otherwise every mutant must still carry an explicit status, so a truncated
	// run can never look like a complete one.
	for _, m := range run.Mutants {
		if m.Status == "" {
			t.Errorf("mutant %s has no status after cancellation", m.ID)
		}
	}
}

func TestRunSerialisesToJSON(t *testing.T) {
	run := runSession(t, sessionCfg(t, "tests/weak_test.yaml"))
	b, err := json.Marshal(run)
	if err != nil {
		t.Fatalf("the run model must be JSON-serialisable: %v", err)
	}
	var back model.Run
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("round trip failed: %v", err)
	}
	if len(back.Mutants) != len(run.Mutants) {
		t.Errorf("round trip lost mutants: %d vs %d", len(back.Mutants), len(run.Mutants))
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
