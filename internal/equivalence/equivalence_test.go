package equivalence

import (
	"strings"
	"testing"

	"helm.sh/helm/v3/pkg/chart"
)

// testChart builds a minimal in-memory chart. Templates are given as
// name -> body, where name is chart-relative ("templates/x.yaml").
func testChart(values map[string]any, templates map[string]string) *chart.Chart {
	c := &chart.Chart{
		Metadata: &chart.Metadata{APIVersion: "v2", Name: "probe", Version: "0.1.0"},
		Values:   values,
	}
	for name, body := range templates {
		c.Templates = append(c.Templates, &chart.File{Name: name, Data: []byte(body)})
	}
	return c
}

func TestRenderUsesTheContextValues(t *testing.T) {
	c := testChart(map[string]any{"replicas": 1}, map[string]string{
		"templates/a.yaml": "replicas: {{ .Values.replicas }}\n",
	})
	out, err := Render(c, RenderContext{Name: "ctx", Values: map[string]any{"replicas": 7}})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := out["probe/templates/a.yaml"]
	if !strings.Contains(got, "replicas: 7") {
		t.Fatalf("context values did not reach the render: %q", got)
	}
}

func TestCompareIgnoresIndentationButNotData(t *testing.T) {
	// Indentation depth carries no meaning in a YAML block mapping, which is why
	// `nindent N -> N+1` is unkillable. Byte comparison alone would miss that.
	a := map[string]string{"x": "spec:\n  a: 1\n"}
	b := map[string]string{"x": "spec:\n    a: 1\n"}
	equal, err := Compare(a, b)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if !equal {
		t.Fatal("re-indented but structurally identical documents must compare equal")
	}

	c := map[string]string{"x": "spec:\n  a: 2\n"}
	equal, err = Compare(a, c)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if equal {
		t.Fatal("a changed value must not compare equal")
	}
}

func TestCompareIsSensitiveToDocumentOrder(t *testing.T) {
	// documentIndex assertions can observe order, so a reordering is observable.
	a := map[string]string{"x": "a: 1\n---\nb: 2\n"}
	b := map[string]string{"x": "b: 2\n---\na: 1\n"}
	equal, err := Compare(a, b)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if equal {
		t.Fatal("reordered documents must not compare equal")
	}
}

func TestCompareTreatsAMissingTemplateAsADifference(t *testing.T) {
	a := map[string]string{"x": "a: 1\n", "y": "b: 2\n"}
	b := map[string]string{"x": "a: 1\n"}
	equal, err := Compare(a, b)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if equal {
		t.Fatal("a template that stopped rendering must not compare equal")
	}
}
