package coverage

import "testing"

func suite(name string, templates ...string) SuiteRef {
	return SuiteRef{ID: name, Templates: templates}
}

func jobSuite(name string, jobTemplates ...string) SuiteRef {
	return SuiteRef{ID: name, JobTemplates: jobTemplates}
}

// names is the identity for this package's opaque IDs; kept so the assertions
// below read the same as the suites they describe.
func names(ids []string) []string { return ids }

func eq(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestForMatchesDeclaredTemplates(t *testing.T) {
	idx := Build([]SuiteRef{
		suite("dep", "deployment.yaml"),
		suite("svc", "service.yaml"),
	})
	eq(t, names(idx.For("templates/deployment.yaml")), "dep")
	eq(t, names(idx.For("templates/service.yaml")), "svc")
}

func TestForNormalisesTemplatePrefix(t *testing.T) {
	// A suite may name the template with or without the templates/ prefix.
	idx := Build([]SuiteRef{
		suite("bare", "deployment.yaml"),
		suite("prefixed", "templates/deployment.yaml"),
	})
	got := names(idx.For("templates/deployment.yaml"))
	if len(got) != 2 {
		t.Fatalf("both spellings should match the same file, got %v", got)
	}
}

func TestForReturnsNothingForAnUnexercisedTemplate(t *testing.T) {
	// This is what makes NoCoverage a real, reportable finding.
	idx := Build([]SuiteRef{suite("dep", "deployment.yaml")})
	if got := idx.For("templates/ingress.yaml"); len(got) != 0 {
		t.Fatalf("ingress.yaml is exercised by no suite, got %v", names(got))
	}
}

func TestSuitesWithNoFilterCoverEverything(t *testing.T) {
	idx := Build([]SuiteRef{
		suite("all"), // no templates declared
		suite("dep", "deployment.yaml"),
	})
	if !idx.HasGlobalSuites() {
		t.Error("a suite with no template filter is global")
	}
	// A global suite must appear for any template, exercised or not.
	if got := names(idx.For("templates/ingress.yaml")); len(got) != 1 || got[0] != "all" {
		t.Errorf("got %v, want [all]", got)
	}
	got := names(idx.For("templates/deployment.yaml"))
	if len(got) != 2 {
		t.Errorf("got %v, want both the global and the specific suite", got)
	}
}

func TestPerJobTemplatesCount(t *testing.T) {
	// A suite may declare templates only on individual test jobs.
	idx := Build([]SuiteRef{jobSuite("jobs", "ingress.yaml")})
	eq(t, names(idx.For("templates/ingress.yaml")), "jobs")
	if got := idx.For("templates/deployment.yaml"); len(got) != 0 {
		t.Errorf("got %v, want none", names(got))
	}
}

// TestValuesFileCoversEveryone is deliberately conservative: any template may
// read any value, so a values.yaml mutation must be checked against all suites.
func TestValuesFileCoversEveryone(t *testing.T) {
	idx := Build([]SuiteRef{
		suite("dep", "deployment.yaml"),
		suite("svc", "service.yaml"),
	})
	for _, f := range []string{"values.yaml", "values.yml", "./values.yaml"} {
		if got := idx.For(f); len(got) != 2 {
			t.Errorf("%s should cover every suite, got %v", f, names(got))
		}
	}
}

// TestPartialsCoverEveryone: any template may include any define in a partial.
func TestPartialsCoverEveryone(t *testing.T) {
	idx := Build([]SuiteRef{
		suite("dep", "deployment.yaml"),
		suite("svc", "service.yaml"),
	})
	for _, f := range []string{"templates/_helpers.tpl", "templates/_labels.tpl"} {
		if got := idx.For(f); len(got) != 2 {
			t.Errorf("%s should cover every suite, got %v", f, names(got))
		}
	}
}

func TestForDeduplicates(t *testing.T) {
	// One suite naming the same template twice must still appear once.
	idx := Build([]SuiteRef{
		{ID: "dup", Templates: []string{"deployment.yaml", "templates/deployment.yaml"}},
	})
	if got := idx.For("templates/deployment.yaml"); len(got) != 1 {
		t.Fatalf("got %v, want one entry", got)
	}
}

func TestExercisedTemplatesIsSorted(t *testing.T) {
	idx := Build([]SuiteRef{
		suite("z", "service.yaml"),
		suite("a", "deployment.yaml", "ingress.yaml"),
	})
	eq(t, idx.ExercisedTemplates(), "deployment.yaml", "ingress.yaml", "service.yaml")
}

func TestNormalise(t *testing.T) {
	tests := []struct{ in, want string }{
		{"deployment.yaml", "deployment.yaml"},
		{"templates/deployment.yaml", "deployment.yaml"},
		{"./templates/deployment.yaml", "deployment.yaml"},
		{"charts/sub/templates/x.yaml", "charts/sub/templates/x.yaml"},
	}
	for _, tc := range tests {
		if got := normalise(tc.in); got != tc.want {
			t.Errorf("normalise(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestForReturnsSortedIDs(t *testing.T) {
	// Deterministic order keeps mutant runs and reports reproducible.
	idx := Build([]SuiteRef{suite("z", "x.yaml"), suite("a", "x.yaml"), suite("m", "x.yaml")})
	eq(t, idx.For("templates/x.yaml"), "a", "m", "z")
}
