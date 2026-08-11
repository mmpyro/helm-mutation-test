// Package equivalence decides whether a mutation can change what a chart
// renders.
//
// A mutant is equivalent when the chart renders to identical parsed documents
// with and without it, under every value set the covering tests use. That is a
// proof rather than a heuristic: helm-unittest computes assertions on YAML
// manifests from the parsed tree, never from rendered text, so two renders that
// parse identically cannot be told apart by any YAML assertion in any suite.
// Raw text templates (paths ending in .txt) are compared exactly; because
// raw validators like matchSnapshotRaw observe the rendered string directly,
// any byte difference is observable.
//
// This package imports Helm but nothing from internal/runner. The runner adapts
// helm-unittest's suites into the RenderContexts consumed here, which keeps the
// helm-unittest dependency confined to internal/runner/unittest.go.
package equivalence

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"
)

// RenderContext is one set of inputs a chart can be rendered under. It mirrors
// what a helm-unittest test job supplies, without depending on its types.
type RenderContext struct {
	// Name identifies the context in report detail text.
	Name string
	// Values is the fully merged user value set, as helm-unittest would compute it.
	Values       map[string]any
	Release      chartutil.ReleaseOptions
	Capabilities *chartutil.Capabilities
	// ChartVersion and ChartAppVersion override the chart metadata when non-empty,
	// matching a test job's chart settings.
	ChartVersion    string
	ChartAppVersion string
}

// Render renders the chart under ctx and returns manifests keyed by their
// chart-prefixed template path.
func Render(chrt *chart.Chart, ctx RenderContext) (map[string]string, error) {
	chrt = withMetadata(chrt, ctx)

	caps := ctx.Capabilities
	if caps == nil {
		// Copy: DefaultCapabilities is a package-level pointer in Helm, and writing
		// through it is the race that forced worker subprocesses on us.
		caps = chartutil.DefaultCapabilities.Copy()
	}

	values, err := chartutil.ToRenderValues(chrt, ctx.Values, ctx.Release, caps)
	if err != nil {
		return nil, fmt.Errorf("composing render values for %s: %w", ctx.Name, err)
	}
	out, err := engine.Engine{}.Render(chrt, values)
	if err != nil {
		return nil, fmt.Errorf("rendering under %s: %w", ctx.Name, err)
	}
	return out, nil
}

// withMetadata returns chrt, or a shallow clone carrying the context's chart
// version overrides. The clone keeps the original untouched so one loaded chart
// can serve every context.
func withMetadata(chrt *chart.Chart, ctx RenderContext) *chart.Chart {
	if ctx.ChartVersion == "" && ctx.ChartAppVersion == "" {
		return chrt
	}
	clone := *chrt
	md := *chrt.Metadata
	if ctx.ChartVersion != "" {
		md.Version = ctx.ChartVersion
	}
	if ctx.ChartAppVersion != "" {
		md.AppVersion = ctx.ChartAppVersion
	}
	clone.Metadata = &md
	return &clone
}

// Compare reports whether two renders are indistinguishable to an assertion.
//
// For .txt templates, exact string equality is required; raw text validators
// like matchSnapshotRaw observe the rendered output directly.
// For YAML templates, bytes are checked first because byte equality is
// unambiguous and cheap. Only on a difference do we parse, because parsed
// equality is what matters: `nindent 4 -> 5` changes every byte yet none of
// the data.
func Compare(a, b map[string]string) (bool, error) {
	if len(a) != len(b) {
		return false, nil
	}
	for name, textA := range a {
		textB, ok := b[name]
		if !ok {
			return false, nil
		}
		if textA == textB {
			continue
		}
		// Raw text templates (.txt) are compared exactly; YAML templates are
		// compared after parsing to ignore harmless formatting differences.
		if strings.HasSuffix(name, ".txt") {
			return false, nil
		}
		docsA, err := parseDocs(textA)
		if err != nil {
			return false, fmt.Errorf("parsing rendered %s: %w", name, err)
		}
		docsB, err := parseDocs(textB)
		if err != nil {
			return false, fmt.Errorf("parsing rendered %s: %w", name, err)
		}
		if !reflect.DeepEqual(docsA, docsB) {
			return false, nil
		}
	}
	return true, nil
}

// parseDocs decodes a multi-document manifest, preserving document order:
// documentIndex assertions can observe it, so a reordering is a real difference.
func parseDocs(text string) ([]any, error) {
	dec := yaml.NewDecoder(strings.NewReader(text))
	var out []any
	for {
		var doc any
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if doc == nil {
			continue // an empty document renders nothing and asserts nothing
		}
		out = append(out, doc)
	}
}
