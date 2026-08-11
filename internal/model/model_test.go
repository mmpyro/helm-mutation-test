package model

import (
	"math"
	"testing"
)

func TestStatusCountsTowardScore(t *testing.T) {
	// Only informative statuses may reach the score. Invalid mutants in particular
	// are killed by every test, so counting them would inflate the number.
	in := []Status{StatusKilled, StatusSurvived}
	out := []Status{StatusNoCoverage, StatusInvalid, StatusTimeout, StatusError}
	for _, s := range in {
		if !s.CountsTowardScore() {
			t.Errorf("%s should count toward the score", s)
		}
	}
	for _, s := range out {
		if s.CountsTowardScore() {
			t.Errorf("%s must not count toward the score", s)
		}
	}
}

func TestTallyScore(t *testing.T) {
	tests := []struct {
		name  string
		tally Tally
		want  float64
	}{
		{"nothing scored", Tally{NoCoverage: 5, Invalid: 3}, 0},
		{"all killed", Tally{Killed: 4}, 100},
		{"none killed", Tally{Survived: 4}, 0},
		{"half", Tally{Killed: 5, Survived: 5}, 50},
		{"noise excluded from denominator", Tally{Killed: 1, Survived: 1, Invalid: 98, NoCoverage: 50}, 50},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.tally.Score(); got != tc.want {
				t.Fatalf("Score() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTallyTotalAndScored(t *testing.T) {
	tl := Tally{Killed: 1, Survived: 2, NoCoverage: 3, Invalid: 4, Timeout: 5, Errored: 6}
	if got := tl.Total(); got != 21 {
		t.Errorf("Total() = %d, want 21", got)
	}
	if got := tl.Scored(); got != 3 {
		t.Errorf("Scored() = %d, want 3", got)
	}
}

func TestComputeTally(t *testing.T) {
	r := Run{Mutants: []Mutant{
		{Status: StatusKilled}, {Status: StatusKilled},
		{Status: StatusSurvived}, {Status: StatusInvalid}, {Status: StatusNoCoverage},
	}}
	r.ComputeTally()
	if r.Tally.Killed != 2 || r.Tally.Survived != 1 || r.Tally.Invalid != 1 || r.Tally.NoCoverage != 1 {
		t.Fatalf("unexpected tally: %+v", r.Tally)
	}
	if got := r.Score(); math.Abs(got-66.6667) > 0.001 {
		t.Fatalf("Score() = %v, want ~66.67", got)
	}
}

func TestMeetsThreshold(t *testing.T) {
	tests := []struct {
		name      string
		threshold float64
		killed    int
		survived  int
		want      bool
	}{
		{"threshold disabled", 0, 0, 10, true},
		{"exactly at threshold passes", 50, 1, 1, true},
		{"above threshold", 50, 9, 1, true},
		{"below threshold", 90, 1, 1, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := Run{Threshold: tc.threshold}
			for range tc.killed {
				r.Mutants = append(r.Mutants, Mutant{Status: StatusKilled})
			}
			for range tc.survived {
				r.Mutants = append(r.Mutants, Mutant{Status: StatusSurvived})
			}
			r.ComputeTally()
			if got := r.MeetsThreshold(); got != tc.want {
				t.Fatalf("MeetsThreshold() = %v, want %v (score %.1f)", got, tc.want, r.Score())
			}
		})
	}
}

func TestGroupByOrdersWeakestFirst(t *testing.T) {
	r := Run{Mutants: []Mutant{
		{Mutator: "good", File: "a.yaml", Status: StatusKilled},
		{Mutator: "good", File: "a.yaml", Status: StatusKilled},
		{Mutator: "bad", File: "b.yaml", Status: StatusSurvived},
		{Mutator: "mid", File: "c.yaml", Status: StatusKilled},
		{Mutator: "mid", File: "c.yaml", Status: StatusSurvived},
	}}
	byMutator := r.ByMutator()
	want := []string{"bad", "mid", "good"} // 0%, 50%, 100%
	if len(byMutator) != len(want) {
		t.Fatalf("got %d groups, want %d", len(byMutator), len(want))
	}
	for i, name := range want {
		if byMutator[i].Name != name {
			t.Fatalf("group %d = %q, want %q (order should be ascending score)", i, byMutator[i].Name, name)
		}
	}
	if got := len(r.ByFile()); got != 3 {
		t.Fatalf("ByFile() returned %d groups, want 3", got)
	}
}

func TestWithStatusFilters(t *testing.T) {
	r := Run{Mutants: []Mutant{
		{ID: "1", Status: StatusSurvived},
		{ID: "2", Status: StatusKilled},
		{ID: "3", Status: StatusNoCoverage},
		{ID: "4", Status: StatusSurvived},
	}}
	if got := r.Survived(); len(got) != 2 || got[0].ID != "1" || got[1].ID != "4" {
		t.Fatalf("Survived() = %+v", got)
	}
	if got := r.NoCoverage(); len(got) != 1 || got[0].ID != "3" {
		t.Fatalf("NoCoverage() = %+v", got)
	}
}
