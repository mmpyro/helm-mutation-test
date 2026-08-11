package runner

import (
	"context"
	"fmt"
	"sync"

	"github.com/mmpyro/helm-mutation-test/internal/equivalence"
	"github.com/mmpyro/helm-mutation-test/internal/model"
	"github.com/mmpyro/helm-mutation-test/internal/source"
)

// EquivalenceInput is what the equivalence pass needs beyond the mutants.
type EquivalenceInput struct {
	ChartDir string
	Suites   []*Suite
	Files    map[string]*source.File
	Parallel int
}

// EquivalenceResult is what one equivalence pass established.
type EquivalenceResult struct {
	// Equivalent is how many survivors were promoted to Equivalent.
	Equivalent int
	// Unchecked is how many survivors the pass could not reach a verdict on at
	// all: an unloadable chart, a covering suite it had to skip, a span it could
	// not probe. Reported separately because "checked, and it is a real
	// survivor" and "never actually checked" are different claims, and a report
	// that shows only the former reads as a fully verified run.
	Unchecked int
}

// CheckEquivalence re-renders every survivor and promotes the provably
// unkillable ones to Equivalent, in place.
//
// It runs in-process rather than in the worker subprocesses: those exist only
// because helm-unittest writes through Helm's package-level DefaultCapabilities,
// and our renderer copies that struct instead of aliasing it.
//
// Errors are not returned. A failure to prove equivalence is not a failure of
// the run — the mutant simply stays Survived, with the reason in its Detail and
// a tick on the returned Unchecked count.
func CheckEquivalence(ctx context.Context, mutants []model.Mutant, in EquivalenceInput) EquivalenceResult {
	idx := survivorIndexes(mutants)
	if len(idx) == 0 {
		return EquivalenceResult{}
	}

	chart, err := LoadChart(in.ChartDir)
	if err != nil {
		markInconclusive(mutants, idx, "inconclusive: "+err.Error())
		return EquivalenceResult{Unchecked: len(idx)}
	}

	ctxsBySuiteFile, skipReasons := contextsBySuiteFile(in)
	checker := equivalence.NewChecker(chart, in.Files)

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		queue  = make(chan int)
		result EquivalenceResult
	)
	workers := min(max(in.Parallel, 1), len(idx))
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range queue {
				m := mutants[i]
				verdict := judgeOne(checker, m, ctxsBySuiteFile, skipReasons)
				mu.Lock()
				switch {
				case verdict.Equivalent:
					mutants[i].Status = model.StatusEquivalent
					result.Equivalent++
				case verdict.Inconclusive:
					result.Unchecked++
				}
				mutants[i].Detail = verdict.Detail
				mu.Unlock()
			}
		}()
	}
	queued := 0
	for _, i := range idx {
		select {
		case <-ctx.Done():
		case queue <- i:
			queued++
			continue
		}
		break
	}
	close(queue)
	wg.Wait()
	// A cancelled run leaves the tail of the queue unjudged. Those survivors were
	// not checked either, and saying so is the difference between an aborted run
	// and a clean one.
	result.Unchecked += len(idx) - queued
	return result
}

// judgeOne assembles the contexts for one mutant's covering suites and judges it.
func judgeOne(
	checker *equivalence.Checker,
	m model.Mutant,
	ctxsBySuiteFile map[string][]equivalence.RenderContext,
	skipReasons map[string]string,
) equivalence.Verdict {
	var ctxs []equivalence.RenderContext
	for _, file := range m.CoveringSuites {
		if reason, ok := skipReasons[file]; ok {
			return equivalence.Verdict{Inconclusive: true, Detail: reason}
		}
		ctxs = append(ctxs, ctxsBySuiteFile[file]...)
	}
	return checker.Judge(equivalence.Mutation{
		File:        m.File,
		Start:       m.StartByte,
		End:         m.EndByte,
		Replacement: m.Mutated,
	}, ctxs)
}

// contextsBySuiteFile groups every suite's render contexts by suite file, which
// is the granularity Mutant.CoveringSuites records. A suite whose contexts
// cannot be extracted, or which uses a fake Kubernetes provider, is recorded in
// the skip map so its mutants are left alone rather than judged on partial
// input — keyed by reason text, not a bare bool, so a report never blames a
// parse failure on the Kubernetes-provider guard or vice versa.
func contextsBySuiteFile(in EquivalenceInput) (map[string][]equivalence.RenderContext, map[string]string) {
	byFile := map[string][]equivalence.RenderContext{}
	skip := map[string]string{}
	for _, s := range in.Suites {
		if s.UsesKubernetesProvider() {
			skip[s.Key.File] = "not checked: a covering suite uses a fake Kubernetes provider"
			continue
		}
		ctxs, err := RenderContexts(in.ChartDir, s)
		if err != nil {
			skip[s.Key.File] = fmt.Sprintf("not checked: extracting render contexts for %s: %s", s.Key, err)
			continue
		}
		byFile[s.Key.File] = append(byFile[s.Key.File], ctxs...)
	}
	return byFile, skip
}

func survivorIndexes(mutants []model.Mutant) []int {
	var out []int
	for i, m := range mutants {
		if m.Status == model.StatusSurvived {
			out = append(out, i)
		}
	}
	return out
}

func markInconclusive(mutants []model.Mutant, idx []int, detail string) {
	for _, i := range idx {
		mutants[i].Detail = detail
	}
}
