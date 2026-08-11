// Package runner drives helm-unittest over a chart and classifies the outcome.
package runner

import (
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/helm-unittest/helm-unittest/pkg/unittest"
	"github.com/helm-unittest/helm-unittest/pkg/unittest/results"
	"github.com/helm-unittest/helm-unittest/pkg/unittest/snapshot"
	log "github.com/sirupsen/logrus"
	v3chart "helm.sh/helm/v3/pkg/chart"
	v3loader "helm.sh/helm/v3/pkg/chart/loader"
)

// This file is the ONLY place that imports helm-unittest. Everything downstream
// speaks in the local Suite/Outcome types below, so a helm-unittest version bump
// is contained to this file.
//
// We deliberately drive TestSuite.RunV3 rather than TestRunner.RunV3: the latter
// returns only a bool and prints to a Printer, while the former hands back the
// full result tree. That tree is what makes per-assertion kill attribution and
// the Invalid-versus-Killed distinction possible at all.

// Options mirrors the helm-unittest flags this tool passes through.
type Options struct {
	TestFiles      []string
	ValuesFiles    []string
	Strict         bool
	WithSubChart   bool
	ChartTestsPath string
}

// SilenceLibraryLogging quiets helm-unittest's logrus output, which would
// otherwise interleave with our own reporting.
func SilenceLibraryLogging(debug bool) {
	if debug {
		log.SetLevel(log.DebugLevel)
		return
	}
	log.SetOutput(io.Discard)
	log.SetLevel(log.PanicLevel)
}

// SuiteKey identifies a suite stably across re-parses of the same files.
//
// A suite file may hold several suites separated by "---", so the file path
// alone is not unique; Ordinal disambiguates them, and Name keeps the key
// readable in reports.
//
// File is CHART-RELATIVE, and must stay that way. Each worker re-parses suites
// from its own copy of the chart in a temp directory, so an absolute path differs
// per worker and the keys would never match the ones the baseline recorded —
// every mutant would silently run zero suites and be reported as NoCoverage.
type SuiteKey struct {
	File    string `json:"file"`
	Name    string `json:"name"`
	Ordinal int    `json:"ordinal"`
}

func (k SuiteKey) String() string { return fmt.Sprintf("%s#%d(%s)", k.File, k.Ordinal, k.Name) }

// Suite is the metadata we need about a discovered suite, without exposing
// helm-unittest's own type.
type Suite struct {
	Key SuiteKey
	// Templates and ExcludeTemplates come from the suite's own declarations.
	Templates        []string
	ExcludeTemplates []string
	// JobTemplates is the union of every per-test template override in the suite.
	JobTemplates []string
	// TestNames lists the suite's test jobs, for the baseline inventory.
	TestNames []string

	suite *unittest.TestSuite
}

// CoversAllTemplates reports whether the suite declares no template filter and
// therefore renders whatever the chart produces.
func (s *Suite) CoversAllTemplates() bool {
	return len(s.Templates) == 0 && len(s.JobTemplates) == 0
}

// LoadChart loads a chart directory into memory. Each mutant needs its own load
// because the mutation lives on disk.
func LoadChart(chartDir string) (*v3chart.Chart, error) {
	c, err := v3loader.Load(chartDir)
	if err != nil {
		return nil, fmt.Errorf("loading chart %s: %w", chartDir, err)
	}
	return c, nil
}

// DiscoverSuites parses the test suite files for a chart directory.
//
// Suites are re-parsed for every mutant run rather than shared: RunV3 mutates
// suite state via polishTestJobsPathInfo, so one *TestSuite cannot safely serve
// two goroutines. Re-parsing YAML is cheap next to rendering a chart.
func DiscoverSuites(chartDir string, chart *v3chart.Chart, opts Options) ([]*Suite, error) {
	testFiles, err := unittest.GetFiles(chartDir, opts.TestFiles, false)
	if err != nil {
		return nil, fmt.Errorf("resolving test file patterns: %w", err)
	}
	valuesFiles, err := unittest.GetFiles("", opts.ValuesFiles, true)
	if err != nil {
		return nil, fmt.Errorf("resolving values file patterns: %w", err)
	}
	// Glob order is filesystem-dependent; sort so mutant runs and reports are
	// deterministic.
	sort.Strings(testFiles)

	chartRoute := chart.Name()
	var out []Suite
	for _, file := range testFiles {
		parsed, err := unittest.ParseTestSuiteFile(file, chartRoute, opts.Strict, valuesFiles)
		if err != nil {
			return nil, fmt.Errorf("parsing suite %s: %w", file, err)
		}
		for i, ts := range parsed {
			if ts == nil {
				continue
			}
			out = append(out, describe(relativeToChart(chartDir, file), i, ts))
		}
	}

	ptrs := make([]*Suite, len(out))
	for i := range out {
		ptrs[i] = &out[i]
	}
	return ptrs, nil
}

