package runner

import (
	"github.com/mmpyro/helm-mutation-test/internal/model"
)

// Classify turns a mutant's suite outcomes into a status plus kill attribution.
//
// The ordering encodes the central judgement of this tool:
//
//   - A *failed assertion* is a genuine kill: a test looked at the rendered
//     output and disagreed with it.
//   - A *render error* is not. A mutation that stops the chart rendering is
//     "caught" by every test in the suite regardless of what they assert, so it
//     measures nothing about test quality. Counting it as a kill inflates the
//     score, which is why it becomes Invalid and leaves the score's denominator.
//   - Everything passing is a survivor: the mutation changed the chart and no
//     test noticed.
//
// A suite-level setup error (an unreadable snapshot cache, say) is our failure
// rather than the chart's, so it maps to Error and is likewise excluded.
func Classify(outcomes []SuiteOutcome) (model.Status, []model.KilledBy, int) {
	var (
		killedBy    []model.KilledBy
		testsRun    int
		sawRenderer bool
		sawSetupErr bool
	)

	for _, oc := range outcomes {
		if oc.SetupError != "" {
			sawSetupErr = true
			continue
		}
		for _, t := range oc.Tests {
			if t.Skipped {
				continue
			}
			testsRun++
			if t.RenderError != "" {
				sawRenderer = true
				continue
			}
			for _, a := range t.FailedAsserts {
				killedBy = append(killedBy, model.KilledBy{
					Suite:       oc.Key.Name,
					SuiteFile:   oc.Key.File,
					Test:        t.Name,
					AssertType:  a.Type,
					AssertIndex: a.Index,
					FailInfo:    a.FailInfo,
				})
			}
		}
	}

	switch {
	case len(killedBy) > 0:
		return model.StatusKilled, killedBy, testsRun
	case sawRenderer:
		return model.StatusInvalid, nil, testsRun
	case sawSetupErr:
		return model.StatusError, nil, testsRun
	case testsRun == 0:
		// Nothing actually ran, so nothing could have caught the mutation. Calling
		// this "survived" would blame the tests for a gap in our own selection.
		return model.StatusNoCoverage, nil, 0
	default:
		return model.StatusSurvived, nil, testsRun
	}
}

// RenderErrorDetail returns the first render error across the outcomes, for the
// Detail field of an Invalid mutant.
func RenderErrorDetail(outcomes []SuiteOutcome) string {
	for _, oc := range outcomes {
		for _, t := range oc.Tests {
			if t.RenderError != "" {
				return t.RenderError
			}
		}
	}
	for _, oc := range outcomes {
		if oc.SetupError != "" {
			return oc.SetupError
		}
	}
	return ""
}
