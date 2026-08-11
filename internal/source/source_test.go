package source

import (
	"strings"
	"testing"
)

func mk(t *testing.T, content string) *File {
	t.Helper()
	return New([]byte(content), "/tmp/x.yaml", "templates/x.yaml", KindTemplate)
}

func TestPositionAndOffsetRoundTrip(t *testing.T) {
	f := mk(t, "line one\nline two\nline three")
	tests := []struct {
		offset    int
		line, col int
	}{
		{0, 1, 1},
		{7, 1, 8},
		{8, 1, 9},  // the '\n' itself belongs to line 1
		{9, 2, 1},  // first byte of line 2
		{18, 3, 1}, // first byte of line 3
	}
	for _, tc := range tests {
		line, col := f.Position(tc.offset)
		if line != tc.line || col != tc.col {
			t.Errorf("Position(%d) = (%d,%d), want (%d,%d)", tc.offset, line, col, tc.line, tc.col)
		}
		got, err := f.Offset(tc.line, tc.col)
		if err != nil {
			t.Fatalf("Offset(%d,%d): %v", tc.line, tc.col, err)
		}
		if got != tc.offset {
			t.Errorf("Offset(%d,%d) = %d, want %d", tc.line, tc.col, got, tc.offset)
		}
	}
}

func TestPositionClampsOutOfRange(t *testing.T) {
	// A parser reporting a position at or past EOF must not crash a run.
	f := mk(t, "abc")
	if line, col := f.Position(-5); line != 1 || col != 1 {
		t.Errorf("Position(-5) = (%d,%d), want (1,1)", line, col)
	}
	if line, _ := f.Position(9999); line != 1 {
		t.Errorf("Position past EOF should clamp to the last line, got line %d", line)
	}
}

func TestOffsetRejectsOutOfRange(t *testing.T) {
	f := mk(t, "abc\ndef")
	for _, tc := range []struct{ line, col int }{{0, 1}, {99, 1}, {1, 0}, {2, 99}} {
		if _, err := f.Offset(tc.line, tc.col); err == nil {
			t.Errorf("Offset(%d,%d) should have failed", tc.line, tc.col)
		}
	}
}

func TestLineTextHandlesCRLFAndFinalLine(t *testing.T) {
	f := mk(t, "alpha\r\nbeta\r\ngamma")
	for i, want := range []string{"alpha", "beta", "gamma"} {
		if got := f.LineText(i + 1); got != want {
			t.Errorf("LineText(%d) = %q, want %q", i+1, got, want)
		}
	}
	if got := f.LineText(99); got != "" {
		t.Errorf("out-of-range LineText should be empty, got %q", got)
	}
}

func TestLineCountCountsFinalUnterminatedLine(t *testing.T) {
	if got := mk(t, "a\nb\nc").LineCount(); got != 3 {
		t.Errorf("LineCount() = %d, want 3", got)
	}
	if got := mk(t, "a\nb\n").LineCount(); got != 3 {
		t.Errorf("trailing newline yields an empty final line: got %d, want 3", got)
	}
}

func TestApplyDoesNotMutateReceiver(t *testing.T) {
	// One File is the source for every mutant of it, so Apply must be pure.
	const original = "replicas: 3\n"
	f := mk(t, original)
	got := string(f.Apply(10, 11, "4"))
	if got != "replicas: 4\n" {
		t.Errorf("Apply() = %q, want %q", got, "replicas: 4\n")
	}
	if f.Text() != original {
		t.Errorf("Apply mutated the receiver: %q", f.Text())
	}
}

func TestApplyClampsBadRanges(t *testing.T) {
	f := mk(t, "abc")
	if got := string(f.Apply(-5, 99, "X")); got != "X" {
		t.Errorf("Apply(-5,99) = %q, want %q", got, "X")
	}
	// An inverted range collapses to a zero-width insertion at start, replacing nothing.
	if got := string(f.Apply(2, 1, "X")); got != "abXc" {
		t.Errorf("inverted range should insert at start without deleting: got %q, want %q", got, "abXc")
	}
}

func TestMutatedLine(t *testing.T) {
	f := mk(t, "spec:\n  replicas: 3\n  other: x\n")
	start, _ := f.Offset(2, 13) // the '3'
	if got := f.MutatedLine(start, start+1, "4"); got != "  replicas: 4" {
		t.Errorf("MutatedLine() = %q, want %q", got, "  replicas: 4")
	}
}

func TestMutatedLineSpanningMultipleLines(t *testing.T) {
	f := mk(t, "a: 1\nb: 2\nc: 3\n")
	start, _ := f.Offset(1, 4)
	end, _ := f.Offset(3, 4)
	got := f.MutatedLine(start, end, "X")
	if !strings.Contains(got, "X") || !strings.HasPrefix(got, "a: ") {
		t.Errorf("MutatedLine across lines = %q, want it to start at line 1 and contain the replacement", got)
	}
}

func TestSliceClamps(t *testing.T) {
	f := mk(t, "hello")
	if got := f.Slice(1, 3); got != "el" {
		t.Errorf("Slice(1,3) = %q, want %q", got, "el")
	}
	if got := f.Slice(-1, 99); got != "hello" {
		t.Errorf("Slice(-1,99) = %q, want %q", got, "hello")
	}
}
