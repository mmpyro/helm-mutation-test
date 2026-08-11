package runner

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/mmpyro/helm-mutation-test/internal/coverage"
	"github.com/mmpyro/helm-mutation-test/internal/model"
	"github.com/mmpyro/helm-mutation-test/internal/mutator"
	"github.com/mmpyro/helm-mutation-test/internal/source"
	"github.com/mmpyro/helm-mutation-test/internal/workspace"
)

// ExecutorConfig is what the executor needs beyond the plans themselves.
type ExecutorConfig struct {
	ChartDir string
	Options  Options
	// Files is the loaded source for every mutable chart file, keyed by the
	// chart-relative path used in Plan.File.
	Files map[string]*source.File
	// Parallel is the worker count.
	Parallel int
	// FailFast stops each mutant's run at the first failing test.
	FailFast bool
	// Timeout bounds a single mutant evaluation.
	Timeout time.Duration
	// KeepWorkdir preserves worker chart copies for debugging.
	KeepWorkdir bool
	// Progress, when set, is called once per completed mutant.
	Progress func(done, total int, m model.Mutant)
	// SuitesByID resolves a coverage index ID back to a suite key. The coverage
	// package works with opaque IDs so it can stay dependency-free.
	SuitesByID map[string]SuiteKey
	// Debug forwards worker stderr to ours.
	Debug bool
}

// suiteKeys maps coverage IDs back to suite keys, dropping any that no longer
// resolve (which can only happen if the suite set changed under us).
func (cfg ExecutorConfig) suiteKeys(ids []string) []SuiteKey {
	out := make([]SuiteKey, 0, len(ids))
	for _, id := range ids {
		if k, ok := cfg.SuitesByID[id]; ok {
			out = append(out, k)
		}
	}
	return out
}

// resolvedTimeout is the per-mutant bound, with a floor so a zero value cannot
// disable the timeout entirely.
func (cfg ExecutorConfig) resolvedTimeout() time.Duration {
	if cfg.Timeout > 0 {
		return cfg.Timeout
	}
	return 30 * time.Second
}

// newEvaluator picks how a worker runs suites.
//
// With a single worker nothing runs concurrently, so in-process execution is
// safe and avoids spawning anything. With more than one worker we must isolate
// by process: helm-unittest mutates a Helm package-level global while rendering
// (see worker.go), which both races and can leak one suite's kubeVersion into
// another's render.
func (cfg ExecutorConfig) newEvaluator(chartRoot string, workers int) (evaluator, error) {
	if workers <= 1 {
		return &inProcessEvaluator{chartRoot: chartRoot, opts: cfg.Options, failFast: cfg.FailFast}, nil
	}
	return newSubprocessEvaluator(chartRoot, cfg.Options, cfg.FailFast, cfg.Debug)
}

// SuiteID is the stable coverage-index identity of a suite.
func SuiteID(s *Suite) string { return s.Key.String() }

// CoverageRefs adapts discovered suites into the coverage package's input, plus
// the reverse lookup the executor needs.
func CoverageRefs(suites []*Suite) ([]coverage.SuiteRef, map[string]SuiteKey) {
	refs := make([]coverage.SuiteRef, 0, len(suites))
	byID := make(map[string]SuiteKey, len(suites))
	for _, s := range suites {
		id := SuiteID(s)
		refs = append(refs, coverage.SuiteRef{
			ID:           id,
			Templates:    s.Templates,
			JobTemplates: s.JobTemplates,
		})
		byID[id] = s.Key
	}
	return refs, byID
}

// Execute evaluates every plan and returns the resulting mutants in plan order.
func Execute(ctx context.Context, plans []mutator.Plan, idx *coverage.Index, cfg ExecutorConfig) ([]model.Mutant, error) {
	if len(plans) == 0 {
		return nil, nil
	}
	workers := min(max(cfg.Parallel, 1), len(plans))

	// Mutants whose file no suite exercises need no chart copy and no test run.
	// Resolving them up front means workers only ever handle real work.
	out := make([]model.Mutant, len(plans))
	type job struct {
		index  int
		plan   mutator.Plan
		suites []SuiteKey
	}
	var jobs []job
	for i, p := range plans {
		covering := cfg.suiteKeys(idx.For(p.File))
		if len(covering) == 0 {
			out[i] = newMutant(p, model.StatusNoCoverage)
			continue
		}
		jobs = append(jobs, job{index: i, plan: p, suites: covering})
	}

	if len(jobs) == 0 {
		reportAll(cfg, out)
		return out, nil
	}
	workers = min(workers, len(jobs))

	var (
		queue    = make(chan job)
		wg       sync.WaitGroup
		mu       sync.Mutex
		done     int
		firstErr error
	)
	fail := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
	}
	// drain keeps producers from blocking on a channel nobody is reading.
	drain := func(ch <-chan job) {
		for range ch {
		}
	}

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// One chart copy per worker, reused across that worker's mutants.
			ws, err := workspace.New(cfg.ChartDir, cfg.KeepWorkdir)
			if err != nil {
				fail(err)
				drain(queue)
				return
			}
			defer ws.Close()

			ev, err := cfg.newEvaluator(ws.Root, workers)
			if err != nil {
				fail(err)
				drain(queue)
				return
			}
			defer ev.Close()

			for j := range queue {
				m := evaluate(ctx, ws, ev, j.plan, j.suites, cfg)
				mu.Lock()
				out[j.index] = m
				done++
				if cfg.Progress != nil {
					cfg.Progress(done, len(jobs), m)
				}
				mu.Unlock()
			}
		}()
	}

	for _, j := range jobs {
		select {
		case <-ctx.Done():
			// Stop feeding work; already-queued mutants finish and the rest stay
			// zero-valued, which the fill below turns into an explicit status.
		case queue <- j:
			continue
		}
		break
	}
	close(queue)
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}
	fillUnevaluated(plans, out, ctx.Err())
	return out, nil
}

