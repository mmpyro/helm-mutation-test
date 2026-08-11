// Command helm-mutation-test is a Helm plugin that measures how much your
// helm-unittest suites actually assert.
//
// It mutates the chart's templates and values, re-runs the existing suites
// against each mutation, and reports which mutations no test noticed. A mutation
// that survives is a precise pointer at a missing assertion.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mmpyro/helm-mutation-test/internal/config"
	"github.com/mmpyro/helm-mutation-test/internal/mutator"
	"github.com/mmpyro/helm-mutation-test/internal/report"
	"github.com/mmpyro/helm-mutation-test/internal/runner"
	"github.com/spf13/cobra"
)

// Exit codes, chosen so CI can distinguish "your tests are weak" from
// "the tool could not run".
const (
	exitOK             = 0
	exitBelowThreshold = 1
	exitCannotRun      = 2
)

func main() {
	// Worker mode short-circuits everything: the parent launched this process to
	// evaluate mutants in isolation. See internal/runner/worker.go for why.
	if chartRoot, opts, failFast, ok := runner.WorkerBootstrapFromEnv(); ok && isWorkerInvocation() {
		if err := runner.RunWorkerLoop(chartRoot, opts, failFast, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "worker:", err)
			os.Exit(1)
		}
		return
	}

	if err := newRootCmd().Execute(); err != nil {
		// cobra has already printed usage errors.
		var coded *codedError
		if errors.As(err, &coded) {
			os.Exit(coded.code)
		}
		os.Exit(exitCannotRun)
	}
}

func isWorkerInvocation() bool {
	return len(os.Args) > 1 && os.Args[1] == "__worker"
}

// codedError carries a process exit code out of RunE.
type codedError struct {
	code int
	err  error
}

func (e *codedError) Error() string { return e.err.Error() }
func (e *codedError) Unwrap() error { return e.err }

func newRootCmd() *cobra.Command {
	cfg := config.Defaults()
	var (
		reports            []string
		colour             bool
		noColour           bool
		noEquivalenceCheck bool
	)

	cmd := &cobra.Command{
		Use:   "mutation-test [flags] CHART",
		Short: "Measure how much your helm-unittest suites actually assert",
		Long: strings.TrimSpace(`
Mutation testing for helm-unittest.

helm-unittest tells you whether your chart tests pass. It cannot tell you whether
they are worth anything: a suite of "isKind: Deployment" assertions stays green
forever while the chart silently regresses replicas, imagePullPolicy, or a whole
{{- if .Values.ingress.enabled }} branch.

This plugin deliberately breaks the chart, one change at a time, and re-runs your
existing suites against each one. A change no test objects to is a "survived"
mutant: a precise, actionable pointer at a missing assertion.

The chart's own tests must pass before mutation begins. A mutation score measured
against a failing suite is meaningless, so a red baseline is a hard stop.

Examples:
  # Score a chart, printing to the terminal
  helm mutation-test ./my-chart

  # Fail CI below 70%, and write reports a CI job can publish
  helm mutation-test ./my-chart --threshold 70 \
    --report console,markdown,html,junit --report-dir ./mutation

  # A fast subset while iterating on tests
  helm mutation-test ./my-chart --mutators cond-negate,str-literal --max-mutants 50
`),
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.ChartPath = args[0]
			cfg.Reports = parseReports(reports)
			cfg.Color = resolveColour(colour, noColour)
			cfg.EquivalenceCheck = !noEquivalenceCheck

			if err := cfg.Validate(mutator.IDs()); err != nil {
				return &codedError{exitCannotRun, err}
			}
			return run(cmd, &cfg)
		},
	}

	f := cmd.Flags()

	// Pass-through to helm-unittest.
	f.StringArrayVarP(&cfg.TestFiles, "file", "f", cfg.TestFiles,
		"glob paths of test suite files, relative to the chart")
	f.StringArrayVarP(&cfg.ValuesFiles, "values", "v", nil,
		"absolute or glob paths of values files to override chart values")
	f.BoolVarP(&cfg.WithSubChart, "with-subchart", "s", cfg.WithSubChart,
		"include tests of subcharts")
	f.BoolVar(&cfg.Strict, "strict", false, "strictly parse the test suites")
	f.StringVar(&cfg.ChartTestsPath, "chart-tests-path", "",
		"folder, relative to the chart, holding a chart that renders test suites")

	// Mutation control.
	f.StringSliceVar(&cfg.Mutators, "mutators", nil,
		"mutators to run (default all)\n"+indentList(mutator.Descriptions()))
	f.StringSliceVar(&cfg.ExcludeMutators, "exclude-mutators", nil, "mutators to skip")
	f.StringSliceVar(&cfg.Include, "include", cfg.Include,
		"chart files to mutate, as globs relative to the chart")
	f.StringSliceVar(&cfg.Exclude, "exclude", nil, "chart files to skip")
	f.IntVar(&cfg.MaxMutants, "max-mutants", 0,
		"evaluate at most this many mutants, sampled deterministically (0 = no limit)")
	f.Int64Var(&cfg.Seed, "seed", cfg.Seed, "sampling seed, for a reproducible --max-mutants subset")
	// pflag has no native negated bool, so bind the negation and invert it after
	// parsing. Keep backticks out of the usage string: pflag's UnquoteUsage turns
	// the first backquoted run into the flag's displayed value type.
	f.BoolVar(&noEquivalenceCheck, "no-equivalence-check", false,
		"skip the post-pass that identifies survivors no assertion could ever catch")
	f.DurationVar(&cfg.Timeout, "timeout", 0,
		"per-mutant timeout (default: 10x the baseline run, at least 30s)")

	// Execution.
	f.IntVarP(&cfg.Parallel, "parallel", "p", cfg.Parallel, "number of concurrent workers")
	f.StringVar((*string)(&cfg.KillAttribution), "kill-attribution", string(cfg.KillAttribution),
		"'first' stops each mutant at the first failing test; 'all' records every killing test")

	// Output.
	f.StringSliceVar(&reports, "report", []string{string(config.ReportConsole)},
		"report formats: console, json, html, markdown, junit")
	f.StringVar(&cfg.ReportDir, "report-dir", cfg.ReportDir, "directory for file reports")
	f.Float64Var(&cfg.Threshold, "threshold", 0,
		"exit 1 if the mutation score is below this percentage (0 disables the check)")
	f.BoolVar(&colour, "color", false, "force coloured output even when stdout is not a terminal")
	f.BoolVar(&noColour, "no-color", false, "disable coloured output")
	f.BoolVarP(&cfg.Debug, "debug", "d", false, "verbose output, including worker stderr")
	f.BoolVar(&cfg.KeepWorkdir, "keep-workdir", false,
		"keep the temporary chart copies for inspection")

	cmd.AddCommand(&cobra.Command{
		Use:    "__worker",
		Hidden: true,
		Short:  "internal: evaluate mutants in an isolated process",
		Run:    func(*cobra.Command, []string) { /* handled in main before cobra */ },
	})
	return cmd
}

