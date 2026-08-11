package report

import (
	"fmt"
	"strings"

	"github.com/mmpyro/helm-mutation-test/internal/model"
)

// maxMarkdownSurvivors bounds the survivor list. A chart with hundreds of
// survivors would otherwise blow past GitHub's step-summary size limit, and the
// count is reported so the truncation is never mistaken for completeness.
const maxMarkdownSurvivors = 40

// Markdown renders a summary sized for a CI job summary or a PR comment.
func Markdown(run *model.Run) ([]byte, error) {
	b := &strings.Builder{}

	fmt.Fprintf(b, "## Mutation score: %.1f%%\n\n", run.Score())
	fmt.Fprintf(b, "`%s` — %s, %s\n\n", run.ChartName,
		plural(len(run.Suites), "suite", "suites"), plural(run.TestCount, "test", "tests"))

	if run.Threshold > 0 {
		if run.MeetsThreshold() {
			fmt.Fprintf(b, "✅ Meets the %.1f%% threshold.\n\n", run.Threshold)
		} else {
			fmt.Fprintf(b, "❌ **Below the %.1f%% threshold.**\n\n", run.Threshold)
		}
	}

	b.WriteString("| Outcome | Count | |\n|---|---:|---|\n")
	rows := []struct {
		label, note string
		count       int
	}{
		{"Killed", "a test caught the mutation", run.Tally.Killed},
		{"Survived", "**no test noticed** — a missing assertion", run.Tally.Survived},
		{"No coverage", "no suite renders the mutated template", run.Tally.NoCoverage},
		{"Equivalent", "cannot change any rendered manifest — unkillable", run.Tally.Equivalent},
		{"Invalid", "broke rendering, so it grades nothing", run.Tally.Invalid},
		{"Timeout", "exceeded the per-mutant timeout", run.Tally.Timeout},
		{"Error", "the tool itself failed", run.Tally.Errored},
	}
	for _, r := range rows {
		if r.count == 0 && r.label != "Killed" && r.label != "Survived" {
			continue // keep the table short; zero-count noise adds nothing
		}
		fmt.Fprintf(b, "| %s | %d | %s |\n", r.label, r.count, r.note)
	}
	fmt.Fprintf(b, "\nScore counts only killed and survived mutants: %d of %d.\n\n",
		run.Tally.Scored(), run.Tally.Total())

	if !run.EquivalenceChecked {
		b.WriteString("> The equivalence check was skipped (`--no-equivalence-check`); " +
			"some survivors may be unkillable.\n\n")
	}

	if run.Capped > 0 {
		fmt.Fprintf(b, "> ⚠️ `--max-mutants` ran %d of %d generated mutants; %d were not evaluated.\n\n",
			len(run.Mutants), run.Generated, run.Capped)
	}

	writeMarkdownBreakdown(b, "Score by mutator", run.ByMutator())
	writeMarkdownBreakdown(b, "Score by file", run.ByFile())
	writeMarkdownSurvivors(b, run)
	writeMarkdownNoCoverage(b, run)

	if len(run.SkippedFiles) > 0 {
		b.WriteString("### Partially analysed\n\n")
		for _, s := range run.SkippedFiles {
			fmt.Fprintf(b, "- `%s` — %s\n", s.File, firstLine(s.Reason))
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(b, "<sub>%d mutants in %s · baseline %s</sub>\n",
		len(run.Mutants), short(run.Duration), short(run.BaselineDuration))
	return []byte(b.String()), nil
}

func writeMarkdownBreakdown(b *strings.Builder, title string, rows []model.Breakdown) {
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(b, "### %s\n\n| Name | Killed | Survived | Score |\n|---|---:|---:|---:|\n", title)
	for _, r := range rows {
		score := fmt.Sprintf("%.1f%%", r.Score)
		if r.Tally.Scored() == 0 {
			score = "—"
		}
		fmt.Fprintf(b, "| `%s` | %d | %d | %s |\n", r.Name, r.Tally.Killed, r.Tally.Survived, score)
	}
	b.WriteString("\n")
}

func writeMarkdownSurvivors(b *strings.Builder, run *model.Run) {
	survivors := run.Survived()
	if len(survivors) == 0 {
		b.WriteString("### Survived mutants\n\nNone — every covered mutation was caught.\n\n")
		return
	}

	shown := survivors
	if len(shown) > maxMarkdownSurvivors {
		shown = shown[:maxMarkdownSurvivors]
	}
	fmt.Fprintf(b, "### Survived mutants (%d)\n\n", len(survivors))
	b.WriteString("Each of these changed the chart without any test noticing.\n\n")
	b.WriteString("<details>\n<summary>Show survivors</summary>\n\n")
	for _, m := range shown {
		before, after := diffLines(m)
		fmt.Fprintf(b, "**`%s:%d`** · `%s`\n\n```diff\n- %s\n+ %s\n```\n\n",
			m.File, m.Line, m.Mutator, before, after)
	}
	if len(survivors) > len(shown) {
		fmt.Fprintf(b, "_%d further survivors omitted; see the JSON or HTML report._\n\n",
			len(survivors)-len(shown))
	}
	b.WriteString("</details>\n\n")
}

func writeMarkdownNoCoverage(b *strings.Builder, run *model.Run) {
	gaps := run.NoCoverage()
	if len(gaps) == 0 {
		return
	}
	counts := map[string]int{}
	var files []string
	for _, m := range gaps {
		if counts[m.File] == 0 {
			files = append(files, m.File)
		}
		counts[m.File]++
	}
	b.WriteString("### Templates no suite renders\n\n")
	for _, f := range files {
		fmt.Fprintf(b, "- `%s` — %d mutants never run\n", f, counts[f])
	}
	b.WriteString("\n")
}
