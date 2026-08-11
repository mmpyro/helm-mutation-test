package mutator

import (
	"strings"
	"text/template/parse"

	"github.com/mmpyro/helm-mutation-test/internal/source"
)

func init() { register(boolFlip{}) }

// boolFlip inverts boolean literals in templates and in values.yaml.
//
// Flipping a values.yaml default is the cheapest way to find out whether any
// test actually pins that default. A chart shipping `serviceAccount.create: true`
// whose suite never asserts the ServiceAccount exists will not notice the flip.
type boolFlip struct{}

func (boolFlip) ID() string { return IDBoolFlip }

func (boolFlip) Describe() string { return "flip boolean literals (true <-> false)" }

func (boolFlip) Mutate(f *source.File) []Candidate {
	if f.Kind == source.KindValues {
		return boolFlipValues(f)
	}
	return append(boolFlipTemplateAST(f), boolFlipTemplateLiterals(f)...)
}

// boolFlipTemplateAST handles bool literals inside {{ ... }} actions.
func boolFlipTemplateAST(f *source.File) []Candidate {
	tree, err := source.ParseTemplate(f)
	if err != nil {
		return nil
	}
	var out []Candidate
	tree.Walk(func(n parse.Node) bool {
		b, ok := n.(*parse.BoolNode)
		if !ok {
			return true
		}
		start, length, ok := source.CommandArgSpan(b)
		if !ok {
			return true
		}
		out = append(out, Candidate{
			Mutator:     IDBoolFlip,
			Start:       start,
			End:         start + length,
			Replacement: negateBoolText(f.Slice(start, start+length)),
		})
		return true
	})
	return out
}

// boolFlipTemplateLiterals handles hardcoded YAML booleans on template lines
// that contain no action, e.g. a literal `hostNetwork: false`.
func boolFlipTemplateLiterals(f *source.File) []Candidate {
	var out []Candidate
	for _, v := range source.PlainYAMLValues(f) {
		if v.Tag != source.TagBool {
			continue
		}
		out = append(out, Candidate{
			Mutator:     IDBoolFlip,
			Start:       v.ValueStart,
			End:         v.ValueEnd,
			Replacement: negateBoolText(v.Value),
		})
	}
	return out
}

func boolFlipValues(f *source.File) []Candidate {
	scalars, err := source.ScalarsOf(f)
	if err != nil {
		return nil
	}
	var out []Candidate
	for _, s := range scalars {
		if s.IsKey || s.Tag != "!!bool" {
			continue
		}
		out = append(out, Candidate{
			Mutator:     IDBoolFlip,
			Start:       s.Start,
			End:         s.End,
			Replacement: negateBoolText(f.Slice(s.Start, s.End)),
		})
	}
	return out
}

// negateBoolText inverts a boolean's source text while preserving its casing and
// any surrounding quotes, so `"True"` becomes `"False"` rather than `false`.
func negateBoolText(text string) string {
	inner, quote := source.Unquote(text)
	var flipped string
	switch {
	case strings.EqualFold(inner, "true"):
		flipped = matchCase(inner, "false")
	case strings.EqualFold(inner, "false"):
		flipped = matchCase(inner, "true")
	default:
		return text
	}
	return quote + flipped + quote
}

// matchCase renders replacement in the same case style as original.
func matchCase(original, replacement string) string {
	switch {
	case original == strings.ToUpper(original):
		return strings.ToUpper(replacement)
	case len(original) > 0 && original[0] >= 'A' && original[0] <= 'Z':
		return strings.ToUpper(replacement[:1]) + replacement[1:]
	default:
		return replacement
	}
}
