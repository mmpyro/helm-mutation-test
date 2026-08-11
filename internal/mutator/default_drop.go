package mutator

import (
	"text/template/parse"

	"github.com/mmpyro/helm-mutation-test/internal/source"
)

func init() { register(defaultDrop{}) }

// defaultDrop removes a `| default X` segment from a pipeline.
//
// `{{ .Values.image.tag | default .Chart.AppVersion }}` has two behaviours: the
// value path and the fallback path. Suites overwhelmingly test only the first,
// leaving the fallback — the part that decides what a fresh install renders —
// entirely unasserted. Dropping the default makes the field render empty.
type defaultDrop struct{}

func (defaultDrop) ID() string { return IDDefaultDrop }

func (defaultDrop) Describe() string {
	return "remove `| default X` from a pipeline, exposing untested fallbacks"
}

func (defaultDrop) Mutate(f *source.File) []Candidate {
	tree, err := source.ParseTemplate(f)
	if err != nil {
		return nil
	}

	var out []Candidate
	tree.Walk(func(n parse.Node) bool {
		pipe, ok := n.(*parse.PipeNode)
		if !ok {
			return true
		}
		for _, seg := range source.SegmentsOf(f, pipe) {
			if !seg.Droppable || !headIdentIs(seg.Cmd, "default") {
				continue
			}
			out = append(out, Candidate{
				Mutator:     IDDefaultDrop,
				Start:       seg.DropStart,
				End:         seg.DropEnd,
				Replacement: "",
			})
		}
		return true
	})
	return out
}

// headIdentIs reports whether a command calls the named function.
func headIdentIs(cmd *parse.CommandNode, name string) bool {
	if cmd == nil || len(cmd.Args) == 0 {
		return false
	}
	id, ok := cmd.Args[0].(*parse.IdentifierNode)
	return ok && id.Ident == name
}
