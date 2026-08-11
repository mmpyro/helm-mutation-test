package mutator

import (
	"regexp"
	"strings"

	"github.com/mmpyro/helm-mutation-test/internal/source"
)

func init() { register(yamlKeyDelete{}) }

// No key is exempt from deletion.
//
// apiVersion, kind and metadata were originally skipped on the assumption that
// Helm rejects a manifest without them. Measuring against the fixture chart
// disproved that: all three render fine when removed, survive the weak suite and
// are caught by the strong one. Deleting `kind` is caught even by the weak suite,
// because that is precisely what its `isKind` assertion is for — the tool working
// as intended, not noise.
//
// A "key:" at the start of a line, with or without an inline value.
var keyLine = regexp.MustCompile(`^(\s*)(?:-\s+)?([A-Za-z_][A-Za-z0-9_.\-/]*)[ \t]*:(\s|$)`)

// yamlKeyDelete removes a key together with its nested block.
//
// This is the bluntest and often most revealing mutator: if a whole field can
// vanish from a rendered manifest with every test still passing, that field is
// completely unasserted.
type yamlKeyDelete struct{}

func (yamlKeyDelete) ID() string { return IDYAMLKeyDelete }

func (yamlKeyDelete) Describe() string {
	return "delete a YAML key and its nested block, exposing entirely unasserted fields"
}

func (yamlKeyDelete) Mutate(f *source.File) []Candidate {
	if f.Kind == source.KindValues {
		return keyDeleteValues(f)
	}
	return keyDeleteTemplate(f)
}

// keyDeleteValues uses the YAML node tree, which knows the real structure.
func keyDeleteValues(f *source.File) []Candidate {
	blocks, err := source.KeyBlocksOf(f)
	if err != nil {
		return nil
	}
	var out []Candidate
	for _, b := range blocks {
		out = append(out, Candidate{
			Mutator: IDYAMLKeyDelete, Start: b.Start, End: b.End, Replacement: "",
		})
	}
	return out
}

// keyDeleteTemplate works line by line, because a template is not valid YAML
// before rendering and so has no node tree to consult.
//
// Two rules keep this safe. A key line containing an action is skipped only when
// the action is control flow — deleting half of an if/end pair would leave an
// unbalanced template, which is a syntax error rather than a mutation. A key
// whose block contains control flow is skipped for the same reason.
func keyDeleteTemplate(f *source.File) []Candidate {
	var out []Candidate
	for line := 1; line <= f.LineCount(); line++ {
		text := f.LineText(line)
		if strings.HasPrefix(strings.TrimSpace(text), "#") {
			continue
		}
		m := keyLine.FindStringSubmatch(text)
		if m == nil {
			continue
		}
		if hasControlFlow(text) {
			continue
		}

		indent := len(m[1])
		start, end, ok := f.BlockExtent(line, indent)
		if !ok {
			continue
		}
		// Removing a block that opens or closes a template action would unbalance
		// the template.
		if !balancedControlFlow(f.Slice(start, end)) {
			continue
		}
		out = append(out, Candidate{
			Mutator: IDYAMLKeyDelete, Start: start, End: end, Replacement: "",
		})
	}
	return out
}

var controlKeyword = regexp.MustCompile(`\{\{-?\s*(if|else|end|range|with|define|block)\b`)

// hasControlFlow reports whether a line opens or closes a template action.
func hasControlFlow(text string) bool { return controlKeyword.MatchString(text) }

// balancedControlFlow reports whether a span contains equal numbers of block
// openers and {{ end }}s, so deleting it leaves the template parseable.
func balancedControlFlow(span string) bool {
	opens := len(regexp.MustCompile(`\{\{-?\s*(if|range|with|define|block)\b`).FindAllString(span, -1))
	ends := len(regexp.MustCompile(`\{\{-?\s*end\b`).FindAllString(span, -1))
	elses := len(regexp.MustCompile(`\{\{-?\s*else\b`).FindAllString(span, -1))
	return opens == ends && elses == 0
}
