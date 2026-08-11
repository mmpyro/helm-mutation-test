//go:build integration

package integration

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// knownStatuses is the complete status vocabulary. A status outside it means the
// tool grew one without the reports being taught about it.
var knownStatuses = map[string]bool{
	"Killed": true, "Survived": true, "NoCoverage": true,
	"Invalid": true, "Equivalent": true, "Timeout": true, "Error": true,
}

// TestJSONReportIsCompleteAndSelfConsistent: the JSON report is the canonical
// model other tooling parses, so its internal arithmetic has to hold exactly.
func TestJSONReportIsCompleteAndSelfConsistent(t *testing.T) {
	j := strong(t).JSON(t)

	if j.Schema != "helm-mutation-test/v1" {
		t.Errorf("schema = %q, want helm-mutation-test/v1", j.Schema)
	}
	if j.ChartName != "sample" {
		t.Errorf("chartName = %q, want sample", j.ChartName)
	}
	if j.ChartPath != fixture {
		t.Errorf("chartPath = %q, want %q", j.ChartPath, fixture)
	}
	// The documented invariant: nothing is dropped without being counted.
	if j.Generated-j.Capped != len(j.Mutants) {
		t.Errorf("generated(%d) - capped(%d) = %d, but %d mutants were reported",
			j.Generated, j.Capped, j.Generated-j.Capped, len(j.Mutants))
	}
	if j.Tally.total() != len(j.Mutants) {
		t.Errorf("tally totals %d but %d mutants were reported", j.Tally.total(), len(j.Mutants))
	}
	// score = killed / (killed + survived), and nothing else.
	want := float64(j.Tally.Killed) / float64(j.Tally.scored()) * 100
	if diff := j.Score - want; diff > 0.01 || diff < -0.01 {
		t.Errorf("score = %v, but killed/scored = %v", j.Score, want)
	}
	if len(j.Mutators) != 8 {
		t.Errorf("mutators = %v, want all 8 by default", j.Mutators)
	}
	if len(j.Suites) == 0 || j.TestCount == 0 {
		t.Errorf("suites = %d, testCount = %d, want both non-zero", len(j.Suites), j.TestCount)
	}
	if j.Timing.BaselineMillis < 0 || j.Timing.TotalMillis <= 0 {
		t.Errorf("timing = %+v, want a positive total", j.Timing)
	}

	for _, m := range j.Mutants {
		if m.ID == "" || m.Mutator == "" || m.File == "" || m.Line == 0 {
			t.Errorf("mutant is missing identity fields: %+v", m)
			break
		}
		if !knownStatuses[m.Status] {
			t.Errorf("mutant %s has unknown status %q", m.ID, m.Status)
			break
		}
	}
}

// TestJSONAttributesEveryKillToAnAssertion: "which test caught this" is the
// report's most useful field, and a kill with no attribution is a silent gap.
//
// Reads the weak run rather than the strong one: with equivalence detection on,
// the strong suite's only survivors are the provably-equivalent ones (see
// TestStrongSuiteHasNoRealSurvivors in internal/runner), so it no longer has any
// real Survived mutants to check the negative case against.
func TestJSONAttributesEveryKillToAnAssertion(t *testing.T) {
	j := weak(t).JSON(t)

	var killed, survived int
	for _, m := range j.Mutants {
		switch m.Status {
		case "Killed":
			killed++
			if len(m.KilledBy) == 0 {
				t.Errorf("killed mutant %s has no killedBy entry", m.ID)
				continue
			}
			k := m.KilledBy[0]
			if k.Test == "" || k.AssertType == "" || k.SuiteFile == "" {
				t.Errorf("mutant %s attribution is incomplete: %+v", m.ID, k)
			}
		case "Survived":
			survived++
			if len(m.KilledBy) != 0 {
				t.Errorf("survived mutant %s claims to have been killed by %+v", m.ID, m.KilledBy)
			}
		}
	}
	if killed == 0 || survived == 0 {
		t.Fatalf("killed = %d, survived = %d; the weak suite should produce both",
			killed, survived)
	}
}

