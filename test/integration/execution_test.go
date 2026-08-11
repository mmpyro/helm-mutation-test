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
