package mutator

import (
	"text/template/parse"

	"github.com/mmpyro/helm-mutation-test/internal/source"
)

func init() { register(strLiteral{}) }

// Sentinel is the value substituted for string literals. It is deliberately
// recognisable so that if it ever leaks into a rendered manifest during
// debugging, its origin is obvious.
const Sentinel = "helm-mutation-test"

// strLiteralSkipKeys are fields whose value must not be replaced with a sentinel.
//
// apiVersion is the only genuine exclusion: a bogus apiVersion makes Helm fail to
// parse the manifest, so every test dies on a render error and the mutant is
// Invalid — noise, not signal. `kind` is deliberately NOT excluded: mutating it
// keeps the YAML valid and is precisely what an `isKind` assertion should catch.
var strLiteralSkipKeys = map[string]bool{
	"apiVersion": true,
}

// strLiteral replaces string literals with a sentinel value.
//
// This is the mutator that punishes presence-only assertions. A suite asserting
// that `imagePullPolicy` exists, without asserting what it equals, cannot tell
// IfNotPresent from a sentinel.
type strLiteral struct{}

func (strLiteral) ID() string { return IDStrLiteral }

func (strLiteral) Describe() string {
	return "replace string literals with a sentinel, exposing presence-only assertions"
}

func (strLiteral) Mutate(f *source.File) []Candidate {
	if f.Kind == source.KindValues {
		return strLiteralValues(f)
	}
	return append(strLiteralTemplateAST(f), strLiteralTemplateLiterals(f)...)
}

func strLiteralTemplateAST(f *source.File) []Candidate {
	tree, err := source.ParseTemplate(f)
	if err != nil {
		return nil
	}

	// A string that is an argument to `include` or `template` names another
	// template. Replacing it turns the call into a lookup of a template that does
	// not exist, which is a render error rather than a missing assertion.
	guarded := map[int]bool{}
	tree.Walk(func(n parse.Node) bool {
		cmd, ok := n.(*parse.CommandNode)
		if !ok || len(cmd.Args) == 0 {
			return true
		}
		id, ok := cmd.Args[0].(*parse.IdentifierNode)
		if !ok {
			return true
		}
		switch id.Ident {
		case "include", "template", "tpl":
			for _, arg := range cmd.Args[1:] {
				if s, ok := arg.(*parse.StringNode); ok {
					guarded[int(s.Position())] = true
				}
			}
		case "required":
			// The first argument is the error message, not chart output. Mutating
			// it changes nothing observable, so it can only ever survive.
			if len(cmd.Args) > 1 {
				if s, ok := cmd.Args[1].(*parse.StringNode); ok {
					guarded[int(s.Position())] = true
				}
			}
		}
		return true
	})

	var out []Candidate
	tree.Walk(func(n parse.Node) bool {
		s, ok := n.(*parse.StringNode)
		if !ok {
			return true
		}
		start, length, ok := source.CommandArgSpan(s)
		if !ok || guarded[start] || s.Text == Sentinel {
			return true
		}
		// A format string is structure, not data.
		if isFormatString(s.Text) {
			return true
		}
		out = append(out, Candidate{
			Mutator: IDStrLiteral, Start: start, End: start + length,
			Replacement: `"` + Sentinel + `"`,
		})
		return true
	})
	return out
}

func strLiteralTemplateLiterals(f *source.File) []Candidate {
	var out []Candidate
	for _, v := range source.PlainYAMLValues(f) {
		if v.Tag != source.TagStr || strLiteralSkipKeys[v.Key] {
			continue
		}
		inner, quote := source.Unquote(v.Value)
		if inner == Sentinel {
			continue
		}
		out = append(out, Candidate{
			Mutator: IDStrLiteral, Start: v.ValueStart, End: v.ValueEnd,
			Replacement: quoteLike(quote, Sentinel),
		})
	}
	return out
}

func strLiteralValues(f *source.File) []Candidate {
	scalars, err := source.ScalarsOf(f)
	if err != nil {
		return nil
	}
	var out []Candidate
	for _, s := range scalars {
		if s.IsKey || s.Tag != "!!str" || s.Value == Sentinel {
			continue
		}
		if strLiteralSkipKeys[lastPathSegment(s.Path)] {
			continue
		}
		_, quote := source.Unquote(f.Slice(s.Start, s.End))
		out = append(out, Candidate{
			Mutator: IDStrLiteral, Start: s.Start, End: s.End,
			Replacement: quoteLike(quote, Sentinel),
		})
	}
	return out
}

// quoteLike wraps v in the same quote style the original used, defaulting to
// double quotes so the sentinel is always an unambiguous YAML string.
func quoteLike(quote, v string) string {
	if quote == "" {
		quote = `"`
	}
	return quote + v + quote
}

// isFormatString reports whether a string looks like a printf template, whose
// verbs are structure rather than data.
func isFormatString(s string) bool {
	for i := 0; i < len(s)-1; i++ {
		if s[i] == '%' && s[i+1] != '%' {
			return true
		}
	}
	return false
}

func lastPathSegment(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '.' {
			return path[i+1:]
		}
	}
	return path
}
