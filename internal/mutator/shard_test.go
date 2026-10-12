package mutator

import (
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"testing"
)

func numberedPlans(n int) []Plan {
	out := make([]Plan, n)
	for i := range out {
		out[i] = Plan{ID: fmt.Sprintf("p%04d", i), Start: i}
	}
	return out
}

func planIDs(ps []Plan) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.ID
	}
	return out
}

// legacyCap is Cap as it was before sharding existed. A seed is a user-facing
// contract — "--max-mutants 50 --seed 7" must keep naming the same 50 mutants
// across releases — so the refactor onto CapAndShard must not change its output.
func legacyCap(plans []Plan, n int, seed int64) ([]Plan, int) {
	if n <= 0 || len(plans) <= n {
		return plans, 0
	}
	idx := make([]int, len(plans))
	for i := range idx {
		idx[i] = i
	}
	rng := rand.New(rand.NewSource(seed))
	rng.Shuffle(len(idx), func(i, j int) { idx[i], idx[j] = idx[j], idx[i] })
	chosen := idx[:n]
	sort.Ints(chosen)
	kept := make([]Plan, 0, n)
	for _, i := range chosen {
		kept = append(kept, plans[i])
	}
	return kept, len(plans) - n
}

func TestCapIsUnchangedBySharding(t *testing.T) {
	plans := numberedPlans(97)
	for _, n := range []int{0, 1, 10, 50, 96, 97, 200} {
		for _, seed := range []int64{1, 7, 42} {
			want, wantDropped := legacyCap(plans, n, seed)
			got, dropped := Cap(plans, n, seed)
			if !slices.Equal(planIDs(got), planIDs(want)) || dropped != wantDropped {
				t.Errorf("Cap(n=%d, seed=%d) changed: got %d kept/%d dropped, want %d/%d",
					n, seed, len(got), dropped, len(want), wantDropped)
			}
		}
	}
}

// Shards are only safe to merge if they partition exactly what an unsharded run
// would have evaluated: an overlap double-counts a mutant, a gap hides one.
func TestShardsPartitionTheCappedPlanExactly(t *testing.T) {
	plans := numberedPlans(97)
	for _, maxMutants := range []int{0, 40} {
		whole, wantCapped := Cap(plans, maxMutants, 3)
		for m := 1; m <= 7; m++ {
			var union []string
			seen := map[string]bool{}
			minSize, maxSize := len(plans), 0
			for k := 1; k <= m; k++ {
				kept, capped, other := CapAndShard(plans, maxMutants, k, m, 3)
				again, _, _ := CapAndShard(plans, maxMutants, k, m, 3)
				if !slices.Equal(planIDs(kept), planIDs(again)) {
					t.Fatalf("shard %d/%d is not deterministic", k, m)
				}
				if capped != wantCapped {
					t.Errorf("shard %d/%d: capped = %d, want %d", k, m, capped, wantCapped)
				}
				if len(kept)+other != len(whole) {
					t.Errorf("shard %d/%d: kept %d + other %d != %d", k, m, len(kept), other, len(whole))
				}
				for _, p := range kept {
					if seen[p.ID] {
						t.Errorf("shard %d/%d: %s is in two shards", k, m, p.ID)
					}
					seen[p.ID] = true
					union = append(union, p.ID)
				}
				if !slices.IsSortedFunc(kept, func(a, b Plan) int { return a.Start - b.Start }) {
					t.Errorf("shard %d/%d is not in report order", k, m)
				}
				minSize, maxSize = min(minSize, len(kept)), max(maxSize, len(kept))
			}
			sort.Strings(union)
			if !slices.Equal(union, planIDs(whole)) {
				t.Errorf("max=%d m=%d: union of shards != unsharded selection", maxMutants, m)
			}
			if maxSize-minSize > 1 {
				t.Errorf("max=%d m=%d: shard sizes range %d..%d, want within 1", maxMutants, m, minSize, maxSize)
			}
		}
	}
}

// A shard of a file-sorted plan list taken as a contiguous block would hand one
// runner every mutant of the slowest template. The stride runs over the shuffled
// order, so each shard samples the whole chart.
func TestShardsSampleTheWholeChart(t *testing.T) {
	plans := numberedPlans(400)
	kept, _, _ := CapAndShard(plans, 0, 1, 4, 1)
	if kept[0].Start > 50 || kept[len(kept)-1].Start < 350 {
		t.Errorf("shard 1/4 spans %d..%d; want it spread across 0..399",
			kept[0].Start, kept[len(kept)-1].Start)
	}
}
