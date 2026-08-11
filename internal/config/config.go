// Package config holds the CLI configuration and its validation.
package config

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
	"time"
)

// KillAttribution controls how much killing-test detail we collect.
type KillAttribution string

const (
	// AttributionFirst stops each mutant run at the first failing test. One kill is
	// enough to classify a mutant, so this is the fast default.
	AttributionFirst KillAttribution = "first"
	// AttributionAll runs every covering test to collect the complete kill set.
	AttributionAll KillAttribution = "all"
)

// ReportFormat identifies an output format.
type ReportFormat string

const (
	ReportConsole  ReportFormat = "console"
	ReportJSON     ReportFormat = "json"
	ReportHTML     ReportFormat = "html"
	ReportMarkdown ReportFormat = "markdown"
	ReportJUnit    ReportFormat = "junit"
)

// AllReportFormats is every supported --report value.
var AllReportFormats = []ReportFormat{
	ReportConsole, ReportJSON, ReportHTML, ReportMarkdown, ReportJUnit,
}

// DefaultIncludes are the chart files mutated when --include is not given.
var DefaultIncludes = []string{"templates/**", "values.yaml"}

// Config is the fully resolved configuration for one run.
type Config struct {
	ChartPath string

	// Pass-through to helm-unittest.
	TestFiles      []string
	ValuesFiles    []string
	WithSubChart   bool
	Strict         bool
	ChartTestsPath string

	// Mutation control.
	Mutators        []string
	ExcludeMutators []string
	Include         []string
	Exclude         []string
	MaxMutants      int
	Seed            int64
	// EquivalenceCheck re-renders each survivor to find mutations that cannot
	// change any manifest. They are reported as Equivalent and left out of the
	// score, which they would otherwise depress by an amount no test can fix.
	EquivalenceCheck bool
	Timeout          time.Duration

	// Execution.
	Parallel        int
	KillAttribution KillAttribution

	// Output.
	Reports     []ReportFormat
	ReportDir   string
	Threshold   float64
	Color       *bool
	Debug       bool
	KeepWorkdir bool
}

// FailFast reports whether mutant runs should stop at the first failing test.
// Baseline runs never fail fast; they need the complete picture.
func (c *Config) FailFast() bool { return c.KillAttribution == AttributionFirst }

// WantsReport reports whether the given format was requested.
func (c *Config) WantsReport(f ReportFormat) bool { return slices.Contains(c.Reports, f) }

// NeedsReportDir reports whether any file-emitting format was requested.
func (c *Config) NeedsReportDir() bool {
	for _, f := range c.Reports {
		if f != ReportConsole {
			return true
		}
	}
	return false
}

// Defaults returns a Config with every default applied except ChartPath.
func Defaults() Config {
	return Config{
		TestFiles:        []string{"tests/*_test.yaml"},
		WithSubChart:     true,
		Include:          slices.Clone(DefaultIncludes),
		Seed:             1,
		EquivalenceCheck: true,
		Parallel:         runtime.NumCPU(),
		KillAttribution:  AttributionFirst,
		Reports:          []ReportFormat{ReportConsole},
		ReportDir:        ".helm-mutation-test",
	}
}

// Validate checks the configuration and normalises defaults that depend on other
// fields. knownMutators is the registry's full ID list, used to reject typos
// early rather than silently running fewer mutators than the user asked for.
func (c *Config) Validate(knownMutators []string) error {
	if strings.TrimSpace(c.ChartPath) == "" {
		return fmt.Errorf("a chart path is required")
	}
	if c.Parallel < 1 {
		return fmt.Errorf("--parallel must be at least 1, got %d", c.Parallel)
	}
	if c.MaxMutants < 0 {
		return fmt.Errorf("--max-mutants cannot be negative, got %d", c.MaxMutants)
	}
	if c.Threshold < 0 || c.Threshold > 100 {
		return fmt.Errorf("--threshold must be between 0 and 100, got %v", c.Threshold)
	}
	if c.Timeout < 0 {
		return fmt.Errorf("--timeout cannot be negative, got %v", c.Timeout)
	}
	switch c.KillAttribution {
	case AttributionFirst, AttributionAll:
	default:
		return fmt.Errorf("--kill-attribution must be %q or %q, got %q",
			AttributionFirst, AttributionAll, c.KillAttribution)
	}
	for _, f := range c.Reports {
		if !slices.Contains(AllReportFormats, f) {
			return fmt.Errorf("unknown --report format %q (valid: %s)", f, joinFormats())
		}
	}
	if err := checkMutators("--mutators", c.Mutators, knownMutators); err != nil {
		return err
	}
	if err := checkMutators("--exclude-mutators", c.ExcludeMutators, knownMutators); err != nil {
		return err
	}
	if len(c.Include) == 0 {
		c.Include = slices.Clone(DefaultIncludes)
	}
	return nil
}

// EnabledMutators resolves --mutators and --exclude-mutators against the registry.
// An empty --mutators means "all".
func (c *Config) EnabledMutators(knownMutators []string) []string {
	enabled := c.Mutators
	if len(enabled) == 0 {
		enabled = knownMutators
	}
	out := make([]string, 0, len(enabled))
	for _, id := range enabled {
		if !slices.Contains(c.ExcludeMutators, id) {
			out = append(out, id)
		}
	}
	return out
}

// ResolveTimeout picks the per-mutant timeout. An explicit --timeout wins;
// otherwise scale off the baseline so slow charts are not cut off spuriously.
func (c *Config) ResolveTimeout(baseline time.Duration) time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	if scaled := baseline * 10; scaled > 30*time.Second {
		return scaled
	}
	return 30 * time.Second
}

func checkMutators(flag string, given, known []string) error {
	for _, id := range given {
		if !slices.Contains(known, id) {
			return fmt.Errorf("%s: unknown mutator %q (valid: %s)", flag, id, strings.Join(known, ", "))
		}
	}
	return nil
}

func joinFormats() string {
	names := make([]string, len(AllReportFormats))
	for i, f := range AllReportFormats {
		names[i] = string(f)
	}
	return strings.Join(names, ", ")
}
