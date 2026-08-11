package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mmpyro/helm-mutation-test/internal/model"
)

// Stryker emits the mutation-testing-elements standard report schema.
//
// Conforming to that schema rather than inventing an HTML report buys the
// interactive, source-annotated viewer for free: the published web component
// renders this JSON directly. See
// https://github.com/stryker-mutator/mutation-testing-elements
//
// chartDir is where the mutated files are read from, since the schema embeds each
// file's full source so the viewer can highlight mutations in place.
func Stryker(run *model.Run, chartDir string) ([]byte, error) {
	report := strykerReport{
		Schema:          strykerSchemaURL,
		SchemaVersion:   "2",
		ThresholdsField: strykerThresholds{High: int(scoreGood), Low: int(scoreOkay)},
		ProjectRoot:     chartDir,
		Files:           map[string]*strykerFile{},
	}

	for _, m := range run.Mutants {
		file, ok := report.Files[m.File]
		if !ok {
			source, err := os.ReadFile(filepath.Join(chartDir, filepath.FromSlash(m.File)))
			if err != nil {
				// The viewer needs source to annotate; without it, show the mutants
				// against an empty file rather than dropping them silently.
				source = nil
			}
			file = &strykerFile{Source: string(source), Language: languageOf(m.File)}
			report.Files[m.File] = file
		}
		file.Mutants = append(file.Mutants, strykerMutant{
			ID:           m.ID,
			MutatorName:  m.Mutator,
			Replacement:  m.Mutated,
			Status:       strykerStatus(m.Status),
			StatusReason: firstLine(m.Detail),
			Description:  describeMutant(m),
			Location:     strykerLocation{Start: strykerPos(m, false), End: strykerPos(m, true)},
			KilledBy:     killedByIDs(m),
			TestsRun:     m.TestsRun,
		})
	}

	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding mutation-testing-elements report: %w", err)
	}
	return append(b, '\n'), nil
}

const strykerSchemaURL = "https://raw.githubusercontent.com/stryker-mutator/mutation-testing-elements/master/packages/report-schema/src/mutation-testing-report-schema.json"

// strykerStatus maps our statuses onto the schema's vocabulary.
//
// Invalid becomes CompileError, which is the schema's term for "the mutation made
// the artefact unbuildable": exactly our case, and like us the viewer excludes it
// from the score.
func strykerStatus(s model.Status) string {
	switch s {
	case model.StatusKilled:
		return "Killed"
	case model.StatusSurvived:
		return "Survived"
	case model.StatusNoCoverage:
		return "NoCoverage"
	case model.StatusInvalid:
		return "CompileError"
	case model.StatusTimeout:
		return "Timeout"
	default:
		return "RuntimeError"
	}
}

// strykerPos converts a byte span endpoint into the schema's 1-based line and
// column. Columns are exclusive at the end, matching the schema.
func strykerPos(m model.Mutant, end bool) strykerPosition {
	if !end {
		return strykerPosition{Line: m.Line, Column: m.Column}
	}
	// A mutation may span lines (yaml-key-delete removes a whole block), but we
	// only track the start line, so clamp the end to the same line's column span.
	width := m.EndByte - m.StartByte
	return strykerPosition{Line: m.Line, Column: m.Column + max(width, 1)}
}

func describeMutant(m model.Mutant) string {
	switch m.Status {
	case model.StatusSurvived:
		return fmt.Sprintf("%s: no test noticed this change", m.Mutator)
	case model.StatusNoCoverage:
		return fmt.Sprintf("%s: no suite renders this template", m.Mutator)
	default:
		return m.Mutator
	}
}

func killedByIDs(m model.Mutant) []string {
	if len(m.KilledBy) == 0 {
		return nil
	}
	out := make([]string, 0, len(m.KilledBy))
	for _, k := range m.KilledBy {
		out = append(out, fmt.Sprintf("%s > %s", k.Suite, k.Test))
	}
	return out
}

func languageOf(file string) string {
	if filepath.Ext(file) == ".tpl" {
		return "html" // no YAML-with-Go-template mode exists; html highlights {{ }}
	}
	return "yaml"
}

type strykerReport struct {
	Schema          string                  `json:"$schema"`
	SchemaVersion   string                  `json:"schemaVersion"`
	ThresholdsField strykerThresholds       `json:"thresholds"`
	ProjectRoot     string                  `json:"projectRoot,omitempty"`
	Files           map[string]*strykerFile `json:"files"`
}

type strykerThresholds struct {
	High int `json:"high"`
	Low  int `json:"low"`
}

type strykerFile struct {
	Source   string          `json:"source"`
	Language string          `json:"language"`
	Mutants  []strykerMutant `json:"mutants"`
}

type strykerMutant struct {
	ID           string          `json:"id"`
	MutatorName  string          `json:"mutatorName"`
	Replacement  string          `json:"replacement,omitempty"`
	Status       string          `json:"status"`
	StatusReason string          `json:"statusReason,omitempty"`
	Description  string          `json:"description,omitempty"`
	Location     strykerLocation `json:"location"`
	KilledBy     []string        `json:"killedBy,omitempty"`
	TestsRun     int             `json:"testsCompleted,omitempty"`
}

type strykerLocation struct {
	Start strykerPosition `json:"start"`
	End   strykerPosition `json:"end"`
}

type strykerPosition struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}
