package mutator

import (
	"fmt"
	"math/rand"
	"path"
	"sort"

	"github.com/mmpyro/helm-mutation-test/internal/source"
)

// Plan is a generated mutant: a candidate bound to the file it came from.
type Plan struct {
	ID      string
	Mutator string
	File    string
	// Line and Column are 1-based, at the start of the mutated span.
	Line, Column int
	Start, End   int
	Original     string
	Replacement  string
	OriginalLine string
	MutatedLine  string
}

// Apply produces the mutated file bytes for this plan.
func (p Plan) Apply(f *source.File) []byte { return f.Apply(p.Start, p.End, p.Replacement) }

// GenerateResult is the outcome of planning mutants for a chart.
type GenerateResult struct {
	Plans []Plan
	// Skipped records files we could not fully analyse, so reports can be honest
	// about incomplete coverage instead of implying everything was mutated.
	Skipped []SkippedFile
}

// SkippedFile is a file excluded from some or all mutation.
type SkippedFile struct {
	File   string
	Reason string
}

// Generate plans every mutant for the given files.
//
// Ordering is fully deterministic — file path, then byte offset, then mutator ID,
// then variant — so two runs over the same chart produce identical mutant lists
// and identical IDs. Reports and any --max-mutants sampling depend on that.
func Generate(files []*source.File, mutatorIDs []string) GenerateResult {
	var res GenerateResult
	selected := Select(mutatorIDs)

	sorted := make([]*source.File, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	for _, f := range sorted {
		if f.Kind == source.KindTemplate {
			if _, err := source.ParseTemplate(f); err != nil {
				// Not fatal: the line-based mutators still work, and a chart with one
				// exotic template should still get a score for everything else.
				res.Skipped = append(res.Skipped, SkippedFile{
					File:   f.Path,
					Reason: "template did not parse; AST-based mutators skipped: " + err.Error(),
				})
			}
		}

		var forFile []Plan
		for _, m := range selected {
			for _, c := range m.Mutate(f) {
				if c.Start == c.End && c.Replacement == "" {
					continue // a zero-width no-op cannot change anything
				}
				line, col := f.Position(c.Start)
				forFile = append(forFile, Plan{
					Mutator:      c.Mutator,
					File:         f.Path,
					Line:         line,
					Column:       col,
					Start:        c.Start,
					End:          c.End,
					Original:     f.Slice(c.Start, c.End),
					Replacement:  c.Replacement,
					OriginalLine: f.LineTextAt(c.Start),
					MutatedLine:  f.MutatedLine(c.Start, c.End, c.Replacement),
					ID:           mutantID(f.Path, c),
				})
			}
		}
		sort.SliceStable(forFile, func(i, j int) bool {
			if forFile[i].Start != forFile[j].Start {
				return forFile[i].Start < forFile[j].Start
			}
			if forFile[i].Mutator != forFile[j].Mutator {
				return forFile[i].Mutator < forFile[j].Mutator
			}
			return forFile[i].ID < forFile[j].ID
		})
		res.Plans = append(res.Plans, forFile...)
	}
	return res
}

// mutantID builds a stable, human-readable identifier. Including the offset and
// variant keeps it unique when one mutator produces several mutants at one site.
func mutantID(file string, c Candidate) string {
	base := fmt.Sprintf("%s:%d:%s", path.Base(file), c.Start, c.Mutator)
	if c.Note != "" {
		return base + ":" + c.Note
	}
	return base
}

// Cap limits the plan list to at most n mutants, sampling deterministically from
// the given seed. It returns the retained plans and how many were dropped.
//
// The drop count is returned rather than swallowed: a silently truncated run
// reads as full coverage, which would overstate what was actually measured.
func Cap(plans []Plan, n int, seed int64) (kept []Plan, dropped int) {
	if n <= 0 || len(plans) <= n {
		return plans, 0
	}
	// Sample by shuffling indices so the subset spans the whole chart rather than
	// stopping at the first n mutants of the first file.
	idx := make([]int, len(plans))
	for i := range idx {
		idx[i] = i
	}
	rng := rand.New(rand.NewSource(seed))
	rng.Shuffle(len(idx), func(i, j int) { idx[i], idx[j] = idx[j], idx[i] })

	chosen := idx[:n]
	sort.Ints(chosen) // restore report order
	kept = make([]Plan, 0, n)
	for _, i := range chosen {
		kept = append(kept, plans[i])
	}
	return kept, len(plans) - n
}
