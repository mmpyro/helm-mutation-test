package equivalence

import (
	"strings"
	"sync"
	"testing"

	"github.com/mmpyro/helm-mutation-test/internal/source"
)

// judgeFixture builds a one-template chart plus the source.File map the checker
// needs, and returns a checker over both.
func judgeFixture(t *testing.T, templateSrc string, values map[string]any) (*Checker, *source.File) {
	t.Helper()
	c := testChart(values, map[string]string{"templates/a.yaml": templateSrc})
	f := source.New([]byte(templateSrc), "a.yaml", "templates/a.yaml", source.KindTemplate)
	return NewChecker(c, map[string]*source.File{"templates/a.yaml": f}), f
}

func mutationAt(t *testing.T, f *source.File, needle, replacement string) Mutation {
	t.Helper()
	start := strings.Index(f.Text(), needle)
	if start < 0 {
		t.Fatalf("needle %q not in source", needle)
	}
	return Mutation{File: f.Path, Start: start, End: start + len(needle), Replacement: replacement}
}

func TestJudgeCallsAReindentationEquivalent(t *testing.T) {
	// The canonical equivalent: YAML does not care how deep a block mapping is
	// indented, only that it is deeper than its parent.
	src := "spec:\n  {{ .Values.block | nindent 2 }}\n"
	c, f := judgeFixture(t, src, map[string]any{"block": "a: 1"})
	v := c.Judge(mutationAt(t, f, "nindent 2", "nindent 3"), []RenderContext{{Name: "ctx"}})
	if !v.Equivalent {
		t.Fatalf("want equivalent, got Survived: %s", v.Detail)
	}
}

func TestJudgeKeepsAnObservableMutationSurvived(t *testing.T) {
	src := "replicas: {{ .Values.replicas }}\n"
	c, f := judgeFixture(t, src, map[string]any{"replicas": 3})
	v := c.Judge(mutationAt(t, f, ".Values.replicas", "99"), []RenderContext{{Name: "ctx"}})
	if v.Equivalent {
		t.Fatal("a mutation that changes the output must not be called equivalent")
	}
}

func TestJudgeKeepsAnUnexercisedBranchSurvived(t *testing.T) {
	// Identical renders, because no context enables the branch. That is a missing
	// test, not an unkillable mutant, so it must stay in the denominator.
	src := "{{- if .Values.enabled }}\nreplicas: {{ .Values.replicas }}\n{{- end }}\n"
	c, f := judgeFixture(t, src, map[string]any{"enabled": false, "replicas": 3})
	v := c.Judge(mutationAt(t, f, ".Values.replicas", "99"), []RenderContext{{Name: "ctx"}})
	if v.Equivalent {
		t.Fatal("a mutation in an unreached branch must not be called equivalent")
	}
	if !strings.Contains(v.Detail, "not exercised") {
		t.Fatalf("detail should say why: %q", v.Detail)
	}
}

func TestJudgeRequiresEveryContextToAgree(t *testing.T) {
	// Equivalent under the default context, observable under the second. One
	// dissenting context is enough to keep the mutant survived.
	src := "{{- if .Values.enabled }}\nreplicas: {{ .Values.replicas }}\n{{- end }}\n"
	c, f := judgeFixture(t, src, map[string]any{"enabled": false, "replicas": 3})
	ctxs := []RenderContext{
		{Name: "defaults"},
		{Name: "enabled", Values: map[string]any{"enabled": true}},
	}
	v := c.Judge(mutationAt(t, f, ".Values.replicas", "99"), ctxs)
	if v.Equivalent {
		t.Fatal("a context that reveals the difference must veto equivalence")
	}
}

func TestJudgeIsInconclusiveWithoutContexts(t *testing.T) {
	src := "replicas: {{ .Values.replicas }}\n"
	c, f := judgeFixture(t, src, map[string]any{"replicas": 3})
	v := c.Judge(mutationAt(t, f, ".Values.replicas", "99"), nil)
	if v.Equivalent {
		t.Fatal("no contexts means no evidence, which must not mean equivalent")
	}
}

