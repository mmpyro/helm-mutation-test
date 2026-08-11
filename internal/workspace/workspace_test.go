package workspace

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func chartFixture(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("../../testdata/charts/sample")
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestNewCopiesTheChart(t *testing.T) {
	ws, err := New(chartFixture(t), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close() })

	for _, rel := range []string{
		"Chart.yaml", "values.yaml",
		"templates/deployment.yaml", "templates/_helpers.tpl",
		"tests/weak_test.yaml",
	} {
		if _, err := os.Stat(ws.Path(rel)); err != nil {
			t.Errorf("%s missing from the workspace: %v", rel, err)
		}
	}
}

func TestCloseRemovesTheWorkspace(t *testing.T) {
	ws, err := New(chartFixture(t), false)
	if err != nil {
		t.Fatal(err)
	}
	root := ws.Root
	if err := ws.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("workspace still present after Close: %v", err)
	}
}

func TestKeepPreservesTheWorkspace(t *testing.T) {
	ws, err := New(chartFixture(t), true)
	if err != nil {
		t.Fatal(err)
	}
	root := ws.Root
	if err := ws.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Errorf("--keep-workdir should preserve the workspace: %v", err)
	}
	os.RemoveAll(filepath.Dir(root))
}

func TestMutatingTheWorkspaceLeavesTheSourceChartUntouched(t *testing.T) {
	src := chartFixture(t)
	before, err := os.ReadFile(filepath.Join(src, "templates", "deployment.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	ws, err := New(src, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close() })

	if _, err := ws.WriteMutation("templates/deployment.yaml", []byte("mutated!")); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(filepath.Join(src, "templates", "deployment.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("the user's chart was modified; mutations must stay inside the workspace")
	}
}

// TestRestoreUndoesTheMutation guards the invariant that makes one workspace
// reusable across many mutants: a leaked mutation would contaminate every
// subsequent evaluation by the same worker.
func TestRestoreUndoesTheMutation(t *testing.T) {
	ws, err := New(chartFixture(t), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close() })

	const rel = "templates/deployment.yaml"
	original, err := os.ReadFile(ws.Path(rel))
	if err != nil {
		t.Fatal(err)
	}

	restore, err := ws.WriteMutation(rel, []byte("mutated!"))
	if err != nil {
		t.Fatal(err)
	}
	mutated, _ := os.ReadFile(ws.Path(rel))
	if string(mutated) != "mutated!" {
		t.Fatalf("mutation not written, got %q", mutated)
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	restored, _ := os.ReadFile(ws.Path(rel))
	if string(restored) != string(original) {
		t.Error("restore did not return the file to its original bytes")
	}
}

func TestWriteMutationRejectsAMissingFile(t *testing.T) {
	ws, err := New(chartFixture(t), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close() })

	if _, err := ws.WriteMutation("templates/nope.yaml", []byte("x")); err == nil {
		t.Fatal("expected an error for a file that is not in the workspace")
	}
}

func TestWorkspacesAreMutuallyIsolated(t *testing.T) {
	src := chartFixture(t)
	a, err := New(src, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	b, err := New(src, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })

	if a.Root == b.Root {
		t.Fatal("two workspaces share a root")
	}
	if _, err := a.WriteMutation("values.yaml", []byte("mutated: true\n")); err != nil {
		t.Fatal(err)
	}
	bContent, err := os.ReadFile(b.Path("values.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(bContent) == "mutated: true\n" {
		t.Error("a mutation in one workspace leaked into another")
	}
}

func TestDiscover(t *testing.T) {
	d, err := Discover(chartFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if d.Values != "values.yaml" {
		t.Errorf("Values = %q, want values.yaml", d.Values)
	}
	for _, want := range []string{
		"templates/_helpers.tpl",
		"templates/deployment.yaml",
		"templates/ingress.yaml",
		"templates/service.yaml",
	} {
		if !slices.Contains(d.Templates, want) {
			t.Errorf("%s missing from discovery, got %v", want, d.Templates)
		}
	}
	// Test files are not chart output and must never be mutated: mutating the
	// tests would measure nothing about the chart.
	for _, got := range d.Templates {
		if filepath.Base(filepath.Dir(got)) == "tests" {
			t.Errorf("a test file was offered for mutation: %s", got)
		}
	}
}

func TestDiscoverSkipsSnapshotsAndNonTemplates(t *testing.T) {
	dir := t.TempDir()
	mkdir := func(p string) {
		if err := os.MkdirAll(filepath.Join(dir, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p string) {
		if err := os.WriteFile(filepath.Join(dir, p), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mkdir("templates/__snapshot__")
	write("templates/deployment.yaml")
	write("templates/NOTES.txt")
	write("templates/_helpers.tpl")
	write("templates/README.md")
	write("templates/__snapshot__/deployment_test.yaml.snap")

	d, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"templates/README.md", "templates/__snapshot__/deployment_test.yaml.snap"} {
		if slices.Contains(d.Templates, unwanted) {
			t.Errorf("%s should not be mutable, got %v", unwanted, d.Templates)
		}
	}
	for _, wanted := range []string{"templates/deployment.yaml", "templates/NOTES.txt", "templates/_helpers.tpl"} {
		if !slices.Contains(d.Templates, wanted) {
			t.Errorf("%s should be mutable, got %v", wanted, d.Templates)
		}
	}
}

func TestDiscoverHandlesAChartWithoutTemplates(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "values.yaml"), []byte("a: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := Discover(dir)
	if err != nil {
		t.Fatalf("a chart with no templates dir is not an error: %v", err)
	}
	if len(d.Templates) != 0 {
		t.Errorf("got %v, want none", d.Templates)
	}
	if d.Values != "values.yaml" {
		t.Errorf("Values = %q", d.Values)
	}
}
