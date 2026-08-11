package runner

import (
	"os"
	"path/filepath"
	"testing"
)

const fixtureChart = "../../testdata/charts/sample"

func TestRenderContextsMergeSuiteAndJobValues(t *testing.T) {
	// helm-unittest merges suite-level `set` under each job's own `set`, and
	// prepends suite `values` files to the job's. Getting this wrong renders the
	// wrong branch, which would make killed mutants look equivalent.
	chart, err := LoadChart(fixtureChart)
	if err != nil {
		t.Fatalf("LoadChart: %v", err)
	}
	suites, err := DiscoverSuites(fixtureChart, chart, Options{TestFiles: []string{"tests/*_test.yaml"}})
	if err != nil {
		t.Fatalf("DiscoverSuites: %v", err)
	}
	if len(suites) == 0 {
		t.Fatal("no suites discovered")
	}

	total := 0
	for _, s := range suites {
		ctxs, err := RenderContexts(fixtureChart, s)
		if err != nil {
			t.Fatalf("RenderContexts(%s): %v", s.Key, err)
		}
		total += len(ctxs)
		for _, ctx := range ctxs {
			if ctx.Name == "" {
				t.Errorf("%s: a context with no name cannot be cached or reported", s.Key)
			}
			if ctx.Capabilities == nil {
				t.Errorf("%s/%s: capabilities must be set explicitly, never left nil", s.Key, ctx.Name)
			}
			if ctx.Release.Name == "" {
				t.Errorf("%s/%s: release name must be populated", s.Key, ctx.Name)
			}
		}
	}
	if total == 0 {
		t.Fatal("no render contexts extracted from the fixture chart")
	}
}

func TestRenderContextNamesAreUnique(t *testing.T) {
	// Original renders are memoised by context name; a collision would silently
	// compare a mutant against the wrong baseline.
	chart, err := LoadChart(fixtureChart)
	if err != nil {
		t.Fatalf("LoadChart: %v", err)
	}
	suites, err := DiscoverSuites(fixtureChart, chart, Options{TestFiles: []string{"tests/*_test.yaml"}})
	if err != nil {
		t.Fatalf("DiscoverSuites: %v", err)
	}
	seen := map[string]bool{}
	for _, s := range suites {
		ctxs, err := RenderContexts(fixtureChart, s)
		if err != nil {
			t.Fatalf("RenderContexts: %v", err)
		}
		for _, ctx := range ctxs {
			if seen[ctx.Name] {
				t.Fatalf("duplicate context name %q", ctx.Name)
			}
			seen[ctx.Name] = true
		}
	}
}

