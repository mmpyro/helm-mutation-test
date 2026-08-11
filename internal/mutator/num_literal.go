package mutator

import (
	"fmt"
	"strconv"
	"strings"
	"text/template/parse"

	"github.com/mmpyro/helm-mutation-test/internal/source"
)

func init() { register(numLiteral{}) }

// layoutFuncs are template functions whose numeric arguments control *layout*
// rather than data: indentation width, repetition count, truncation length,
// format-string width.
//
// Mutating those numbers reindents or truncates the surrounding YAML, so the
// template stops parsing and every test fails on a render error. That is an
// Invalid mutant: excluded from the score, and pure noise in the report. The
// mutation is our bug, not the user's missing assertion, so we never generate it.
var layoutFuncs = map[string]bool{
	"indent":  true,
	"nindent": true,
	"repeat":  true,
	"trunc":   true,
	"printf":  true,
	"substr":  true,
	"abbrev":  true,
	"wrap":    true,
}

// Variant notes, which become part of a mutant's ID.
const (
	variantIncrement = "increment"
	variantZero      = "zero"
)

// numLiteral perturbs numeric literals: once by +1 to catch off-by-one blindness,
// and once to zero to catch fields nobody asserts at all.
type numLiteral struct{}

func (numLiteral) ID() string { return IDNumLiteral }

func (numLiteral) Describe() string {
	return "perturb numeric literals (n -> n+1, n -> 0); skips layout args like nindent"
}

func (numLiteral) Mutate(f *source.File) []Candidate {
	if f.Kind == source.KindValues {
		return numLiteralValues(f)
	}
	return append(numLiteralTemplateAST(f), numLiteralTemplateLiterals(f)...)
}

func numLiteralTemplateAST(f *source.File) []Candidate {
	tree, err := source.ParseTemplate(f)
	if err != nil {
		return nil
	}

	// First pass: every numeric argument of a layout function is off limits.
	guarded := map[int]bool{}
	tree.Walk(func(n parse.Node) bool {
		cmd, ok := n.(*parse.CommandNode)
		if !ok || len(cmd.Args) == 0 {
			return true
		}
		id, ok := cmd.Args[0].(*parse.IdentifierNode)
		if !ok || !layoutFuncs[id.Ident] {
			return true
		}
		for _, arg := range cmd.Args[1:] {
			if num, ok := arg.(*parse.NumberNode); ok {
				guarded[int(num.Position())] = true
			}
		}
		return true
	})

	var out []Candidate
	tree.Walk(func(n parse.Node) bool {
		num, ok := n.(*parse.NumberNode)
		if !ok {
			return true
		}
		start, length, ok := source.CommandArgSpan(num)
		if !ok || guarded[start] {
			return true
		}
		out = append(out, variantsFor(f.Slice(start, start+length), start, start+length)...)
		return true
	})
	return out
}

func numLiteralTemplateLiterals(f *source.File) []Candidate {
	var out []Candidate
	for _, v := range source.PlainYAMLValues(f) {
		if v.Tag != source.TagInt && v.Tag != source.TagFloat {
			continue
		}
		out = append(out, variantsFor(v.Value, v.ValueStart, v.ValueEnd)...)
	}
	return out
}

func numLiteralValues(f *source.File) []Candidate {
	scalars, err := source.ScalarsOf(f)
	if err != nil {
		return nil
	}
	var out []Candidate
	for _, s := range scalars {
		if s.IsKey || (s.Tag != "!!int" && s.Tag != "!!float") {
			continue
		}
		out = append(out, variantsFor(f.Slice(s.Start, s.End), s.Start, s.End)...)
	}
	return out
}

// variantsFor builds the increment and zero mutants for one numeric literal.
// The two are deduplicated: for 0 the "zero" variant would be a no-op, so it
// becomes 1 instead, and any variant identical to the original is dropped.
func variantsFor(text string, start, end int) []Candidate {
	inc, ok := incrementNumber(text)
	if !ok {
		return nil
	}
	zero, ok := towardZero(text)
	if !ok {
		return nil
	}

	out := make([]Candidate, 0, 2)
	if inc != text {
		out = append(out, Candidate{
			Mutator: IDNumLiteral, Start: start, End: end,
			Replacement: inc, Note: variantIncrement,
		})
	}
	if zero != text && zero != inc {
		out = append(out, Candidate{
			Mutator: IDNumLiteral, Start: start, End: end,
			Replacement: zero, Note: variantZero,
		})
	}
	return out
}

// incrementNumber adds one, preserving quoting, integer/float form and the
// original number of decimal places.
func incrementNumber(text string) (string, bool) {
	inner, quote := source.Unquote(text)
	if i, err := strconv.ParseInt(inner, 10, 64); err == nil {
		return quote + strconv.FormatInt(i+1, 10) + quote, true
	}
	if fl, err := strconv.ParseFloat(inner, 64); err == nil {
		return quote + formatFloatLike(inner, fl+1) + quote, true
	}
	return "", false
}

// towardZero replaces a number with 0, or with 1 when it is already 0 so the
// mutation is never a no-op.
func towardZero(text string) (string, bool) {
	inner, quote := source.Unquote(text)
	if i, err := strconv.ParseInt(inner, 10, 64); err == nil {
		if i == 0 {
			return quote + "1" + quote, true
		}
		return quote + "0" + quote, true
	}
	if fl, err := strconv.ParseFloat(inner, 64); err == nil {
		if fl == 0 {
			return quote + formatFloatLike(inner, 1) + quote, true
		}
		return quote + formatFloatLike(inner, 0) + quote, true
	}
	return "", false
}

// formatFloatLike renders v with as many decimal places as the original had, so
// 1.5 becomes 2.5 rather than 2.5000000001 or 2.
func formatFloatLike(original string, v float64) string {
	decimals := 0
	if dot := strings.IndexByte(original, '.'); dot >= 0 {
		decimals = len(original) - dot - 1
	}
	return fmt.Sprintf("%.*f", decimals, v)
}
