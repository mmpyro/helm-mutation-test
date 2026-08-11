package report

import (
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/mmpyro/helm-mutation-test/internal/model"
)

// JUnit renders one testcase per mutant so any CI system displays the results
// natively, without needing to understand mutation testing.
//
// The mapping inverts the usual sense on purpose:
//
//   - Survived becomes a <failure>. A survivor is the actionable defect, so it is
//     what should turn a CI job red.
//   - Killed becomes a passing case: the tests did their job.
//   - NoCoverage, Invalid, Equivalent, Timeout and Error become <skipped>, because
//     none of them says anything about assertion quality and none should fail a
//     build.
func JUnit(run *model.Run) ([]byte, error) {
	byFile := map[string][]model.Mutant{}
	var files []string
	for _, m := range run.Mutants {
		if _, seen := byFile[m.File]; !seen {
			files = append(files, m.File)
		}
		byFile[m.File] = append(byFile[m.File], m)
	}

	suites := junitSuites{
		Name:  "helm-mutation-test:" + run.ChartName,
		Tests: len(run.Mutants),
	}
	for _, file := range files {
		mutants := byFile[file]
		ts := junitSuite{
			Name:     file,
			Tests:    len(mutants),
			Hostname: "localhost",
		}
		for _, m := range mutants {
			tc := junitCase{
				Name:      fmt.Sprintf("%s at %s:%d [%s]", m.Mutator, m.File, m.Line, m.ID),
				ClassName: file,
				Time:      fmt.Sprintf("%.4f", m.Duration.Seconds()),
			}
			switch m.Status {
			case model.StatusSurvived:
				ts.Failures++
				tc.Failure = &junitFailure{
					Type:    "SurvivedMutant",
					Message: fmt.Sprintf("no test noticed this change to %s:%d", m.File, m.Line),
					Text:    survivorDetail(m),
				}
			case model.StatusKilled:
				// Nothing to add: a killed mutant is a passing testcase.
			default:
				ts.Skipped++
				tc.Skipped = &junitSkipped{Message: skipReason(m)}
			}
			ts.Cases = append(ts.Cases, tc)
		}
		suites.Failures += ts.Failures
		suites.Skipped += ts.Skipped
		suites.Suites = append(suites.Suites, ts)
	}
	// Truncation has to be visible here too. Prepended rather than appended so a
	// CI UI that lists suites in order shows it before the results it qualifies.
	if run.Capped > 0 {
		suites.Suites = append([]junitSuite{cappedNotice(run)}, suites.Suites...)
		suites.Tests++
		suites.Skipped++
	}
	// A skipped equivalence check must be visible too, or "equivalent 0" in the
	// tally reads as "checked, found none" rather than "never looked".
	if !run.EquivalenceChecked {
		suites.Suites = append([]junitSuite{equivalenceCheckSkippedNotice()}, suites.Suites...)
		suites.Tests++
		suites.Skipped++
	}

	suites.Time = fmt.Sprintf("%.4f", run.Duration.Seconds())

	body, err := xml.MarshalIndent(suites, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding JUnit report: %w", err)
	}
	return append([]byte(xml.Header), append(body, '\n')...), nil
}

func survivorDetail(m model.Mutant) string {
	b := &strings.Builder{}
	fmt.Fprintf(b, "mutator: %s\n", m.Mutator)
	fmt.Fprintf(b, "location: %s:%d:%d\n\n", m.File, m.Line, m.Column)
	before, after := diffLines(m)
	fmt.Fprintf(b, "- %s\n+ %s\n\n", before, after)
	fmt.Fprintf(b, "%s\n", coverageNote(m))
	if len(m.CoveringSuites) > 0 {
		fmt.Fprintf(b, "suites run: %s\n", strings.Join(m.CoveringSuites, ", "))
	}
	b.WriteString("\nAdd an assertion that distinguishes the original from the mutation.\n")
	return b.String()
}

func skipReason(m model.Mutant) string {
	switch m.Status {
	case model.StatusNoCoverage:
		return fmt.Sprintf("no suite renders %s, so this mutation was never run", m.File)
	case model.StatusEquivalent:
		return "equivalent: the mutation cannot change any rendered manifest, so no assertion could catch it"
	case model.StatusInvalid:
		return "the mutation stopped the chart rendering, so it grades nothing: " + firstLine(m.Detail)
	case model.StatusTimeout:
		return "evaluation timed out: " + firstLine(m.Detail)
	case model.StatusError:
		return "the tool failed on this mutant: " + firstLine(m.Detail)
	default:
		return string(m.Status)
	}
}

// cappedNotice makes --max-mutants truncation visible in the XML.
//
// Every other format names what it excluded. Without this, a capped run's JUnit
// output is indistinguishable from a complete one, so a CI UI showing only these
// results would present a sample as full coverage.
//
// It is counted in the top-level tests and skipped attributes, so those stay
// equal to the sum over child testsuites.
func cappedNotice(run *model.Run) junitSuite {
	return junitSuite{
		Name:     "--max-mutants",
		Tests:    1,
		Skipped:  1,
		Hostname: "localhost",
		Cases: []junitCase{{
			Name:      "truncated run",
			ClassName: "--max-mutants",
			Time:      "0.0000",
			Skipped: &junitSkipped{Message: fmt.Sprintf(
				"%d of %d generated mutants were not evaluated (--max-mutants); "+
					"this run is a sample, not full coverage",
				run.Capped, run.Generated)},
		}},
	}
}

// equivalenceCheckSkippedNotice makes a skipped equivalence pass visible in the
// XML, the same way cappedNotice does for --max-mutants: without it, a CI UI
// showing only this report has no way to tell "0 equivalent because none exist"
// from "0 equivalent because nobody checked".
func equivalenceCheckSkippedNotice() junitSuite {
	return junitSuite{
		Name:     "--no-equivalence-check",
		Tests:    1,
		Skipped:  1,
		Hostname: "localhost",
		Cases: []junitCase{{
			Name:      "equivalence check skipped",
			ClassName: "--no-equivalence-check",
			Time:      "0.0000",
			Skipped: &junitSkipped{Message: "the equivalence check did not run " +
				"(--no-equivalence-check); some survivors may be unkillable"},
		}},
	}
}

type junitSuites struct {
	XMLName  xml.Name     `xml:"testsuites"`
	Name     string       `xml:"name,attr"`
	Tests    int          `xml:"tests,attr"`
	Failures int          `xml:"failures,attr"`
	Skipped  int          `xml:"skipped,attr"`
	Time     string       `xml:"time,attr"`
	Suites   []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name     string      `xml:"name,attr"`
	Tests    int         `xml:"tests,attr"`
	Failures int         `xml:"failures,attr"`
	Skipped  int         `xml:"skipped,attr"`
	Hostname string      `xml:"hostname,attr"`
	Cases    []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	ClassName string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
	Skipped   *junitSkipped `xml:"skipped,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Text    string `xml:",chardata"`
}

type junitSkipped struct {
	Message string `xml:"message,attr"`
}
