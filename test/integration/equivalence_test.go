//go:build integration

package integration

import (
	"strings"
	"testing"
)

// TestConsoleNamesEquivalentMutantsByDefault is the black-box counterpart to
// internal/runner's TestStrongSuiteHasNoRealSurvivors: a user who never reads the
// JSON still needs to see that the strong suite's would-be survivors were proven
// unkillable, not silently dropped from the score.
//
// The counts are the fixture's measured numbers with detection on: 417 killed,
// 21 equivalent, 20 invalid, 0 survived — a 100.0% score.
func TestConsoleNamesEquivalentMutantsByDefault(t *testing.T) {
	r := run(t, fixture, "-f", "tests/strong_test.yaml")
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.ExitCode, r.Stdout, r.Stderr)
	}
	console := r.Console()

	if !strings.Contains(console, "100.0%") {
		t.Errorf("console does not report the fixture's measured 100%% score:\n%s", console)
	}
	if !strings.Contains(console, "not scored: 21 equivalent · 20 invalid") {
		t.Errorf("console does not name the excluded equivalent and invalid mutants:\n%s", console)
	}
	if !strings.Contains(console, "Equivalent") {
		t.Errorf("console has no Equivalent section:\n%s", console)
	}
	if strings.Contains(console, "equivalence check skipped") {
		t.Errorf("the default run must not claim the check was skipped:\n%s", console)
	}
}

// TestNoEquivalenceCheckSkipsDetectionAndSaysSo: --no-equivalence-check must
// leave the would-be-equivalent mutants as ordinary survivors and say plainly
// that nothing was checked, so a reader does not mistake the lower score for a
// regression rather than a deliberately disabled pass.
//
// The counts are the fixture's measured numbers with detection off: 417 killed,
// 21 survived (the same 21 that detection would otherwise reclassify), 20
// invalid — a 95.2% score. Exit code is unaffected: --no-equivalence-check
// changes what is measured, not whether the run succeeds.
func TestNoEquivalenceCheckSkipsDetectionAndSaysSo(t *testing.T) {
	r := run(t, fixture, "-f", "tests/strong_test.yaml", "--no-equivalence-check")
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.ExitCode, r.Stdout, r.Stderr)
	}
	console := r.Console()

	if !strings.Contains(console, "95.2%") {
		t.Errorf("console does not report the fixture's measured 95.2%% score with detection off:\n%s", console)
	}
	if !strings.Contains(console, "not scored: 20 invalid") {
		t.Errorf("console should list only the invalid exclusion, not equivalent, with the check off:\n%s", console)
	}
	if !strings.Contains(console, "equivalence check skipped (--no-equivalence-check)") {
		t.Errorf("console should say the check was skipped:\n%s", console)
	}
	if strings.Contains(console, "equivalent") {
		t.Errorf("no mutant should be named equivalent with detection skipped:\n%s", console)
	}
}

// TestConsoleNamesSurvivorsTheCheckCouldNotDecide: "the pass ran" and "the pass
// concluded" are different claims. The weak suite leaves survivors whose spans
// cannot be probed at all, and a report showing only an equivalent count would
// read as a fully verified run — the same misreading --max-mutants truncation
// would cause.
func TestConsoleNamesSurvivorsTheCheckCouldNotDecide(t *testing.T) {
	r := weak(t)
	j := r.JSON(t)
	if !j.EquivalenceChecked {
		t.Fatal("the default run should have checked equivalence")
	}
	if j.EquivalenceUnchecked == 0 {
		t.Skip("this weak run left no undecidable survivors; nothing to disclose")
	}
	if !strings.Contains(r.Console(), "survivors could not be checked for equivalence") {
		t.Errorf("console does not disclose %d undecided survivors:\n%s",
			j.EquivalenceUnchecked, r.Console())
	}
}
