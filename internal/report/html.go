package report

import (
	"bytes"
	_ "embed"
	"fmt"
	"strings"

	"github.com/mmpyro/helm-mutation-test/internal/model"
)

//go:embed report.html.tmpl
var htmlShell string

// HTML renders a self-contained page around the mutation-testing-elements
// report.
//
// The interactive viewer is the published web component, loaded from a CDN, so
// the payload here is just the standard-schema JSON plus a plain-HTML summary.
// The summary matters: it means the page still communicates the result when the
// CDN is unreachable or scripts are blocked, which is the normal state of an
// artefact opened out of a CI run.
func HTML(run *model.Run, chartDir string) ([]byte, error) {
	payload, err := Stryker(run, chartDir)
	if err != nil {
		return nil, err
	}

	replacements := map[string]string{
		"{{CHART}}":     escapeHTML(run.ChartName),
		"{{SCORE}}":     fmt.Sprintf("%.1f", run.Score()),
		"{{BAND}}":      scoreBand(run.Score()),
		"{{SUMMARY}}":   summaryTable(run),
		"{{SURVIVORS}}": survivorList(run),
		"{{REPORT}}":    embedJSON(payload),
	}
	out := htmlShell
	for k, v := range replacements {
		out = strings.ReplaceAll(out, k, v)
	}
	return []byte(out), nil
}

func scoreBand(score float64) string {
	switch {
	case score >= scoreGood:
		return "good"
	case score >= scoreOkay:
		return "okay"
	default:
		return "poor"
	}
}

// embedJSON prepares a JSON payload for inlining in a <script> element.
//
// encoding/json escapes <, > and & to <, > and & inside strings by
// default, so a "</script>" appearing in chart source cannot terminate the
// element early. The belt-and-braces replacement below therefore normally matches
// nothing; it exists so the guarantee does not rest on a default that a future
// switch to a custom Encoder (SetEscapeHTML(false)) would quietly remove.
func embedJSON(b []byte) string {
	s := string(bytes.TrimSpace(b))
	s = strings.ReplaceAll(s, "<", `<`)
	s = strings.ReplaceAll(s, ">", `>`)
	s = strings.ReplaceAll(s, "&", `&`)
	return s
}

func summaryTable(run *model.Run) string {
	b := &strings.Builder{}
	rows := []struct {
		label, note string
		count       int
		class       string
	}{
		{"Killed", "a test caught the mutation", run.Tally.Killed, "killed"},
		{"Survived", "no test noticed — a missing assertion", run.Tally.Survived, "survived"},
		{"No coverage", "no suite renders the mutated template", run.Tally.NoCoverage, "muted"},
		{"Equivalent", "cannot change any rendered manifest — unkillable", run.Tally.Equivalent, "muted"},
		{"Invalid", "broke rendering, so it grades nothing", run.Tally.Invalid, "muted"},
		{"Timeout", "exceeded the per-mutant timeout", run.Tally.Timeout, "muted"},
		{"Error", "the tool itself failed", run.Tally.Errored, "muted"},
	}
	for _, r := range rows {
		if r.count == 0 && r.class == "muted" {
			continue
		}
		fmt.Fprintf(b, `<tr class="%s"><th>%s</th><td>%d</td><td>%s</td></tr>`,
			r.class, escapeHTML(r.label), r.count, escapeHTML(r.note))
	}
	fmt.Fprintf(b, `<tr class="total"><th>Scored</th><td>%d</td><td>of %d mutants; only killed and survived count</td></tr>`,
		run.Tally.Scored(), run.Tally.Total())
	if !run.EquivalenceChecked {
		fmt.Fprint(b, `<tr class="warn"><th>Equivalence</th><td>-</td>`+
			`<td>check skipped (--no-equivalence-check); some survivors may be unkillable</td></tr>`)
	}
	if run.EquivalenceUnchecked > 0 {
		// The pass having run is not the same as the pass having concluded.
		fmt.Fprintf(b, `<tr class="warn"><th>Not checked</th><td>%d</td>`+
			`<td>survivors the equivalence check could not reach a verdict on; `+
			`each one's reason is in its detail</td></tr>`, run.EquivalenceUnchecked)
	}
	if run.Capped > 0 {
		fmt.Fprintf(b, `<tr class="warn"><th>Not evaluated</th><td>%d</td><td>--max-mutants ran %d of %d generated</td></tr>`,
			run.Capped, len(run.Mutants), run.Generated)
	}
	return b.String()
}

func survivorList(run *model.Run) string {
	survivors := run.Survived()
	if len(survivors) == 0 {
		return `<p class="none">None — every covered mutation was caught.</p>`
	}
	b := &strings.Builder{}
	fmt.Fprintf(b, "<p>%d mutations changed the chart without any test noticing.</p>", len(survivors))
	b.WriteString("<ol class=\"survivors\">")
	for _, m := range survivors {
		before, after := diffLines(m)
		fmt.Fprintf(b, `<li><code class="loc">%s:%d</code> <span class="mut">%s</span><pre><span class="del">- %s</span>
<span class="add">+ %s</span></pre><p class="note">%s</p></li>`,
			escapeHTML(m.File), m.Line, escapeHTML(m.Mutator),
			escapeHTML(before), escapeHTML(after), escapeHTML(coverageNote(m)))
	}
	b.WriteString("</ol>")
	return b.String()
}

func escapeHTML(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&#39;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
