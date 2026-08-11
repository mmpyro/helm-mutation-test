package runner

import (
	"fmt"
	"strings"
	"time"

	"github.com/mmpyro/helm-mutation-test/internal/model"
)

// Baseline is the unmutated state of the chart and its suites.
type Baseline struct {
	ChartName string
	Suites    []*Suite
	Outcomes  []SuiteOutcome
	TestCount int
	Duration  time.Duration
}

// BaselineFailure reports that the chart's own tests do not pass.
//
// This is a hard stop rather than a warning. A mutation score is defined as the
// fraction of mutants a *passing* suite catches; over a red suite, every mutant
// looks "killed" by the pre-existing failure and the number is meaningless.
type BaselineFailure struct {
	Detail string
}

func (e *BaselineFailure) Error() string {
	return "the chart's tests do not pass before mutation:\n" + e.Detail
}

// RunBaseline loads the chart, discovers its suites, and runs them unmutated.
//
// It returns a *BaselineFailure if any test fails, errors, or if the chart has
// no tests at all — with nothing to measure, a score would be a fiction.
func RunBaseline(chartDir string, opts Options) (*Baseline, error) {
	chart, err := LoadChart(chartDir)
	if err != nil {
		return nil, err
	}
	suites, err := DiscoverSuites(chartDir, chart, opts)
	if err != nil {
		return nil, err
	}
	if len(suites) == 0 {
		return nil, &BaselineFailure{
			Detail: fmt.Sprintf("no test suites matched %v under %s", opts.TestFiles, chartDir),
		}
	}

	start := time.Now()
	// failFast is false here: the baseline needs the complete picture so the error
	// message can name every broken test at once.
	outcomes := RunSuites(chart, suites, false)
	elapsed := time.Since(start)

	b := &Baseline{
		ChartName: chart.Name(),
		Suites:    suites,
		Outcomes:  outcomes,
		Duration:  elapsed,
	}
	for _, oc := range outcomes {
		b.TestCount += len(oc.Tests)
	}

	if detail := describeBaselineFailures(outcomes); detail != "" {
		return nil, &BaselineFailure{Detail: detail}
	}
	if b.TestCount == 0 {
		return nil, &BaselineFailure{Detail: "the suites contain no test jobs"}
	}
	return b, nil
}

func describeBaselineFailures(outcomes []SuiteOutcome) string {
	var sb strings.Builder
	for _, oc := range outcomes {
		if oc.SetupError != "" {
			fmt.Fprintf(&sb, "  %s: %s\n", oc.Key.File, oc.SetupError)
			continue
		}
		for _, t := range oc.Tests {
			if t.Passed || t.Skipped {
				continue
			}
			fmt.Fprintf(&sb, "  %s > %s\n", oc.Key.Name, t.Name)
			if t.RenderError != "" {
				fmt.Fprintf(&sb, "      render error: %s\n", t.RenderError)
			}
			for _, a := range t.FailedAsserts {
				fmt.Fprintf(&sb, "      failed assert %s: %s\n", a.Type, firstLine(a.FailInfo))
			}
		}
	}
	return sb.String()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// SuiteInfos converts the baseline inventory into the report model.
func (b *Baseline) SuiteInfos() []model.SuiteInfo {
	out := make([]model.SuiteInfo, 0, len(b.Suites))
	for _, s := range b.Suites {
		out = append(out, model.SuiteInfo{
			Name:  s.Key.Name,
			File:  s.Key.File,
			Tests: s.TestNames,
		})
	}
	return out
}
