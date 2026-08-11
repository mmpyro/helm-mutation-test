package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestWorkerLoopServesRequests(t *testing.T) {
	dir := fixture(t)

	var in bytes.Buffer
	enc := json.NewEncoder(&in)
	// Two requests in one stream: the worker must be reusable, which is what makes
	// process startup a per-worker rather than per-mutant cost.
	for range 2 {
		if err := enc.Encode(workerRequest{Suites: []SuiteKey{
			{File: "tests/weak_test.yaml", Name: "weak assertions", Ordinal: 0},
		}}); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	if err := RunWorkerLoop(dir, weakOpts(), true, &in, &out); err != nil {
		t.Fatalf("worker loop: %v", err)
	}

	dec := json.NewDecoder(&out)
	for i := range 2 {
		var resp workerResponse
		if err := dec.Decode(&resp); err != nil {
			t.Fatalf("response %d: %v", i, err)
		}
		if resp.Error != "" {
			t.Fatalf("response %d reported %q", i, resp.Error)
		}
		if len(resp.Outcomes) != 1 {
			t.Fatalf("response %d has %d outcomes, want 1", i, len(resp.Outcomes))
		}
		if !resp.Outcomes[0].Passed {
			t.Errorf("response %d: the unmutated weak suite should pass", i)
		}
	}
}

func TestWorkerLoopStopsCleanlyOnEOF(t *testing.T) {
	var out bytes.Buffer
	if err := RunWorkerLoop(fixture(t), weakOpts(), true, strings.NewReader(""), &out); err != nil {
		t.Fatalf("an empty stream should be a clean exit, got %v", err)
	}
}

func TestWorkerLoopReportsABadChartRoot(t *testing.T) {
	var in bytes.Buffer
	json.NewEncoder(&in).Encode(workerRequest{})

	var out bytes.Buffer
	if err := RunWorkerLoop(t.TempDir()+"/missing", weakOpts(), true, &in, &out); err != nil {
		t.Fatalf("a load failure belongs in the response, not the loop error: %v", err)
	}
	var resp workerResponse
	if err := json.NewDecoder(&out).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error == "" {
		t.Error("expected the response to carry the load error")
	}
}

func TestWorkerBootstrapRoundTrip(t *testing.T) {
	if _, _, _, ok := WorkerBootstrapFromEnv(); ok {
		t.Skip("already running as a worker")
	}
	payload, err := json.Marshal(workerBootstrap{
		ChartRoot: "/some/chart",
		Options:   Options{TestFiles: []string{"tests/*_test.yaml"}, Strict: true},
		FailFast:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(WorkerEnvVar, string(payload))

	root, opts, failFast, ok := WorkerBootstrapFromEnv()
	if !ok {
		t.Fatal("bootstrap should be recognised")
	}
	if root != "/some/chart" || !failFast || !opts.Strict {
		t.Errorf("round trip lost detail: root=%q failFast=%v opts=%+v", root, failFast, opts)
	}
	if len(opts.TestFiles) != 1 || opts.TestFiles[0] != "tests/*_test.yaml" {
		t.Errorf("TestFiles = %v", opts.TestFiles)
	}
}

func TestWorkerBootstrapRejectsGarbage(t *testing.T) {
	t.Setenv(WorkerEnvVar, "not json")
	if _, _, _, ok := WorkerBootstrapFromEnv(); ok {
		t.Error("malformed bootstrap must not be accepted")
	}
}

// TestNewEvaluatorIsolatesByProcessWhenParallel pins the reason process isolation
// exists at all: helm-unittest mutates a Helm package-level global while
// rendering, so concurrent in-process suites both race and can leak one suite's
// kubeVersion into another's render.
func TestNewEvaluatorIsolatesByProcessWhenParallel(t *testing.T) {
	cfg := ExecutorConfig{Options: weakOpts()}

	serial, err := cfg.newEvaluator(fixture(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer serial.Close()
	if _, ok := serial.(*inProcessEvaluator); !ok {
		t.Errorf("a single worker should run in process, got %T", serial)
	}

	parallel, err := cfg.newEvaluator(fixture(t), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer parallel.Close()
	if _, ok := parallel.(*subprocessEvaluator); !ok {
		t.Errorf("multiple workers must be isolated by process, got %T", parallel)
	}
}

func TestSubprocessEvaluatorRunsSuites(t *testing.T) {
	dir := fixture(t)
	ev, err := newSubprocessEvaluator(dir, weakOpts(), true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer ev.Close()

	want := []SuiteKey{{File: "tests/weak_test.yaml", Name: "weak assertions", Ordinal: 0}}
	// Twice, to prove the worker is reusable across mutants.
	for i := range 2 {
		outcomes, err := ev.Run(context.Background(), want, 60*time.Second)
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if len(outcomes) != 1 || !outcomes[0].Passed {
			t.Fatalf("run %d: got %+v", i, outcomes)
		}
	}
}

func TestSubprocessEvaluatorSurfacesTimeout(t *testing.T) {
	ev, err := newSubprocessEvaluator(fixture(t), weakOpts(), true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer ev.Close()

	want := []SuiteKey{{File: "tests/weak_test.yaml", Name: "weak assertions", Ordinal: 0}}
	// A timeout this small cannot be met, so the call must report errMutantTimeout
	// rather than hanging or returning bogus outcomes.
	_, err = ev.Run(context.Background(), want, time.Nanosecond)
	if err != errMutantTimeout {
		t.Fatalf("err = %v, want errMutantTimeout", err)
	}
	// The worker is retired after a timeout: its pipe state is unknown.
	if _, err := ev.Run(context.Background(), want, time.Minute); err == nil {
		t.Error("a timed-out worker should not be reused")
	}
}

func TestSubprocessEvaluatorHonoursContextCancellation(t *testing.T) {
	ev, err := newSubprocessEvaluator(fixture(t), weakOpts(), true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer ev.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	want := []SuiteKey{{File: "tests/weak_test.yaml", Name: "weak assertions", Ordinal: 0}}
	if _, err := ev.Run(ctx, want, time.Minute); err == nil {
		t.Error("expected cancellation to surface as an error")
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	ev, err := newSubprocessEvaluator(fixture(t), weakOpts(), true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := ev.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ev.Close(); err != nil {
		t.Errorf("second Close should be a no-op, got %v", err)
	}
}
