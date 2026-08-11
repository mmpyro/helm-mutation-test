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
