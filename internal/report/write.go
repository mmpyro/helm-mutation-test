package report

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mmpyro/helm-mutation-test/internal/config"
	"github.com/mmpyro/helm-mutation-test/internal/model"
)

// Filenames written into --report-dir, one per format. A shard inserts its
// position after the stem; see FileName.
const (
	fileStem     = "mutation-report"
	FileJSON     = fileStem + ".json"
	FileStryker  = fileStem + ".stryker.json"
	FileHTML     = fileStem + ".html"
	FileMarkdown = fileStem + ".md"
	FileJUnit    = fileStem + ".junit.xml"
)

// FileName is the name run's report is written under: name itself, or for a
// shard "mutation-report.shard-K-of-N" plus name's extension. Shards of one run
// often land in a single directory — CI artifact steps unpack every job into one
// folder — and identical names would silently overwrite each other there. The
// count is in the name too, so a leftover file from a differently split run is
// visible at a glance rather than only when merge refuses it.
func FileName(run *model.Run, name string) string {
	if !run.IsSlice() {
		return name
	}
	return fmt.Sprintf("%s.shard-%d-of-%d%s", fileStem, run.Shard.Index, run.Shard.Count,
		strings.TrimPrefix(name, fileStem))
}

// Written records an emitted report file so the CLI can tell the user where to look.
type Written struct {
	Format config.ReportFormat
	Path   string
}

// WriteFiles emits every requested file format. Console is not handled here: it
// goes to the terminal, not to disk.
//
// The HTML format also writes its standard-schema JSON alongside it, because that
// file is useful on its own — it is what other mutation-testing tooling consumes.
func WriteFiles(run *model.Run, cfg *config.Config) ([]Written, error) {
	if !cfg.NeedsReportDir() {
		return nil, nil
	}
	if err := os.MkdirAll(cfg.ReportDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating report directory %s: %w", cfg.ReportDir, err)
	}

	type job struct {
		format config.ReportFormat
		name   string
		render func() ([]byte, error)
	}
	jobs := []job{
		{config.ReportJSON, FileJSON, func() ([]byte, error) { return JSON(run) }},
		{config.ReportMarkdown, FileMarkdown, func() ([]byte, error) { return Markdown(run) }},
		{config.ReportJUnit, FileJUnit, func() ([]byte, error) { return JUnit(run) }},
		{config.ReportHTML, FileStryker, func() ([]byte, error) { return Stryker(run, run.ChartPath) }},
		{config.ReportHTML, FileHTML, func() ([]byte, error) { return HTML(run, run.ChartPath) }},
	}

	var written []Written
	for _, j := range jobs {
		if !cfg.WantsReport(j.format) {
			continue
		}
		content, err := j.render()
		if err != nil {
			return written, err
		}
		path := filepath.Join(cfg.ReportDir, FileName(run, j.name))
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return written, fmt.Errorf("writing %s: %w", path, err)
		}
		written = append(written, Written{Format: j.format, Path: path})
	}
	return written, nil
}
