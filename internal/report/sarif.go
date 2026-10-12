package report

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/mmpyro/helm-mutation-test/internal/model"
)

// SARIF renders a SARIF 2.1.0 log, the format behind GitHub code-scanning
// alerts, inline PR annotations and the editor's Problems panel.
//
// Every result becomes an alert, so only actionable findings are results:
//
//   - Survived becomes one result per mutant. Its level is error when the run
//     misses --threshold — the same condition as exit code 1 — and warning
//     otherwise.
//   - NoCoverage becomes one result per file, not per mutant: "no suite renders
//     this template" is a per-file finding, and per-mutant alerts would bury it.
//   - Killed, Invalid, Equivalent, Timeout and Error are not results. None of them
//     is a missing assertion.
//
// Leaving them out of the results must not hide them, so the invocation's
// notifications name the score and every excluded count, plus the same cap and
// equivalence disclosures JUnit carries, and the run's properties repeat them as
// numbers.
func SARIF(run *model.Run) ([]byte, error) {
	level := "warning"
	if run.Threshold > 0 && !run.MeetsThreshold() {
		level = "error"
	}

	log := sarifLog{
		Schema:  sarifSchemaURL,
		Version: "2.1.0",
		Runs: []sarifRun{{
			Tool: sarifTool{Driver: sarifDriver{
				Name:           "helm-mutation-test",
				InformationURI: sarifInfoURI,
				Rules:          sarifRules(run),
			}},
			ColumnKind: "unicodeCodePoints",
			Invocations: []sarifInvocation{{
				ExecutionSuccessful: true,
				Notifications:       sarifNotifications(run),
			}},
			Results: []sarifResult{},
			Properties: map[string]any{
				"chart":                run.ChartName,
				"score":                round(run.Score(), 2),
				"tally":                run.Tally,
				"generated":            run.Generated,
				"capped":               run.Capped,
				"equivalenceChecked":   run.EquivalenceChecked,
				"equivalenceUnchecked": run.EquivalenceUnchecked,
				"threshold":            run.Threshold,
			},
		}},
	}

	seen := map[string]int{}
	uncovered := map[string]int{}
	var uncoveredFiles []string
	for _, m := range run.Mutants {
		switch m.Status {
		case model.StatusSurvived:
			log.Runs[0].Results = append(log.Runs[0].Results, sarifSurvivor(run, m, level, seen))
		case model.StatusNoCoverage:
			if uncovered[m.File] == 0 {
				uncoveredFiles = append(uncoveredFiles, m.File)
			}
			uncovered[m.File]++
		}
	}
	for _, file := range uncoveredFiles {
		log.Runs[0].Results = append(log.Runs[0].Results, sarifResult{
			RuleID: sarifRuleNoCoverage,
			Level:  "warning",
			Message: sarifMessage{Text: fmt.Sprintf(
				"no suite renders %s; %s never ran. Add a suite that renders this template.",
				file, plural(uncovered[file], "mutant", "mutants"))},
			Locations: []sarifLocation{{PhysicalLocation: sarifPhysicalLocation{
				ArtifactLocation: sarifArtifact(run.ChartPath, file),
				Region:           sarifRegion{StartLine: 1, StartColumn: 1},
			}}},
			PartialFingerprints: map[string]string{
				sarifFingerprintKey: fingerprint(sarifRuleNoCoverage, file),
			},
		})
	}

	b, err := json.MarshalIndent(log, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding SARIF report: %w", err)
	}
	return append(b, '\n'), nil
}

const (
	sarifSchemaURL      = "https://json.schemastore.org/sarif-2.1.0.json"
	sarifInfoURI        = "https://github.com/mmpyro/helm-mutation-test"
	sarifDocsURI        = sarifInfoURI + "/blob/main/docs/"
	sarifRuleNoCoverage = "no-coverage"
	sarifFingerprintKey = "mutantFingerprint/v1"
)

