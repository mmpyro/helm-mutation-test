package equivalence

import (
	"fmt"
	"sort"
	"strings"
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

// contextsKey identifies a set of contexts by name, independent of order, so the
// probe cache cannot serve one caller's context list with another's answer. Task
// 7 guarantees context names are unique per test job, so names alone are a valid
// identity.
func contextsKey(ctxs []RenderContext) string {
	names := make([]string, len(ctxs))
	for i, ctx := range ctxs {
		names[i] = ctx.Name
	}
	sort.Strings(names)
	return strings.Join(names, "\x00")
}

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
	original map[string]outcome // context name -> render outcome
	probed   map[string]bool    // span key + context set -> was the span proven to execute
}

// outcome is what rendering one chart under one context produced: either
// manifests, or a render error. A render error is itself an observable
// outcome — helm-unittest's matchFailedTemplate validator asserts on the error
// text (pkg/unittest/validators/failed_template_validator.go), so a mutation
// that leaves that text unchanged is invisible to it. Treating a render error
// as an automatic non-match would make every mutation covered by such a suite
// job inconclusive, which is exactly the bug this type fixes.
type outcome struct {
	manifests map[string]string
	errText   string // empty if the render succeeded
}

// renderOutcome renders chrt under ctx and folds a render error into the
// outcome instead of returning it: the caller's own failures (an unparseable
// mutated file, an unloadable chart) are errors, but a chart failing to
// render under a real value set is data.
func renderOutcome(chrt *chart.Chart, ctx RenderContext) outcome {
	manifests, err := Render(chrt, ctx)
	if err != nil {
		return outcome{errText: err.Error()}
	}
	return outcome{manifests: manifests}
}

// sameOutcome reports whether two outcomes are indistinguishable to any
// assertion. Both erroring identically is a match; one erroring and the other
// not, or the two error texts differing, is a real difference.
func sameOutcome(a, b outcome) (bool, error) {
	if (a.errText == "") != (b.errText == "") {
		return false, nil
	}
	if a.errText != "" {
		return a.errText == b.errText, nil
	}
	return Compare(a.manifests, b.manifests)
}

// NewChecker returns a checker over a loaded chart and the source files the
// mutations refer to, keyed by chart-relative path.
func NewChecker(base *chart.Chart, files map[string]*source.File) *Checker {
	return &Checker{
		base:     base,
		files:    files,
		original: map[string]outcome{},
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
		before := c.renderOriginal(ctx)
		after := renderOutcome(mutated, ctx)
		same, err := sameOutcome(before, after)
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
			"not exercised by any covering test: the probe caused no render error and no output "+
				"difference under any of %d value sets, so the span itself never ran",
			len(ctxs))}
	}
	return Verdict{Equivalent: true, Detail: fmt.Sprintf(
		"identical under %d test-job value sets; span proven executed", len(ctxs))}
}

// renderOriginal renders the unmutated chart under ctx, memoised by context name
// so every survivor shares one render per context.
func (c *Checker) renderOriginal(ctx RenderContext) outcome {
	c.mu.Lock()
	cached, ok := c.original[ctx.Name]
	c.mu.Unlock()
	if ok {
		return cached
	}
	out := renderOutcome(c.base, ctx)
	c.mu.Lock()
	c.original[ctx.Name] = out
	c.mu.Unlock()
	return out
}

// probeExecuted reports whether the mutated span demonstrably runs under ctxs.
// The probe itself depends only on the span, but "proven to execute" is a claim
// about a specific context set — a span proven to run under [ctx1, ctx2] is not
// proven to run under [ctx3], so the cache key must include the context set, not
// just the span, or a later call with a narrower or different set of contexts
// could read a stale true and call a survivor equivalent on evidence it never saw.
//
// The probe proves execution iff its outcome differs from the original's under
// some context — one rule that subsumes both a probe-induced render error (the
// `fail` only evaluates if reached) and a probe-induced output change (the
// values.yaml sentinel). It cannot be "the probe errored" alone: if the
// original already errors identically, that error pre-exists the probe and
// proves nothing about whether this particular span ran.
func (c *Checker) probeExecuted(m Mutation, f *source.File, ctxs []RenderContext) (bool, error) {
	key := m.spanKey() + "|" + contextsKey(ctxs)
	c.mu.Lock()
	cached, ok := c.probed[key]
	c.mu.Unlock()
	if ok {
		return cached, nil
	}

	data, ok := ProbeBytes(f, m.Start, m.End)
	if !ok {
		return false, fmt.Errorf("no probe can be built for %s", m.spanKey())
	}
	probeChart, err := WithMutatedFile(c.base, m.File, data)
	if err != nil {
		return false, err
	}

	executed := false
	for _, ctx := range ctxs {
		before := c.renderOriginal(ctx)
		after := renderOutcome(probeChart, ctx)
		same, err := sameOutcome(before, after)
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