// TestJUnitInvertsTheUsualSense: a survivor is the actionable defect, so it is
// the survivor -- not the killed mutant -- that must turn a CI job red.
//
// Reads the weak run: the strong suite's survivors are now all equivalent (see
// TestStrongSuiteHasNoRealSurvivors in internal/runner), so it produces zero
// failing testcases and could never exercise the sawFailure path below.
func TestJUnitInvertsTheUsualSense(t *testing.T) {
	r := weak(t)
	j, x := r.JSON(t), r.JUnit(t)

	if x.Name != "helm-mutation-test:sample" {
		t.Errorf("testsuites name = %q", x.Name)
	}
	if x.Failures != j.Tally.Survived {
		t.Errorf("failures = %d, want %d (the survivors)", x.Failures, j.Tally.Survived)
	}
	if x.Skipped != j.Tally.nonScoring() {
		t.Errorf("skipped = %d, want %d (everything that grades nothing)",
			x.Skipped, j.Tally.nonScoring())
	}
	if x.Tests != len(j.Mutants) {
		t.Errorf("tests = %d, want %d (one per mutant, this run is uncapped)",
			x.Tests, len(j.Mutants))
	}

	// The top-level attributes must equal the sum over child suites, or a CI UI
	// that recomputes them will disagree with one that trusts them.
	var tests, failures, skipped int
	for _, ts := range x.Suites {
		tests += ts.Tests
		failures += ts.Failures
		skipped += ts.Skipped
	}
	if tests != x.Tests || failures != x.Failures || skipped != x.Skipped {
		t.Errorf("children sum to tests=%d failures=%d skipped=%d, but the root says %d/%d/%d",
			tests, failures, skipped, x.Tests, x.Failures, x.Skipped)
	}

	var sawFailure, sawPass bool
	for _, c := range x.cases() {
		switch {
		case c.Failure != nil:
			sawFailure = true
			if c.Failure.Type != "SurvivedMutant" {
				t.Errorf("failure type = %q, want SurvivedMutant", c.Failure.Type)
			}
			if !strings.Contains(c.Failure.Text, "Add an assertion") {
				t.Errorf("failure body does not say what to do:\n%s", c.Failure.Text)
			}
			if !strings.Contains(c.Failure.Text, "mutator:") ||
				!strings.Contains(c.Failure.Text, "location:") {
				t.Errorf("failure body lacks mutator/location:\n%s", c.Failure.Text)
			}
		case c.Skipped == nil:
			sawPass = true // a killed mutant: a plain passing testcase
		}
	}
	if !sawFailure || !sawPass {
		t.Errorf("sawFailure = %v, sawPass = %v; expected both", sawFailure, sawPass)
	}
}

// TestJUnitSkipReasonsAreNeverMysterious: a skip that does not say why reads as
// "the tool gave up". The weak run produces no-coverage, invalid and (now that
// equivalence detection reclassifies some of its survivors) equivalent mutants.
func TestJUnitSkipReasonsAreNeverMysterious(t *testing.T) {
	for _, c := range weak(t).JUnit(t).cases() {
		if c.Skipped == nil {
			continue
		}
		msg := c.Skipped.Message
		switch {
		case strings.Contains(msg, "no suite renders"),
			strings.Contains(msg, "stopped the chart rendering"),
			strings.Contains(msg, "cannot change any rendered manifest"),
			strings.Contains(msg, "timed out"),
			strings.Contains(msg, "the tool failed"),
			strings.Contains(msg, "--max-mutants"):
		default:
			t.Errorf("skip reason is not one of the documented explanations: %q", msg)
		}
	}
}

// TestMarkdownCarriesTheWholeSummary: this format is what a reviewer reads in a
// job summary, so the score, both breakdowns, and the exclusion arithmetic all
// have to be present without opening anything else.
func TestMarkdownCarriesTheWholeSummary(t *testing.T) {
	r := strong(t)
	j, md := r.JSON(t), r.Markdown(t)

	for _, want := range []string{
		fmt.Sprintf("## Mutation score: %.1f%%", j.Score),
		"| Outcome | Count | |",
		"### Score by mutator",
		"### Score by file",
		fmt.Sprintf("Score counts only killed and survived mutants: %d of %d.",
			j.Tally.scored(), j.Tally.total()),
		fmt.Sprintf("| Killed | %d |", j.Tally.Killed),
		fmt.Sprintf("| Survived | %d |", j.Tally.Survived),
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown is missing %q", want)
		}
	}
	// Every mutator that ran should appear in the breakdown table.
	for _, b := range j.ByMutator {
		if !strings.Contains(md, "| `"+b.Name+"` |") {
			t.Errorf("markdown byMutator table omits %q", b.Name)
		}
	}
}

// TestMarkdownTruncatesSurvivorsHonestly: GitHub's step summary has a size limit,
// so the list is capped -- but a silent cut would misrepresent the run. Asserts
// the relationship rather than the cap's value, which is free to change.
func TestMarkdownTruncatesSurvivorsHonestly(t *testing.T) {
	r := weak(t)
	j, md := r.JSON(t), r.Markdown(t)

	total := j.Tally.Survived
	if total < 50 {
		t.Fatalf("the weak suite produced only %d survivors; this test needs a long list", total)
	}
	if !strings.Contains(md, fmt.Sprintf("### Survived mutants (%d)", total)) {
		t.Errorf("markdown does not state the true survivor count %d", total)
	}

	// Parse the note with a regexp, not by finding the first "_": the byFile table
	// contains `templates/_helpers.tpl`, whose underscore comes first in the file.
	shown := strings.Count(md, "```diff")
	m := regexp.MustCompile(`_(\d+) further survivors omitted`).FindStringSubmatch(md)
	if m == nil {
		t.Fatalf("no truncation note in a report with %d survivors", total)
	}
	omitted, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatal(err)
	}
	if shown+omitted != total {
		t.Errorf("markdown shows %d and admits omitting %d, but there are %d survivors",
			shown, omitted, total)
	}
}

