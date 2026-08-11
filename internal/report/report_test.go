package report

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmpyro/helm-mutation-test/internal/config"
	"github.com/mmpyro/helm-mutation-test/internal/model"
)

// sampleRun covers every status — Killed, Survived, NoCoverage, Invalid,
// Equivalent, Timeout and Error — so each format is exercised on all of them.
func sampleRun() *model.Run {
	run := &model.Run{
		ChartName: "sample",
		ChartPath: "testdata/chart",
		Mutators:  []string{"cond-negate", "str-literal", "num-literal"},
		Suites: []model.SuiteInfo{
			{Name: "weak assertions", File: "tests/weak_test.yaml", Tests: []string{"renders a Deployment"}},
		},
		TestCount:          4,
		Generated:          9,
		Capped:             2,
		EquivalenceChecked: true,
		BaselineDuration:   5 * time.Millisecond,
		Duration:           320 * time.Millisecond,
		Threshold:          80,
		SkippedFiles: []model.SkippedFile{
			{File: "templates/exotic.yaml", Reason: "template did not parse: unexpected {{end}}\nsecond line"},
		},
		Mutants: []model.Mutant{
			{
				ID: "deployment.yaml:120:cond-negate", Mutator: "cond-negate",
				File: "templates/deployment.yaml", Line: 7, Column: 12,
				StartByte: 120, EndByte: 145,
				Original: ".Values.enabled", Mutated: "not (.Values.enabled)",
				OriginalLine: "      {{- if .Values.enabled }}",
				MutatedLine:  "      {{- if not (.Values.enabled) }}",
				Status:       model.StatusSurvived,
				TestsRun:     3, CoveringSuites: []string{"tests/weak_test.yaml"},
				Duration: 2 * time.Millisecond,
			},
			{
				ID: "deployment.yaml:200:str-literal", Mutator: "str-literal",
				File: "templates/deployment.yaml", Line: 9, Column: 20,
				StartByte: 200, EndByte: 214,
				Original: "IfNotPresent", Mutated: `"helm-mutation-test"`,
				OriginalLine: "          imagePullPolicy: IfNotPresent",
				MutatedLine:  `          imagePullPolicy: "helm-mutation-test"`,
				Status:       model.StatusKilled,
				TestsRun:     2, CoveringSuites: []string{"tests/weak_test.yaml"},
				KilledBy: []model.KilledBy{{
					Suite: "weak assertions", SuiteFile: "tests/weak_test.yaml",
					Test: "pins every field", AssertType: "equal", AssertIndex: 3,
					FailInfo: "expected IfNotPresent",
				}},
				Duration: 3 * time.Millisecond,
			},
			{
				ID: "deployment.yaml:250:num-literal:equiv", Mutator: "num-literal",
				File: "templates/deployment.yaml", Line: 11, Column: 30,
				StartByte: 250, EndByte: 252,
				Original: "80", Mutated: "0",
				OriginalLine: "        - containerPort: 80",
				MutatedLine:  "        - containerPort: 0",
				Status:       model.StatusEquivalent,
				Detail:       "identical across all render contexts; span proven executed",
			},
			{
				ID: "ingress.yaml:10:num-literal:zero", Mutator: "num-literal",
				File: "templates/ingress.yaml", Line: 3, Column: 5,
				StartByte: 10, EndByte: 12,
				Original: "80", Mutated: "0",
				OriginalLine: "    port: 80", MutatedLine: "    port: 0",
				Status: model.StatusNoCoverage,
			},
			{
				ID: "values.yaml:30:str-literal", Mutator: "str-literal",
				File: "values.yaml", Line: 2, Column: 8,
				StartByte: 30, EndByte: 35,
				Original: "nginx", Mutated: `"helm-mutation-test"`,
				OriginalLine: "  repo: nginx", MutatedLine: `  repo: "helm-mutation-test"`,
				Status: model.StatusInvalid,
				Detail: "parse error at deployment.yaml:4\nmore detail",
			},
			{
				ID: "values.yaml:60:num-literal", Mutator: "num-literal",
				File: "values.yaml", Line: 4, Column: 3,
				StartByte: 60, EndByte: 61,
				Original: "2", Mutated: "3",
				OriginalLine: "  n: 2", MutatedLine: "  n: 3",
				Status: model.StatusTimeout, Detail: "exceeded 30s",
			},
			{
				ID: "values.yaml:70:bool-flip", Mutator: "bool-flip",
				File: "values.yaml", Line: 5, Column: 3,
				StartByte: 70, EndByte: 74,
				Original: "true", Mutated: "false",
				OriginalLine: "  on: true", MutatedLine: "  on: false",
				Status: model.StatusError, Detail: "worker died",
			},
		},
	}
	run.ComputeTally()
	return run
}

