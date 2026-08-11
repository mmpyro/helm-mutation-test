// Package runner drives helm-unittest over a chart and classifies the outcome.
package runner

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/helm-unittest/helm-unittest/pkg/unittest"
	"github.com/helm-unittest/helm-unittest/pkg/unittest/results"
	"github.com/helm-unittest/helm-unittest/pkg/unittest/snapshot"
	"github.com/helm-unittest/helm-unittest/pkg/unittest/valueutils"
	"github.com/mmpyro/helm-mutation-test/internal/equivalence"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
	v3chart "helm.sh/helm/v3/pkg/chart"
	v3loader "helm.sh/helm/v3/pkg/chart/loader"
	v3util "helm.sh/helm/v3/pkg/chartutil"
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

// UsesKubernetesProvider reports whether the suite installs a fake Kubernetes
// client. Such a suite's `lookup` calls return objects our own renderer will not
// see, so two renders could agree here that helm-unittest would find different.
// Mutants covered by such a suite skip equivalence detection entirely.
func (s *Suite) UsesKubernetesProvider() bool {
	if s.suite == nil {
		return false
	}
	if len(s.suite.KubernetesProvider.Objects) > 0 || s.suite.KubernetesProvider.Scheme != nil {
		return true
	}
	for _, job := range s.suite.Tests {
		if job == nil {
			continue
		}
		if len(job.KubernetesProvider.Objects) > 0 || job.KubernetesProvider.Scheme != nil {
			return true
		}
	}
	return false
}

// RenderContexts returns one equivalence.RenderContext per non-skipped test job
// in the suite.
//
// This reproduces the suite-to-job merge helm-unittest performs internally,
// spread across ParseTestSuiteFile's initial polish pass and RunV3's
// polishTestJobsPathInfo/getUserValues/releaseV3Option/capabilitiesV3: suite
// values files are prepended to the job's, the suite's `set` acts as a global
// set merged before the job's own, and suite-level release, chart and
// capability settings fill in wherever the job leaves them empty. Every merge
// below reads the suite's and job's own raw fields directly rather than calling
// helm-unittest's polish methods, so the answer does not depend on whether
// RunV3 has already mutated this *Suite. It is insensitive to that, not
// oblivious to it: mergedValues and capabilities each say below why re-applying
// a merge RunV3 may already have applied changes nothing.
//
// Reproducing it is the price of rendering under the values the tests actually
// used, which is what makes an Equivalent verdict safe. It is also the most
// likely thing here to go wrong, which is why
// TestNoKilledMutantIsJudgedEquivalent exists.
func RenderContexts(chartDir string, s *Suite) ([]equivalence.RenderContext, error) {
	// An error, not an empty list: contributing zero contexts silently would make
	// every mutant this suite covers inconclusive-but-unreported, and everything
	// else on this path fails safe.
	if s == nil || s.suite == nil {
		return nil, errors.New("suite carries no parsed helm-unittest suite")
	}
	ts := s.suite
	suiteDir := filepath.Dir(filepath.Join(chartDir, filepath.FromSlash(s.Key.File)))

	out := make([]equivalence.RenderContext, 0, len(ts.Tests))
	for i, job := range ts.Tests {
		if job == nil || job.Skip.Reason != "" || ts.Skip.Reason != "" {
			continue
		}

		values, err := mergedValues(suiteDir, ts, job)
		if err != nil {
			return nil, fmt.Errorf("%s: job %q: %w", s.Key, job.Name, err)
		}

		out = append(out, equivalence.RenderContext{
			Name:            fmt.Sprintf("%s#%d/%d %s", s.Key.File, s.Key.Ordinal, i, job.Name),
			Values:          values,
			Release:         releaseOptions(ts, job),
			Capabilities:    capabilities(ts, job),
			ChartVersion:    cmpOr(job.Chart.Version, ts.Chart.Version),
			ChartAppVersion: cmpOr(job.Chart.AppVersion, ts.Chart.AppVersion),
		})
	}
	return out, nil
}

