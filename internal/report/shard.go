package report

import (
	"fmt"

	"github.com/mmpyro/helm-mutation-test/internal/model"
)

// sliceNotice is the sentence every format shows on a shard's report. A shard's
// score covers only its slice of the mutants, so the reader has to be told — in
// the same words everywhere — that the number is not the chart's score and how
// to get the one that is.
func sliceNotice(run *model.Run) string {
	s := run.Shard
	return fmt.Sprintf("shard %d of %d: %d of %d selected mutants evaluated here, %d in other shards; "+
		"this is a slice, not a score — merge every shard's JSON report with 'helm mutation-test merge' to grade",
		s.Index, s.Count, len(run.Mutants), len(run.Mutants)+s.OtherShards, s.OtherShards)
}

// scoreLabel names the headline number: a mutation score, or a slice score that
// must not be read as one.
func scoreLabel(run *model.Run) string {
	if run.IsSlice() {
		return fmt.Sprintf("slice score, shard %d of %d — not a mutation score", run.Shard.Index, run.Shard.Count)
	}
	return "mutation score"
}

// selectedCount is how many mutants the cap kept across the whole run. On a
// shard that is more than len(run.Mutants), and the --max-mutants notice must
// use it or its numbers stop adding up: "20 of 458 run, 398 not evaluated".
func selectedCount(run *model.Run) int { return run.Generated - run.Capped }