// allFormatsChartDir is a fixed stand-in chart directory for allFormats' HTML
// render. It deliberately does NOT come from t.TempDir(): that path embeds the
// calling test's name (e.g. "TestEveryFormatNamesEquivalentMutants"), which
// Stryker echoes verbatim into the report's projectRoot field — letting a test
// named after the very word it is asserting on pass regardless of what the
// production code does. The directory need not exist; a missing chart source
// is tolerated (see TestStrykerToleratesUnreadableSource).
const allFormatsChartDir = "testdata/allformats-fixture"

// allFormats renders run through every one of the five report formats, so a
// property that must hold across all of them can be checked in one loop
// instead of five near-identical tests.
func allFormats(t *testing.T, run *model.Run) []struct{ name, output string } {
	t.Helper()
	var out []struct{ name, output string }

	var console bytes.Buffer
	if err := Console(&console, run, false); err != nil {
		t.Fatalf("Console: %v", err)
	}
	out = append(out, struct{ name, output string }{"console", console.String()})

	j, err := JSON(run)
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	out = append(out, struct{ name, output string }{"json", string(j)})

	md, err := Markdown(run)
	if err != nil {
		t.Fatalf("Markdown: %v", err)
	}
	out = append(out, struct{ name, output string }{"markdown", string(md)})

	ju, err := JUnit(run)
	if err != nil {
		t.Fatalf("JUnit: %v", err)
	}
	out = append(out, struct{ name, output string }{"junit", string(ju)})

	html, err := HTML(run, allFormatsChartDir)
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	out = append(out, struct{ name, output string }{"html", string(html)})

	return out
}

// ---------- equivalent (cross-format) ----------

func TestEveryFormatNamesEquivalentMutants(t *testing.T) {
	// An exclusion no format mentions reads as full coverage to anyone looking at
	// that format. Equivalent joins no-coverage and invalid in all five.
	run := &model.Run{
		ChartName:          "sample",
		EquivalenceChecked: true,
		Mutants: []model.Mutant{
			{ID: "1", Mutator: "num-literal", File: "templates/a.yaml", Line: 3, Column: 5,
				Status: model.StatusKilled},
			{ID: "2", Mutator: "num-literal", File: "templates/a.yaml", Line: 7, Column: 9,
				Original: "nindent 4", Mutated: "nindent 5", Status: model.StatusEquivalent,
				Detail: "identical under 12 test-job value sets; span proven executed"},
		},
	}
	run.ComputeTally()

	for _, tc := range allFormats(t, run) {
		if !strings.Contains(strings.ToLower(tc.output), "equivalent") {
			t.Errorf("%s does not name equivalent mutants:\n%s", tc.name, tc.output)
		}
	}
}

func TestEveryFormatSaysWhenEquivalenceWasNotChecked(t *testing.T) {
	// "equivalent 0" with the check disabled would read as "checked, found none".
	run := &model.Run{
		ChartName:          "sample",
		EquivalenceChecked: false,
		Mutants: []model.Mutant{
			{ID: "1", Mutator: "num-literal", File: "templates/a.yaml", Line: 3, Column: 5,
				Status: model.StatusSurvived},
		},
	}
	run.ComputeTally()

	for _, tc := range allFormats(t, run) {
		if !strings.Contains(strings.ToLower(tc.output), "equivalence") {
			t.Errorf("%s does not say the equivalence check was skipped:\n%s", tc.name, tc.output)
		}
	}
}

