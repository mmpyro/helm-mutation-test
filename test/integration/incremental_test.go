//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// committedChart returns a copy of the fixture committed to a fresh git
// repository, so a --since HEAD run sees exactly the edits a test then makes.
func committedChart(t *testing.T) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "chart")
	copyTree(t, filepath.Join(repoRoot, fixture), dst)
	for _, args := range [][]string{
		{"init", "-q"}, {"add", "."}, {"commit", "-q", "-m", "base"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dst}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dst
}

// TestSinceScoresTheDiffAndSaysSoEverywhere: a diff-only score must name itself
// in every format, and the JSON must account for every generated mutant.
func TestSinceScoresTheDiffAndSaysSoEverywhere(t *testing.T) {
	chart := committedChart(t)
	f, err := os.OpenFile(filepath.Join(chart, "templates", "service.yaml"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("# touched\n")
	f.Close()

	r := run(t, chart, "-f", "tests/weak_test.yaml", "--since", "HEAD", "--report", allFormats)
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", r.ExitCode, r.Stdout, r.Stderr)
	}
	j := r.JSON(t)
	inc := j.Incremental
	if inc == nil || inc.Base != "HEAD" || inc.MutantsSkipped == 0 || inc.FullRunReason != "" {
		t.Fatalf("incremental block = %+v", inc)
	}
	if len(inc.FilesChanged) != 1 || inc.FilesChanged[0] != "templates/service.yaml" {
		t.Errorf("filesChanged = %v", inc.FilesChanged)
	}
	if got := len(j.Mutants) + j.Capped + inc.MutantsSkipped; got != j.Generated {
		t.Errorf("evaluated + capped + skipped = %d, generated = %d", got, j.Generated)
	}
	for _, m := range j.Mutants {
		if m.File != "templates/service.yaml" {
			t.Fatalf("evaluated a mutant outside the diff: %s", m.File)
		}
	}
	for name, out := range map[string]string{
		"console": r.Console(), "markdown": r.Markdown(t), "html": r.HTML(t),
		"junit": string(r.file(t, "mutation-report.junit.xml")),
	} {
		if !strings.Contains(out, "score covers the diff only") {
			t.Errorf("%s does not say the score covers the diff only", name)
		}
	}
}

// TestSinceWithNothingChangedPassesTheThreshold: a PR that touches no chart
// file must not fail the gate on a 0% that graded nothing.
func TestSinceWithNothingChangedPassesTheThreshold(t *testing.T) {
	r := run(t, committedChart(t), "-f", "tests/weak_test.yaml", "--since", "HEAD",
		"--threshold", "70", "--report", "console,json")
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.ExitCode, r.Stdout, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "nothing in the diff to score; threshold not applied") {
		t.Errorf("console does not explain the pass:\n%s", r.Stdout)
	}
	if j := r.JSON(t); !j.Passed || len(j.Mutants) != 0 || j.Incremental == nil {
		t.Errorf("passed = %v, mutants = %d, incremental = %+v", j.Passed, len(j.Mutants), j.Incremental)
	}
}

// TestSinceWithAnUnknownRefCannotRun: exit 2, and the message says what to do
// about the most likely cause in CI.
func TestSinceWithAnUnknownRefCannotRun(t *testing.T) {
	r := run(t, committedChart(t), "--since", "no-such-ref")
	if r.ExitCode != 2 {
		t.Fatalf("exit = %d, want 2\n%s", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stderr, "fetch-depth") {
		t.Errorf("stderr gives no remedy:\n%s", r.Stderr)
	}
}
