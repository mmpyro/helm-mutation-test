package incremental

import (
	"slices"
	"strings"
	"testing"
)

var suites = []string{"tests/weak_test.yaml", "tests/strong_test.yaml"}

func TestChangedTemplateSelectsOnlyThatTemplate(t *testing.T) {
	sel := Classify([]string{"templates/service.yaml"}, suites)
	if sel.Full {
		t.Fatalf("a plain template change forced a full run: %s", sel.Reason)
	}
	if !slices.Equal(sel.Templates, []string{"templates/service.yaml"}) || len(sel.Suites) != 0 {
		t.Fatalf("got templates %v suites %v", sel.Templates, sel.Suites)
	}
}

func TestChangedSuiteSelectsTheSuite(t *testing.T) {
	sel := Classify([]string{"tests/weak_test.yaml", "./tests/weak_test.yaml"}, suites)
	if sel.Full || !slices.Equal(sel.Suites, []string{"tests/weak_test.yaml"}) {
		t.Fatalf("got %+v", sel)
	}
}

// A snapshot is part of what its suite asserts; regenerating one alongside a
// template edit is routine and must not force a full run.
func TestChangedSnapshotSelectsItsSuite(t *testing.T) {
	sel := Classify([]string{"tests/__snapshot__/strong_test.yaml.snap"}, suites)
	if sel.Full || !slices.Equal(sel.Suites, []string{"tests/strong_test.yaml"}) {
		t.Fatalf("got %+v", sel)
	}
}

// Each of these can change any rendered manifest, so attributing the change to
// particular files would skip mutants whose verdict may have moved.
func TestChangesThatCannotBeAttributedForceAFullRun(t *testing.T) {
	for _, p := range []string{
		"values.yaml",
		"templates/_helpers.tpl",
		"Chart.yaml",
		"Chart.lock",
		"values.schema.json",
		"charts/sub/templates/x.yaml",
		".helmignore",
		"files/config.ini",
		"tests/deleted_test.yaml",           // not a baseline suite: deleted or unmatched by -f
		"tests/values/override.yaml",        // read by some suite, which one is unknown
		"tests/__snapshot__/gone.yaml.snap", // snapshot of no known suite
	} {
		sel := Classify([]string{"templates/service.yaml", p}, suites)
		if !sel.Full {
			t.Errorf("%s: want a full run, got %+v", p, sel)
			continue
		}
		if !strings.Contains(sel.Reason, p) {
			t.Errorf("%s: reason %q does not name the path", p, sel.Reason)
		}
	}
}

func TestNoChangesSelectsNothing(t *testing.T) {
	sel := Classify(nil, suites)
	if sel.Full || len(sel.Templates) != 0 || len(sel.Suites) != 0 {
		t.Fatalf("got %+v", sel)
	}
}
