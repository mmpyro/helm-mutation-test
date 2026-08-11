package equivalence

import (
	"strings"
	"testing"
)

func TestWithMutatedFileReplacesATemplate(t *testing.T) {
	c := testChart(nil, map[string]string{"templates/a.yaml": "a: original\n"})
	mutated, err := WithMutatedFile(c, "templates/a.yaml", []byte("a: mutated\n"))
	if err != nil {
		t.Fatalf("WithMutatedFile: %v", err)
	}
	out, err := Render(mutated, RenderContext{Name: "ctx"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out["probe/templates/a.yaml"], "a: mutated") {
		t.Fatalf("template was not replaced: %q", out["probe/templates/a.yaml"])
	}
}

func TestWithMutatedFileLeavesTheOriginalChartIntact(t *testing.T) {
	// One loaded chart serves every mutant, so a mutation must never be visible
	// through the base chart or the next mutant inherits it.
	c := testChart(nil, map[string]string{"templates/a.yaml": "a: original\n"})
	if _, err := WithMutatedFile(c, "templates/a.yaml", []byte("a: mutated\n")); err != nil {
		t.Fatalf("WithMutatedFile: %v", err)
	}
	if got := string(c.Templates[0].Data); got != "a: original\n" {
		t.Fatalf("base chart was modified: %q", got)
	}
}

func TestWithMutatedFileReparsesValuesYAML(t *testing.T) {
	c := testChart(map[string]any{"replicas": 1}, map[string]string{
		"templates/a.yaml": "replicas: {{ .Values.replicas }}\n",
	})
	mutated, err := WithMutatedFile(c, "values.yaml", []byte("replicas: 4\n"))
	if err != nil {
		t.Fatalf("WithMutatedFile: %v", err)
	}
	out, err := Render(mutated, RenderContext{Name: "ctx"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out["probe/templates/a.yaml"], "replicas: 4") {
		t.Fatalf("mutated values.yaml did not reach the render: %q", out["probe/templates/a.yaml"])
	}
}

func TestWithMutatedFileRejectsAnUnknownPath(t *testing.T) {
	// Silently rendering the unmutated chart would compare it against itself and
	// declare every such mutant equivalent.
	c := testChart(nil, map[string]string{"templates/a.yaml": "a: 1\n"})
	if _, err := WithMutatedFile(c, "templates/nope.yaml", []byte("x: 1\n")); err == nil {
		t.Fatal("want an error for a path the chart does not contain")
	}
}
