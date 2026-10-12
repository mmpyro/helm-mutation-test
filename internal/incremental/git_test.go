package incremental

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// repo builds a repository whose chart lives in a subdirectory, so the test
// also proves paths come back chart-relative and files outside the chart are
// dropped.
func repo(t *testing.T) (root, chart string) {
	t.Helper()
	root = t.TempDir()
	chart = filepath.Join(root, "charts", "app")
	run(t, root, "init", "-q", "-b", "main")
	write(t, filepath.Join(root, "README.md"), "x\n")
	write(t, filepath.Join(chart, "Chart.yaml"), "name: app\n")
	write(t, filepath.Join(chart, "templates", "a.yaml"), "a: 1\n")
	write(t, filepath.Join(chart, "templates", "b.yaml"), "b: 1\n")
	run(t, root, "add", ".")
	run(t, root, "commit", "-q", "-m", "base")
	return root, chart
}

func TestChangedFilesCoversCommittedUncommittedAndUntracked(t *testing.T) {
	root, chart := repo(t)
	run(t, root, "checkout", "-q", "-b", "feature")
	write(t, filepath.Join(chart, "templates", "a.yaml"), "a: 2\n")
	run(t, root, "commit", "-q", "-am", "committed edit")
	write(t, filepath.Join(chart, "templates", "b.yaml"), "b: 2\n") // uncommitted
	write(t, filepath.Join(chart, "templates", "c.yaml"), "c: 1\n") // untracked
	write(t, filepath.Join(root, "README.md"), "y\n")               // outside the chart

	got, err := ChangedFiles(chart, "main")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"templates/a.yaml", "templates/b.yaml", "templates/c.yaml"}
	if !slices.Equal(got.Files, want) {
		t.Fatalf("got %v, want %v", got.Files, want)
	}
	if got.BaseCommit == "" {
		t.Fatal("no base commit recorded")
	}
}

// A rename must surface the old name too: a renamed-away suite is a deleted
// suite, which the classifier needs to see.
func TestChangedFilesReportsBothSidesOfARename(t *testing.T) {
	root, chart := repo(t)
	run(t, root, "mv", filepath.Join(chart, "templates", "a.yaml"), filepath.Join(chart, "templates", "z.yaml"))
	got, err := ChangedFiles(chart, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"templates/a.yaml", "templates/z.yaml"}
	if !slices.Equal(got.Files, want) {
		t.Fatalf("got %v, want %v", got.Files, want)
	}
}

func TestChangedFilesIsEmptyForACleanTree(t *testing.T) {
	_, chart := repo(t)
	got, err := ChangedFiles(chart, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 0 {
		t.Fatalf("got %v", got.Files)
	}
}

// An unknown ref must fail loudly and say what to do, not fall back to an
// empty diff, which would score nothing and pass.
func TestChangedFilesRejectsAnUnknownRef(t *testing.T) {
	_, chart := repo(t)
	_, err := ChangedFiles(chart, "no-such-ref")
	if err == nil || !strings.Contains(err.Error(), "fetch-depth") {
		t.Fatalf("want a merge-base error with a remedy, got %v", err)
	}
}
