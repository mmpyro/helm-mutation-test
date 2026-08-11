// Package coverage maps chart files to the test suites that exercise them.
//
// helm-unittest suites declare which templates they render, via a suite-level
// `templates:` list or a per-test `template:`. That declaration is free
// information with two uses:
//
//   - Speed. A mutant in ingress.yaml only needs the suites that render
//     ingress.yaml, not the whole suite set.
//   - Honesty. If no suite renders a template at all, its mutants are NoCoverage,
//     a reportable gap in its own right, rather than "survived" — which would
//     blame the tests for something they never had the chance to catch.
//
// The package deliberately depends on nothing else in this module: it works with
// opaque suite IDs so it stays a pure, independently testable algorithm.
package coverage

import (
	"path"
	"slices"
	"strings"
)

// SuiteRef is the minimum a suite must expose to be indexed.
type SuiteRef struct {
	// ID is opaque to this package; callers map it back to their own suite type.
	ID string
	// Templates is the suite-level `templates:` declaration.
	Templates []string
	// JobTemplates is the union of per-test `template:` overrides.
	JobTemplates []string
}

// coversAllTemplates reports whether a suite declares no filter and therefore
// renders whatever the chart produces.
func (r SuiteRef) coversAllTemplates() bool {
	return len(r.Templates) == 0 && len(r.JobTemplates) == 0
}

// Index answers "which suites exercise this file?".
type Index struct {
	all []string
	// global holds suites that declare no template filter.
	global []string
	// byTemplate maps a normalised template path to the suites naming it.
	byTemplate map[string][]string
}

// Build constructs the index from the discovered suites.
func Build(refs []SuiteRef) *Index {
	idx := &Index{byTemplate: map[string][]string{}}
	for _, r := range refs {
		idx.all = append(idx.all, r.ID)
		if r.coversAllTemplates() {
			idx.global = append(idx.global, r.ID)
			continue
		}
		for _, tpl := range declaredTemplates(r) {
			key := normalise(tpl)
			idx.byTemplate[key] = append(idx.byTemplate[key], r.ID)
		}
	}
	return idx
}

func declaredTemplates(r SuiteRef) []string {
	all := slices.Clone(r.Templates)
	all = append(all, r.JobTemplates...)
	slices.Sort(all)
	return slices.Compact(all)
}

// normalise reduces a declared template reference to a chart-relative path with
// forward slashes. Suites may write "deployment.yaml" or "templates/deployment.yaml"
// for the same file, and subchart suites use "charts/sub/templates/x.yaml".
func normalise(p string) string {
	p = strings.TrimPrefix(path.Clean(strings.ReplaceAll(p, "\\", "/")), "./")
	return strings.TrimPrefix(p, "templates/")
}

// For returns the IDs of the suites that exercise a chart-relative file path.
//
// Two file kinds cover everything, conservatively:
//
//   - values.yaml, because any template may read any value.
//   - a partials file (_helpers.tpl and friends), because any template may
//     include any define in it.
//
// Being conservative here costs runtime but never misattributes a survivor.
func (idx *Index) For(file string) []string {
	file = strings.TrimPrefix(strings.ReplaceAll(file, "\\", "/"), "./")
	if coversEverything(file) {
		return dedupe(idx.all)
	}
	out := slices.Clone(idx.global)
	out = append(out, idx.byTemplate[normalise(file)]...)
	return dedupe(out)
}

// coversEverything reports whether a change to this file could affect any
// rendered template.
func coversEverything(file string) bool {
	base := path.Base(file)
	if base == "values.yaml" || base == "values.yml" {
		return true
	}
	// Helm treats files starting with "_" as partials: they define named templates
	// rather than producing manifests of their own.
	return strings.HasPrefix(base, "_")
}

// ExercisedTemplates lists every template path any suite declares, normalised.
func (idx *Index) ExercisedTemplates() []string {
	out := make([]string, 0, len(idx.byTemplate))
	for k := range idx.byTemplate {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// HasGlobalSuites reports whether any suite renders every template. When true,
// no template can be NoCoverage.
func (idx *Index) HasGlobalSuites() bool { return len(idx.global) > 0 }

// dedupe removes duplicate IDs while keeping a stable, sorted order.
func dedupe(ids []string) []string {
	out := slices.Clone(ids)
	slices.Sort(out)
	return slices.Compact(out)
}
