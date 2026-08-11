package config

import (
	"strings"
	"testing"
	"time"
)

var known = []string{"cond-negate", "bool-flip", "num-literal"}

func validConfig() Config {
	c := Defaults()
	c.ChartPath = "./chart"
	return c
}

func TestValidateAcceptsDefaults(t *testing.T) {
	c := validConfig()
	if err := c.Validate(known); err != nil {
		t.Fatalf("defaults should validate, got %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"missing chart", func(c *Config) { c.ChartPath = "" }, "chart path is required"},
		{"zero parallel", func(c *Config) { c.Parallel = 0 }, "--parallel must be at least 1"},
		{"negative max-mutants", func(c *Config) { c.MaxMutants = -1 }, "cannot be negative"},
		{"threshold too high", func(c *Config) { c.Threshold = 101 }, "--threshold must be between"},
		{"negative threshold", func(c *Config) { c.Threshold = -1 }, "--threshold must be between"},
		{"negative timeout", func(c *Config) { c.Timeout = -time.Second }, "--timeout cannot be negative"},
		{"bad attribution", func(c *Config) { c.KillAttribution = "some" }, "--kill-attribution must be"},
		{"bad report", func(c *Config) { c.Reports = []ReportFormat{"pdf"} }, `unknown --report format "pdf"`},
		{"unknown mutator", func(c *Config) { c.Mutators = []string{"nope"} }, `unknown mutator "nope"`},
		{"unknown excluded mutator", func(c *Config) { c.ExcludeMutators = []string{"nope"} }, `unknown mutator "nope"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig()
			tc.mutate(&c)
			err := c.Validate(known)
			if err == nil {
				t.Fatalf("expected an error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateRestoresEmptyIncludes(t *testing.T) {
	c := validConfig()
	c.Include = nil
	if err := c.Validate(known); err != nil {
		t.Fatal(err)
	}
	if len(c.Include) != len(DefaultIncludes) {
		t.Fatalf("expected includes to fall back to defaults, got %v", c.Include)
	}
}

func TestEnabledMutators(t *testing.T) {
	tests := []struct {
		name    string
		enable  []string
		exclude []string
		want    []string
	}{
		{"empty means all", nil, nil, known},
		{"explicit subset", []string{"bool-flip"}, nil, []string{"bool-flip"}},
		{"exclusion from all", nil, []string{"bool-flip"}, []string{"cond-negate", "num-literal"}},
		{"exclusion wins over inclusion", []string{"bool-flip", "num-literal"}, []string{"bool-flip"}, []string{"num-literal"}},
		{"exclude everything", nil, known, []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig()
			c.Mutators, c.ExcludeMutators = tc.enable, tc.exclude
			got := c.EnabledMutators(known)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestResolveTimeout(t *testing.T) {
	tests := []struct {
		name     string
		explicit time.Duration
		baseline time.Duration
		want     time.Duration
	}{
		{"explicit wins", 5 * time.Second, time.Hour, 5 * time.Second},
		{"floor of 30s for fast charts", 0, 100 * time.Millisecond, 30 * time.Second},
		{"10x baseline when slow", 0, 10 * time.Second, 100 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig()
			c.Timeout = tc.explicit
			if got := c.ResolveTimeout(tc.baseline); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFailFastFollowsAttribution(t *testing.T) {
	c := validConfig()
	if !c.FailFast() {
		t.Error("default attribution 'first' should fail fast")
	}
	c.KillAttribution = AttributionAll
	if c.FailFast() {
		t.Error("attribution 'all' must not fail fast, or the kill set is incomplete")
	}
}

func TestNeedsReportDir(t *testing.T) {
	c := validConfig()
	if c.NeedsReportDir() {
		t.Error("console-only should not need a report dir")
	}
	c.Reports = append(c.Reports, ReportJSON)
	if !c.NeedsReportDir() {
		t.Error("json output needs a report dir")
	}
}