// mergedValues reproduces TestJob.getUserValues: values files first, then the
// suite-level set, then the job's own set, each merged over what came before.
// getUserValues also scopes every value under the chart's route
// (scopeValuesWithRoutes) for subchart-aware rendering; at the top-level route -
// the only one this tool supports, since subchart-aware scoring is out of scope
// - that scoping is a no-op, so it is omitted here rather than reproduced.
//
// Prepending the suite's values files is deliberately unconditional even though
// polishTestJobsPathInfo already did it during the baseline run, so on an
// already-polished job each suite file is read twice. That is a no-op: the first
// occurrence of a path is the one that wins (MergeTables keeps what the
// accumulator already holds), so a duplicate merges a map over itself and the
// relative precedence of suite and job files is unchanged. The alternative -
// trusting job.Values to already carry them - would silently drop the suite's
// values for any *Suite RunV3 has not touched.
func mergedValues(suiteDir string, ts *unittest.TestSuite, job *unittest.TestJob) (map[string]any, error) {
	base := map[string]any{}

	files := append(slices.Clone(ts.Values), job.Values...)
	for _, p := range files {
		path := p
		if !filepath.IsAbs(path) {
			path = filepath.Join(suiteDir, path)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading values file %s: %w", p, err)
		}
		var v map[string]any
		if err := yaml.Unmarshal(b, &v); err != nil {
			return nil, fmt.Errorf("parsing values file %s: %w", p, err)
		}
		base = v3util.MergeTables(v, base)
	}

	for _, set := range []map[string]any{ts.Set, job.Set} {
		for path, val := range set {
			built, err := valueutils.BuildValueOfSetPath(val, path)
			if err != nil {
				return nil, fmt.Errorf("building set path %s: %w", path, err)
			}
			base = v3util.MergeTables(built, base)
		}
	}
	return base, nil
}

// releaseOptions reproduces polishReleaseSettings plus releaseV3Option: the job
// wins field-by-field, the suite fills in, and Name/Namespace only fall back to
// Helm's own "RELEASE-NAME"/"NAMESPACE" once both are empty. Revision has no
// such floor - helm-unittest never defaults it to 1, so a mutation gated on
// `.Release.Revision` must be judged under 0, not under a first-install value
// nobody asked for.
func releaseOptions(ts *unittest.TestSuite, job *unittest.TestJob) v3util.ReleaseOptions {
	isUpgrade := job.Release.IsUpgrade || ts.Release.IsUpgrade
	return v3util.ReleaseOptions{
		Name:      cmpOr(job.Release.Name, ts.Release.Name, "RELEASE-NAME"),
		Namespace: cmpOr(job.Release.Namespace, ts.Release.Namespace, "NAMESPACE"),
		Revision:  cmpOr(job.Release.Revision, ts.Release.Revision),
		IsUpgrade: isUpgrade,
		// releaseV3Option ties IsInstall directly to IsUpgrade; helm-unittest's
		// YAML has no independent way to declare an install.
		IsInstall: !isUpgrade,
	}
}

// capabilities reproduces polishCapabilitiesSettings and capabilitiesV3.
//
// The Copy() is not optional: helm-unittest itself does `capabilities :=
// v3util.DefaultCapabilities` and writes through that package-level pointer,
// which is the race that forced worker subprocesses. We must not repeat it.
//
// This is the one place that does write to the shared *unittest.TestJob, and the
// write is load-bearing rather than incidental. SetCapabilities rebuilds
// job.Capabilities from CapabilitiesFields, which resets APIVersions to exactly
// what the job's own YAML declared — so calling it is what stops the suite-level
// append below from applying a second time to a job RunV3 has already polished.
// It is idempotent for the same reason: the result depends only on
// CapabilitiesFields, which nothing here touches.
func capabilities(ts *unittest.TestSuite, job *unittest.TestJob) *v3util.Capabilities {
	job.SetCapabilities() // fills job.Capabilities from its CapabilitiesFields map,
	// defaulting APIVersions to an empty (non-nil) slice when the job declares no
	// `capabilities` block at all.

	caps := v3util.DefaultCapabilities.Copy()
	major := cmpOr(job.Capabilities.MajorVersion, ts.Capabilities.MajorVersion, caps.KubeVersion.Major)
	minor := cmpOr(job.Capabilities.MinorVersion, ts.Capabilities.MinorVersion, caps.KubeVersion.Minor)
	caps.KubeVersion = v3util.KubeVersion{
		Version: fmt.Sprintf("v%s.%s.0", major, minor),
		Major:   major,
		Minor:   minor,
	}

	// Unlike KubeVersion, capabilitiesV3 has no "keep Helm's built-in list"
	// fallback for APIVersions: it always assigns v3util.VersionSet(job's own
	// merged set), even when that set is empty - wiping out DefaultCapabilities'
	// rich API surface unless a test declares its own. A suite's
	// `capabilities.apiVersions` only reaches the job when the job's own field is
	// non-nil, which SetCapabilities makes true unless the job's YAML explicitly
	// sets `apiVersions: null`. Preserving the chart's default set here would
	// render under a richer API surface than helm-unittest actually tests under.
	apis := job.Capabilities.APIVersions
	if len(ts.Capabilities.APIVersions) > 0 && apis != nil {
		apis = append(slices.Clone(apis), ts.Capabilities.APIVersions...)
	}
	caps.APIVersions = v3util.VersionSet(apis)
	return caps
}

// cmpOr returns the first non-zero argument.
func cmpOr[T comparable](vals ...T) T {
	var zero T
	for _, v := range vals {
		if v != zero {
			return v
		}
	}
	return zero
}
