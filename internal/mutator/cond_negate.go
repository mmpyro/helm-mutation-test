package mutator

import (
	"text/template/parse"

	"github.com/mmpyro/helm-mutation-test/internal/source"
)

func init() { register(condNegate{}) }

// condNegate inverts every {{ if }} and {{ with }} condition.
//
// This is the highest-value mutator for Helm charts. A chart guards whole
// resources behind `{{- if .Values.ingress.enabled }}`, and a suite that only
// tests the enabled case never notices the resource appearing when it should
// not. Negating the condition makes that omission fail loudly.
type condNegate struct{}

func (condNegate) ID() string { return IDCondNegate }

func (condNegate) Describe() string {
	return "negate if/else-if/with conditions (COND -> not (COND))"
}

func (condNegate) Mutate(f *source.File) []Candidate {
	tree, err := source.ParseTemplate(f)
	if err != nil {
		return nil
	}

	var out []Candidate
	tree.Walk(func(n parse.Node) bool {
		var pipe *parse.PipeNode
		switch node := n.(type) {
		case *parse.IfNode:
			pipe = node.Pipe
		case *parse.WithNode:
			pipe = node.Pipe
		default:
			return true
		}

		start, end, ok := source.SpanOfCondition(f, pipe)
		if !ok {
			return true
		}
		out = append(out, Candidate{
			Mutator: IDCondNegate,
			Start:   start,
			End:     end,
			// Parenthesised because the condition may be a multi-argument call
			// such as `and .Values.a .Values.b`, where `not and ...` would not parse.
			Replacement: "not (" + f.Slice(start, end) + ")",
		})
		return true
	})
	return out
}
