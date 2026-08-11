package runner

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/mmpyro/helm-mutation-test/internal/config"
	"github.com/mmpyro/helm-mutation-test/internal/coverage"
	"github.com/mmpyro/helm-mutation-test/internal/model"
	"github.com/mmpyro/helm-mutation-test/internal/mutator"
	"github.com/mmpyro/helm-mutation-test/internal/source"
	"github.com/mmpyro/helm-mutation-test/internal/workspace"
)

// Session runs a full mutation testing pass: baseline, generate, execute.
type Session struct {
	Cfg config.Config
	// OnPhase reports progress to the caller. Optional.
	OnPhase func(string)
	// OnProgress reports each completed mutant. Optional.
	OnProgress func(done, total int, m model.Mutant)
}

// Run executes the session and returns the completed model.
//
// A *BaselineFailure means the chart's own tests do not pass, in which case no
// mutants are generated at all: a score measured against a red suite is a fiction.
func (s *Session) Run(ctx context.Context) (*model.Run, error) {
	start := time.Now()
	opts := Options{
		TestFiles:      s.Cfg.TestFiles,
		ValuesFiles:    s.Cfg.ValuesFiles,
		Strict:         s.Cfg.Strict,
		WithSubChart:   s.Cfg.WithSubChart,
		ChartTestsPath: s.Cfg.ChartTestsPath,
	}

	s.phase("Running the chart's tests unmutated")
	baseline, err := RunBaseline(s.Cfg.ChartPath, opts)
	if err != nil {
		return nil, err
	}

	run := &model.Run{
		ChartName:        baseline.ChartName,
		ChartPath:        s.Cfg.ChartPath,
		Mutators:         s.Cfg.EnabledMutators(mutator.IDs()),
		Suites:           baseline.SuiteInfos(),
		TestCount:        baseline.TestCount,
		BaselineDuration: baseline.Duration,
		Threshold:        s.Cfg.Threshold,
	}

	s.phase("Planning mutants")
	files, skipped, err := loadMutableFiles(s.Cfg)
	if err != nil {
		return nil, err
	}
	gen := mutator.Generate(files, run.Mutators)
	for _, sk := range append(skipped, gen.Skipped...) {
		run.SkippedFiles = append(run.SkippedFiles, model.SkippedFile{File: sk.File, Reason: sk.Reason})
	}
	run.Generated = len(gen.Plans)

	plans, dropped := mutator.Cap(gen.Plans, s.Cfg.MaxMutants, s.Cfg.Seed)
	run.Capped = dropped
	if dropped > 0 {
		s.phase(fmt.Sprintf("--max-mutants=%d kept %d of %d mutants (%d dropped)",
			s.Cfg.MaxMutants, len(plans), run.Generated, dropped))
	}
	if len(plans) == 0 {
		run.Duration = time.Since(start)
		run.ComputeTally()
		return run, nil
	}

	byPath := make(map[string]*source.File, len(files))
	for _, f := range files {
		byPath[f.Path] = f
	}

	refs, byID := CoverageRefs(baseline.Suites)

	s.phase(fmt.Sprintf("Evaluating %d mutants across %d workers", len(plans), s.Cfg.Parallel))
	mutants, err := Execute(ctx, plans, coverage.Build(refs), ExecutorConfig{
		ChartDir:    s.Cfg.ChartPath,
		Options:     opts,
		Files:       byPath,
		Parallel:    s.Cfg.Parallel,
		FailFast:    s.Cfg.FailFast(),
		Timeout:     s.Cfg.ResolveTimeout(baseline.Duration),
		KeepWorkdir: s.Cfg.KeepWorkdir,
		Progress:    s.OnProgress,
		SuitesByID:  byID,
		Debug:       s.Cfg.Debug,
	})
	if err != nil {
		return nil, err
	}

	run.Mutants = mutants
	if s.Cfg.EquivalenceCheck {
		s.phase("Checking survivors for equivalence")
		n := CheckEquivalence(ctx, run.Mutants, EquivalenceInput{
			ChartDir: s.Cfg.ChartPath,
			Suites:   baseline.Suites,
			Files:    byPath,
			Parallel: s.Cfg.Parallel,
		})
		run.EquivalenceChecked = true
		if n > 0 {
			s.phase(fmt.Sprintf("%d survivors are equivalent and cannot be killed", n))
		}
	}
	run.ComputeTally()
	run.Duration = time.Since(start)
	return run, nil
}

func (s *Session) phase(msg string) {
	if s.OnPhase != nil {
		s.OnPhase(msg)
	}
}

// loadMutableFiles reads the chart files eligible for mutation, honouring
// --include and --exclude.
func loadMutableFiles(cfg config.Config) ([]*source.File, []mutator.SkippedFile, error) {
	discovered, err := workspace.Discover(cfg.ChartPath)
	if err != nil {
		return nil, nil, err
	}

	type target struct {
		rel  string
		kind source.Kind
	}
	var targets []target
	for _, rel := range discovered.Templates {
		targets = append(targets, target{rel, source.KindTemplate})
	}
	if discovered.Values != "" {
		targets = append(targets, target{discovered.Values, source.KindValues})
	}

	var (
		files   []*source.File
		skipped []mutator.SkippedFile
	)
	for _, t := range targets {
		if !included(t.rel, cfg.Include, cfg.Exclude) {
			continue
		}
		f, err := source.Load(filepath.Join(cfg.ChartPath, filepath.FromSlash(t.rel)), t.rel, t.kind)
		if err != nil {
			skipped = append(skipped, mutator.SkippedFile{File: t.rel, Reason: err.Error()})
			continue
		}
		files = append(files, f)
	}
	return files, skipped, nil
}

// included applies the --include and --exclude glob lists. Exclusion wins.
func included(rel string, include, exclude []string) bool {
	for _, pat := range exclude {
		if matchGlob(pat, rel) {
			return false
		}
	}
	if len(include) == 0 {
		return true
	}
	for _, pat := range include {
		if matchGlob(pat, rel) {
			return true
		}
	}
	return false
}

// matchGlob matches a chart-relative path against a pattern, treating a trailing
// "/**" as "this directory and everything beneath it" — which filepath.Match
// alone does not support, since its * never crosses a separator.
func matchGlob(pattern, rel string) bool {
	pattern = filepath.ToSlash(pattern)
	rel = filepath.ToSlash(rel)

	if suffix := "/**"; len(pattern) > len(suffix) && pattern[len(pattern)-len(suffix):] == suffix {
		prefix := pattern[:len(pattern)-len(suffix)]
		return rel == prefix || hasPathPrefix(rel, prefix)
	}
	if ok, _ := filepath.Match(pattern, rel); ok {
		return true
	}
	// Also allow a bare basename pattern such as "values.yaml" or "*.tpl".
	ok, _ := filepath.Match(pattern, filepath.Base(rel))
	return ok
}

func hasPathPrefix(rel, prefix string) bool {
	return len(rel) > len(prefix) && rel[:len(prefix)] == prefix && rel[len(prefix)] == '/'
}