// ---------- console ----------

func TestConsoleShowsScoreAndSurvivors(t *testing.T) {
	var buf bytes.Buffer
	if err := Console(&buf, sampleRun(), false); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	for _, want := range []string{
		"Mutation testing sample",
		"Score",
		"50.0%", // 1 killed, 1 survived
		"1 killed / 1 survived",
		"By mutator",
		"By file",
		"SURVIVED (1)",
		"templates/deployment.yaml:7",
		// The survivor is shown as a diff, which is what makes it actionable.
		"-       {{- if .Values.enabled }}",
		"+       {{- if not (.Values.enabled) }}",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("console output is missing %q\n%s", want, out)
		}
	}
}

// TestConsoleExplainsEverythingExcludedFromTheScore is the honesty requirement:
// the score must never be mistaken for "all mutants were caught".
func TestConsoleExplainsEverythingExcludedFromTheScore(t *testing.T) {
	var buf bytes.Buffer
	if err := Console(&buf, sampleRun(), false); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"not scored:", "1 no-coverage", "1 equivalent", "1 invalid", "1 timeout", "1 error",
		"NO COVERAGE (1)", "no suite renders this template",
		"Equivalent", "cannot change any rendered manifest; not scored",
		"INVALID (1)", "excluded from the score",
		"PARTIALLY ANALYSED (1)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("console output is missing %q\n%s", want, out)
		}
	}
}

// TestConsoleWarnsWhenMutantsWereCapped: a truncated run must not read as full coverage.
func TestConsoleWarnsWhenMutantsWereCapped(t *testing.T) {
	var buf bytes.Buffer
	if err := Console(&buf, sampleRun(), false); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); !strings.Contains(out, "--max-mutants") || !strings.Contains(out, "2 not evaluated") {
		t.Errorf("expected a cap warning:\n%s", out)
	}
}

func TestConsoleReportsThresholdOutcome(t *testing.T) {
	run := sampleRun() // score 50%, threshold 80%
	var buf bytes.Buffer
	if err := Console(&buf, run, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "below the 80.0% threshold") {
		t.Errorf("expected a threshold failure:\n%s", buf.String())
	}

	run.Threshold = 10
	buf.Reset()
	if err := Console(&buf, run, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "meets the 10.0% threshold") {
		t.Errorf("expected a threshold pass:\n%s", buf.String())
	}
}

func TestConsoleColourIsOptIn(t *testing.T) {
	var plain, coloured bytes.Buffer
	if err := Console(&plain, sampleRun(), false); err != nil {
		t.Fatal(err)
	}
	if err := Console(&coloured, sampleRun(), true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain.String(), "\x1b[") {
		t.Error("colour must be absent when disabled")
	}
	if !strings.Contains(coloured.String(), "\x1b[") {
		t.Error("colour must be present when enabled")
	}
}

