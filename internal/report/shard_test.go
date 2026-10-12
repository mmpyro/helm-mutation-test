package report

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mmpyro/helm-mutation-test/internal/config"
	"github.com/mmpyro/helm-mutation-test/internal/model"
)

func shardedRun() *model.Run {
	run := sampleRun()
	run.Threshold = 0 // config refuses a threshold on a shard
	run.Shard = &model.Shard{Index: 2, Count: 4, OtherShards: 21, Seed: 1}
	return run
}

// renderAll renders every one of the five formats, keyed by name.
func renderAll(t *testing.T, run *model.Run) map[string]string {
	t.Helper()
	var console bytes.Buffer
	if err := Console(&console, run, false); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{"console": console.String()}
	for name, render := range map[string]func() ([]byte, error){
		"json":     func() ([]byte, error) { return JSON(run) },
		"markdown": func() ([]byte, error) { return Markdown(run) },
		"junit":    func() ([]byte, error) { return JUnit(run) },
		"html":     func() ([]byte, error) { return HTML(run, run.ChartPath) },
	} {
		b, err := render()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out[name] = string(b)
	}
	return out
}

// A shard's score covers a slice of the mutants. Every format must say so, or a
// reader handed one shard's artefact grades the chart on a quarter of it.
func TestEveryFormatNamesTheShard(t *testing.T) {
	for name, out := range renderAll(t, shardedRun()) {
		if name == "json" {
			if !strings.Contains(out, `"shard": {`) || !strings.Contains(out, `"otherShards": 21`) {
				t.Errorf("json has no shard object:\n%s", out)
			}
			continue
		}
		if !strings.Contains(out, "shard 2 of 4") {
			t.Errorf("%s does not name the shard", name)
		}
		if !strings.Contains(out, "not a score") {
			t.Errorf("%s does not say the slice is not a score", name)
		}
		if !strings.Contains(out, "helm mutation-test merge") {
			t.Errorf("%s does not say how to get the real score", name)
		}
	}
}

// The cap notice counts the whole run's selection, not this shard's slice of it,
// so its numbers still add up: kept + dropped = generated.
func TestCapNoticeOnAShardCountsTheWholeSelection(t *testing.T) {
	run := shardedRun() // the shard holds 7 mutants
	run.Generated, run.Capped = 30, 2
	for name, out := range renderAll(t, run) {
		if name == "json" || name == "junit" {
			continue // they carry generated/capped as raw numbers
		}
		if !strings.Contains(out, "28 of 30") {
			t.Errorf("%s cap notice does not say 28 of 30 were run:\n%s", name, out)
		}
	}
}

func TestUnshardedReportsDoNotMentionShards(t *testing.T) {
	for name, out := range renderAll(t, sampleRun()) {
		if strings.Contains(strings.ToLower(out), "shard") {
			t.Errorf("%s mentions shards on an unsharded run", name)
		}
	}
}

// merge reads shards back from their JSON reports, so every field Merge relies
// on must survive the round trip — a dropped field would merge as its zero value.
func TestReadJSONRoundTrips(t *testing.T) {
	want := shardedRun()
	want.EquivalenceUnchecked = 3
	b, err := JSON(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	// Run durations are stored at millisecond resolution.
	want.Duration = want.Duration.Truncate(time.Millisecond)
	want.BaselineDuration = want.BaselineDuration.Truncate(time.Millisecond)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip changed the run:\n got %+v\nwant %+v", got, want)
	}
}

func TestReadJSONRefusesOtherFiles(t *testing.T) {
	stryker, err := Stryker(sampleRun(), "testdata/chart")
	if err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string][]byte{
		"stryker report": stryker,
		"not json":       []byte("<testsuites/>"),
	} {
		if _, err := ReadJSON(in); err == nil {
			t.Errorf("%s: ReadJSON accepted it", name)
		}
	}
}

// Shards of one run commonly land in the same directory — CI artifact steps
// unpack every job into one folder — so a shard's files must not collide with
// each other's, nor with the plain-named report merge writes beside them.
func TestShardReportFilesAreNamedForTheShard(t *testing.T) {
	cfg := config.Defaults()
	cfg.ReportDir = t.TempDir()
	cfg.Reports = []config.ReportFormat{
		config.ReportJSON, config.ReportHTML, config.ReportMarkdown, config.ReportJUnit,
	}

	written, err := WriteFiles(shardedRun(), &cfg)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, w := range written {
		got = append(got, filepath.Base(w.Path))
	}
	want := []string{
		"mutation-report.shard-2-of-4.json",
		"mutation-report.shard-2-of-4.md",
		"mutation-report.shard-2-of-4.junit.xml",
		"mutation-report.shard-2-of-4.stryker.json",
		"mutation-report.shard-2-of-4.html",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("shard wrote %v, want %v", got, want)
	}
}
