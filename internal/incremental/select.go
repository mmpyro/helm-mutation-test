// Package incremental narrows a run to the mutants a change could have affected.
//
// The narrowing is only honest if every skipped mutant provably has nothing new
// to say. Two kinds of change are attributable precisely:
//
//   - a non-partial template: only that file's mutants can have changed;
//   - a test suite: only mutants in files that suite covers can be graded
//     differently.
//
// Everything else — values.yaml, a partial, Chart.yaml, a subchart, a file the
// templates may read through .Files.Get — can change any rendered manifest, so it
// forces a full run. An unrecognised path is treated the same way: guessing that
// it is harmless would let the score flatter itself.
//
// Like internal/coverage, the package depends on nothing else in this module.
package incremental

import (
	"path"
	"slices"
	"strings"
)

// Selection is the outcome of classifying a change set.
type Selection struct {
	// Full means the change cannot be attributed to particular files, so every
	// mutant must run. Reason names the path that forced it.
	Full   bool
	Reason string
	// Templates are changed chart-relative template paths whose own mutants run.
	Templates []string
	// Suites are changed suite files; mutants in every file they cover run.
	Suites []string
}

// Classify decides which mutants a set of changed chart-relative paths can
// affect. suiteFiles are the chart-relative files of the suites the baseline
// discovered; a changed path under tests/ that is not one of them (a deleted
// suite, a values file a suite reads) forces a full run, because which suites
// depend on it is unknown.
func Classify(changed, suiteFiles []string) Selection {
	var sel Selection
	for _, p := range changed {
		p = clean(p)
		switch {
		case slices.Contains(suiteFiles, p):
			sel.Suites = append(sel.Suites, p)
		case isSnapshot(p) && slices.Contains(suiteFiles, snapshotSuite(p)):
			sel.Suites = append(sel.Suites, snapshotSuite(p))
		case isTemplate(p):
			sel.Templates = append(sel.Templates, p)
		default:
			return Selection{Full: true, Reason: fullRunReason(p)}
		}
	}
	sel.Templates = sortedUnique(sel.Templates)
	sel.Suites = sortedUnique(sel.Suites)
	return sel
}

// isTemplate reports whether p is a manifest-producing template of this chart.
// Partials ("_" prefix) are excluded: any template may include them.
func isTemplate(p string) bool {
	return strings.HasPrefix(p, "templates/") && !strings.HasPrefix(path.Base(p), "_")
}

// isSnapshot reports whether p is a helm-unittest snapshot file. A changed
// snapshot changes what its suite asserts, exactly like editing the suite.
func isSnapshot(p string) bool {
	return path.Base(path.Dir(p)) == "__snapshot__" && strings.HasSuffix(p, ".snap")
}

// snapshotSuite maps tests/__snapshot__/x_test.yaml.snap to tests/x_test.yaml.
func snapshotSuite(p string) string {
	dir := path.Dir(path.Dir(p))
	return path.Join(dir, strings.TrimSuffix(path.Base(p), ".snap"))
}

func fullRunReason(p string) string {
	base := path.Base(p)
	switch {
	case base == "values.yaml" || base == "values.yml":
		return p + " changed, and any template may read any value"
	case strings.HasPrefix(p, "templates/") && strings.HasPrefix(base, "_"):
		return p + " changed, and any template may include a partial"
	default:
		return p + " changed, and its effect on rendering cannot be attributed to particular templates"
	}
}

func clean(p string) string {
	return strings.TrimPrefix(path.Clean(strings.ReplaceAll(p, "\\", "/")), "./")
}

func sortedUnique(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}