// relativeToChart converts a discovered suite path into a chart-relative one, so
// SuiteKeys are comparable across the baseline chart and each worker's copy.
func relativeToChart(chartDir, file string) string {
	absChart, err1 := filepath.Abs(chartDir)
	absFile, err2 := filepath.Abs(file)
	if err1 != nil || err2 != nil {
		return filepath.ToSlash(file)
	}
	rel, err := filepath.Rel(absChart, absFile)
	if err != nil {
		return filepath.ToSlash(file)
	}
	return filepath.ToSlash(rel)
}

func describe(file string, ordinal int, ts *unittest.TestSuite) Suite {
	s := Suite{
		Key:              SuiteKey{File: file, Name: ts.Name, Ordinal: ordinal},
		Templates:        slices.Clone(ts.Templates),
		ExcludeTemplates: slices.Clone(ts.ExcludeTemplates),
		suite:            ts,
	}
	for _, job := range ts.Tests {
		if job == nil {
			continue
		}
		s.TestNames = append(s.TestNames, job.Name)
		if job.Template != "" {
			s.JobTemplates = append(s.JobTemplates, job.Template)
		}
		s.JobTemplates = append(s.JobTemplates, job.Templates...)
	}
	slices.Sort(s.JobTemplates)
	s.JobTemplates = slices.Compact(s.JobTemplates)
	return s
}

// AssertOutcome is one failed assertion.
type AssertOutcome struct {
	Index    int
	Type     string
	Not      bool
	FailInfo string
}

// TestOutcome is the result of one test job.
type TestOutcome struct {
	Name    string
	Passed  bool
	Skipped bool
	// RenderError is set when the template failed to render, as opposed to an
	// assertion failing. The distinction is what separates an Invalid mutant from
	// a genuinely Killed one.
	RenderError   string
	FailedAsserts []AssertOutcome
	Duration      time.Duration
}

// SuiteOutcome is the result of running one suite.
type SuiteOutcome struct {
	Key         SuiteKey
	Passed      bool
	Skipped     bool
	SetupError  string
	Tests       []TestOutcome
	TotalAssert int
}

// RunSuites runs the given suites against an already-loaded chart.
//
// failFast stops a suite at its first failing test. Mutant runs enable it (one
// kill is enough); the baseline does not (it needs the full picture).
func RunSuites(chart *v3chart.Chart, suites []*Suite, failFast bool) []SuiteOutcome {
	out := make([]SuiteOutcome, 0, len(suites))
	for _, s := range suites {
		out = append(out, runOne(chart, s, failFast))
	}
	return out
}

func runOne(chart *v3chart.Chart, s *Suite, failFast bool) SuiteOutcome {
	oc := SuiteOutcome{Key: s.Key}

	cache, err := snapshot.CreateSnapshotOfSuite(s.suite.SnapshotFileUrl(), false)
	if err != nil {
		oc.SetupError = err.Error()
		return oc
	}

	res := s.suite.RunV3(chart, cache, failFast, "", &results.TestSuiteResult{})
	// Deliberately no cache.StoreToFileIfNeeded(): a mutant run must never
	// rewrite the chart's committed snapshots.

	oc.Passed = res.Passed
	oc.Skipped = res.Skipped
	if res.ExecError != nil {
		oc.SetupError = res.ExecError.Error()
	}
	for _, tr := range res.TestsResult {
		if tr == nil {
			continue
		}
		t := TestOutcome{
			Name:     tr.DisplayName,
			Passed:   tr.Passed,
			Skipped:  tr.Skipped,
			Duration: tr.Duration,
		}
		if tr.ExecError != nil {
			t.RenderError = tr.ExecError.Error()
		}
		for _, ar := range tr.AssertsResult {
			if ar == nil {
				continue
			}
			oc.TotalAssert++
			if ar.Passed || ar.Skipped {
				continue
			}
			t.FailedAsserts = append(t.FailedAsserts, AssertOutcome{
				Index:    ar.Index,
				Type:     ar.AssertType,
				Not:      ar.Not,
				FailInfo: joinLines(ar.FailInfo),
			})
		}
		oc.Tests = append(oc.Tests, t)
	}
	return oc
}

func joinLines(lines []string) string {
	const maxLines = 6 // failure text can be a whole rendered manifest
	if len(lines) > maxLines {
		lines = append(slices.Clone(lines[:maxLines]), "...")
	}
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}
