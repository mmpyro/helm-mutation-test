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
		ps, pe, ok := source.EnclosingPipelineSpan(f, start)
		if !ok {
			return nil, false
		}
		return f.Apply(ps, pe, `fail "`+Canary+`"`), true
	}
	return valuesProbe(f, start, end)
}

// valuesProbe rewrites a values.yaml span so that any template reading the key
// sees a different value.
//
// Two shapes occur. A yaml-key-delete span covers "key: value" — possibly a
// whole nested block — and is rebuilt as `key: "<canary>"`, which also flattens
// a map into a scalar and so is maximally disruptive. Any other span is the
// scalar value itself and is simply replaced.
func valuesProbe(f *source.File, start, end int) ([]byte, bool) {
	span := f.Slice(start, end)
	quoted := `"` + Canary + `"`

	colon := strings.IndexByte(span, ':')
	if colon < 0 {
		// A bare scalar. Reject anything that looks structural rather than a value.
		if strings.ContainsAny(span, "\n-") {
			return nil, false
		}
		return f.Apply(start, end, quoted), true
	}

	key := span[:colon]
	if strings.ContainsAny(key, "\n#") || strings.TrimSpace(key) == "" {
		return nil, false
	}
	trailing := ""
	if strings.HasSuffix(span, "\n") {
		trailing = "\n"
	}
	return f.Apply(start, end, key+": "+quoted+trailing), true
}
