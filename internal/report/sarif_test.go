package report

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mmpyro/helm-mutation-test/internal/model"
)

// sarifDoc is the subset of SARIF 2.1.0 the tests assert on. It mirrors the
// schema's required properties so a missing one fails to decode as empty.
type sarifDoc struct {
	Schema  string `json:"$schema"`
	Version string `json:"version"`
	Runs    []struct {
		Tool struct {
			Driver struct {
				Name           string `json:"name"`
				InformationURI string `json:"informationUri"`
				Rules          []struct {
					ID               string `json:"id"`
					ShortDescription struct {
						Text string `json:"text"`
					} `json:"shortDescription"`
					HelpURI string `json:"helpUri"`
				} `json:"rules"`
			} `json:"driver"`
		} `json:"tool"`
		ColumnKind  string `json:"columnKind"`
		Invocations []struct {
			ExecutionSuccessful bool `json:"executionSuccessful"`
			Notifications       []struct {
				Level   string `json:"level"`
				Message struct {
					Text string `json:"text"`
				} `json:"message"`
			} `json:"toolExecutionNotifications"`
		} `json:"invocations"`
		Results    []sarifResultDoc `json:"results"`
		Properties map[string]any   `json:"properties"`
	} `json:"runs"`
}

type sarifResultDoc struct {
	RuleID  string `json:"ruleId"`
	Level   string `json:"level"`
	Message struct {
		Text string `json:"text"`
	} `json:"message"`
	Locations []struct {
		PhysicalLocation struct {
			ArtifactLocation struct {
				URI       string `json:"uri"`
				URIBaseID string `json:"uriBaseId"`
			} `json:"artifactLocation"`
			Region struct {
				StartLine   int `json:"startLine"`
				StartColumn int `json:"startColumn"`
				EndColumn   int `json:"endColumn"`
			} `json:"region"`
		} `json:"physicalLocation"`
	} `json:"locations"`
	PartialFingerprints map[string]string `json:"partialFingerprints"`
	Properties          map[string]any    `json:"properties"`
}

func decodeSARIF(t *testing.T, run *model.Run) sarifDoc {
	t.Helper()
	b, err := SARIF(run)
	if err != nil {
		t.Fatal(err)
	}
	var doc sarifDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, b)
	}
	if len(doc.Runs) != 1 {
		t.Fatalf("got %d runs, want exactly 1", len(doc.Runs))
	}
	return doc
}

