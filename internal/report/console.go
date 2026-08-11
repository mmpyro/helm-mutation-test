// Package report renders a mutation run in each supported output format.
//
// Every format is a pure function of model.Run, which keeps them golden-testable
// and means adding a format never touches the execution path.
package report

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/mmpyro/helm-mutation-test/internal/model"
)

// Console writes the human-facing summary.
//
// The ordering is deliberate: the score first, then where the gaps are, then the
// individual survivors. A survived mutant is the actionable output of this whole
// tool, so each one is shown as a diff at a file:line a reader can go and fix.
func Console(w io.Writer, run *model.Run, colour bool) error {
	c := palette(colour)
	b := &strings.Builder{}

	fmt.Fprintf(b, "\nMutation testing %s  (%s, %s, baseline %s)\n\n",
		c.bold(run.ChartName),
		plural(len(run.Suites), "suite", "suites"),
		plural(run.TestCount, "test", "tests"),
		short(run.BaselineDuration))

	writeScore(b, run, c)
	writeBreakdowns(b, run, c)
	writeSurvivors(b, run, c)
	writeNoCoverage(b, run, c)
	writeEquivalent(b, run, c)
	writeInvalid(b, run, c)
	writeSkippedFiles(b, run, c)
	writeFooter(b, run, c)

	_, err := io.WriteString(w, b.String())
	return err
}

func writeScore(b *strings.Builder, run *model.Run, c colours) {
	score := run.Score()
	fmt.Fprintf(b, "  Score  %s  %s   (%d killed / %d survived)\n",
		c.forScore(score, fmt.Sprintf("%5.1f%%", score)),
		bar(score, 26, c),
		run.Tally.Killed, run.Tally.Survived)

	// Everything excluded from the score is listed explicitly, so the number is
	// never mistaken for "all mutants were caught".
	var excluded []string
	if run.Tally.NoCoverage > 0 {
		excluded = append(excluded, fmt.Sprintf("%d no-coverage", run.Tally.NoCoverage))
	}
	if run.Tally.Equivalent > 0 {
		excluded = append(excluded, fmt.Sprintf("%d equivalent", run.Tally.Equivalent))
	}
	if run.Tally.Invalid > 0 {
		excluded = append(excluded, fmt.Sprintf("%d invalid", run.Tally.Invalid))
	}
	if run.Tally.Timeout > 0 {
		excluded = append(excluded, fmt.Sprintf("%d timeout", run.Tally.Timeout))
	}
	if run.Tally.Errored > 0 {
		excluded = append(excluded, fmt.Sprintf("%d error", run.Tally.Errored))
	}
	if len(excluded) > 0 {
		fmt.Fprintf(b, "  %s\n", c.faint("not scored: "+strings.Join(excluded, " · ")))
	}
	if !run.EquivalenceChecked {
		fmt.Fprintf(b, "  %s\n", c.faint(
			"equivalence check skipped (--no-equivalence-check): some survivors may be unkillable"))
	}
	if run.EquivalenceUnchecked > 0 {
		// The pass having run is not the same as the pass having concluded.
		fmt.Fprintf(b, "  %s\n", c.warn(fmt.Sprintf(
			"%d survivors could not be checked for equivalence and may be unkillable; each one's reason is in its detail",
			run.EquivalenceUnchecked)))
	}
	if run.Capped > 0 {
		// Never let a truncated run read as full coverage.
		fmt.Fprintf(b, "  %s\n", c.warn(fmt.Sprintf(
			"only %d of %d generated mutants were run (--max-mutants); %d not evaluated",
			len(run.Mutants), run.Generated, run.Capped)))
	}
	b.WriteString("\n")
}

func writeBreakdowns(b *strings.Builder, run *model.Run, c colours) {
	sections := []struct {
		title string
		rows  []model.Breakdown
	}{
		{"By mutator", run.ByMutator()},
		{"By file", run.ByFile()},
	}
	for _, s := range sections {
		if len(s.rows) == 0 {
			continue
		}
		fmt.Fprintf(b, "  %-34s %7s %9s %8s\n", c.bold(s.title), "killed", "survived", "score")
		for _, row := range s.rows {
			note := ""
			// A row with nothing scored would otherwise show a meaningless 0.0%.
			if row.Tally.Scored() == 0 {
				note = c.faint("  not scored")
			}
			fmt.Fprintf(b, "    %-32s %7d %9d %7s%s\n",
				truncate(row.Name, 32), row.Tally.Killed, row.Tally.Survived,
				scoreCell(row, c), note)
		}
		b.WriteString("\n")
	}
}

func scoreCell(row model.Breakdown, c colours) string {
	if row.Tally.Scored() == 0 {
		return c.faint("   —")
	}
	return c.forScore(row.Score, fmt.Sprintf("%5.1f%%", row.Score))
}

// diffLines renders a mutation's before/after for display.
//
// A deletion has no "after" text, so the added side becomes an explicit note
// rather than an empty line — and for a multi-line block it says how much went,
// since only the first deleted line is shown.
func diffLines(m model.Mutant) (before, after string) {
	before = strings.TrimRight(m.OriginalLine, " ")
	if m.MutatedLine != "" {
		return before, strings.TrimRight(m.MutatedLine, " ")
	}
	lines := strings.Count(strings.TrimRight(m.Original, "\n"), "\n") + 1
	if lines > 1 {
		return before, fmt.Sprintf("(%d lines removed)", lines)
	}
	return before, "(line removed)"
}

