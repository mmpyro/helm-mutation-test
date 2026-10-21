package mutator

import (
	"text/template/parse"

	"github.com/mmpyro/helm-mutation-test/internal/source"
)

func init() { register(pipeDrop{}) }

// pipeDrop removes a type- or format-changing function from a pipeline, keeping
// the value it was applied to.
//
// `{{ .Values.port | quote }}` and `{{ .Release.Name | trunc 63 }}` each decide
// the *type* or *shape* of what renders, not just its content: dropping `quote`
// turns the string "8080" into the integer 8080, which Kubernetes rejects on
// string fields; dropping `trunc 63` lets a name exceed the DNS label limit.
// Suites that assert only on presence never notice.
//
// Both call forms are mutated. Piped (`x | quote`) removes the segment; prefix
// (`quote x`, `trunc 63 x`) removes the function and its leading arguments.
type pipeDrop struct{}

// pipeDropFuncs maps each droppable function to the number of arguments it
// takes before the value it transforms. A call with any other argument count is
// not the shape we know how to drop, so it is left alone: removing `quote` from
// the variadic `quote .a .b` would keep only one of its values.
//
// int, int64 and float64 are absent on evidence: Helm decodes every values
// number as float64, so the conversions exist to feed comparisons, and without
// them `gt .Values.replicaCount 1` fails with "incompatible types for
// comparison". All four such sites in the sample chart rendered Invalid.
var pipeDropFuncs = map[string]int{
	"quote":        0,
	"squote":       0,
	"toString":     0,
	"toYaml":       0,
	"toJson":       0,
	"toPrettyJson": 0,
	"trim":         0,
	"lower":        0,
	"upper":        0,
	"title":        0,
	"b64enc":       0,
	"trunc":        1,
	"trimPrefix":   1,
	"trimSuffix":   1,
}

// serialisers turn a map or list into text. Followed by indent or nindent, which
// accept only a string, dropping one hands them the map itself — a type error
// whatever the values, so the mutant could only ever be Invalid.
var serialisers = map[string]bool{"toYaml": true, "toJson": true, "toPrettyJson": true}

var indenters = map[string]bool{"indent": true, "nindent": true}

func (pipeDrop) ID() string { return IDPipeDrop }

func (pipeDrop) Describe() string {
	return "remove a type- or format-changing function such as quote, toYaml or trunc from a pipeline"
}

func (pipeDrop) Mutate(f *source.File) []Candidate {
	tree, err := source.ParseTemplate(f)
	if err != nil {
		return nil
	}

	var out []Candidate
	tree.Walk(func(n parse.Node) bool {
		pipe, ok := n.(*parse.PipeNode)
		if !ok || len(pipe.Cmds) == 0 {
			return true
		}
		if !feedsIndenter(pipe, 0) {
			if c, ok := prefixDrop(f, pipe.Cmds[0]); ok {
				out = append(out, c)
			}
		}
		for _, seg := range source.SegmentsOf(f, pipe) {
			if feedsIndenter(pipe, seg.Index) {
				continue
			}
			if c, ok := pipedDrop(seg); ok {
				out = append(out, c)
			}
		}
		return true
	})
	return out
}

// feedsIndenter reports whether pipeline command i is a serialiser whose output
// goes straight into indent or nindent.
func feedsIndenter(pipe *parse.PipeNode, i int) bool {
	if i+1 >= len(pipe.Cmds) {
		return false
	}
	return headIdentIn(pipe.Cmds[i], serialisers) && headIdentIn(pipe.Cmds[i+1], indenters)
}

func headIdentIn(cmd *parse.CommandNode, names map[string]bool) bool {
	if cmd == nil || len(cmd.Args) == 0 {
		return false
	}
	id, ok := cmd.Args[0].(*parse.IdentifierNode)
	return ok && names[id.Ident]
}

// pipedDrop removes a `| fn args` segment that is not the pipeline's first
// command.
func pipedDrop(seg source.PipeSegment) (Candidate, bool) {
	if seg.Index == 0 || !seg.Droppable {
		return Candidate{}, false
	}
	lead, ok := droppableHead(seg.Cmd)
	if !ok || len(seg.Cmd.Args) != 1+lead {
		return Candidate{}, false
	}
	return Candidate{Mutator: IDPipeDrop, Start: seg.DropStart, End: seg.DropEnd}, true
}

// prefixDrop removes `fn args ` from a pipeline's first command, leaving the
// value argument in place.
//
// The deletion is anchored on the end of the argument before the value, never
// on the value's own position: a FieldNode reports a position past its first
// segment, so `.Values.deep.x` would appear to start at `.deep.x`.
func prefixDrop(f *source.File, cmd *parse.CommandNode) (Candidate, bool) {
	lead, ok := droppableHead(cmd)
	if !ok || len(cmd.Args) != 2+lead {
		return Candidate{}, false
	}
	anchorStart, anchorLen, ok := source.CommandArgSpan(cmd.Args[lead])
	if !ok {
		return Candidate{}, false
	}
	start := int(cmd.Args[0].Position())
	innerEnd, ok := source.ActionInnerEnd(f, start)
	if !ok {
		return Candidate{}, false
	}
	valueStart := source.SkipSpaceForward(f, anchorStart+anchorLen, innerEnd)
	if valueStart <= start || valueStart >= innerEnd {
		return Candidate{}, false
	}
	return Candidate{Mutator: IDPipeDrop, Start: start, End: valueStart}, true
}

// droppableHead reports whether cmd calls a droppable function, and how many
// arguments that function takes before its value.
func droppableHead(cmd *parse.CommandNode) (lead int, ok bool) {
	if cmd == nil || len(cmd.Args) == 0 {
		return 0, false
	}
	id, isIdent := cmd.Args[0].(*parse.IdentifierNode)
	if !isIdent {
		return 0, false
	}
	lead, ok = pipeDropFuncs[id.Ident]
	return lead, ok
}
