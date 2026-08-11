package mutator

import (
	"strings"
	"text/template/parse"

	"github.com/mmpyro/helm-mutation-test/internal/source"
)

func init() { register(rangeEmpty{}) }

// rangeEmpty forces every {{ range }} to iterate zero times.
//
// Loops were entirely unmutated before this: cond-negate covers if and with,
// and nothing targeted RangeNode, so a suite could assert nothing whatsoever
// about env vars, ports, ingress hosts or volume mounts and still score 100%.
// This is to range what cond-negate is to if.
//
// Where the range has an {{ else }}, the mutant renders the else branch rather
// than nothing. That is still a change to the manifest and still observable, so
// it needs no special case — but "the block disappears" is the natural intuition
// and it is wrong for that shape.
//
// The replacement is sprig's `list`, which with no arguments is a real empty
// list. A bare nil is a shape text/template treats inconsistently in an argument
// position.
type rangeEmpty struct{}

func (rangeEmpty) ID() string { return IDRangeEmpty }

func (rangeEmpty) Describe() string {
	return "force range loops to iterate zero times (range X -> range list)"
}

func (rangeEmpty) Mutate(f *source.File) []Candidate {
	tree, err := source.ParseTemplate(f)
	if err != nil {
		return nil
	}

	var out []Candidate
	tree.Walk(func(n parse.Node) bool {
		rng, ok := n.(*parse.RangeNode)
		if !ok {
			return true
		}
		// SpanOfRangedExpression, not SpanOfCondition: the latter starts at "$k"
		// for `range $k, $v := X`, and dropping the declaration leaves the body's
		// variables undefined so the mutant would not parse.
		start, end, ok := source.SpanOfRangedExpression(f, rng.Pipe)
		if !ok {
			return true
		}
		// An already-empty range would yield a no-op candidate, which can only
		// ever "survive" and inflate the survived count with a non-mutation.
		if strings.TrimSpace(f.Slice(start, end)) == "list" {
			return true
		}
		out = append(out, Candidate{
			Mutator:     IDRangeEmpty,
			Start:       start,
			End:         end,
			Replacement: "list",
		})
		return true
	})
	return out
}
