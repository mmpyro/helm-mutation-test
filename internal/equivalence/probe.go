package equivalence

import (
	"strings"

	"github.com/mmpyro/helm-mutation-test/internal/source"
)

// Canary is the sentinel the execution probe injects. It is deliberately unlike
// anything a chart would contain, and non-empty so that it is truthy wherever
// the original value was false, 0 or "".
const Canary = "__helm_mutation_test_canary__"

// ProbeBytes returns the file content with the span at [start,end) perturbed as
// disruptively as the span allows, reporting false when no probe can be built.
//
// The probe is what separates "unkillable by anyone" from "no test reaches this
// line". Both produce identical renders; only the first should leave the score's
// denominator. A probe that changes nothing anywhere means the span never ran.
//
// The probe depends only on the span, not on the mutation, so one result serves
// every mutant at that site.
func ProbeBytes(f *source.File, start, end int) ([]byte, bool) {
	if f.Kind == source.KindTemplate {
		// ProbeSpan, not the enclosing pipeline: a probe wider than the mutation
		// proves only that the enclosing action was reached, which is a different
		// claim as soon as an and/or short circuit sits in between.
		ps, pe, ok := source.ProbeSpan(f, start, end)
		if !ok {
			return nil, false
		}
		return f.Apply(ps, pe, `fail "`+Canary+`"`), true
	}
	return valuesProbe(f, start, end)
}

// valuesProbe rewrites a values.yaml span so that any template reading the key
// sees a different value, using the YAML parser to safely locate key blocks and
// scalar values.
//
// Two shapes occur. A key block covers "key: value" — possibly a whole nested
// block — and is rebuilt as `key: "<canary>"`, preserving indentation. A non-key
// scalar is simply replaced with the quoted canary. Any other span returns ok=false,
// preferring safety to guessing.
func valuesProbe(f *source.File, start, end int) ([]byte, bool) {
	// Check if this span matches a key block exactly.
	keyBlocks, err := source.KeyBlocksOf(f)
	if err == nil {
		for _, kb := range keyBlocks {
			if kb.Start == start && kb.End == end {
				// Rebuild as "key: "<canary>"" at the same indentation.
				indent := strings.Repeat(" ", kb.Indent)
				newValue := indent + kb.Name + ": \"" + Canary + "\"\n"
				return f.Apply(start, end, newValue), true
			}
		}
	}

	// Check if this span matches a non-key scalar exactly.
	scalars, err := source.ScalarsOf(f)
	if err == nil {
		for _, s := range scalars {
			if !s.IsKey && s.Start == start && s.End == end {
				// Replace the scalar with the quoted canary.
				return f.Apply(start, end, `"`+Canary+`"`), true
			}
		}
	}

	// No exact match found; cannot safely probe this span.
	return nil, false
}
