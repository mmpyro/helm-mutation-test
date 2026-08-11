package equivalence

import (
	"strings"
	"testing"

	"github.com/mmpyro/helm-mutation-test/internal/source"
	"gopkg.in/yaml.v3"
)

func templateFile(src string) *source.File {
	return source.New([]byte(src), "t.yaml", "templates/t.yaml", source.KindTemplate)
}

func valuesFile(src string) *source.File {
	return source.New([]byte(src), "values.yaml", "values.yaml", source.KindValues)
}

func TestProbeBytesSubstitutesFailIntoTemplateActions(t *testing.T) {
	tests := []struct {
		name, src, needle, want string
	}{
		{
			"plain action",
			"x: {{ .Values.a }}\n", ".Values.a",
			`x: {{ fail "` + Canary + `" }}` + "\n",
		},
		{
			"if keyword survives",
			"{{- if .Values.a }}\nx: 1\n{{- end }}\n", ".Values.a",
			`{{- if fail "` + Canary + `" }}` + "\nx: 1\n{{- end }}\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := templateFile(tc.src)
			off := strings.Index(tc.src, tc.needle)
			got, ok := ProbeBytes(f, off, off+len(tc.needle))
			if !ok {
				t.Fatal("ProbeBytes returned !ok")
			}
			if string(got) != tc.want {
				t.Fatalf("probe =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

func TestProbeBytesRewritesAValuesKeyToTheCanary(t *testing.T) {
	// yaml-key-delete spans a whole key. Replacing the span outright would break
	// the YAML, so the key is kept and only its value becomes the canary — a
	// non-empty string, therefore truthy where false, 0 or "" used to be.
	src := "autoscaling:\n  enabled: false\n"
	f := valuesFile(src)
	start := strings.Index(src, "  enabled")
	end := len(src)
	got, ok := ProbeBytes(f, start, end)
	if !ok {
		t.Fatal("ProbeBytes returned !ok")
	}
	want := "autoscaling:\n  enabled: \"" + Canary + "\"\n"
	if string(got) != want {
		t.Fatalf("probe = %q, want %q", got, want)
	}
}

func TestProbeBytesReplacesABareValuesScalar(t *testing.T) {
	src := "replicas: 3\n"
	f := valuesFile(src)
	start := strings.Index(src, "3")
	got, ok := ProbeBytes(f, start, start+1)
	if !ok {
		t.Fatal("ProbeBytes returned !ok")
	}
	want := "replicas: \"" + Canary + "\"\n"
	if string(got) != want {
		t.Fatalf("probe = %q, want %q", got, want)
	}
}

func TestProbeBytesHandlesScalarsWithColons(t *testing.T) {
	// A bare scalar whose value contains a colon must produce valid YAML.
	// The old heuristic would mistake the colon in the value for a key:value separator.
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			"quoted scalar with colon",
			"annotation: \"a: b\"\n",
			"annotation: \"" + Canary + "\"\n",
		},
		{
			"unquoted image tag",
			"image: nginx:1.21\n",
			"image: \"" + Canary + "\"\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := valuesFile(tc.src)
			scalars, err := source.ScalarsOf(f)
			if err != nil {
				t.Fatalf("ScalarsOf error: %v", err)
			}
			if len(scalars) == 0 {
				t.Fatal("no scalars found")
			}
			// Find the non-key scalar (the value)
			var s source.Scalar
			for _, sc := range scalars {
				if !sc.IsKey {
					s = sc
					break
				}
			}
			if s.Start == 0 && s.End == 0 {
				t.Fatal("no non-key scalar found")
			}

			got, ok := ProbeBytes(f, s.Start, s.End)
			if !ok {
				t.Fatal("ProbeBytes returned !ok")
			}
			if string(got) != tc.want {
				t.Fatalf("probe = %q, want %q", got, tc.want)
			}
			// Verify the result is valid YAML
			var result interface{}
			if err := yaml.Unmarshal(got, &result); err != nil {
				t.Fatalf("probe output is not valid YAML: %v", err)
			}
		})
	}
}

func TestProbeBytesHandlesScalarsWithHyphens(t *testing.T) {
	// A bare scalar like "my-service-name" contains a hyphen but should still get
	// a probe. The old heuristic would reject any value containing a hyphen.
	src := "service: my-service-name\n"
	f := valuesFile(src)
	scalars, err := source.ScalarsOf(f)
	if err != nil {
		t.Fatalf("ScalarsOf error: %v", err)
	}
	if len(scalars) == 0 {
		t.Fatal("no scalars found")
	}
	// Find the non-key scalar
	var s source.Scalar
	for _, sc := range scalars {
		if !sc.IsKey {
			s = sc
			break
		}
	}
	if s.Start == 0 && s.End == 0 {
		t.Fatal("no non-key scalar found")
	}

	got, ok := ProbeBytes(f, s.Start, s.End)
	if !ok {
		t.Fatal("ProbeBytes returned !ok for scalar with hyphens")
	}
	want := "service: \"" + Canary + "\"\n"
	if string(got) != want {
		t.Fatalf("probe = %q, want %q", got, want)
	}
	// Verify the result is valid YAML
	var result interface{}
	if err := yaml.Unmarshal(got, &result); err != nil {
		t.Fatalf("probe output is not valid YAML: %v", err)
	}
}

func TestProbeBytesRefusesSpansThatDontMatchParsedElements(t *testing.T) {
	// A span that doesn't exactly match a parsed scalar or key block must be refused.
	// This prevents guessing at partial or off-by-one inputs.
	src := "replicas: 3\n"
	f := valuesFile(src)
	// Span that spans from the colon into the value (doesn't match either scalar or key)
	if _, ok := ProbeBytes(f, 8, 11); ok {
		t.Fatal("want !ok for span that doesn't match a parsed element")
	}
}

func TestProbeBytesRefusesWhatItCannotPerturb(t *testing.T) {
	// No probe means no proof of execution, which must leave the mutant Survived
	// rather than let it be called equivalent on weaker evidence.
	f := templateFile("metadata:\n  name: plain\n")
	if _, ok := ProbeBytes(f, 0, 8); ok {
		t.Fatal("want !ok for template text outside any action")
	}
	g := valuesFile("- item\n")
	if _, ok := ProbeBytes(g, 0, 6); ok {
		t.Fatal("want !ok for a values span with no key to rewrite")
	}
}

// TestProbeBytesStaysInsideAShortCircuitedOperand pins the bytes the probe writes
// for the case that matters most: `and` stops at its first falsey operand, so a
// probe that replaced the whole pipeline would error on reaching the action and
// prove nothing about the operand the mutation actually touched.
func TestProbeBytesStaysInsideAShortCircuitedOperand(t *testing.T) {
	src := `{{- if and .Values.a (eq .Values.b "x") }}y{{- end }}`
	f := templateFile(src)
	off := strings.Index(src, `"x"`)
	got, ok := ProbeBytes(f, off, off+len(`"x"`))
	if !ok {
		t.Fatal("ProbeBytes returned !ok")
	}
	want := `{{- if and .Values.a (fail "` + Canary + `") }}y{{- end }}`
	if string(got) != want {
		t.Fatalf("probe =\n%q\nwant\n%q", got, want)
	}
}

// TestProbeBytesRefusesABareShortCircuitedOperand: with nothing to isolate the
// operand, any probe would be wider than the mutation, so there is no probe at
// all and the mutant must stay Survived.
func TestProbeBytesRefusesABareShortCircuitedOperand(t *testing.T) {
	src := `{{- if or .Values.a .Values.b }}y{{- end }}`
	f := templateFile(src)
	off := strings.Index(src, ".Values.b")
	if _, ok := ProbeBytes(f, off, off+len(".Values.b")); ok {
		t.Fatal("want !ok: a bare operand of a short circuit cannot be probed in isolation")
	}
}
