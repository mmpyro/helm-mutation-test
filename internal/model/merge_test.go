package model

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// splitRun builds count shards of a 9-mutant run (1 capped), dealing mutants
// round-robin the way the stride does.
func splitRun(count int) []*Run {
	statuses := []Status{StatusKilled, StatusSurvived, StatusKilled, StatusInvalid,
		StatusKilled, StatusNoCoverage, StatusSurvived, StatusKilled}
	shards := make([]*Run, count)
	for k := range shards {
		shards[k] = &Run{
			ChartName:          "sample",
			ChartPath:          "./sample",
			Mutators:           []string{"bool-flip", "cond-negate"},
			Generated:          9,
			Capped:             1,
			EquivalenceChecked: true,
			Duration:           time.Duration(k+1) * time.Second,
			SkippedFiles:       []SkippedFile{{File: "templates/odd.tpl", Reason: "parse"}},
			Shard:              &Shard{Index: k + 1, Count: count, OtherShards: 0, Seed: 1},
		}
	}
	for i, st := range statuses {
		m := Mutant{ID: fmt.Sprintf("m%d", i), File: "templates/a.yaml", StartByte: i, Mutator: "bool-flip", Status: st}
		s := shards[i%count]
		s.Mutants = append(s.Mutants, m)
		s.Tally.Add(st)
	}
	for _, s := range shards {
		s.Shard.OtherShards = len(statuses) - len(s.Mutants)
	}
	return shards
}

func TestMergeRecomputesTallyAndScore(t *testing.T) {
	shards := splitRun(3)
	// Hand shards over out of order: merge must not depend on argument order.
	merged, err := Merge([]*Run{shards[2], shards[0], shards[1]}, 50)
	if err != nil {
		t.Fatal(err)
	}
	want := Tally{Killed: 4, Survived: 2, Invalid: 1, NoCoverage: 1}
	if merged.Tally != want {
		t.Errorf("tally = %+v, want %+v", merged.Tally, want)
	}
	if got := merged.Score(); got < 66.6 || got > 66.7 {
		t.Errorf("score = %v, want 66.67", got)
	}
	var ids []string
	for _, m := range merged.Mutants {
		ids = append(ids, m.ID)
	}
	if !slices.Equal(ids, []string{"m0", "m1", "m2", "m3", "m4", "m5", "m6", "m7"}) {
		t.Errorf("mutants not restored to plan order: %v", ids)
	}
	if merged.Threshold != 50 || !merged.MeetsThreshold() {
		t.Errorf("threshold %v should be taken from merge and met", merged.Threshold)
	}
	if merged.Duration != 3*time.Second {
		t.Errorf("duration = %v, want the slowest shard's 3s", merged.Duration)
	}
	if len(merged.SkippedFiles) != 1 {
		t.Errorf("skipped files not deduplicated: %v", merged.SkippedFiles)
	}
}

// The merged report is what CI grades, so it must not keep claiming to be a slice.
func TestMergedRunIsNotASlice(t *testing.T) {
	merged, err := Merge(splitRun(2), 0)
	if err != nil {
		t.Fatal(err)
	}
	if merged.IsSlice() {
		t.Error("merged run still reports itself as a slice")
	}
}

// Each refusal guards against a merged report that silently covers less than
// the run it claims to be.
func TestMergeRefusesAnIncompleteShardSet(t *testing.T) {
	tests := []struct {
		name    string
		shards  func() []*Run
		wantErr string
	}{
		{"missing shard", func() []*Run { s := splitRun(3); return []*Run{s[0], s[2]} }, "missing shard 2 of 3"},
		{"duplicate shard", func() []*Run { s := splitRun(2); return []*Run{s[0], s[1], s[1]} }, "given twice"},
		{"unsharded report", func() []*Run { s := splitRun(2); s[1].Shard = nil; return s }, "not a shard"},
		{"different seeds", func() []*Run { s := splitRun(2); s[1].Shard.Seed = 7; return s }, "--seed 1 vs 7"},
		{"different counts", func() []*Run { s := splitRun(2); s[1].Shard.Count = 3; return s }, "--shard-count"},
		{"different charts", func() []*Run { s := splitRun(2); s[1].ChartName = "other"; return s }, "chart"},
		{"different generated", func() []*Run { s := splitRun(2); s[1].Generated = 10; return s }, "generated"},
		{"different caps", func() []*Run { s := splitRun(2); s[1].Capped = 0; return s }, "capped"},
		{"different mutators", func() []*Run { s := splitRun(2); s[1].Mutators = []string{"bool-flip"}; return s }, "mutators"},
		{"index out of range", func() []*Run { s := splitRun(2); s[1].Shard.Index = 3; return s }, "outside 1..2"},
		{"duplicate mutant", func() []*Run {
			s := splitRun(2)
			s[1].Mutants = append(s[1].Mutants, s[0].Mutants[0])
			return s
		}, "appears in shard"},
		{"shard lost mutants", func() []*Run {
			s := splitRun(2)
			s[1].Mutants = s[1].Mutants[1:]
			return s
		}, "hold 7 mutants, but the run selected 8"},
		{"nothing to merge", func() []*Run { return nil }, "no shard reports"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Merge(tc.shards(), 0)
			if err == nil {
				t.Fatalf("expected an error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}
