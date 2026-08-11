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
		case "Equivalent":
			x.Equivalent++
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
