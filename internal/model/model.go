// Package model holds the canonical data model for a mutation testing run.
//
// Every report format is a pure serializer over Run, so this package must stay
// free of dependencies on helm-unittest, Helm, or the CLI.
package model

import (
	"sort"
	"time"
)

// Status is the outcome of evaluating a single mutant.
type Status string

const (
	// StatusKilled means at least one assertion failed: the tests noticed the mutation.
	StatusKilled Status = "Killed"
	// StatusSurvived means every covering test still passed: a missing assertion.
	StatusSurvived Status = "Survived"
	// StatusNoCoverage means no test suite exercises the mutated file at all.
	StatusNoCoverage Status = "NoCoverage"
	// StatusInvalid means the mutation broke template rendering. Every test "kills"
	// such a mutant, so it carries no information about test quality and is excluded
	// from the score.
	StatusInvalid Status = "Invalid"
	// StatusEquivalent means the mutation provably cannot change any rendered
	// manifest: the chart renders to identical parsed documents under every value
	// set the covering tests use, and the mutated span was proven to execute. No
	// assertion could distinguish it, so it is excluded from the score.
	StatusEquivalent Status = "Equivalent"
	// StatusTimeout means the mutant run exceeded the per-mutant timeout.
	StatusTimeout Status = "Timeout"
	// StatusError means the tool itself failed on this mutant (copy, load, parse).
	StatusError Status = "Error"
)

// CountsTowardScore reports whether a status participates in the mutation score.
// Only Killed and Survived do; everything else is reported separately so that
// render-breaking or unreachable mutants cannot inflate the number.
func (s Status) CountsTowardScore() bool {
	return s == StatusKilled || s == StatusSurvived
}

// KilledBy identifies the assertion that killed a mutant.
type KilledBy struct {
	Suite       string `json:"suite"`
	SuiteFile   string `json:"suiteFile"`
	Test        string `json:"test"`
	AssertType  string `json:"assertType"`
	AssertIndex int    `json:"assertIndex"`
	FailInfo    string `json:"failInfo,omitempty"`
}

// Mutant is a single mutation and its evaluation result.
type Mutant struct {
	ID      string `json:"id"`
	Mutator string `json:"mutator"`
	// File is slash-separated and relative to the chart root.
	File string `json:"file"`
	// Line and Column are 1-based, pointing at the start of the mutated span.
	Line   int `json:"line"`
	Column int `json:"column"`
	// StartByte and EndByte delimit the replaced span in the original file.
	StartByte int `json:"startByte"`
	EndByte   int `json:"endByte"`
	// OriginalLine and MutatedLine are the full source lines before and after,
	// for display in reports.
	OriginalLine string `json:"originalLine"`
	MutatedLine  string `json:"mutatedLine"`
	// Original and Mutated are the replaced span itself.
	Original string `json:"original"`
	Mutated  string `json:"mutated"`

	Status Status `json:"status"`
	// KilledBy is populated for StatusKilled. With --kill-attribution=first it
	// holds a single entry.
	KilledBy []KilledBy `json:"killedBy,omitempty"`
	// CoveringSuites lists the suite files that were run for this mutant.
	CoveringSuites []string `json:"coveringSuites,omitempty"`
	// TestsRun is how many test jobs executed before the run stopped.
	TestsRun int `json:"testsRun"`
	// Detail carries the render error for Invalid, or the tool error for Error.
	Detail   string        `json:"detail,omitempty"`
	Duration time.Duration `json:"durationNanos"`
}

// Tally counts mutants by status and derives a score.
type Tally struct {
	Killed     int `json:"killed"`
	Survived   int `json:"survived"`
	NoCoverage int `json:"noCoverage"`
	Invalid    int `json:"invalid"`
	Equivalent int `json:"equivalent"`
	Timeout    int `json:"timeout"`
	Errored    int `json:"error"`
}

// Add records one mutant status.
func (t *Tally) Add(s Status) {
	switch s {
	case StatusKilled:
		t.Killed++
	case StatusSurvived:
		t.Survived++
	case StatusNoCoverage:
		t.NoCoverage++
	case StatusInvalid:
		t.Invalid++
	case StatusEquivalent:
		t.Equivalent++
	case StatusTimeout:
		t.Timeout++
	case StatusError:
		t.Errored++
	}
}

// Total is the number of mutants counted, across every status.
func (t Tally) Total() int {
	return t.Killed + t.Survived + t.NoCoverage + t.Invalid + t.Equivalent + t.Timeout + t.Errored
}

// Scored is the score denominator: mutants that actually tell us something.
func (t Tally) Scored() int { return t.Killed + t.Survived }

