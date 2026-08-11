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
		"num-literal", "range-empty", "required-drop", "str-literal", "yaml-key-delete",
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
