package source

import "testing"

func TestPlainYAMLValuesFindsLiteralFields(t *testing.T) {
	src := `apiVersion: apps/v1
kind: Deployment
spec:
  replicas: 3
  paused: false
  ratio: 0.5
  name: "quoted-name"
  other: 'single'
`
	f := mk(t, src)
	got := PlainYAMLValues(f)

	byKey := map[string]PlainYAMLValue{}
	for _, v := range got {
		byKey[v.Key] = v
	}

	tests := []struct {
		key      string
		tag      ScalarTag
		wantText string
	}{
		{"apiVersion", TagStr, "apps/v1"},
		{"kind", TagStr, "Deployment"},
		{"replicas", TagInt, "3"},
		{"paused", TagBool, "false"},
		{"ratio", TagFloat, "0.5"},
		{"name", TagStr, `"quoted-name"`},
		{"other", TagStr, `'single'`},
	}
	for _, tc := range tests {
		v, ok := byKey[tc.key]
		if !ok {
			t.Errorf("no value found for key %q", tc.key)
			continue
		}
		if v.Tag != tc.tag {
			t.Errorf("%s: tag = %q, want %q", tc.key, v.Tag, tc.tag)
		}
		if got := f.Slice(v.ValueStart, v.ValueEnd); got != tc.wantText {
			t.Errorf("%s: span = %q, want %q", tc.key, got, tc.wantText)
		}
	}
}

func TestPlainYAMLValuesSkipsTemplatedLines(t *testing.T) {
	// Lines with actions belong to the AST. Guessing at them textually is how you
	// produce a wrong edit.
	src := `replicas: {{ .Values.replicaCount }}
name: {{ include "chart.fullname" . }}
{{- if .Values.enabled }}
plain: literal
{{- end }}
`
	f := mk(t, src)
	got := PlainYAMLValues(f)
	if len(got) != 1 {
		t.Fatalf("expected exactly the one non-templated line, got %d: %+v", len(got), got)
	}
	if got[0].Key != "plain" || got[0].Value != "literal" {
		t.Errorf("got %+v, want key=plain value=literal", got[0])
	}
}

func TestPlainYAMLValuesHandlesListItems(t *testing.T) {
	src := `containers:
  - name: app
    image: nginx
`
	f := mk(t, src)
	got := PlainYAMLValues(f)
	byKey := map[string]string{}
	for _, v := range got {
		byKey[v.Key] = v.Value
	}
	if byKey["name"] != "app" {
		t.Errorf("list-item key not found: %+v", byKey)
	}
	if byKey["image"] != "nginx" {
		t.Errorf("nested key not found: %+v", byKey)
	}
}

func TestPlainYAMLValuesStripsTrailingComments(t *testing.T) {
	f := mk(t, "replicas: 3 # how many\nname: value  # trailing\n")
	got := PlainYAMLValues(f)
	if len(got) != 2 {
		t.Fatalf("got %d values, want 2", len(got))
	}
	if got[0].Value != "3" {
		t.Errorf("comment leaked into value: %q", got[0].Value)
	}
	if got[1].Value != "value" {
		t.Errorf("comment leaked into value: %q", got[1].Value)
	}
	// The span must not include the comment either.
	if s := f.Slice(got[0].ValueStart, got[0].ValueEnd); s != "3" {
		t.Errorf("span = %q, want %q", s, "3")
	}
}

func TestPlainYAMLValuesKeepsHashInsideQuotes(t *testing.T) {
	f := mk(t, `color: "#ff0000"`+"\n")
	got := PlainYAMLValues(f)
	if len(got) != 1 {
		t.Fatalf("got %d values, want 1", len(got))
	}
	if got[0].Value != `"#ff0000"` {
		t.Errorf("a # inside quotes is not a comment: got %q", got[0].Value)
	}
}

func TestPlainYAMLValuesSkipsNonScalars(t *testing.T) {
	src := `comment: value
# a full comment line
empty:
block: |
  text
folded: >
  text
flow: [1, 2]
inline: {a: 1}
anchored: &ref
aliased: *ref
`
	f := mk(t, src)
	for _, v := range PlainYAMLValues(f) {
		if v.Key != "comment" {
			t.Errorf("key %q should have been skipped (value %q)", v.Key, v.Value)
		}
	}
}

func TestUnquote(t *testing.T) {
	tests := []struct {
		in        string
		wantInner string
		wantQuote string
	}{
		{`"abc"`, "abc", `"`},
		{`'abc'`, "abc", `'`},
		{`abc`, "abc", ""},
		{`"`, `"`, ""},
		{`"mismatched'`, `"mismatched'`, ""},
	}
	for _, tc := range tests {
		inner, quote := Unquote(tc.in)
		if inner != tc.wantInner || quote != tc.wantQuote {
			t.Errorf("Unquote(%q) = (%q,%q), want (%q,%q)", tc.in, inner, quote, tc.wantInner, tc.wantQuote)
		}
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		in   string
		want ScalarTag
	}{
		{"true", TagBool}, {"TRUE", TagBool}, {"False", TagBool},
		{"3", TagInt}, {"-7", TagInt},
		{"1.5", TagFloat}, {"-0.25", TagFloat},
		{"nginx", TagStr}, {"v1.2.3", TagStr},
		{`"true"`, TagStr}, // quoting makes it a string
		{"apps/v1", TagStr},
	}
	for _, tc := range tests {
		if got := classify(tc.in); got != tc.want {
			t.Errorf("classify(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
