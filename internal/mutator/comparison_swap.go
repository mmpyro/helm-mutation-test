package mutator

import (
	"text/template/parse"

	"github.com/mmpyro/helm-mutation-test/internal/source"
)

func init() { register(comparisonSwap{}) }

// swaps maps each comparison and boolean operator to its opposite.
//
// eq/ne and the ordering pair are true inversions. and/or is not an inversion
// but it does change which inputs produce output, which is what exposes a suite
// that only ever exercises one combination of flags.
var swaps = map[string]string{
	"eq":  "ne",
	"ne":  "eq",
	"lt":  "ge",
	"ge":  "lt",
	"gt":  "le",
	"le":  "gt",
	"and": "or",
	"or":  "and",
}

// comparisonSwap replaces a comparison or boolean operator with its opposite.
type comparisonSwap struct{}

func (comparisonSwap) ID() string { return IDComparisonSwap }

func (comparisonSwap) Describe() string {
	return "swap comparison and boolean operators (eq<->ne, gt<->le, and<->or)"
}

func (comparisonSwap) Mutate(f *source.File) []Candidate {
	tree, err := source.ParseTemplate(f)
	if err != nil {
		return nil
	}

	var out []Candidate
	tree.Walk(func(n parse.Node) bool {
		cmd, ok := n.(*parse.CommandNode)
		if !ok || len(cmd.Args) == 0 {
			return true
		}
		// Only the head of a command is the function being called; an operator
		// name appearing as a later argument is a value, not a call.
		id, ok := cmd.Args[0].(*parse.IdentifierNode)
		if !ok {
			return true
		}
		swapped, found := swaps[id.Ident]
		if !found {
			return true
		}
		start, length, ok := source.CommandArgSpan(id)
		if !ok {
			return true
		}
		out = append(out, Candidate{
			Mutator:     IDComparisonSwap,
			Start:       start,
			End:         start + length,
			Replacement: swapped,
		})
		return true
	})
	return out
}