func TestConsoleHandlesAPerfectAndAnEmptyRun(t *testing.T) {
	perfect := &model.Run{ChartName: "c", Mutants: []model.Mutant{{Status: model.StatusKilled}}}
	perfect.ComputeTally()
	var buf bytes.Buffer
	if err := Console(&buf, perfect, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "100.0%") {
		t.Errorf("expected a perfect score:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "SURVIVED") {
		t.Error("a perfect run should list no survivors")
	}

	empty := &model.Run{ChartName: "c"}
	empty.ComputeTally()
	buf.Reset()
	if err := Console(&buf, empty, false); err != nil {
		t.Fatalf("an empty run must not error: %v", err)
	}
}

func TestConsoleMarksUnscoredBreakdownRows(t *testing.T) {
	// A file whose every mutant is NoCoverage would otherwise show a meaningless 0.0%.
	var buf bytes.Buffer
	if err := Console(&buf, sampleRun(), false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "not scored") {
		t.Errorf("expected unscored rows to be marked:\n%s", buf.String())
	}
}

// ---------- json ----------

func TestJSONRoundTripsAndCarriesEverything(t *testing.T) {
	b, err := JSON(sampleRun())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	for _, key := range []string{
		"schema", "chartName", "score", "threshold", "passed", "tally",
		"generated", "capped", "mutators", "suites", "byMutator", "byFile",
		"mutants", "skippedFiles", "timing", "equivalenceChecked",
	} {
		if _, ok := got[key]; !ok {
			t.Errorf("JSON report is missing %q", key)
		}
	}
	if got["score"].(float64) != 50 {
		t.Errorf("score = %v, want 50", got["score"])
	}
	if got["passed"].(bool) {
		t.Error("passed should be false at 50%% against an 80%% threshold")
	}
	mutants := got["mutants"].([]any)
	if len(mutants) != 7 {
		t.Fatalf("got %d mutants, want 7", len(mutants))
	}
}

func TestJSONPreservesKillAttribution(t *testing.T) {
	b, err := JSON(sampleRun())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"assertType": "equal"`) {
		t.Errorf("kill attribution lost:\n%s", b)
	}
	if !strings.Contains(string(b), `"test": "pins every field"`) {
		t.Errorf("killing test name lost:\n%s", b)
	}
}

func TestJSONIsStableAcrossRuns(t *testing.T) {
	a, err := JSON(sampleRun())
	if err != nil {
		t.Fatal(err)
	}
	b, err := JSON(sampleRun())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Error("JSON output must be byte-stable for the same run")
	}
}

func TestRound(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want float64
	}{{66.666666, 66.67}, {50, 50}, {0, 0}, {99.999, 100}} {
		if got := round(tc.in, 2); got != tc.want {
			t.Errorf("round(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// ---------- markdown ----------

func TestMarkdownSummary(t *testing.T) {
	b, err := Markdown(sampleRun())
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	for _, want := range []string{
		"## Mutation score: 50.0%",
		"❌ **Below the 80.0% threshold.**",
		"| Killed | 1 |",
		"| Survived | 1 |",
		"no test noticed",
		"### Score by mutator",
		"### Score by file",
		"### Survived mutants (1)",
		"```diff",
		"templates/deployment.yaml:7",
		"### Templates no suite renders",
		"`--max-mutants` ran 7 of 9",
		"### Partially analysed",
		"| Equivalent | 1 |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown is missing %q\n%s", want, out)
		}
	}
}

func TestMarkdownReportsAPerfectRun(t *testing.T) {
	run := &model.Run{ChartName: "c", Threshold: 50, Mutants: []model.Mutant{{Status: model.StatusKilled}}}
	run.ComputeTally()
	b, err := Markdown(run)
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	if !strings.Contains(out, "✅ Meets the 50.0% threshold.") {
		t.Errorf("expected a threshold pass:\n%s", out)
	}
	if !strings.Contains(out, "None — every covered mutation was caught.") {
		t.Errorf("expected an explicit no-survivors line:\n%s", out)
	}
}

func TestMarkdownTruncatesLongSurvivorLists(t *testing.T) {
	run := &model.Run{ChartName: "c"}
	for i := range maxMarkdownSurvivors + 15 {
		run.Mutants = append(run.Mutants, model.Mutant{
			ID: string(rune('a' + i%26)), Mutator: "str-literal", File: "templates/x.yaml",
			Line: i + 1, OriginalLine: "a: 1", MutatedLine: "a: 2",
			Status: model.StatusSurvived,
		})
	}
	run.ComputeTally()
	b, err := Markdown(run)
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	if !strings.Contains(out, "15 further survivors omitted") {
		t.Errorf("truncation must be reported, not silent:\n%s", out[:min(len(out), 900)])
	}
}

// ---------- junit ----------