// Score is the fraction of informative mutants that were killed, in [0,100].
// Returns 0 when nothing was scored.
func (t Tally) Score() float64 {
	if t.Scored() == 0 {
		return 0
	}
	return float64(t.Killed) / float64(t.Scored()) * 100
}

// Breakdown is a named Tally, used for per-mutator and per-file tables.
type Breakdown struct {
	Name  string `json:"name"`
	Tally Tally  `json:"tally"`
	Score float64
}

// SuiteInfo describes a test suite discovered during the baseline run.
type SuiteInfo struct {
	Name  string   `json:"name"`
	File  string   `json:"file"`
	Tests []string `json:"tests"`
}

// SkippedFile records a file we could not fully analyse, so reports can be
// honest about incomplete coverage rather than implying we mutated everything.
type SkippedFile struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
}

// Run is the complete result of a mutation testing session.
type Run struct {
	ChartName string `json:"chartName"`
	ChartPath string `json:"chartPath"`
	// Mutators lists the mutator IDs that were enabled.
	Mutators []string `json:"mutators"`

	Suites    []SuiteInfo `json:"suites"`
	TestCount int         `json:"testCount"`

	Mutants []Mutant `json:"mutants"`
	Tally   Tally    `json:"tally"`

	// Generated is the number of mutants generated before any --max-mutants cap.
	Generated int `json:"generated"`
	// Capped is how many mutants the cap discarded. Reported explicitly: a silent
	// truncation would read as full coverage.
	Capped int `json:"capped"`
	// EquivalenceChecked records whether the equivalence pass ran. Without it,
	// "equivalent 0" is ambiguous between "checked and found none" and "never
	// looked", and the second reads as the first.
	EquivalenceChecked bool `json:"equivalenceChecked"`
	// EquivalenceUnchecked counts survivors the pass ran over but could not reach
	// a verdict on. EquivalenceChecked alone overclaims: a chart that fails to
	// load, or covering suites that all have to be skipped, leave every survivor
	// unexamined while still reporting "checked, found none equivalent".
	EquivalenceUnchecked int `json:"equivalenceUnchecked"`
	// SkippedFiles records files excluded from AST mutation (e.g. parse failures).
	SkippedFiles []SkippedFile `json:"skippedFiles,omitempty"`

	BaselineDuration time.Duration `json:"baselineDurationNanos"`
	Duration         time.Duration `json:"durationNanos"`
	Threshold        float64       `json:"threshold"`
}

// Score is the run's mutation score in [0,100].
func (r *Run) Score() float64 { return r.Tally.Score() }

// MeetsThreshold reports whether the score satisfies the configured threshold.
// A threshold of 0 disables the check.
func (r *Run) MeetsThreshold() bool {
	return r.Threshold <= 0 || r.Score() >= r.Threshold
}

// ComputeTally recomputes the aggregate tally from the mutant list.
func (r *Run) ComputeTally() {
	r.Tally = Tally{}
	for _, m := range r.Mutants {
		r.Tally.Add(m.Status)
	}
}

// ByMutator groups mutants by mutator ID, sorted by ascending score so the
// weakest-covered mutators surface first.
func (r *Run) ByMutator() []Breakdown { return r.groupBy(func(m Mutant) string { return m.Mutator }) }

// ByFile groups mutants by file, sorted by ascending score.
func (r *Run) ByFile() []Breakdown { return r.groupBy(func(m Mutant) string { return m.File }) }

func (r *Run) groupBy(key func(Mutant) string) []Breakdown {
	tallies := map[string]*Tally{}
	for _, m := range r.Mutants {
		k := key(m)
		if tallies[k] == nil {
			tallies[k] = &Tally{}
		}
		tallies[k].Add(m.Status)
	}
	out := make([]Breakdown, 0, len(tallies))
	for name, t := range tallies {
		out = append(out, Breakdown{Name: name, Tally: *t, Score: t.Score()})
	}
	// Ascending score, then name, so ordering is deterministic and the most
	// under-tested groups appear first.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score < out[j].Score
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Survived returns the survived mutants in report order.
func (r *Run) Survived() []Mutant { return r.withStatus(StatusSurvived) }

// NoCoverage returns the mutants no suite exercised.
func (r *Run) NoCoverage() []Mutant { return r.withStatus(StatusNoCoverage) }

// Equivalent returns the mutants proven unable to change any rendered manifest.
func (r *Run) Equivalent() []Mutant { return r.withStatus(StatusEquivalent) }

func (r *Run) withStatus(s Status) []Mutant {
	var out []Mutant
	for _, m := range r.Mutants {
		if m.Status == s {
			out = append(out, m)
		}
	}
	return out
}