func run(cmd *cobra.Command, cfg *config.Config) error {
	runner.SilenceLibraryLogging(cfg.Debug)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	out := cmd.OutOrStdout()
	session := &runner.Session{Cfg: *cfg}
	if cfg.Debug {
		session.OnPhase = func(msg string) { fmt.Fprintln(os.Stderr, "==>", msg) }
	}

	result, err := session.Run(ctx)
	if err != nil {
		var baseline *runner.BaselineFailure
		if errors.As(err, &baseline) {
			// Distinguish "your tests are red" from a tool failure, and say what to do.
			fmt.Fprintf(os.Stderr, "\nCannot measure mutation score: %s\n", baseline.Detail)
			fmt.Fprintln(os.Stderr, "A score measured against failing tests is meaningless.")
			fmt.Fprintln(os.Stderr, "Fix the suite first, then re-run.")
			return &codedError{exitCannotRun, err}
		}
		return &codedError{exitCannotRun, err}
	}

	if cfg.WantsReport(config.ReportConsole) {
		if err := report.Console(out, result, *cfg.Color); err != nil {
			return &codedError{exitCannotRun, err}
		}
	}
	written, err := report.WriteFiles(result, cfg)
	if err != nil {
		return &codedError{exitCannotRun, err}
	}
	for _, w := range written {
		fmt.Fprintf(out, "  %s report: %s\n", w.Format, w.Path)
	}
	if len(written) > 0 {
		fmt.Fprintln(out)
	}

	if !result.MeetsThreshold() {
		return &codedError{exitBelowThreshold, fmt.Errorf(
			"mutation score %.1f%% is below the %.1f%% threshold",
			result.Score(), result.Threshold)}
	}
	return nil
}

func parseReports(in []string) []config.ReportFormat {
	out := make([]config.ReportFormat, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, config.ReportFormat(strings.ToLower(s)))
		}
	}
	return out
}

// resolveColour turns the two flags into a tri-state: forced on, forced off, or
// auto-detected from whether stdout is a terminal.
func resolveColour(force, disable bool) *bool {
	on := false
	switch {
	case disable:
		on = false
	case force:
		on = true
	default:
		on = isTerminal(os.Stdout)
	}
	return &on
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// indentList formats mutator descriptions for the flag's help text.
func indentList(lines []string) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString("  ")
		b.WriteString(l)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