// TestJUnitMapsSurvivedToFailure is the deliberate inversion: a survivor is the
// defect, so it is what should turn a CI job red.
func TestJUnitMapsSurvivedToFailure(t *testing.T) {
	b, err := JUnit(sampleRun())
	if err != nil {
		t.Fatal(err)
	}

	var suites struct {
		Tests    int `xml:"tests,attr"`
		Failures int `xml:"failures,attr"`
		Skipped  int `xml:"skipped,attr"`
		Suites   []struct {
			Name  string `xml:"name,attr"`
			Cases []struct {
				Name    string `xml:"name,attr"`
				Failure *struct {
					Type    string `xml:"type,attr"`
					Message string `xml:"message,attr"`
					Text    string `xml:",chardata"`
				} `xml:"failure"`
				Skipped *struct {
					Message string `xml:"message,attr"`
				} `xml:"skipped"`
			} `xml:"testcase"`
		} `xml:"testsuite"`
	}
	if err := xml.Unmarshal(b, &suites); err != nil {
		t.Fatalf("invalid XML: %v\n%s", err, b)
	}

	// 7 mutants + the --max-mutants notice, which sampleRun triggers with Capped: 2.
	if suites.Tests != 8 {
		t.Errorf("tests = %d, want 8", suites.Tests)
	}
	if suites.Failures != 1 {
		t.Errorf("failures = %d, want 1 (only the survivor)", suites.Failures)
	}
	// NoCoverage, Invalid, Equivalent, Timeout and Error all skip: none grades
	// assertions. Plus the --max-mutants notice, which also reports as a skip.
	if suites.Skipped != 6 {
		t.Errorf("skipped = %d, want 6", suites.Skipped)
	}

	var sawFailure, sawKilledAsPass bool
	for _, ts := range suites.Suites {
		for _, tc := range ts.Cases {
			if tc.Failure != nil {
				sawFailure = true
				if tc.Failure.Type != "SurvivedMutant" {
					t.Errorf("failure type = %q", tc.Failure.Type)
				}
				if !strings.Contains(tc.Failure.Text, "Add an assertion") {
					t.Errorf("failure text should say what to do:\n%s", tc.Failure.Text)
				}
			}
			if strings.Contains(tc.Name, "str-literal") && strings.Contains(tc.Name, "deployment") {
				if tc.Failure == nil && tc.Skipped == nil {
					sawKilledAsPass = true
				}
			}
		}
	}
	if !sawFailure {
		t.Error("no failure element was emitted for the survivor")
	}
	if !sawKilledAsPass {
		t.Error("a killed mutant should be a passing testcase")
	}
}

func TestJUnitHasXMLHeader(t *testing.T) {
	b, err := JUnit(sampleRun())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), `<?xml version="1.0"`) {
		t.Errorf("missing XML declaration: %.60s", b)
	}
}

// TestJUnitNamesTheCap: every other format says what --max-mutants dropped. A
// CI UI fed only this XML would otherwise read a truncated sample as a complete
// run, which is the exact misreading the reports are designed to prevent.
func TestJUnitNamesTheCap(t *testing.T) {
	run := sampleRun() // Capped: 2, Generated: 9
	b, err := JUnit(run)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, `name="--max-mutants"`) {
		t.Errorf("no --max-mutants testsuite in:\n%s", got)
	}
	if !strings.Contains(got, "2 of 9 generated mutants were not evaluated") {
		t.Errorf("cap notice does not state the numbers:\n%s", got)
	}
	if !strings.Contains(got, "not full coverage") {
		t.Errorf("cap notice does not say the run is a sample:\n%s", got)
	}
}

// TestJUnitOmitsTheCapNoticeWhenNothingWasDropped keeps a complete run's XML
// free of a testsuite that would only ever say "nothing was dropped".
func TestJUnitOmitsTheCapNoticeWhenNothingWasDropped(t *testing.T) {
	run := sampleRun()
	run.Capped = 0
	b, err := JUnit(run)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "--max-mutants") {
		t.Errorf("uncapped run should not mention --max-mutants:\n%s", b)
	}
}

