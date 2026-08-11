package report

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mmpyro/helm-mutation-test/internal/config"
	"github.com/mmpyro/helm-mutation-test/internal/model"
)

// Filenames written into --report-dir, one per format.
const (
	FileJSON     = "mutation-report.json"
	FileStryker  = "mutation-report.stryker.json"
	FileHTML     = "mutation-report.html"
	FileMarkdown = "mutation-report.md"
	FileJUnit    = "mutation-report.junit.xml"
)

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
		path := filepath.Join(cfg.ReportDir, j.name)
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return written, fmt.Errorf("writing %s: %w", path, err)
		}
		written = append(written, Written{Format: j.format, Path: path})
	}
	return written, nil
}
