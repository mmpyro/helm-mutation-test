package equivalence

import (
	"fmt"
	"sync"

	"github.com/mmpyro/helm-mutation-test/internal/source"
	"helm.sh/helm/v3/pkg/chart"
)

// Mutation is one mutation to judge, as a byte-range edit on a chart file.
type Mutation struct {
	// File is chart-relative and slash-separated, matching source.File.Path.
	File        string
	Start, End  int
	Replacement string
}

func (m Mutation) spanKey() string { return fmt.Sprintf("%s:%d:%d", m.File, m.Start, m.End) }

// Verdict is the outcome of judging one mutation. Detail is human-facing and
// lands in Mutant.Detail, so it must explain the verdict either way.
type Verdict struct {
	Equivalent bool
	Detail     string
}

// Checker judges mutations against one loaded chart.
//
// It caches the two renders that do not depend on the mutation — the original,
// and the probe at a given span — so a survivor typically costs a single extra
// render. Safe for concurrent use.
type Checker struct {
	base  *chart.Chart
	files map[string]*source.File

	mu       sync.Mutex
	original map[string]map[string]string // context name -> manifests
	probed   map[string]bool              // span key -> was the span proven to execute
}

// NewChecker returns a checker over a loaded chart and the source files the
// mutations refer to, keyed by chart-relative path.
func NewChecker(base *chart.Chart, files map[string]*source.File) *Checker {
	return &Checker{
		base:     base,
		files:    files,
		original: map[string]map[string]string{},
		probed:   map[string]bool{},
	}
}

// Judge decides whether a mutation can change what the chart renders under any
// of the given contexts.
//
// Every uncertain path returns a non-equivalent verdict. Removing a mutant from
// the score's denominator raises the score, so it happens only on positive
// evidence: identical parsed output under every context, plus a probe proving
// the span executed under at least one.
func (c *Checker) Judge(m Mutation, ctxs []RenderContext) Verdict {
	if len(ctxs) == 0 {
		return Verdict{Detail: "inconclusive: no usable test-job value sets"}
	}
	f, ok := c.files[m.File]
	if !ok {
		return Verdict{Detail: "inconclusive: no loaded source for " + m.File}
	}

	mutated, err := WithMutatedFile(c.base, m.File, f.Apply(m.Start, m.End, m.Replacement))
	if err != nil {
		return Verdict{Detail: "inconclusive: " + err.Error()}
	}

	for _, ctx := range ctxs {
		before, err := c.renderOriginal(ctx)
		if err != nil {
			return Verdict{Detail: "inconclusive: " + err.Error()}
		}
		after, err := Render(mutated, ctx)
		if err != nil {
			// The mutant rendered fine under helm-unittest or it would not have
			// survived, so a failure here is our renderer disagreeing, not evidence.
			return Verdict{Detail: "inconclusive: " + err.Error()}
		}
		same, err := Compare(before, after)
		if err != nil {
			return Verdict{Detail: "inconclusive: " + err.Error()}
		}
		if !same {
			return Verdict{Detail: fmt.Sprintf("output differs under %s", ctx.Name)}
		}
	}

	executed, err := c.probeExecuted(m, f, ctxs)
	if err != nil {
		return Verdict{Detail: "inconclusive: " + err.Error()}
	}
	if !executed {
		return Verdict{Detail: fmt.Sprintf(
			"not exercised by any covering test: the span produced no output under any of %d value sets",
			len(ctxs))}
	}
	return Verdict{Equivalent: true, Detail: fmt.Sprintf(
		"identical under %d test-job value sets; span proven executed", len(ctxs))}
}

// renderOriginal renders the unmutated chart under ctx, memoised by context name
// so every survivor shares one render per context.
func (c *Checker) renderOriginal(ctx RenderContext) (map[string]string, error) {
	c.mu.Lock()
	cached, ok := c.original[ctx.Name]
	c.mu.Unlock()
	if ok {
		return cached, nil
	}
	out, err := Render(c.base, ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.original[ctx.Name] = out
	c.mu.Unlock()
	return out, nil
}

// probeExecuted reports whether the mutated span demonstrably runs. The probe
// depends only on the span, so the answer is memoised per span.
//
// A render error counts as proof: the probe's `fail` evaluates only when it is
// reached. So does any change in output, which is what the values.yaml sentinel
// produces.
func (c *Checker) probeExecuted(m Mutation, f *source.File, ctxs []RenderContext) (bool, error) {
	key := m.spanKey()
	c.mu.Lock()
	cached, ok := c.probed[key]
	c.mu.Unlock()
	if ok {
		return cached, nil
	}

	data, ok := ProbeBytes(f, m.Start, m.End)
	if !ok {
		return false, fmt.Errorf("no probe can be built for %s", key)
	}
	probeChart, err := WithMutatedFile(c.base, m.File, data)
	if err != nil {
		return false, err
	}

	executed := false
	for _, ctx := range ctxs {
		before, err := c.renderOriginal(ctx)
		if err != nil {
			return false, err
		}
		after, err := Render(probeChart, ctx)
		if err != nil {
			executed = true // the probe's fail was evaluated
			break
		}
		same, err := Compare(before, after)
		if err != nil {
			return false, err
		}
		if !same {
			executed = true
			break
		}
	}

	c.mu.Lock()
	c.probed[key] = executed
	c.mu.Unlock()
	return executed, nil
}