// TestJUnitNamesTheSkippedEquivalenceCheck mirrors TestJUnitNamesTheCap: a
// skipped equivalence pass must be visible in the XML itself, not just
// implied by an equivalent count of zero, or a CI UI fed only this report
// would read "never checked" as "checked, found none".
func TestJUnitNamesTheSkippedEquivalenceCheck(t *testing.T) {
	run := sampleRun()
	run.EquivalenceChecked = false
	b, err := JUnit(run)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, `name="--no-equivalence-check"`) {
		t.Errorf("no --no-equivalence-check testsuite in:\n%s", got)
	}
	if !strings.Contains(got, "equivalence check did not run") {
		t.Errorf("notice does not say the check was skipped:\n%s", got)
	}
}

// TestJUnitOmitsTheEquivalenceNoticeWhenTheCheckRan mirrors
// TestJUnitOmitsTheCapNoticeWhenNothingWasDropped: a run where the check did
// happen should not carry a notice that would only ever say "skipped".
func TestJUnitOmitsTheEquivalenceNoticeWhenTheCheckRan(t *testing.T) {
	run := sampleRun() // EquivalenceChecked: true
	b, err := JUnit(run)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "--no-equivalence-check") {
		t.Errorf("a checked run should not mention --no-equivalence-check:\n%s", b)
	}
}

func TestSkipReasonExplainsEachStatus(t *testing.T) {
	tests := []struct {
		status model.Status
		want   string
	}{
		{model.StatusNoCoverage, "no suite renders"},
		// Pinned on the explanatory sentence, not the bare word "Equivalent": the
		// default branch also happens to emit that word (it falls back to
		// string(m.Status)), so a substring check alone would pass even with the
		// dedicated case deleted.
		{model.StatusEquivalent, "no assertion could catch it"},
		{model.StatusInvalid, "stopped the chart rendering"},
		{model.StatusTimeout, "timed out"},
		{model.StatusError, "the tool failed"},
	}
	for _, tc := range tests {
		got := skipReason(model.Mutant{Status: tc.status, File: "templates/x.yaml", Detail: "d"})
		if !strings.Contains(got, tc.want) {
			t.Errorf("skipReason(%s) = %q, want it to contain %q", tc.status, got, tc.want)
		}
	}
}

// ---------- stryker / html ----------

func TestStrykerConformsToTheSchemaShape(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "templates", "deployment.yaml"), "apiVersion: apps/v1\nkind: Deployment\n")

	b, err := Stryker(sampleRun(), dir)
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Schema        string `json:"$schema"`
		SchemaVersion string `json:"schemaVersion"`
		Thresholds    struct {
			High int `json:"high"`
			Low  int `json:"low"`
		} `json:"thresholds"`
		Files map[string]struct {
			Source   string `json:"source"`
			Language string `json:"language"`
			Mutants  []struct {
				ID          string `json:"id"`
				MutatorName string `json:"mutatorName"`
				Status      string `json:"status"`
				Location    struct {
					Start struct{ Line, Column int } `json:"start"`
					End   struct{ Line, Column int } `json:"end"`
				} `json:"location"`
			} `json:"mutants"`
		} `json:"files"`
	}
	if err := json.Unmarshal(b, &report); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if report.SchemaVersion == "" || !strings.Contains(report.Schema, "mutation-testing") {
		t.Errorf("schema fields wrong: %+v", report.Schema)
	}
	if report.Thresholds.High == 0 || report.Thresholds.Low == 0 {
		t.Error("thresholds should be populated so the viewer colours correctly")
	}
	if len(report.Files) != 3 {
		t.Errorf("got %d files, want 3", len(report.Files))
	}
	dep, ok := report.Files["templates/deployment.yaml"]
	if !ok {
		t.Fatal("deployment.yaml missing from the report")
	}
	if !strings.Contains(dep.Source, "kind: Deployment") {
		t.Error("the viewer needs file source embedded to annotate mutations")
	}
	if dep.Language != "yaml" {
		t.Errorf("language = %q", dep.Language)
	}
	for _, m := range dep.Mutants {
		if m.Location.Start.Line < 1 || m.Location.Start.Column < 1 {
			t.Errorf("mutant %s has a non 1-based location: %+v", m.ID, m.Location)
		}
		if m.Location.End.Column <= m.Location.Start.Column {
			t.Errorf("mutant %s has an empty span: %+v", m.ID, m.Location)
		}
	}
}