// sarifRules declares one rule per enabled mutator plus no-coverage. Results may
// only reference declared rules, and the help link is what an editor offers next
// to the finding.
func sarifRules(run *model.Run) []sarifRule {
	// Survivors' mutators are added too: a hand-built Run may omit one from
	// run.Mutators, and a result must never reference an undeclared rule.
	ids := append([]string{}, run.Mutators...)
	for _, m := range run.Survived() {
		ids = append(ids, m.Mutator)
	}
	declared := map[string]bool{}
	var rules []sarifRule
	for _, id := range ids {
		if declared[id] {
			continue
		}
		declared[id] = true
		rules = append(rules, sarifRule{
			ID:               id,
			ShortDescription: sarifMessage{Text: "A " + id + " mutation survived: no test noticed the change"},
			HelpURI:          sarifDocsURI + "mutators.md#" + id,
		})
	}
	return append(rules, sarifRule{
		ID:               sarifRuleNoCoverage,
		ShortDescription: sarifMessage{Text: "No test suite renders this template, so none of its mutations ran"},
		HelpURI:          sarifDocsURI + "concepts.md#why-invalid-and-nocoverage-are-excluded",
	})
}

func sarifSurvivor(run *model.Run, m model.Mutant, level string, seen map[string]int) sarifResult {
	b := &strings.Builder{}
	fmt.Fprintf(b, "no test noticed this %s mutation at %s:%d\n", m.Mutator, m.File, m.Line)
	before, after := diffLines(m)
	fmt.Fprintf(b, "- %s\n+ %s\n", before, after)
	fmt.Fprintf(b, "%s\n", coverageNote(m))
	if len(m.CoveringSuites) > 0 {
		fmt.Fprintf(b, "suites run: %s\n", strings.Join(m.CoveringSuites, ", "))
	}
	b.WriteString("Add an assertion that distinguishes the original from the mutation.")

	// Keyed on content, not m.ID: the ID embeds a byte offset, so any edit above
	// the mutation would close its alert and open an identical new one. The
	// occurrence index keeps identical mutations on identical lines apart.
	key := fingerprint(m.File, m.Mutator, m.Original, m.Mutated, strings.TrimSpace(m.OriginalLine))
	seen[key]++

	return sarifResult{
		RuleID:  m.Mutator,
		Level:   level,
		Message: sarifMessage{Text: b.String()},
		Locations: []sarifLocation{{PhysicalLocation: sarifPhysicalLocation{
			ArtifactLocation: sarifArtifact(run.ChartPath, m.File),
			Region:           sarifSpan(m),
		}}},
		PartialFingerprints: map[string]string{
			sarifFingerprintKey: fmt.Sprintf("%s:%d", key, seen[key]),
		},
		Properties: map[string]any{
			"mutantId":       m.ID,
			"status":         m.Status,
			"original":       m.Original,
			"mutated":        m.Mutated,
			"coveringSuites": m.CoveringSuites,
		},
	}
}

func fingerprint(parts ...string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(parts, "\x00"))))
}

// sarifSpan converts the mutant's byte column into code points, which is what the
// run's columnKind promises. A byte column on a line with a multi-byte character
// would put the editor's underline on the wrong text. Like strykerPos, the end is
// clamped to the start line because only the start line is tracked.
func sarifSpan(m model.Mutant) sarifRegion {
	line := m.OriginalLine
	startByte := max(m.Column-1, 0)
	if startByte > len(line) {
		// Past the end of the recorded line: keep the reported column rather than
		// inventing one.
		return sarifRegion{StartLine: m.Line, StartColumn: max(m.Column, 1)}
	}
	start := utf8.RuneCountInString(line[:startByte]) + 1
	endByte := min(startByte+max(m.EndByte-m.StartByte, 0), len(line))
	width := utf8.RuneCountInString(line[startByte:endByte])
	return sarifRegion{StartLine: m.Line, StartColumn: start, EndColumn: start + max(width, 1)}
}

