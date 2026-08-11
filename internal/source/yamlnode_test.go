package source

import (
	"testing"
)

func values(t *testing.T, content string) *File {
	t.Helper()
	return New([]byte(content), "/tmp/values.yaml", "values.yaml", KindValues)
}

func TestScalarsOfResolvesSpansAndTags(t *testing.T) {
	src := `replicaCount: 3
enabled: true
name: myapp
quoted: "hello"
single: 'world'
ratio: 1.5
empty:
`
	f := values(t, src)
	got, err := ScalarsOf(f)
	if err != nil {
		t.Fatal(err)
	}

	// Index the value scalars by path for readable assertions.
	byPath := map[string]Scalar{}
	for _, s := range got {
		if !s.IsKey {
			byPath[s.Path] = s
		}
	}

	tests := []struct {
		path     string
		tag      string
		wantText string
	}{
		{"replicaCount", "!!int", "3"},
		{"enabled", "!!bool", "true"},
		{"name", "!!str", "myapp"},
		{"quoted", "!!str", `"hello"`}, // span must include the quotes
		{"single", "!!str", `'world'`}, // ditto
		{"ratio", "!!float", "1.5"},
	}
	for _, tc := range tests {
		s, ok := byPath[tc.path]
		if !ok {
			t.Errorf("no scalar found for %q", tc.path)
			continue
		}
		if s.Tag != tc.tag {
			t.Errorf("%s: tag = %q, want %q", tc.path, s.Tag, tc.tag)
		}
		if got := f.Slice(s.Start, s.End); got != tc.wantText {
			t.Errorf("%s: span text = %q, want %q", tc.path, got, tc.wantText)
		}
	}
}

func TestScalarsOfMarksKeys(t *testing.T) {
	// str-literal must not rename keys; that is yaml-key-delete's territory.
	f := values(t, "image:\n  repository: nginx\n")
	got, err := ScalarsOf(f)
	if err != nil {
		t.Fatal(err)
	}
	var keys, vals []string
	for _, s := range got {
		if s.IsKey {
			keys = append(keys, s.Value)
		} else {
			vals = append(vals, s.Value)
		}
	}
	if len(keys) != 2 || keys[0] != "image" || keys[1] != "repository" {
		t.Errorf("keys = %v, want [image repository]", keys)
	}
	if len(vals) != 1 || vals[0] != "nginx" {
		t.Errorf("values = %v, want [nginx]", vals)
	}
}

func TestScalarsOfBuildsPaths(t *testing.T) {
	f := values(t, "image:\n  tag: v1\nports:\n  - 80\n  - 443\n")
	got, err := ScalarsOf(f)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"image.tag": false, "ports[0]": false, "ports[1]": false}
	for _, s := range got {
		if !s.IsKey {
			if _, tracked := want[s.Path]; tracked {
				want[s.Path] = true
			}
		}
	}
	for path, found := range want {
		if !found {
			t.Errorf("expected a value scalar at path %q", path)
		}
	}
}