// writeSuite drops a suite YAML file into a copy of the fixture chart and
// returns the suites discovered from just that file, so each merge scenario
// gets its own isolated suite/job pair to inspect.
func writeSuite(t *testing.T, dir, name, content string) []*Suite {
	t.Helper()
	path := filepath.Join(dir, "tests", name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	chart, err := LoadChart(dir)
	if err != nil {
		t.Fatalf("LoadChart: %v", err)
	}
	suites, err := DiscoverSuites(dir, chart, Options{TestFiles: []string{"tests/" + name}})
	if err != nil {
		t.Fatalf("DiscoverSuites: %v", err)
	}
	if len(suites) == 0 {
		t.Fatal("no suites discovered from the written file")
	}
	return suites
}

// writeFile drops an arbitrary file into a copy of the fixture chart, for
// values files a suite or job references by relative path.
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMergedValuesPrecedence(t *testing.T) {
	// Every value source getUserValues merges, in one scenario, so dropping any
	// one of them - not just getting the order backwards - shows up as a
	// failure: a suite-level values file, a job-level values file that must win
	// over it on a shared key, a suite-level `set` key the job overrides, and a
	// job-only `set` key. Silently dropping a source renders under the wrong
	// values, which is the "wrong branch" failure mode equivalence detection
	// cannot tolerate.
	dir := fixture(t)
	writeFile(t, dir, "tests/values-suite.yaml", "shared: from-suite-file\nfileOnly: from-suite-file-only\n")
	writeFile(t, dir, "tests/values-job.yaml", "shared: from-job-file\n")

	suites := writeSuite(t, dir, "values_merge_test.yaml", `
suite: value merge precedence
values:
  - values-suite.yaml
set:
  setShared: from-suite-set
  setSuiteOnly: from-suite-set-only
tests:
  - it: overrides the shared keys and adds its own
    values:
      - values-job.yaml
    set:
      setShared: from-job-set
      jobOnlySet: only-job-set
    asserts:
      - isKind:
          of: Deployment
`)
	ctxs, err := RenderContexts(dir, suites[0])
	if err != nil {
		t.Fatalf("RenderContexts: %v", err)
	}
	if len(ctxs) != 1 {
		t.Fatalf("want 1 context, got %d", len(ctxs))
	}
	vals := ctxs[0].Values
	if vals["shared"] != "from-job-file" {
		t.Fatalf("job values file must win over the suite's on a shared key, got %v", vals["shared"])
	}
	if vals["fileOnly"] != "from-suite-file-only" {
		t.Fatalf("a suite-only values-file key must still be present, got %v", vals["fileOnly"])
	}
	if vals["setShared"] != "from-job-set" {
		t.Fatalf("job set must win over the suite's set, got %v", vals["setShared"])
	}
	// A key the job never touches: this is the one assertion that fails if the
	// suite's `set` is dropped entirely rather than merely losing precedence
	// ties, since setShared would still read "from-job-set" either way.
	if vals["setSuiteOnly"] != "from-suite-set-only" {
		t.Fatalf("a suite-only set key must still be present, got %v", vals["setSuiteOnly"])
	}
	if vals["jobOnlySet"] != "only-job-set" {
		t.Fatalf("a job-only set key must be present, got %v", vals["jobOnlySet"])
	}
}

func TestReleaseOptionsDefaultToHelmsOwnZeroValues(t *testing.T) {
	// A naive reconstruction is tempted to default Revision to 1, as a first
	// install has. helm-unittest never does: releaseV3Option only defaults Name
	// and Namespace, leaving Revision at whatever was declared (zero if never
	// declared). Getting this wrong would render `.Release.Revision`-gated
	// content under a value no real helm-unittest run ever produces.
	dir := fixture(t)
	suites := writeSuite(t, dir, "release_defaults_test.yaml", `
suite: release defaults
tests:
  - it: uses defaults
    asserts:
      - isKind:
          of: Deployment
`)
	ctxs, err := RenderContexts(dir, suites[0])
	if err != nil {
		t.Fatalf("RenderContexts: %v", err)
	}
	if len(ctxs) != 1 {
		t.Fatalf("want 1 context, got %d", len(ctxs))
	}
	rel := ctxs[0].Release
	if rel.Name != "RELEASE-NAME" || rel.Namespace != "NAMESPACE" {
		t.Fatalf("want Helm's own release defaults, got %+v", rel)
	}
	if rel.Revision != 0 {
		t.Fatalf("want revision 0 (no floor of 1), got %d", rel.Revision)
	}
	if rel.IsUpgrade || !rel.IsInstall {
		t.Fatalf("want a fresh install, got IsUpgrade=%v IsInstall=%v", rel.IsUpgrade, rel.IsInstall)
	}
}

func TestReleaseOptionsJobOverridesSuite(t *testing.T) {
	// polishReleaseSettings lets the job win field-by-field, not wholesale: a job
	// that only overrides namespace must still inherit the suite's name and
	// revision.
	dir := fixture(t)
	suites := writeSuite(t, dir, "release_merge_test.yaml", `
suite: release merge
release:
  name: suite-release
  namespace: suite-ns
  revision: 3
tests:
  - it: inherits the suite untouched
    asserts:
      - isKind:
          of: Deployment
  - it: overrides namespace and upgrades
    release:
      namespace: job-ns
      upgrade: true
    asserts:
      - isKind:
          of: Deployment
`)
	ctxs, err := RenderContexts(dir, suites[0])
	if err != nil {
		t.Fatalf("RenderContexts: %v", err)
	}
	if len(ctxs) != 2 {
		t.Fatalf("want 2 contexts, got %d", len(ctxs))
	}
	if got := ctxs[0].Release; got.Name != "suite-release" || got.Namespace != "suite-ns" || got.Revision != 3 || got.IsUpgrade || !got.IsInstall {
		t.Fatalf("first job should inherit the suite untouched, got %+v", got)
	}
	if got := ctxs[1].Release; got.Name != "suite-release" || got.Namespace != "job-ns" || got.Revision != 3 || !got.IsUpgrade || got.IsInstall {
		t.Fatalf("second job should override only namespace and upgrade, got %+v", got)
	}
}

func TestCapabilitiesAPIVersionsDefaultToEmpty(t *testing.T) {
	// capabilitiesV3 unconditionally overwrites APIVersions with the job's own
	// (possibly empty) set - unlike KubeVersion, there is no "keep Helm's
	// built-in list" fallback. Preserving the chart-wide default here would
	// render under a richer API surface than helm-unittest actually tests under,
	// which could hide a mutation an apiVersions-gated branch would have caught.
	dir := fixture(t)
	suites := writeSuite(t, dir, "capabilities_default_test.yaml", `
suite: capabilities defaults
tests:
  - it: declares nothing
    asserts:
      - isKind:
          of: Deployment
`)
	ctxs, err := RenderContexts(dir, suites[0])
	if err != nil {
		t.Fatalf("RenderContexts: %v", err)
	}
	caps := ctxs[0].Capabilities
	if caps.APIVersions.Has("v1") {
		t.Fatalf("want an empty API surface by default, got %v", caps.APIVersions)
	}
	if caps.KubeVersion.Major == "" || caps.KubeVersion.Minor == "" {
		t.Fatalf("KubeVersion must still fall back to Helm's own default, got %+v", caps.KubeVersion)
	}
}

func TestCapabilitiesAPIVersionsMergeSuiteAndJob(t *testing.T) {
	// The suite's apiVersions only reach the job when the job also declares its
	// own (SetCapabilities makes the job's field non-nil in the ordinary case);
	// once that holds, both lists must be present in the final render.
	dir := fixture(t)
	suites := writeSuite(t, dir, "capabilities_merge_test.yaml", `
suite: capabilities merge
capabilities:
  apiVersions:
    - suite/v1
tests:
  - it: adds its own
    capabilities:
      apiVersions:
        - job/v1
    asserts:
      - isKind:
          of: Deployment
`)
	ctxs, err := RenderContexts(dir, suites[0])
	if err != nil {
		t.Fatalf("RenderContexts: %v", err)
	}
	caps := ctxs[0].Capabilities
	if !caps.APIVersions.Has("suite/v1") || !caps.APIVersions.Has("job/v1") {
		t.Fatalf("want both suite and job API versions, got %v", caps.APIVersions)
	}
}

func TestUsesKubernetesProviderReflectsJobAndSuiteDeclarations(t *testing.T) {
	dir := fixture(t)

	withJobProvider := writeSuite(t, dir, "kube_provider_job_test.yaml", `
suite: uses a fake client at job level
tests:
  - it: declares one
    kubernetesProvider:
      objects:
        - apiVersion: v1
          kind: ConfigMap
          metadata:
            name: foo
    asserts:
      - isKind:
          of: Deployment
`)
	if !withJobProvider[0].UsesKubernetesProvider() {
		t.Fatal("a job-level kubernetesProvider must be detected")
	}

	withSuiteProvider := writeSuite(t, dir, "kube_provider_suite_test.yaml", `
suite: uses a fake client at suite level
kubernetesProvider:
  objects:
    - apiVersion: v1
      kind: ConfigMap
      metadata:
        name: foo
tests:
  - it: inherits it
    asserts:
      - isKind:
          of: Deployment
`)
	if !withSuiteProvider[0].UsesKubernetesProvider() {
		t.Fatal("a suite-level kubernetesProvider must be detected")
	}

	without := writeSuite(t, dir, "no_kube_provider_test.yaml", `
suite: no fake client
tests:
  - it: declares nothing
    asserts:
      - isKind:
          of: Deployment
`)
	if without[0].UsesKubernetesProvider() {
		t.Fatal("a suite with no kubernetesProvider must not be flagged")
	}
}
