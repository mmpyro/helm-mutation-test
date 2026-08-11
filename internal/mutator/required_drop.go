package mutator

import (
	"text/template/parse"

	"github.com/mmpyro/helm-mutation-test/internal/source"
)

func init() { register(requiredDrop{}) }

// requiredDrop strips a `required "msg" X` guard down to plain X.
//
// `required` is the chart author's contract that a value must be supplied. The
// only way to verify it is a negative test — helm-unittest's `failedTemplate`
// assertion — and negative tests are exactly what suites tend to lack. If
// removing the guard changes nothing, nobody is testing the failure path.
type requiredDrop struct{}

func (requiredDrop) ID() string { return IDRequiredDrop }

func (requiredDrop) Describe() string {
	return "strip `required \"msg\" X` to X, exposing missing failure-path tests"
}

func (requiredDrop) Mutate(f *source.File) []Candidate {
	tree, err := source.ParseTemplate(f)
	if err != nil {
		return nil
	}

	var out []Candidate
	tree.Walk(func(n parse.Node) bool {
		cmd, ok := n.(*parse.CommandNode)
		if !ok || !headIdentIs(cmd, "required") {
			return true
		}
		// required takes (message, value). Without the value argument there is
		// nothing to keep, so leave such a call alone.
		if len(cmd.Args) < 3 {
			return true
		}
		start := int(cmd.Args[0].Position())

		// Anchor the deletion on the end of the message argument rather than on
		// the value's own position: a FieldNode reports a position past its first
		// segment, so `.Values.token` would appear to start at `.token` and the
		// deletion would swallow `.Values`.
		msgStart, msgLen, ok := source.CommandArgSpan(cmd.Args[1])
		if !ok {
			return true
		}
		innerEnd, ok := source.ActionInnerEnd(f, start)
		if !ok {
			return true
		}
		valueStart := source.SkipSpaceForward(f, msgStart+msgLen, innerEnd)
		if valueStart <= start || valueStart >= innerEnd {
			return true
		}
		// Deleting `required "msg" ` leaves the value argument in place, which
		// keeps the surrounding pipeline (`| quote`, `| nindent`) intact.
		out = append(out, Candidate{
			Mutator:     IDRequiredDrop,
			Start:       start,
			End:         valueStart,
			Replacement: "",
		})
		return true
	})
	return out
}