// TestHTMLCommunicatesWithoutScripts: an artifact opened out of a CI run is
// routinely opened with the CDN unreachable or scripts blocked, which is exactly
// when the embedded viewer shows nothing. The static summary must stand alone.
func TestHTMLCommunicatesWithoutScripts(t *testing.T) {
	r := strong(t)
	j, h := r.JSON(t), r.HTML(t)

	for _, want := range []string{
		"Mutation report — sample",
		fmt.Sprintf("%.1f%%", j.Score),
		"<h2>Outcomes</h2>",
		"<h2>Survived mutants</h2>",
		fmt.Sprintf("<th>Killed</th><td>%d</td>", j.Tally.Killed),
		fmt.Sprintf("<th>Survived</th><td>%d</td>", j.Tally.Survived),
		`<script id="mutation-report" type="application/json">`,
	} {
		if !strings.Contains(h, want) {
			t.Errorf("HTML is missing %q", want)
		}
	}
	// Every survivor is listed here, uncapped, unlike markdown.
	if got := strings.Count(h, `<li><code class="loc">`); got != j.Tally.Survived {
		t.Errorf("HTML lists %d survivors, want all %d", got, j.Tally.Survived)
	}
	// The embedded payload must not be able to close its own script element.
	payload := h[strings.Index(h, `id="mutation-report"`):]
	payload = payload[:strings.Index(payload, "</script>")]
	if strings.Contains(payload, "</script") {
		t.Error("the embedded JSON contains an unescaped </script")
	}
}

// TestStrykerConformsToTheSchema: this file is what other mutation-testing
// tooling and the published viewer consume, so the schema's vocabulary and the
// embedded source both have to be right.
func TestStrykerConformsToTheSchema(t *testing.T) {
	r := strong(t)
	j, s := r.JSON(t), r.Stryker(t)

	if s.SchemaVersion != "2" {
		t.Errorf("schemaVersion = %q, want 2", s.SchemaVersion)
	}
	if !strings.Contains(s.Schema, "mutation-testing-report-schema.json") {
		t.Errorf("$schema = %q", s.Schema)
	}
	if s.ProjectRoot != fixture {
		t.Errorf("projectRoot = %q, want %q", s.ProjectRoot, fixture)
	}
	if s.Thresholds.High != 80 || s.Thresholds.Low != 50 {
		t.Errorf("thresholds = %+v, want high 80 / low 50", s.Thresholds)
	}
	if len(s.Files) == 0 {
		t.Fatal("no files in the stryker report")
	}

	// Our statuses map onto the schema's vocabulary; Invalid becomes CompileError
	// and Equivalent becomes Ignored, the schema's term for a mutant deliberately
	// excluded from scoring.
	want := map[string]int{
		"Killed":       j.Tally.Killed,
		"Survived":     j.Tally.Survived,
		"NoCoverage":   j.Tally.NoCoverage,
		"CompileError": j.Tally.Invalid,
		"Ignored":      j.Tally.Equivalent,
		"Timeout":      j.Tally.Timeout,
		"RuntimeError": j.Tally.Errored,
	}
	got := s.byStatus()
	for status, n := range want {
		if n == 0 {
			continue
		}
		if got[status] != n {
			t.Errorf("stryker has %d %s, want %d", got[status], status, n)
		}
	}

	for name, f := range s.Files {
		// The viewer needs source to annotate mutations in place.
		if f.Source == "" {
			t.Errorf("%s has no embedded source", name)
		}
		wantLang := "yaml"
		if strings.HasSuffix(name, ".tpl") {
			wantLang = "html" // no YAML-with-Go-template mode exists
		}
		if f.Language != wantLang {
			t.Errorf("%s language = %q, want %q", name, f.Language, wantLang)
		}
		for _, m := range f.Mutants {
			if m.Location.Start.Line == 0 || m.Location.End.Column <= m.Location.Start.Column {
				t.Errorf("%s mutant %s has a degenerate location %+v", name, m.ID, m.Location)
				break
			}
		}
	}
}

// TestEveryWrittenReportIsAnnounced: the CLI prints where it put each file, and a
// file written but not announced is a file nobody finds.
func TestEveryWrittenReportIsAnnounced(t *testing.T) {
	r := strong(t)
	for _, name := range []string{
		"mutation-report.json",
		"mutation-report.md",
		"mutation-report.junit.xml",
		"mutation-report.html",
		"mutation-report.stryker.json",
	} {
		if !strings.Contains(r.Stdout, name) {
			t.Errorf("stdout does not announce %s", name)
		}
	}
}

// TestOnlyRequestedFormatsAreWritten: --report is a whitelist, and writing an
// unasked-for file surprises anyone collecting a directory as a CI artifact.
func TestOnlyRequestedFormatsAreWritten(t *testing.T) {
	r := run(t, fixture, "-f", "tests/strong_test.yaml", "--max-mutants", "5",
		"--report", "json")
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d\n%s", r.ExitCode, r.Stderr)
	}
	entries, err := osReadDirNames(r.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0] != "mutation-report.json" {
		t.Errorf("report dir holds %v, want only mutation-report.json", entries)
	}
}
