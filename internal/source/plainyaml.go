package source

import (
	"regexp"
	"strings"
)

// ScalarTag classifies a plain YAML value found in template text.
type ScalarTag string

const (
	TagBool  ScalarTag = "!!bool"
	TagInt   ScalarTag = "!!int"
	TagFloat ScalarTag = "!!float"
	TagStr   ScalarTag = "!!str"
)

// PlainYAMLValue is a `key: value` pair on a template line that contains no
// template action.
//
// Such a line is literal YAML, so its value is safe to treat as a scalar. This
// is what lets str-literal and friends reach hardcoded fields — a chart writing
// `imagePullPolicy: IfNotPresent` directly has no AST node to mutate, yet a test
// suite that never asserts that field is exactly the weakness we are hunting.
type PlainYAMLValue struct {
	Key    string
	Tag    ScalarTag
	Value  string
	Line   int
	Indent int
	// ValueStart and ValueEnd delimit the value, including any quotes.
	ValueStart, ValueEnd int
}

// A key, optionally preceded by a "- " list marker, followed by a value.
var plainYAMLLine = regexp.MustCompile(`^(\s*)(?:-\s+)?([A-Za-z_][A-Za-z0-9_.\-/]*)[ \t]*:[ \t]+(.+)$`)

// PlainYAMLValues returns the literal `key: value` pairs in a template.
//
// Lines containing "{{" are skipped entirely: those are the AST's territory, and
// guessing at their structure textually is how you produce wrong edits.
func PlainYAMLValues(f *File) []PlainYAMLValue {
	var out []PlainYAMLValue
	for line := 1; line <= f.LineCount(); line++ {
		text := f.LineText(line)
		if strings.Contains(text, "{{") || strings.Contains(text, "}}") {
			continue
		}
		if t := strings.TrimSpace(text); t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		m := plainYAMLLine.FindStringSubmatchIndex(text)
		if m == nil {
			continue
		}

		key := text[m[4]:m[5]]
		rawStart, rawEnd := m[6], m[7]
		value := text[rawStart:rawEnd]

		// Drop a trailing comment, then any padding it left behind.
		if i := commentIndex(value); i >= 0 {
			value = value[:i]
			rawEnd = rawStart + i
		}
		trimmed := strings.TrimRight(value, " \t\r")
		rawEnd -= len(value) - len(trimmed)
		value = trimmed
		if value == "" {
			continue
		}
		// Block scalars span lines; flow collections are not single scalars.
		if value == "|" || value == ">" || strings.HasPrefix(value, "|") || strings.HasPrefix(value, ">") {
			continue
		}
		if value[0] == '[' || value[0] == '{' || value[0] == '&' || value[0] == '*' {
			continue
		}

		lineStart, _, err := f.LineRange(line)
		if err != nil {
			continue
		}
		out = append(out, PlainYAMLValue{
			Key:        key,
			Tag:        classify(value),
			Value:      value,
			Line:       line,
			Indent:     m[3] - m[2],
			ValueStart: lineStart + rawStart,
			ValueEnd:   lineStart + rawEnd,
		})
	}
	return out
}

// commentIndex finds a " #" comment start outside quotes, or -1.
func commentIndex(s string) int {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"', '\'':
			if j := skipQuoted([]byte(s), i); j > i {
				i = j
			}
		case '#':
			if i > 0 && (s[i-1] == ' ' || s[i-1] == '\t') {
				return i
			}
		}
	}
	return -1
}

var (
	intRe   = regexp.MustCompile(`^-?\d+$`)
	floatRe = regexp.MustCompile(`^-?\d+\.\d+$`)
)

func classify(v string) ScalarTag {
	// A quoted value is a string no matter what it looks like inside.
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') {
		return TagStr
	}
	switch strings.ToLower(v) {
	case "true", "false":
		return TagBool
	}
	switch {
	case intRe.MatchString(v):
		return TagInt
	case floatRe.MatchString(v):
		return TagFloat
	}
	return TagStr
}

// Unquote strips surrounding quotes from a scalar's source text, reporting the
// quote character used ("" when the value was plain).
func Unquote(s string) (inner, quote string) {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1], string(s[0])
	}
	return s, ""
}
