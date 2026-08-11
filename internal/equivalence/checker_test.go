package equivalence

import (
	"strings"
	"testing"

	"github.com/mmpyro/helm-mutation-test/internal/source"
)

// judgeFixture builds a one-template chart plus the source.File map the checker
// needs, and returns a checker over both.
func judgeFixture(t *testing.T, templateSrc string, values map[string]any) (*Checker, *source.File) {
	t.Helper()
	c := testChart(values, map[string]string{"templates/a.yaml": templateSrc})
	f := source.New([]byte(templateSrc), "a.yaml", "templates/a.yaml", source.KindTemplate)
	return NewChecker(c, map[string]*source.File{"templates/a.yaml": f}), f
}

func mutationAt(t *testing.T, f *source.File, needle, replacement string) Mutation {
	t.Helper()
	start := strings.Index(f.Text(), needle)
	if start < 0 {
		t.Fatalf("needle %q not in source", needle)
	}
	return Mutation{File: f.Path, Start: start, End: start + len(needle), Replacement: replacement}
}

func TestJudgeCallsAReindentationEquivalent(t *testing.T) {
	// The canonical equivalent: YAML does not care how deep a block mapping is
	// indented, only that it is deeper than its parent.
	src := "spec:\n  {{ .Values.block | nindent 2 }}\n"
	c, f := judgeFixture(t, src, map[string]any{"block": "a: 1"})
	v := c.Judge(mutationAt(t, f, "nindent 2", "nindent 3"), []RenderContext{{Name: "ctx"}})
	if !v.Equivalent {
		t.Fatalf("want equivalent, got Survived: %s", v.Detail)
	}
}

func TestJudgeKeepsAnObservableMutationSurvived(t *testing.T) {
	src := "replicas: {{ .Values.replicas }}\n"
	c, f := judgeFixture(t, src, map[string]any{"replicas": 3})
	v := c.Judge(mutationAt(t, f, ".Values.replicas", "99"), []RenderContext{{Name: "ctx"}})
	if v.Equivalent {
		t.Fatal("a mutation that changes the output must not be called equivalent")
	}
}

func TestJudgeKeepsAnUnexercisedBranchSurvived(t *testing.T) {
	// Identical renders, because no context enables the branch. That is a missing
	// test, not an unkillable mutant, so it must stay in the denominator.
	src := "{{- if .Values.enabled }}\nreplicas: {{ .Values.replicas }}\n{{- end }}\n"
	c, f := judgeFixture(t, src, map[string]any{"enabled": false, "replicas": 3})
	v := c.Judge(mutationAt(t, f, ".Values.replicas", "99"), []RenderContext{{Name: "ctx"}})
	if v.Equivalent {
		t.Fatal("a mutation in an unreached branch must not be called equivalent")
	}
	if !strings.Contains(v.Detail, "not exercised") {
		t.Fatalf("detail should say why: %q", v.Detail)
	}
}

func TestJudgeRequiresEveryContextToAgree(t *testing.T) {
	// Equivalent under the default context, observable under the second. One
	// dissenting context is enough to keep the mutant survived.
	src := "{{- if .Values.enabled }}\nreplicas: {{ .Values.replicas }}\n{{- end }}\n"
	c, f := judgeFixture(t, src, map[string]any{"enabled": false, "replicas": 3})
	ctxs := []RenderContext{
		{Name: "defaults"},
		{Name: "enabled", Values: map[string]any{"enabled": true}},
	}
	v := c.Judge(mutationAt(t, f, ".Values.replicas", "99"), ctxs)
	if v.Equivalent {
		t.Fatal("a context that reveals the difference must veto equivalence")
	}
}

func TestJudgeIsInconclusiveWithoutContexts(t *testing.T) {
	src := "replicas: {{ .Values.replicas }}\n"
	c, f := judgeFixture(t, src, map[string]any{"replicas": 3})
	v := c.Judge(mutationAt(t, f, ".Values.replicas", "99"), nil)
	if v.Equivalent {
		t.Fatal("no contexts means no evidence, which must not mean equivalent")
	}
}
