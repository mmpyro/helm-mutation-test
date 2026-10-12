package model

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Merge recombines the shards of a split run into the run an unsharded
// invocation would have produced.
//
// Every check here guards the same failure: a merged report that is quietly
// missing mutants reads as full coverage. So an incomplete or inconsistent shard
// set is an error, never a best-effort partial merge — the last check, that the
// mutants add up to exactly what was considered minus what was capped, proves
// completeness end to end rather than trusting the shard metadata.
func Merge(shards []*Run, threshold float64) (*Run, error) {
	if len(shards) == 0 {
		return nil, fmt.Errorf("no shard reports to merge")
	}
	for i, s := range shards {
		if s.Shard == nil {
			return nil, fmt.Errorf("report %d (%s) is not a shard: it was run without --shard-count", i+1, s.ChartName)
		}
	}

	first := shards[0]
	count := first.Shard.Count
	for _, s := range shards[1:] {
		if err := sameRun(first, s); err != nil {
			return nil, fmt.Errorf("shard %d/%d and shard %d/%d are from different runs: %w",
				first.Shard.Index, count, s.Shard.Index, s.Shard.Count, err)
		}
	}
	if err := checkIndices(shards, count); err != nil {
		return nil, err
	}

	merged := &Run{
		ChartName:          first.ChartName,
		ChartPath:          first.ChartPath,
		Mutators:           slices.Clone(first.Mutators),
		Suites:             first.Suites,
		TestCount:          first.TestCount,
		Generated:          first.Generated,
		Capped:             first.Capped,
		Incremental:        first.Incremental,
		EquivalenceChecked: true,
		Threshold:          threshold,
	}
	seenMutant := map[string]int{}
	seenSkipped := map[string]bool{}
	for _, s := range shards {
		for _, m := range s.Mutants {
			if prev, dup := seenMutant[m.ID]; dup {
				return nil, fmt.Errorf("mutant %s appears in shard %d and shard %d", m.ID, prev, s.Shard.Index)
			}
			seenMutant[m.ID] = s.Shard.Index
			merged.Mutants = append(merged.Mutants, m)
		}
		for _, sk := range s.SkippedFiles {
			if !seenSkipped[sk.File] {
				seenSkipped[sk.File] = true
				merged.SkippedFiles = append(merged.SkippedFiles, sk)
			}
		}
		merged.EquivalenceChecked = merged.EquivalenceChecked && s.EquivalenceChecked
		merged.EquivalenceUnchecked += s.EquivalenceUnchecked
		// Shards run concurrently, so the run took as long as its slowest shard.
		merged.Duration = max(merged.Duration, s.Duration)
		merged.BaselineDuration = max(merged.BaselineDuration, s.BaselineDuration)
	}

	if want := merged.Considered() - merged.Capped; len(merged.Mutants) != want {
		return nil, fmt.Errorf("the %d shards hold %d mutants, but the run selected %d (%d considered, %d capped)",
			count, len(merged.Mutants), want, merged.Considered(), merged.Capped)
	}

	// Restore Generate's order — file, offset, mutator, ID — so a merged report
	// lists mutants exactly as an unsharded one does.
	sort.SliceStable(merged.Mutants, func(i, j int) bool {
		a, b := merged.Mutants[i], merged.Mutants[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.StartByte != b.StartByte {
			return a.StartByte < b.StartByte
		}
		if a.Mutator != b.Mutator {
			return a.Mutator < b.Mutator
		}
		return a.ID < b.ID
	})
	merged.ComputeTally()
	return merged, nil
}

// sameRun reports why two shards could not have come from one split run.
func sameRun(a, b *Run) error {
	switch {
	case a.Shard.Count != b.Shard.Count:
		return fmt.Errorf("--shard-count %d vs %d", a.Shard.Count, b.Shard.Count)
	case a.Shard.Seed != b.Shard.Seed:
		return fmt.Errorf("--seed %d vs %d", a.Shard.Seed, b.Shard.Seed)
	case a.ChartName != b.ChartName:
		return fmt.Errorf("chart %q vs %q", a.ChartName, b.ChartName)
	case !slices.Equal(a.Mutators, b.Mutators):
		return fmt.Errorf("mutators [%s] vs [%s]", strings.Join(a.Mutators, ","), strings.Join(b.Mutators, ","))
	case a.Generated != b.Generated:
		return fmt.Errorf("%d vs %d mutants generated; the chart or --include/--exclude differ", a.Generated, b.Generated)
	case a.Capped != b.Capped:
		return fmt.Errorf("%d vs %d mutants capped; --max-mutants differs", a.Capped, b.Capped)
	case (a.Incremental == nil) != (b.Incremental == nil):
		return fmt.Errorf("one was run with --since and the other was not")
	case a.Incremental != nil && (a.Incremental.BaseCommit != b.Incremental.BaseCommit ||
		a.Incremental.MutantsSkipped != b.Incremental.MutantsSkipped):
		return fmt.Errorf("--since selected different mutants (base %s vs %s)",
			a.Incremental.BaseCommit, b.Incremental.BaseCommit)
	}
	return nil
}

func checkIndices(shards []*Run, count int) error {
	have := map[int]bool{}
	for _, s := range shards {
		if s.Shard.Index < 1 || s.Shard.Index > count {
			return fmt.Errorf("shard index %d is outside 1..%d", s.Shard.Index, count)
		}
		if have[s.Shard.Index] {
			return fmt.Errorf("shard %d of %d was given twice", s.Shard.Index, count)
		}
		have[s.Shard.Index] = true
	}
	var missing []string
	for i := 1; i <= count; i++ {
		if !have[i] {
			missing = append(missing, fmt.Sprint(i))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing shard %s of %d: a merge without every shard is a slice, not a score",
			strings.Join(missing, ", "), count)
	}
	return nil
}
