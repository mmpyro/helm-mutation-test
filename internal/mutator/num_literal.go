package mutator

import (
	"fmt"
	"strconv"
	"strings"
	"text/template/parse"

	"github.com/mmpyro/helm-mutation-test/internal/source"
)

func init() { register(numLiteral{}) }

// Variant notes, which become part of a mutant's ID.
const (
	variantIncrement = "increment"
	variantZero      = "zero"
)

// numLiteral perturbs numeric literals: once by +1 to catch off-by-one blindness,
// and once to zero to catch fields nobody asserts at all.
//
// Layout arguments such as `nindent 4` are deliberately NOT exempt. That guard
// existed here originally on the assumption that reindenting breaks rendering and
// yields a useless Invalid mutant; measuring against the fixture chart disproved
// it. `nindent 4 -> 0` and `-> 99` both render fine, survive the weak suite and
// are caught by the strong one, which makes them exactly the discriminating
// signal this tool is for. Where such a mutation genuinely does break a render,
// the Invalid classification handles it honestly rather than silently.
type numLiteral struct{}

func (numLiteral) ID() string { return IDNumLiteral }

func (numLiteral) Describe() string {
	return "perturb numeric literals (n -> n+1, n -> 0)"
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

	var out []Candidate
	tree.Walk(func(n parse.Node) bool {
		num, ok := n.(*parse.NumberNode)
		if !ok {
			return true
		}
		start, length, ok := source.CommandArgSpan(num)
		if !ok {
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
