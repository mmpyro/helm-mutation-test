package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmpyro/helm-mutation-test/internal/config"
	"github.com/mmpyro/helm-mutation-test/internal/model"
)

func gitIn(t *testing.T, dir string, args ...string) {
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

// sinceCfg is the weak-suite session config over a fixture copy committed to a
// fresh repository, so each test can edit the working tree and run --since HEAD.
func sinceCfg(t *testing.T) config.Config {
	t.Helper()
	cfg := sessionCfg(t, "tests/weak_test.yaml")
	gitIn(t, cfg.ChartPath, "init", "-q")
	gitIn(t, cfg.ChartPath, "add", ".")
	gitIn(t, cfg.ChartPath, "commit", "-q", "-m", "base")
	cfg.Since = "HEAD"
	return cfg
}

func appendTo(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

func filesOf(run *model.Run) map[string]bool {
	out := map[string]bool{}
	for _, m := range run.Mutants {
		out[m.File] = true
	}
	return out
}

// The accounting must close: every generated mutant is either evaluated,
// dropped by the cap, or named as outside the diff. A gap here is a silently
// filtered run, which reads as full coverage.
func assertAccountingCloses(t *testing.T, run *model.Run) {
	t.Helper()
	if got := len(run.Mutants) + run.Capped + run.Incremental.MutantsSkipped; got != run.Generated {
		t.Fatalf("evaluated %d + capped %d + diff-skipped %d = %d, want generated %d",
			len(run.Mutants), run.Capped, run.Incremental.MutantsSkipped, got, run.Generated)
	}
}

func TestSinceScoresOnlyTheChangedTemplate(t *testing.T) {
	cfg := sinceCfg(t)
	appendTo(t, filepath.Join(cfg.ChartPath, "templates", "service.yaml"), "# touched\n")

	run := runSession(t, cfg)
	inc := run.Incremental
	if inc == nil || inc.FullRunReason != "" {
		t.Fatalf("want a narrowed run, got %+v", inc)
	}
	files := filesOf(run)
	if len(files) != 1 || !files["templates/service.yaml"] {
		t.Fatalf("evaluated mutants from %v, want only templates/service.yaml", files)
	}
	if inc.FilesInspected != 1 || inc.FilesSkipped == 0 || inc.MutantsSkipped == 0 {
		t.Fatalf("incremental counts %+v", inc)
	}
	assertAccountingCloses(t, run)
}

// A stronger or weaker suite regrades every mutant it renders, so editing a
// suite re-scores exactly the files it covers — values.yaml included, since
// every suite reads it — and nothing it does not.
func TestSinceRescoresEverythingAChangedSuiteCovers(t *testing.T) {
	cfg := sinceCfg(t)
	appendTo(t, filepath.Join(cfg.ChartPath, "tests", "weak_test.yaml"), "# touched\n")

	run := runSession(t, cfg)
	if run.Incremental == nil || run.Incremental.FullRunReason != "" {
		t.Fatalf("want a narrowed run, got %+v", run.Incremental)
	}
	files := filesOf(run)
	for _, want := range []string{"templates/deployment.yaml", "templates/service.yaml", "values.yaml"} {
		if !files[want] {
			t.Errorf("a weak-suite edit did not re-score %s (got %v)", want, files)
		}
	}
	if files["templates/ingress.yaml"] {
		t.Errorf("re-scored ingress.yaml, which the weak suite does not render (got %v)", files)
	}
	if run.Incremental.FilesSkipped == 0 {
		t.Error("no file was skipped; the suite's coverage did not narrow anything")
	}
	assertAccountingCloses(t, run)
}

func TestSinceFallsBackToAFullRunWhenValuesChange(t *testing.T) {
	cfg := sinceCfg(t)
	cfg.MaxMutants = 5 // the selection is what is under test, not the scores
	appendTo(t, filepath.Join(cfg.ChartPath, "values.yaml"), "# touched\n")

	run := runSession(t, cfg)
	inc := run.Incremental
	if inc == nil || !strings.Contains(inc.FullRunReason, "values.yaml") {
		t.Fatalf("want a full run naming values.yaml, got %+v", inc)
	}
	if inc.MutantsSkipped != 0 || run.Considered() != run.Generated {
		t.Fatalf("a full run skipped mutants: %+v", inc)
	}
	assertAccountingCloses(t, run)
}

func TestSinceWithACleanTreeScoresNothingAndPasses(t *testing.T) {
	cfg := sinceCfg(t)
	cfg.Threshold = 70

	run := runSession(t, cfg)
	if len(run.Mutants) != 0 || run.Incremental.MutantsSkipped != run.Generated || run.Generated == 0 {
		t.Fatalf("evaluated %d, skipped %d of %d", len(run.Mutants), run.Incremental.MutantsSkipped, run.Generated)
	}
	if !run.NothingToScore() || !run.MeetsThreshold() {
		t.Fatal("an empty diff must pass the threshold")
	}
}

// A bad ref must stop the run before the baseline spends any time, and must not
// degrade into an empty diff that scores nothing and passes.
func TestSinceRejectsAnUnknownRef(t *testing.T) {
	cfg := sinceCfg(t)
	cfg.Since = "no-such-ref"
	if _, err := (&Session{Cfg: cfg}).Run(t.Context()); err == nil {
		t.Fatal("an unknown ref was accepted")
	}
}