func (d sarifDoc) notices() string {
	var b strings.Builder
	for _, inv := range d.Runs[0].Invocations {
		for _, n := range inv.Notifications {
			b.WriteString(n.Message.Text)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func TestSARIFConformsToTheSchemaShape(t *testing.T) {
	doc := decodeSARIF(t, sampleRun())
	r := doc.Runs[0]

	if doc.Version != "2.1.0" || !strings.Contains(doc.Schema, "sarif-2.1.0") {
		t.Errorf("envelope = %q / %q, want SARIF 2.1.0", doc.Version, doc.Schema)
	}
	if r.Tool.Driver.Name != "helm-mutation-test" || r.Tool.Driver.InformationURI == "" {
		t.Errorf("driver = %+v", r.Tool.Driver)
	}
	// Columns are converted to code points; the run must say so, or a consumer
	// defaults to UTF-16 code units.
	if r.ColumnKind != "unicodeCodePoints" {
		t.Errorf("columnKind = %q", r.ColumnKind)
	}
	if len(r.Invocations) != 1 || !r.Invocations[0].ExecutionSuccessful {
		t.Errorf("invocations = %+v, want one successful invocation", r.Invocations)
	}

	rules := map[string]bool{}
	for _, rule := range r.Tool.Driver.Rules {
		if rule.ShortDescription.Text == "" || rule.HelpURI == "" {
			t.Errorf("rule %s lacks a description or help link", rule.ID)
		}
		rules[rule.ID] = true
	}
	for _, res := range r.Results {
		if !rules[res.RuleID] {
			t.Errorf("result references undeclared rule %q", res.RuleID)
		}
		if res.Message.Text == "" || len(res.Locations) != 1 {
			t.Errorf("result %s lacks a message or a location", res.RuleID)
			continue
		}
		reg := res.Locations[0].PhysicalLocation.Region
		if reg.StartLine < 1 || reg.StartColumn < 1 {
			t.Errorf("result %s has a non 1-based region: %+v", res.RuleID, reg)
		}
		if reg.EndColumn != 0 && reg.EndColumn <= reg.StartColumn {
			t.Errorf("result %s has an empty span: %+v", res.RuleID, reg)
		}
	}
}

// TestSARIFReportsOnlySurvivorsAndUncoveredFiles: every SARIF result becomes a
// code-scanning alert. A killed mutant is not a defect, and an invalid, timed-out
// or equivalent one says nothing about assertions, so none of them may raise an
// alert. A missing suite is a per-file finding, so it is one alert per file.
func TestSARIFReportsOnlySurvivorsAndUncoveredFiles(t *testing.T) {
	run := sampleRun()
	// A second uncovered mutant in the same file must not add a second alert.
	run.Mutants = append(run.Mutants, model.Mutant{
		ID: "ingress.yaml:40:str-literal", Mutator: "str-literal",
		File: "templates/ingress.yaml", Line: 5, Column: 9,
		StartByte: 40, EndByte: 44, Original: "path", Mutated: `""`,
		OriginalLine: "    path: /", Status: model.StatusNoCoverage,
	})
	run.ComputeTally()

	r := decodeSARIF(t, run).Runs[0]
	byRule := map[string]int{}
	for _, res := range r.Results {
		byRule[res.RuleID]++
	}
	if len(r.Results) != 2 {
		t.Fatalf("got %d results %v, want 1 survivor + 1 uncovered file", len(r.Results), byRule)
	}
	if byRule["cond-negate"] != 1 {
		t.Errorf("the survivor is missing: %v", byRule)
	}
	if byRule["no-coverage"] != 1 {
		t.Errorf("want exactly one no-coverage result for ingress.yaml: %v", byRule)
	}
	for _, res := range r.Results {
		if res.RuleID == "no-coverage" && !strings.Contains(res.Message.Text, "2 mutants") {
			t.Errorf("no-coverage message should count the mutants: %q", res.Message.Text)
		}
	}
}

func TestSARIFSurvivorIsErrorOnlyWhenTheThresholdIsMissed(t *testing.T) {
	// sampleRun scores 50% against an 80% threshold.
	for _, tc := range []struct {
		name      string
		threshold float64
		want      string
	}{
		{"missed", 80, "error"},
		{"met", 40, "warning"},
		{"unset", 0, "warning"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := sampleRun()
			run.Threshold = tc.threshold
			for _, res := range decodeSARIF(t, run).Runs[0].Results {
				want := tc.want
				if res.RuleID == "no-coverage" {
					want = "warning" // no coverage never fails a threshold by itself
				}
				if res.Level != want {
					t.Errorf("%s level = %q, want %q", res.RuleID, res.Level, want)
				}
			}
		})
	}
}

// TestSARIFColumnsCountCodePointsNotBytes: Mutant.Column is a byte offset. An
// editor placing a squiggle by it on a line with a multi-byte character would
// underline the wrong text.
func TestSARIFColumnsCountCodePointsNotBytes(t *testing.T) {
	line := `  # zażółć: {{ .Values.x }}`
	start := strings.Index(line, ".Values.x") // byte offset
	run := &model.Run{
		ChartName: "c", Mutators: []string{"cond-negate"}, EquivalenceChecked: true,
		Mutants: []model.Mutant{{
			ID: "a", Mutator: "cond-negate", File: "templates/a.yaml", Line: 1,
			Column: start + 1, StartByte: start, EndByte: start + len(".Values.x"),
			Original: ".Values.x", Mutated: "not .Values.x",
			OriginalLine: line, Status: model.StatusSurvived,
		}},
	}
	run.ComputeTally()

	reg := decodeSARIF(t, run).Runs[0].Results[0].Locations[0].PhysicalLocation.Region
	wantStart := len([]rune(line[:start])) + 1
	if reg.StartColumn != wantStart {
		t.Errorf("startColumn = %d, want %d (code points, not bytes = %d)",
			reg.StartColumn, wantStart, start+1)
	}
	if reg.EndColumn != wantStart+len(".Values.x") {
		t.Errorf("endColumn = %d, want %d", reg.EndColumn, wantStart+len(".Values.x"))
	}
}

// TestSARIFFingerprintSurvivesALineShift: GitHub matches alerts across runs by
// partialFingerprints. Mutant.ID embeds a byte offset, so keying on it would
// close and reopen every alert below any edit.
func TestSARIFFingerprintSurvivesALineShift(t *testing.T) {
	fingerprint := func(run *model.Run) string {
		for _, res := range decodeSARIF(t, run).Runs[0].Results {
			if res.RuleID == "cond-negate" {
				return res.PartialFingerprints["mutantFingerprint/v1"]
			}
		}
		t.Fatal("survivor missing")
		return ""
	}
	before := sampleRun()
	after := sampleRun()
	after.Mutants[0].Line += 4
	after.Mutants[0].StartByte += 90
	after.Mutants[0].EndByte += 90
	after.Mutants[0].ID = "deployment.yaml:210:cond-negate"

	a, b := fingerprint(before), fingerprint(after)
	if a == "" {
		t.Fatal("survivor has no fingerprint")
	}
	if a != b {
		t.Errorf("fingerprint changed when the mutation moved: %s -> %s", a, b)
	}

	changed := sampleRun()
	changed.Mutants[0].Mutated = "not (not .Values.enabled)"
	if fingerprint(changed) == a {
		t.Error("a different mutation must have a different fingerprint")
	}
}

func TestSARIFFingerprintsAreUniqueForIdenticalLines(t *testing.T) {
	m := model.Mutant{
		Mutator: "cond-negate", File: "templates/a.yaml", Column: 7,
		StartByte: 6, EndByte: 21, Original: ".Values.enabled", Mutated: "not .Values.enabled",
		OriginalLine: "{{ if .Values.enabled }}", Status: model.StatusSurvived,
	}
	first, second := m, m
	first.ID, first.Line = "a", 1
	second.ID, second.Line = "b", 9
	run := &model.Run{ChartName: "c", Mutators: []string{"cond-negate"}, EquivalenceChecked: true,
		Mutants: []model.Mutant{first, second}}
	run.ComputeTally()

	res := decodeSARIF(t, run).Runs[0].Results
	if res[0].PartialFingerprints["mutantFingerprint/v1"] == res[1].PartialFingerprints["mutantFingerprint/v1"] {
		t.Error("two survivors on identical lines share a fingerprint, so GitHub would merge their alerts")
	}
}

// TestSARIFNamesWhatTheScoreExcludes is the honesty rule from CLAUDE.md, applied
// to the one format whose results deliberately omit every non-survivor.
func TestSARIFNamesWhatTheScoreExcludes(t *testing.T) {
	run := sampleRun()
	doc := decodeSARIF(t, run)
	n := doc.notices()

	for _, want := range []string{
		fmt.Sprintf("%.1f%%", run.Score()),
		"1 no-coverage", "1 invalid", "1 equivalent", "1 timeout", "1 error",
		"excluded from the score",
		// sampleRun has Capped: 2 of 9.
		"2 of 9 generated mutants were not evaluated",
	} {
		if !strings.Contains(n, want) {
			t.Errorf("notifications do not name %q:\n%s", want, n)
		}
	}

	props := doc.Runs[0].Properties
	for _, key := range []string{"score", "tally", "generated", "capped",
		"equivalenceChecked", "equivalenceUnchecked", "threshold"} {
		if _, ok := props[key]; !ok {
			t.Errorf("run properties lack %q", key)
		}
	}
}

func TestSARIFOmitsTheCapNoticeWhenNothingWasDropped(t *testing.T) {
	run := sampleRun()
	run.Capped = 0
	if n := decodeSARIF(t, run).notices(); strings.Contains(n, "--max-mutants") {
		t.Errorf("uncapped run mentions --max-mutants:\n%s", n)
	}
}

func TestSARIFURIsAreRelativeToTheChartPath(t *testing.T) {
	run := sampleRun() // ChartPath: testdata/chart
	loc := decodeSARIF(t, run).Runs[0].Results[0].Locations[0].PhysicalLocation.ArtifactLocation
	if loc.URI != "testdata/chart/templates/deployment.yaml" || loc.URIBaseID != "%SRCROOT%" {
		t.Errorf("artifactLocation = %+v", loc)
	}

	run.ChartPath = "./charts/../chart/"
	loc = decodeSARIF(t, run).Runs[0].Results[0].Locations[0].PhysicalLocation.ArtifactLocation
	if loc.URI != "chart/templates/deployment.yaml" {
		t.Errorf("uri = %q, want a cleaned relative path", loc.URI)
	}

	run.ChartPath = "/abs/chart"
	loc = decodeSARIF(t, run).Runs[0].Results[0].Locations[0].PhysicalLocation.ArtifactLocation
	if loc.URI != "file:///abs/chart/templates/deployment.yaml" || loc.URIBaseID != "" {
		t.Errorf("absolute chart path gave %+v, want a file:// URI with no base", loc)
	}
}

func TestSARIFIsStableAcrossRuns(t *testing.T) {
	a, err := SARIF(sampleRun())
	if err != nil {
		t.Fatal(err)
	}
	b, err := SARIF(sampleRun())
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Error("SARIF output differs between identical runs; diffs and caches would churn")
	}
}

func TestSARIFToleratesOutOfRangeColumns(t *testing.T) {
	// Column past the end of OriginalLine (e.g. a removed trailing block) must
	// not panic on the code-point conversion.
	run := &model.Run{ChartName: "c", Mutators: []string{"yaml-key-delete"}, EquivalenceChecked: true,
		Mutants: []model.Mutant{{ID: "a", Mutator: "yaml-key-delete", File: "values.yaml",
			Line: 3, Column: 99, StartByte: 10, EndByte: 30, OriginalLine: "short",
			Status: model.StatusSurvived}}}
	run.ComputeTally()
	reg := decodeSARIF(t, run).Runs[0].Results[0].Locations[0].PhysicalLocation.Region
	if reg.StartLine != 3 || reg.StartColumn < 1 {
		t.Errorf("region = %+v", reg)
	}
}
