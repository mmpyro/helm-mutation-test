package report

import (
	"encoding/json"
	"fmt"

	"github.com/mmpyro/helm-mutation-test/internal/model"
)

// JSON is the canonical machine-readable report: the full model, every mutant,
// with kill attribution. The other file formats are summaries; this is the one to
// parse if you want to build something on top.
func JSON(run *model.Run) ([]byte, error) {
	b, err := json.MarshalIndent(jsonReport{
		Schema:    "helm-mutation-test/v1",
		ChartName: run.ChartName,
		ChartPath: run.ChartPath,
		Score:     round(run.Score(), 2),
		Threshold: run.Threshold,
		Passed:    run.MeetsThreshold(),
		Tally:     run.Tally,
		Generated: run.Generated,
		Capped:    run.Capped,
		Mutators:  run.Mutators,
		Suites:    run.Suites,
		TestCount: run.TestCount,
		ByMutator: breakdowns(run.ByMutator()),
		ByFile:    breakdowns(run.ByFile()),
		Mutants:   run.Mutants,
		Skipped:   run.SkippedFiles,
		Timing: timing{
			BaselineMillis: run.BaselineDuration.Milliseconds(),
			TotalMillis:    run.Duration.Milliseconds(),
		},
	}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding JSON report: %w", err)
	}
	return append(b, '\n'), nil
}

// jsonReport is a stable wire shape, deliberately separate from model.Run so
// internal refactors do not silently change the published format.
type jsonReport struct {
	Schema    string              `json:"schema"`
	ChartName string              `json:"chartName"`
	ChartPath string              `json:"chartPath"`
	Score     float64             `json:"score"`
	Threshold float64             `json:"threshold"`
	Passed    bool                `json:"passed"`
	Tally     model.Tally         `json:"tally"`
	Generated int                 `json:"generated"`
	Capped    int                 `json:"capped"`
	Mutators  []string            `json:"mutators"`
	Suites    []model.SuiteInfo   `json:"suites"`
	TestCount int                 `json:"testCount"`
	ByMutator []breakdown         `json:"byMutator"`
	ByFile    []breakdown         `json:"byFile"`
	Mutants   []model.Mutant      `json:"mutants"`
	Skipped   []model.SkippedFile `json:"skippedFiles,omitempty"`
	Timing    timing              `json:"timing"`
}

type breakdown struct {
	Name  string      `json:"name"`
	Score float64     `json:"score"`
	Tally model.Tally `json:"tally"`
}

type timing struct {
	BaselineMillis int64 `json:"baselineMillis"`
	TotalMillis    int64 `json:"totalMillis"`
}

func breakdowns(in []model.Breakdown) []breakdown {
	out := make([]breakdown, 0, len(in))
	for _, b := range in {
		out = append(out, breakdown{Name: b.Name, Score: round(b.Score, 2), Tally: b.Tally})
	}
	return out
}

// round trims float noise so golden comparisons and diffs stay stable.
func round(v float64, places int) float64 {
	pow := 1.0
	for range places {
		pow *= 10
	}
	return float64(int64(v*pow+0.5)) / pow
}
