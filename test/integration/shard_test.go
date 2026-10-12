//go:build integration

package integration

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// shardArgs selects a 200-mutant sample of the weak suite: both kills and
// survivors to grade (a 60-mutant sample happened to contain no kills at all),
// while the cap still exercises cap-then-shard.
var shardArgs = []string{fixture, "-f", "tests/weak_test.yaml", "--max-mutants", "200"}

// shardFile is the name shard k of 3 writes its JSON report under.
func shardFile(k int) string { return fmt.Sprintf("mutation-report.shard-%d-of-3.json", k) }

// shardsOnce runs every shard of a 3-way split once per process. The results
// are shared read-only, like the strong and weak runs. Every shard writes into
// the same directory, as they do when CI unpacks all jobs' artifacts into one
// folder: their reports must not overwrite each other there.
var shardsOnce = sync.OnceValues(func() ([]result, error) {
	var out []result
	for k := 1; k <= 3; k++ {
		r, err := invoke(filepath.Join(sharedDir, "shards"), append(slices.Clone(shardArgs),
			"--shard-index", fmt.Sprint(k), "--shard-count", "3", "--report", "console,json"))
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
})

// runShards returns each shard's JSON report path, after checking every shard
// ran and labelled itself as a slice.
func runShards(t *testing.T) []string {
	t.Helper()
	shards, err := shardsOnce()
	if err != nil {
		t.Fatalf("shard runs failed: %v", err)
	}
	var paths []string
	for i, r := range shards {
		k := i + 1
		if r.ExitCode != 0 {
			t.Fatalf("shard %d: exit = %d\n%s", k, r.ExitCode, r.Stderr)
		}
		if j := r.jsonFile(t, shardFile(k)); j.Shard == nil || j.Shard.Index != k || j.Shard.Count != 3 {
			t.Fatalf("shard %d: JSON shard object = %+v", k, j.Shard)
		}
		if !strings.Contains(r.Console(), "not a score") {
			t.Errorf("shard %d console does not say it is a slice:\n%s", k, r.Console())
		}
		paths = append(paths, filepath.Join(r.Dir, shardFile(k)))
	}
	return paths
}

// The whole point of merge: CI grades the merged report, so it has to be
// indistinguishable from the run one machine would have produced.
func TestMergedShardsEqualTheUnshardedRun(t *testing.T) {
	whole := run(t, append(slices.Clone(shardArgs), "--report", "json")...).JSON(t)
	merged := run(t, append([]string{"merge"}, append(runShards(t), "--report", "json")...)...)
	if merged.ExitCode != 0 {
		t.Fatalf("merge: exit = %d\n%s", merged.ExitCode, merged.Stderr)
	}
	m := merged.JSON(t)

	if m.Shard != nil {
		t.Errorf("merged report still carries a shard object: %+v", m.Shard)
	}
	if m.Tally != whole.Tally || m.Score != whole.Score {
		t.Errorf("merged tally %+v (score %v) != unsharded %+v (score %v)", m.Tally, m.Score, whole.Tally, whole.Score)
	}
	if !slices.Equal(mutantKeys(m), mutantKeys(whole)) {
		t.Errorf("merged mutants differ from the unsharded run")
	}
	if m.Generated != whole.Generated || m.Capped != whole.Capped {
		t.Errorf("merged generated/capped %d/%d != unsharded %d/%d", m.Generated, m.Capped, whole.Generated, whole.Capped)
	}
}

func TestShardingExitCodes(t *testing.T) {
	shards := runShards(t)
	// Thresholds straddle the measured merged score rather than guessing at it.
	score := run(t, append([]string{"merge", "--report", "json"}, shards...)...).JSON(t).Score
	if score <= 0 || score >= 100 {
		t.Fatalf("merged score %v leaves no room for a threshold on either side", score)
	}
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"threshold on a shard is refused",
			append(slices.Clone(shardArgs), "--shard-index", "1", "--shard-count", "3", "--threshold", "10"), 2},
		{"merge with a shard missing is refused",
			[]string{"merge", shards[0], shards[2]}, 2},
		{"merge of a non-report file is refused",
			[]string{"merge", filepath.Join(repoRoot, fixture, "Chart.yaml")}, 2},
		{"merged score below the threshold fails",
			append([]string{"merge", "--threshold", fmt.Sprint(min(score+1, 100))}, shards...), 1},
		// Just under, not at: the JSON score is rounded and may sit above the real one.
		{"merged score above the threshold passes",
			append([]string{"merge", "--threshold", fmt.Sprint(score - 0.01)}, shards...), 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := run(t, tc.args...)
			if r.ExitCode != tc.want {
				t.Fatalf("exit = %d, want %d\nstdout:\n%s\nstderr:\n%s", r.ExitCode, tc.want, r.Stdout, r.Stderr)
			}
		})
	}
}