// sarifArtifact locates a chart file. A relative chart path is emitted relative to
// %SRCROOT% — the working directory, which in CI is the checkout root — because
// that is what GitHub resolves alerts against. An absolute one cannot be made
// relative without guessing the repository root, so it stays an absolute file URI.
func sarifArtifact(chartPath, file string) sarifArtifactLocation {
	if filepath.IsAbs(chartPath) {
		u := url.URL{Scheme: "file", Path: path.Join(filepath.ToSlash(chartPath), file)}
		return sarifArtifactLocation{URI: u.String()}
	}
	u := url.URL{Path: path.Join(filepath.ToSlash(chartPath), file)}
	return sarifArtifactLocation{URI: u.String(), URIBaseID: "%SRCROOT%"}
}

// sarifNotifications carries everything the results deliberately leave out. A
// SARIF consumer shows only results, so without these a run with five uncovered
// templates and a --max-mutants cap would read as "one survivor, all else fine".
func sarifNotifications(run *model.Run) []sarifNotification {
	t := run.Tally
	notes := []sarifNotification{{
		Level: "note",
		Message: sarifMessage{Text: fmt.Sprintf(
			"mutation score %.1f%% (%d killed of %d scored); excluded from the score: "+
				"%d no-coverage, %d invalid, %d equivalent, %d timeout, %d error",
			run.Score(), t.Killed, t.Scored(),
			t.NoCoverage, t.Invalid, t.Equivalent, t.Timeout, t.Errored)},
	}}
	if run.Threshold > 0 && !run.NothingToScore() {
		verdict := "meets"
		if !run.MeetsThreshold() {
			verdict = "is below"
		}
		notes = append(notes, sarifNotification{Level: "note", Message: sarifMessage{Text: fmt.Sprintf(
			"score %.1f%% %s the %.1f%% threshold", run.Score(), verdict, run.Threshold)}})
	}
	warn := func(text string) {
		notes = append(notes, sarifNotification{Level: "warning", Message: sarifMessage{Text: text}})
	}
	for _, n := range incrementalNotices(run) {
		warn(n)
	}
	if run.Capped > 0 {
		warn(cappedMessage(run))
	}
	if !run.EquivalenceChecked {
		warn(equivalenceSkippedMessage)
	}
	if run.EquivalenceUnchecked > 0 {
		warn(equivalenceUncheckedMessage(run))
	}
	for _, s := range run.SkippedFiles {
		warn(fmt.Sprintf("%s was only partially analysed: %s", s.File, firstLine(s.Reason)))
	}
	return notes
}

type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool        sarifTool         `json:"tool"`
	ColumnKind  string            `json:"columnKind"`
	Invocations []sarifInvocation `json:"invocations"`
	Results     []sarifResult     `json:"results"`
	Properties  map[string]any    `json:"properties"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	InformationURI string      `json:"informationUri"`
	Rules          []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID               string       `json:"id"`
	ShortDescription sarifMessage `json:"shortDescription"`
	HelpURI          string       `json:"helpUri"`
}

type sarifInvocation struct {
	ExecutionSuccessful bool                `json:"executionSuccessful"`
	Notifications       []sarifNotification `json:"toolExecutionNotifications,omitempty"`
}

type sarifNotification struct {
	Level   string       `json:"level"`
	Message sarifMessage `json:"message"`
}

type sarifMessage struct {
	Text string `json:"text"`
}

type sarifResult struct {
	RuleID              string            `json:"ruleId"`
	Level               string            `json:"level"`
	Message             sarifMessage      `json:"message"`
	Locations           []sarifLocation   `json:"locations"`
	PartialFingerprints map[string]string `json:"partialFingerprints"`
	Properties          map[string]any    `json:"properties,omitempty"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysicalLocation `json:"physicalLocation"`
}

type sarifPhysicalLocation struct {
	ArtifactLocation sarifArtifactLocation `json:"artifactLocation"`
	Region           sarifRegion           `json:"region"`
}

type sarifArtifactLocation struct {
	URI       string `json:"uri"`
	URIBaseID string `json:"uriBaseId,omitempty"`
}

type sarifRegion struct {
	StartLine   int `json:"startLine"`
	StartColumn int `json:"startColumn"`
	EndColumn   int `json:"endColumn,omitempty"`
}
