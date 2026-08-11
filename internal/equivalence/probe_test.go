package equivalence

import (
	"strings"
	"testing"

	"github.com/mmpyro/helm-mutation-test/internal/source"
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