func writeSurvivors(b *strings.Builder, run *model.Run, c colours) {
	survivors := run.Survived()
	if len(survivors) == 0 {
		return
	}
	fmt.Fprintf(b, "  %s\n\n", c.bold(fmt.Sprintf("SURVIVED (%d)", len(survivors))))

	// Group by file so a reader fixes one file at a time.
	byFile := map[string][]model.Mutant{}
	var files []string
	for _, m := range survivors {
		if _, seen := byFile[m.File]; !seen {
			files = append(files, m.File)
		}
		byFile[m.File] = append(byFile[m.File], m)
	}
	sort.Strings(files)

	for _, file := range files {
		for _, m := range byFile[file] {
			before, after := diffLines(m)
			fmt.Fprintf(b, "  %s  %s\n", c.bold(fmt.Sprintf("%s:%d", m.File, m.Line)), c.faint(m.Mutator))
			fmt.Fprintf(b, "    %s\n", c.removed("- "+before))
			fmt.Fprintf(b, "    %s\n", c.added("+ "+after))
			fmt.Fprintf(b, "    %s\n\n", c.faint(coverageNote(m)))
		}
	}
}

// coverageNote explains what did run, so a survivor is not confused with an
// untested one.
func coverageNote(m model.Mutant) string {
	switch {
	case m.TestsRun == 0:
		return "no tests ran"
	case len(m.CoveringSuites) == 1:
		return fmt.Sprintf("ran %s in %s — all passed",
			plural(m.TestsRun, "test", "tests"), m.CoveringSuites[0])
	default:
		return fmt.Sprintf("ran %s across %s — all passed",
			plural(m.TestsRun, "test", "tests"),
			plural(len(m.CoveringSuites), "suite file", "suite files"))
	}
}

func writeNoCoverage(b *strings.Builder, run *model.Run, c colours) {
	gaps := run.NoCoverage()
	if len(gaps) == 0 {
		return
	}
	// Count per file: "no suite renders this template" is a per-file finding, and
	// listing every mutant would bury it.
	counts := map[string]int{}
	var files []string
	for _, m := range gaps {
		if counts[m.File] == 0 {
			files = append(files, m.File)
		}
		counts[m.File]++
	}
	sort.Strings(files)

	fmt.Fprintf(b, "  %s\n", c.bold(fmt.Sprintf("NO COVERAGE (%d)", len(gaps))))
	for _, f := range files {
		fmt.Fprintf(b, "    %s %s\n", f,
			c.faint(fmt.Sprintf("— no suite renders this template (%d mutants never run)", counts[f])))
	}
	b.WriteString("\n")
}

// writeEquivalent lists mutants no assertion could ever catch. They are not
// work items, so they are shown compactly — the point is to account for them,
// not to send anyone chasing them.
func writeEquivalent(b *strings.Builder, run *model.Run, c colours) {
	eq := run.Equivalent()
	if len(eq) == 0 {
		return
	}
	fmt.Fprintf(b, "  %s  %s\n", c.bold("Equivalent"),
		c.faint("cannot change any rendered manifest; not scored"))
	for _, m := range eq {
		fmt.Fprintf(b, "    %s:%d:%d  %s\n", m.File, m.Line, m.Column, c.faint(m.Mutator))
	}
	b.WriteString("\n")
}

func writeInvalid(b *strings.Builder, run *model.Run, c colours) {
	if run.Tally.Invalid == 0 {
		return
	}
	fmt.Fprintf(b, "  %s\n", c.bold(fmt.Sprintf("INVALID (%d)", run.Tally.Invalid)))
	fmt.Fprintf(b, "    %s\n", c.faint(
		"these mutations stopped the chart rendering, so every test caught them"))
	fmt.Fprintf(b, "    %s\n\n", c.faint(
		"they measure nothing about test quality and are excluded from the score"))
}

func writeSkippedFiles(b *strings.Builder, run *model.Run, c colours) {
	if len(run.SkippedFiles) == 0 {
		return
	}
	fmt.Fprintf(b, "  %s\n", c.warn(fmt.Sprintf("PARTIALLY ANALYSED (%d)", len(run.SkippedFiles))))
	for _, s := range run.SkippedFiles {
		fmt.Fprintf(b, "    %s %s\n", s.File, c.faint("— "+firstLine(s.Reason)))
	}
	b.WriteString("\n")
}

func writeFooter(b *strings.Builder, run *model.Run, c colours) {
	fmt.Fprintf(b, "  %s\n", c.faint(fmt.Sprintf("%d mutants in %s",
		len(run.Mutants), short(run.Duration))))

	if run.Threshold > 0 {
		if run.MeetsThreshold() {
			fmt.Fprintf(b, "  %s\n", c.pass(fmt.Sprintf(
				"score %.1f%% meets the %.1f%% threshold", run.Score(), run.Threshold)))
		} else {
			fmt.Fprintf(b, "  %s\n", c.fail(fmt.Sprintf(
				"score %.1f%% is below the %.1f%% threshold", run.Score(), run.Threshold)))
		}
	}
	b.WriteString("\n")
}

func bar(score float64, width int, c colours) string {
	filled := int(score/100*float64(width) + 0.5)
	filled = max(min(filled, width), 0)
	return c.forScore(score, strings.Repeat("█", filled)) + c.faint(strings.Repeat("░", width-filled))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return "…" + s[len(s)-n+1:]
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
