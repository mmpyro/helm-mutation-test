package runner

import (
	"testing"

	"github.com/mmpyro/helm-mutation-test/internal/model"
)

func key(name string) SuiteKey { return SuiteKey{File: "tests/x_test.yaml", Name: name} }

func TestClassify(t *testing.T) {
	tests := []struct {
		name     string
		outcomes []SuiteOutcome
		want     model.Status
		wantRun  int
	}{
		{
			name: "a failed assertion is a kill",
			outcomes: []SuiteOutcome{{Key: key("s"), Tests: []TestOutcome{
				{Name: "t1", Passed: false, FailedAsserts: []AssertOutcome{{Type: "equal"}}},
			}}},
			want:    model.StatusKilled,
			wantRun: 1,
		},
		{
			name: "everything passing is a survivor",
			outcomes: []SuiteOutcome{{Key: key("s"), Passed: true, Tests: []TestOutcome{
				{Name: "t1", Passed: true}, {Name: "t2", Passed: true},
			}}},
			want:    model.StatusSurvived,
			wantRun: 2,
		},
		{
			// The judgement that keeps the score honest: a mutation that stops the
			// chart rendering is "caught" by every test regardless of what they
			// assert, so it says nothing about test quality.
			name: "a render error is Invalid, not a kill",
			outcomes: []SuiteOutcome{{Key: key("s"), Tests: []TestOutcome{
				{Name: "t1", RenderError: "parse error at :4"},
			}}},
			want:    model.StatusInvalid,
			wantRun: 1,
		},
		{
			name: "a real kill outranks a render error elsewhere",
			outcomes: []SuiteOutcome{{Key: key("s"), Tests: []TestOutcome{
				{Name: "t1", RenderError: "boom"},
				{Name: "t2", FailedAsserts: []AssertOutcome{{Type: "isKind"}}},
			}}},
			want:    model.StatusKilled,
			wantRun: 2,
		},
		{
			name:     "a suite setup failure is our Error, not the chart's",
			outcomes: []SuiteOutcome{{Key: key("s"), SetupError: "cannot read snapshot cache"}},
			want:     model.StatusError,
			wantRun:  0,
		},
		{
			name:     "nothing ran means no coverage",
			outcomes: nil,
			want:     model.StatusNoCoverage,
			wantRun:  0,
		},
		{
			name: "only skipped tests means no coverage",
			outcomes: []SuiteOutcome{{Key: key("s"), Tests: []TestOutcome{
				{Name: "t1", Skipped: true},
			}}},
			want:    model.StatusNoCoverage,
			wantRun: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, killedBy, run := Classify(tc.outcomes)
			if got != tc.want {
				t.Errorf("status = %q, want %q", got, tc.want)
			}
			if run != tc.wantRun {
				t.Errorf("testsRun = %d, want %d", run, tc.wantRun)
			}
			if tc.want == model.StatusKilled && len(killedBy) == 0 {
				t.Error("a Killed mutant must carry kill attribution")
			}
			if tc.want != model.StatusKilled && len(killedBy) != 0 {
				t.Errorf("only a Killed mutant may carry attribution, got %+v", killedBy)
			}
		})
	}
}

func TestClassifyCollectsFullAttribution(t *testing.T) {
	outcomes := []SuiteOutcome{
		{Key: SuiteKey{File: "tests/a_test.yaml", Name: "suite a"}, Tests: []TestOutcome{
			{Name: "checks replicas", FailedAsserts: []AssertOutcome{
				{Index: 2, Type: "equal", FailInfo: "expected 2, got 3"},
			}},
		}},
		{Key: SuiteKey{File: "tests/b_test.yaml", Name: "suite b"}, Tests: []TestOutcome{
			{Name: "checks kind", FailedAsserts: []AssertOutcome{{Index: 0, Type: "isKind"}}},
		}},
	}
	status, killedBy, _ := Classify(outcomes)
	if status != model.StatusKilled {
		t.Fatalf("status = %q", status)
	}
	if len(killedBy) != 2 {
		t.Fatalf("got %d attributions, want 2", len(killedBy))
	}
	first := killedBy[0]
	if first.Suite != "suite a" || first.Test != "checks replicas" ||
		first.AssertType != "equal" || first.AssertIndex != 2 ||
		first.FailInfo != "expected 2, got 3" || first.SuiteFile != "tests/a_test.yaml" {
		t.Errorf("attribution lost detail: %+v", first)
	}
}

func TestRenderErrorDetail(t *testing.T) {
	outcomes := []SuiteOutcome{
		{Key: key("s"), Tests: []TestOutcome{{Name: "t", RenderError: "the real error"}}},
	}
	if got := RenderErrorDetail(outcomes); got != "the real error" {
		t.Errorf("got %q", got)
	}
	// Falls back to a suite-level setup error.
	if got := RenderErrorDetail([]SuiteOutcome{{Key: key("s"), SetupError: "setup"}}); got != "setup" {
		t.Errorf("got %q", got)
	}
	if got := RenderErrorDetail(nil); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestJoinLinesTruncates(t *testing.T) {
	// Assertion failure text can be an entire rendered manifest.
	many := make([]string, 20)
	for i := range many {
		many[i] = "line"
	}
	got := joinLines(many)
	if n := len(splitLines(got)); n != 7 {
		t.Errorf("got %d lines, want 6 plus an ellipsis", n)
	}
	if got := joinLines([]string{"a", "b"}); got != "a\nb" {
		t.Errorf("got %q", got)
	}
	if got := joinLines(nil); got != "" {
		t.Errorf("got %q", got)
	}
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, c := range s {
		if c == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(c)
	}
	return append(out, cur)
}