func TestProbeProofDoesNotLeakAcrossDifferentContextSets(t *testing.T) {
	// The probe answers "did this span run under these contexts", not "does this
	// span ever run anywhere". A cache keyed on the span alone would let a later,
	// narrower context set inherit a proof it never earned for itself — exactly
	// the false equivalent this feature must never produce. Both calls share one
	// Checker so its cache is actually exercised across the two context sets.
	src := "{{- if .Values.enabled }}\nspec:\n  {{ .Values.block | nindent 2 }}\n{{- end }}\n"
	c, f := judgeFixture(t, src, map[string]any{"enabled": false, "block": "a: 1"})
	m := mutationAt(t, f, "nindent 2", "nindent 3")

	enabled := []RenderContext{{Name: "enabled", Values: map[string]any{"enabled": true, "block": "a: 1"}}}
	v := c.Judge(m, enabled)
	if !v.Equivalent {
		t.Fatalf("want equivalent when the branch runs, got survived: %s", v.Detail)
	}

	// A disjoint context set that never reaches the branch. If the probe cache
	// keyed on the span alone, this call would read the "enabled" set's cached
	// proof and wrongly call this span equivalent too.
	disabled := []RenderContext{{Name: "disabled"}}
	v = c.Judge(m, disabled)
	if v.Equivalent {
		t.Fatalf("a different context set that never reaches the span must not inherit the earlier proof: %s", v.Detail)
	}
}

func TestJudgeCallsAMutationEquivalentWhenARequiredCheckAbortsIdenticallyAfterIt(t *testing.T) {
	// A test job that deliberately renders with a required value missing (and
	// asserts on the failure, via matchFailedTemplate) makes the whole render
	// fail identically before and after — Helm's engine executes into a buffer
	// it only commits on success, so a mutation upstream of the abort changes
	// nothing about the outcome the suite can observe. The probe proves the
	// mutated span itself still ran: injected before the abort, its own
	// distinct failure preempts "boom".
	src := `replicas: {{ .Values.replicas }}` + "\n" + `{{ required "boom" .Values.apiToken }}` + "\n"
	c, f := judgeFixture(t, src, map[string]any{"replicas": 3})
	v := c.Judge(mutationAt(t, f, ".Values.replicas", "99"), []RenderContext{{Name: "ctx"}})
	if !v.Equivalent {
		t.Fatalf("want equivalent, got survived: %s", v.Detail)
	}
}

func TestJudgeKeepsADifferingRenderFailureSurvived(t *testing.T) {
	// Both sides fail to render, but with different error text. helm-unittest's
	// matchFailedTemplate asserts on that text, so it is a real difference and
	// must not be folded into "both erred, therefore equivalent".
	src := `{{ required "boom" .Values.apiToken }}` + "\n"
	c, f := judgeFixture(t, src, nil)
	v := c.Judge(mutationAt(t, f, "boom", "bang"), []RenderContext{{Name: "ctx"}})
	if v.Equivalent {
		t.Fatal("a mutation that changes the render-failure text must not be called equivalent")
	}
}

func TestJudgeKeepsSurvivedWhenOnlyTheMutantFailsToRender(t *testing.T) {
	// The original renders fine; the mutation introduces a failure. That is the
	// clearest possible observable difference and must never be called
	// equivalent just because a render "error" is now a comparable outcome.
	src := `replicas: {{ .Values.replicas }}` + "\n"
	c, f := judgeFixture(t, src, map[string]any{"replicas": 3})
	v := c.Judge(mutationAt(t, f, ".Values.replicas", `fail "boom"`), []RenderContext{{Name: "ctx"}})
	if v.Equivalent {
		t.Fatal("a mutation that makes a previously-clean render fail must not be called equivalent")
	}
}

func TestJudgeIsSafeForConcurrentCallers(t *testing.T) {
	// Task 8 calls Judge from a worker pool. Sequential tests under -race prove
	// nothing about that; this drives the shared render and probe caches from
	// many goroutines at once, including repeated calls for the same span and
	// context set so both cache paths in renderOriginal and probeExecuted are
	// actually contended.
	src := "spec:\n  {{ .Values.block | nindent 2 }}\n"
	c, f := judgeFixture(t, src, map[string]any{"block": "a: 1"})
	m := mutationAt(t, f, "nindent 2", "nindent 3")
	ctxs := []RenderContext{{Name: "ctx"}}

	const goroutines = 50
	verdicts := make([]Verdict, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			verdicts[i] = c.Judge(m, ctxs)
		}(i)
	}
	wg.Wait()

	for i, v := range verdicts {
		if !v.Equivalent {
			t.Fatalf("goroutine %d: want equivalent, got survived: %s", i, v.Detail)
		}
		if v.Detail != verdicts[0].Detail {
			t.Fatalf("goroutine %d: verdicts disagree: %q vs %q", i, v.Detail, verdicts[0].Detail)
		}
	}
}