func TestScalarsOfIgnoresCommentsAndBlockScalars(t *testing.T) {
	src := `count: 5   # how many replicas
script: |
  line one
  line two
folded: >
  wrapped text
`
	f := values(t, src)
	got, err := ScalarsOf(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range got {
		if s.IsKey {
			continue
		}
		text := f.Slice(s.Start, s.End)
		switch s.Path {
		case "count":
			if text != "5" {
				t.Errorf("trailing comment leaked into the span: %q", text)
			}
		case "script", "folded":
			t.Errorf("block scalar %q should be skipped, got span %q", s.Path, text)
		}
	}
}

func TestScalarsOfSkipsFlowCollections(t *testing.T) {
	f := values(t, "list: [1, 2, 3]\nmap: {a: 1}\n")
	got, err := ScalarsOf(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range got {
		if text := f.Slice(s.Start, s.End); text == "[1, 2, 3]" || text == "{a: 1}" {
			t.Errorf("a flow collection was reported as a scalar: %q", text)
		}
	}
}

func TestScalarsOfRejectsInvalidYAML(t *testing.T) {
	if _, err := ScalarsOf(values(t, "a: [unclosed\n")); err == nil {
		t.Fatal("expected a YAML parse error")
	}
}

func TestScalarsOfHandlesEmptyFile(t *testing.T) {
	got, err := ScalarsOf(values(t, ""))
	if err != nil {
		t.Fatalf("an empty values.yaml is valid, got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no scalars, got %d", len(got))
	}
}

func TestBlockExtentCoversNestedBlock(t *testing.T) {
	src := `image:
  repository: nginx
  tag: v1
service:
  port: 80
`
	f := values(t, src)
	// Deleting the "image" key must take its two nested lines with it.
	start, end, ok := f.BlockExtent(1, 0)
	if !ok {
		t.Fatal("BlockExtent failed")
	}
	remaining := string(f.Apply(start, end, ""))
	want := "service:\n  port: 80\n"
	if remaining != want {
		t.Errorf("after deleting the image block:\ngot  %q\nwant %q", remaining, want)
	}
}

func TestBlockExtentSingleLine(t *testing.T) {
	f := values(t, "a: 1\nb: 2\nc: 3\n")
	start, end, ok := f.BlockExtent(2, 0)
	if !ok {
		t.Fatal("BlockExtent failed")
	}
	if got := string(f.Apply(start, end, "")); got != "a: 1\nc: 3\n" {
		t.Errorf("got %q, want %q", got, "a: 1\nc: 3\n")
	}
}

func TestBlockExtentStopsAtSameIndent(t *testing.T) {
	f := values(t, "  a: 1\n  b: 2\n")
	start, end, ok := f.BlockExtent(1, 2)
	if !ok {
		t.Fatal("BlockExtent failed")
	}
	if got := string(f.Apply(start, end, "")); got != "  b: 2\n" {
		t.Errorf("got %q, want %q", got, "  b: 2\n")
	}
}

func TestBlockExtentLastLineWithoutTrailingNewline(t *testing.T) {
	f := values(t, "a: 1\nb: 2")
	start, end, ok := f.BlockExtent(2, 0)
	if !ok {
		t.Fatal("BlockExtent failed")
	}
	if got := string(f.Apply(start, end, "")); got != "a: 1\n" {
		t.Errorf("got %q, want %q", got, "a: 1\n")
	}
}

func TestKeyBlocksOfYieldsDeletableEntries(t *testing.T) {
	src := `replicaCount: 1
image:
  repository: nginx
  tag: v1
`
	f := values(t, src)
	blocks, err := KeyBlocksOf(f)
	if err != nil {
		t.Fatal(err)
	}

	byPath := map[string]KeyBlock{}
	for _, b := range blocks {
		byPath[b.Path] = b
	}
	for _, path := range []string{"replicaCount", "image", "image.repository", "image.tag"} {
		if _, ok := byPath[path]; !ok {
			t.Errorf("expected a key block for %q", path)
		}
	}

	// Every block must delete to still-valid YAML.
	for path, b := range byPath {
		out := f.Apply(b.Start, b.End, "")
		if _, err := ScalarsOf(New(out, "/tmp/v.yaml", "values.yaml", KindValues)); err != nil {
			t.Errorf("deleting %q produced invalid YAML: %v\n%s", path, err, out)
		}
	}
}

func TestKeyBlocksOfNestedDeletionLeavesParentValid(t *testing.T) {
	f := values(t, "image:\n  repository: nginx\n  tag: v1\n")
	blocks, err := KeyBlocksOf(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range blocks {
		if b.Path != "image.repository" {
			continue
		}
		if got := string(f.Apply(b.Start, b.End, "")); got != "image:\n  tag: v1\n" {
			t.Errorf("got %q, want %q", got, "image:\n  tag: v1\n")
		}
		return
	}
	t.Fatal("no key block found for image.repository")
}