// fillUnevaluated gives a status to any mutant left unevaluated by cancellation,
// so a truncated run never silently reports fewer mutants than it generated.
func fillUnevaluated(plans []mutator.Plan, out []model.Mutant, cause error) {
	for i := range out {
		if out[i].Status != "" {
			continue
		}
		m := newMutant(plans[i], model.StatusError)
		m.Detail = "not evaluated: run was cancelled"
		if cause != nil {
			m.Detail = "not evaluated: " + cause.Error()
		}
		out[i] = m
	}
}

func reportAll(cfg ExecutorConfig, out []model.Mutant) {
	if cfg.Progress == nil {
		return
	}
	for i, m := range out {
		cfg.Progress(i+1, len(out), m)
	}
}

// evaluate runs one mutant: write the mutation, load, run the covering suites,
// classify, restore.
func evaluate(ctx context.Context, ws *workspace.Workspace, ev evaluator, plan mutator.Plan, want []SuiteKey, cfg ExecutorConfig) model.Mutant {
	m := newMutant(plan, "")
	for _, k := range want {
		m.CoveringSuites = append(m.CoveringSuites, k.File)
	}
	m.CoveringSuites = dedupeStrings(m.CoveringSuites)

	f, ok := cfg.Files[plan.File]
	if !ok {
		m.Status = model.StatusError
		m.Detail = "no loaded source for " + plan.File
		return m
	}

	start := time.Now()
	restore, err := ws.WriteMutation(plan.File, plan.Apply(f))
	if err != nil {
		m.Status = model.StatusError
		m.Detail = err.Error()
		m.Duration = time.Since(start)
		return m
	}
	defer func() {
		if rerr := restore(); rerr != nil && m.Detail == "" {
			// A failed restore would contaminate this worker's later mutants, so it
			// must be surfaced rather than dropped.
			m.Detail = "failed to restore " + plan.File + ": " + rerr.Error()
		}
	}()

	outcomes, err := ev.Run(ctx, want, cfg.resolvedTimeout())
	m.Duration = time.Since(start)
	switch {
	case err == errMutantTimeout:
		m.Status = model.StatusTimeout
		m.Detail = fmt.Sprintf("exceeded %s", cfg.Timeout)
		return m
	case err != nil:
		// A chart that no longer loads is the mutation's doing: the same class of
		// outcome as a render failure, and equally uninformative about test quality.
		m.Status = model.StatusInvalid
		m.Detail = err.Error()
		return m
	}

	status, killedBy, testsRun := Classify(outcomes)
	m.Status = status
	m.KilledBy = killedBy
	m.TestsRun = testsRun
	if status == model.StatusInvalid || status == model.StatusError {
		m.Detail = RenderErrorDetail(outcomes)
	}
	if cfg.FailFast && len(m.KilledBy) > 1 {
		// With --kill-attribution=first the run stops at the first failing test, but
		// that test may fail several assertions. Keep one for a stable report.
		m.KilledBy = m.KilledBy[:1]
	}
	return m
}

var errMutantTimeout = fmt.Errorf("mutant evaluation timed out")

// selectSuites picks the re-parsed suites matching the wanted keys, preserving
// the order in which they were discovered.
func selectSuites(all []*Suite, want []SuiteKey) []*Suite {
	if len(want) == 0 {
		return nil
	}
	wanted := make(map[SuiteKey]bool, len(want))
	for _, k := range want {
		wanted[k] = true
	}
	out := make([]*Suite, 0, len(want))
	for _, s := range all {
		if wanted[s.Key] {
			out = append(out, s)
		}
	}
	return out
}

func newMutant(p mutator.Plan, status model.Status) model.Mutant {
	return model.Mutant{
		ID:           p.ID,
		Mutator:      p.Mutator,
		File:         p.File,
		Line:         p.Line,
		Column:       p.Column,
		StartByte:    p.Start,
		EndByte:      p.End,
		Original:     p.Original,
		Mutated:      p.Replacement,
		OriginalLine: p.OriginalLine,
		MutatedLine:  p.MutatedLine,
		Status:       status,
	}
}

func dedupeStrings(in []string) []string {
	out := slices.Clone(in)
	sort.Strings(out)
	return slices.Compact(out)
}