func TestStrykerStatusMapping(t *testing.T) {
	tests := []struct {
		in   model.Status
		want string
	}{
		{model.StatusKilled, "Killed"},
		{model.StatusSurvived, "Survived"},
		{model.StatusNoCoverage, "NoCoverage"},
		// The schema's term for "the mutation made the artefact unbuildable".
		{model.StatusInvalid, "CompileError"},
		// The schema's term for a mutant deliberately excluded from scoring.
		{model.StatusEquivalent, "Ignored"},
		{model.StatusTimeout, "Timeout"},
		{model.StatusError, "RuntimeError"},
	}
	for _, tc := range tests {
		if got := strykerStatus(tc.in); got != tc.want {
			t.Errorf("strykerStatus(%s) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestStrykerToleratesUnreadableSource(t *testing.T) {
	// Mutants must still be reported when the chart is no longer on disk.
	b, err := Stryker(sampleRun(), filepath.Join(t.TempDir(), "gone"))
	if err != nil {
		t.Fatalf("a missing source file must not fail the report: %v", err)
	}
	if !strings.Contains(string(b), "cond-negate") {
		t.Error("mutants were dropped along with the unreadable source")
	}
}

func TestHTMLIsSelfDescribingWithoutScripts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "templates", "deployment.yaml"), "kind: Deployment\n")

	b, err := HTML(sampleRun(), dir)
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	for _, want := range []string{
		"<!DOCTYPE html>", "Mutation report — sample", "50.0%",
		"mutation-test-report-app", `id="mutation-report"`,
		// The static summary must carry the result on its own, because a CI
		// artefact is usually opened with the CDN unreachable.
		"Survived", "no test noticed", "templates/deployment.yaml:7",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("HTML is missing %q", want)
		}
	}
}

func TestHTMLEscapesChartContent(t *testing.T) {
	run := sampleRun()
	run.ChartName = `<script>alert(1)</script>`
	run.Mutants[0].OriginalLine = `name: "</script><img onerror=x>"`
	b, err := HTML(run, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	if strings.Contains(out, "<script>alert(1)</script>") {
		t.Error("the chart name was not escaped")
	}
	if strings.Contains(out, "<img onerror=x>") {
		t.Error("survivor source was not escaped")
	}
}

// TestEmbeddedJSONCannotCloseTheScriptElement: chart source containing
// "</script>" must not terminate the payload element early.
func TestEmbeddedJSONCannotCloseTheScriptElement(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "templates", "deployment.yaml"), "a: \"</script><b>\"\n")

	b, err := HTML(sampleRun(), dir)
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	payloadStart := strings.Index(out, `<script id="mutation-report"`)
	if payloadStart < 0 {
		t.Fatal("payload script element not found")
	}
	payload := out[payloadStart:]
	end := strings.Index(payload, "</script>")
	if end < 0 {
		t.Fatal("payload element is unterminated")
	}
	// The JSON body must contain no literal "</script>" before its real close tag.
	body := payload[strings.Index(payload, ">")+1 : end]
	if strings.Contains(body, "</script") {
		t.Errorf("the embedded payload can break out of its element:\n%s", body)
	}
	// And it must still be valid JSON.
	var any map[string]any
	if err := json.Unmarshal([]byte(unescapeEntities(body)), &any); err != nil {
		t.Errorf("embedded payload is not valid JSON: %v", err)
	}
}

func unescapeEntities(s string) string {
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	return strings.ReplaceAll(s, "&amp;", "&")
}

// ---------- writer ----------

func TestWriteFilesEmitsEveryRequestedFormat(t *testing.T) {
	dir := t.TempDir()
	chartDir := filepath.Join(dir, "chart")
	writeFile(t, filepath.Join(chartDir, "templates", "deployment.yaml"), "kind: Deployment\n")

	run := sampleRun()
	run.ChartPath = chartDir

	cfg := config.Defaults()
	cfg.ReportDir = filepath.Join(dir, "out")
	cfg.Reports = []config.ReportFormat{
		config.ReportConsole, config.ReportJSON, config.ReportHTML,
		config.ReportMarkdown, config.ReportJUnit,
	}

	written, err := WriteFiles(run, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	// HTML also emits its standard-schema JSON, which other tooling consumes.
	if len(written) != 5 {
		t.Errorf("got %d files, want 5: %+v", len(written), written)
	}
	for _, name := range []string{FileJSON, FileMarkdown, FileJUnit, FileHTML, FileStryker} {
		path := filepath.Join(cfg.ReportDir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("%s was not written: %v", name, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", name)
		}
	}
}

func TestWriteFilesSkipsDiskWorkForConsoleOnly(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.ReportDir = filepath.Join(dir, "out")
	cfg.Reports = []config.ReportFormat{config.ReportConsole}

	written, err := WriteFiles(sampleRun(), &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 0 {
		t.Errorf("got %+v, want nothing written", written)
	}
	if _, err := os.Stat(cfg.ReportDir); !os.IsNotExist(err) {
		t.Error("console-only output should not create a report directory")
	}
}

func TestWriteFilesOnlyWritesWhatWasAsked(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.ReportDir = filepath.Join(dir, "out")
	cfg.Reports = []config.ReportFormat{config.ReportJSON}

	written, err := WriteFiles(sampleRun(), &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 || filepath.Base(written[0].Path) != FileJSON {
		t.Fatalf("got %+v, want only the JSON report", written)
	}
	if _, err := os.Stat(filepath.Join(cfg.ReportDir, FileHTML)); !os.IsNotExist(err) {
		t.Error("HTML was written although it was not requested")
	}
}

// ---------- helpers ----------

func TestShortDuration(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{0, "0s"},
		{500 * time.Microsecond, "0.50ms"},
		{5 * time.Millisecond, "5ms"},
		{1500 * time.Millisecond, "1.5s"},
		{90 * time.Second, "1m30s"},
	}
	for _, tc := range tests {
		if got := short(tc.in); got != tc.want {
			t.Errorf("short(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTruncateKeepsTheTail(t *testing.T) {
	// File paths are more identifiable by their end than their start.
	if got := truncate("templates/very/deep/path/deployment.yaml", 20); !strings.HasSuffix(got, "deployment.yaml") {
		t.Errorf("truncate kept the wrong end: %q", got)
	}
	if got := truncate("short", 20); got != "short" {
		t.Errorf("got %q", got)
	}
}

func TestPlural(t *testing.T) {
	if got := plural(1, "suite", "suites"); got != "1 suite" {
		t.Errorf("got %q", got)
	}
	if got := plural(2, "suite", "suites"); got != "2 suites" {
		t.Errorf("got %q", got)
	}
	if got := plural(0, "test", "tests"); got != "0 tests" {
		t.Errorf("got %q", got)
	}
}

func TestCoverageNoteDistinguishesUnrunFromPassed(t *testing.T) {
	if got := coverageNote(model.Mutant{TestsRun: 0}); got != "no tests ran" {
		t.Errorf("got %q", got)
	}
	got := coverageNote(model.Mutant{TestsRun: 3, CoveringSuites: []string{"tests/a_test.yaml"}})
	if !strings.Contains(got, "3 tests") || !strings.Contains(got, "tests/a_test.yaml") {
		t.Errorf("got %q", got)
	}
	got = coverageNote(model.Mutant{TestsRun: 5, CoveringSuites: []string{"a", "b"}})
	if !strings.Contains(got, "2 suite files") {
		t.Errorf("got %q", got)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
