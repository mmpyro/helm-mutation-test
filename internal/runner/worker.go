package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Process-level parallelism.
//
// helm-unittest cannot safely run suites concurrently inside one process. Its
// TestJob.capabilitiesV3 does:
//
//	capabilities := v3util.DefaultCapabilities   // a *Capabilities POINTER
//	capabilities.KubeVersion = ...               // writes through the shared pointer
//
// chartutil.DefaultCapabilities is a package-level pointer in Helm, so every test
// job mutates process-global state. `go test -race` flags it, and it is a real
// correctness bug rather than mere untidiness: a suite declaring a custom
// capabilities.majorVersion leaks that version into suites rendering concurrently.
// The same code is present in the latest release (v1.1.2), so upgrading is not a
// fix.
//
// Each worker therefore runs in its own process, where that global is private.
// Workers are long-lived and stream jobs, so process startup is paid once per
// worker rather than once per mutant. As a bonus this makes the per-mutant timeout
// genuinely enforceable: a hung worker can be killed, whereas an abandoned
// goroutine cannot.

// WorkerEnvVar switches a process into worker mode. Set on the child by the
// parent; also honoured by the test binary so tests exercise the real protocol.
const WorkerEnvVar = "HELM_MUTATION_TEST_WORKER"

// workerRequest asks a worker to run the named suites against the chart that
// currently sits at its workspace root. The parent has already written the
// mutation there.
type workerRequest struct {
	Suites []SuiteKey `json:"suites"`
}

// workerResponse is the worker's reply.
type workerResponse struct {
	Outcomes []SuiteOutcome `json:"outcomes"`
	Error    string         `json:"error,omitempty"`
}

// RunWorkerLoop serves requests until stdin closes. Called by the child process.
//
// chartRoot is that worker's private chart copy; the parent mutates files inside
// it between requests. The chart is reloaded and the suites re-parsed per request
// because both reflect the mutation currently on disk.
func RunWorkerLoop(chartRoot string, opts Options, failFast bool, in io.Reader, out io.Writer) error {
	dec := json.NewDecoder(bufio.NewReader(in))
	enc := json.NewEncoder(out)

	for {
		var req workerRequest
		if err := dec.Decode(&req); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("worker: decoding request: %w", err)
		}
		if err := enc.Encode(serveRequest(chartRoot, opts, failFast, req)); err != nil {
			return fmt.Errorf("worker: encoding response: %w", err)
		}
	}
}

func serveRequest(chartRoot string, opts Options, failFast bool, req workerRequest) workerResponse {
	chart, err := LoadChart(chartRoot)
	if err != nil {
		return workerResponse{Error: err.Error()}
	}
	suites, err := DiscoverSuites(chartRoot, chart, opts)
	if err != nil {
		return workerResponse{Error: err.Error()}
	}
	return workerResponse{Outcomes: RunSuites(chart, selectSuites(suites, req.Suites), failFast)}
}

// evaluator runs suites for one worker slot. Both an in-process and a subprocess
// implementation satisfy it, so the executor does not care which is in use.
type evaluator interface {
	// Run evaluates the named suites against the chart at the workspace root.
	Run(ctx context.Context, want []SuiteKey, timeout time.Duration) ([]SuiteOutcome, error)
	Close() error
}

// inProcessEvaluator runs suites in this process. Safe only when nothing else is
// running suites at the same time, i.e. with a single worker.
type inProcessEvaluator struct {
	chartRoot string
	opts      Options
	failFast  bool
}

func (e *inProcessEvaluator) Run(ctx context.Context, want []SuiteKey, timeout time.Duration) ([]SuiteOutcome, error) {
	type result struct {
		outcomes []SuiteOutcome
		err      error
	}
	ch := make(chan result, 1)
	go func() {
		r := serveRequest(e.chartRoot, e.opts, e.failFast, workerRequest{Suites: want})
		if r.Error != "" {
			ch <- result{err: fmt.Errorf("%s", r.Error)}
			return
		}
		ch <- result{outcomes: r.Outcomes}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r.outcomes, r.err
	case <-timer.C:
		// RunV3 takes no context, so the goroutine is abandoned rather than
		// cancelled. Acceptable in a short-lived CLI; the subprocess path can
		// actually kill its worker.
		return nil, errMutantTimeout
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (e *inProcessEvaluator) Close() error { return nil }

// subprocessEvaluator drives one long-lived worker process.
type subprocessEvaluator struct {
	mu   sync.Mutex
	cmd  *exec.Cmd
	enc  *json.Encoder
	dec  *json.Decoder
	stop func()
	dead bool
}

// newSubprocessEvaluator launches a worker for the given chart root.
func newSubprocessEvaluator(chartRoot string, opts Options, failFast bool, debug bool) (*subprocessEvaluator, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locating own binary for a worker: %w", err)
	}
	payload, err := json.Marshal(workerBootstrap{
		ChartRoot: chartRoot, Options: opts, FailFast: failFast,
	})
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(self, workerArgs()...)
	cmd.Env = append(os.Environ(), WorkerEnvVar+"="+string(payload))
	if debug {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = io.Discard
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting worker: %w", err)
	}

	return &subprocessEvaluator{
		cmd: cmd,
		enc: json.NewEncoder(stdin),
		dec: json.NewDecoder(bufio.NewReader(stdout)),
		stop: func() {
			stdin.Close()
			// Give the worker a moment to exit cleanly, then insist.
			done := make(chan struct{})
			go func() { cmd.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				cmd.Process.Kill()
				<-done
			}
		},
	}, nil
}

// workerBootstrap is passed to the child via the environment, so the worker needs
// no command-line surface of its own beyond the mode flag.
type workerBootstrap struct {
	ChartRoot string  `json:"chartRoot"`
	Options   Options `json:"options"`
	FailFast  bool    `json:"failFast"`
}

// WorkerBootstrapFromEnv reads the worker configuration a parent process set.
// Reports false when this process is not a worker.
func WorkerBootstrapFromEnv() (chartRoot string, opts Options, failFast bool, ok bool) {
	raw := os.Getenv(WorkerEnvVar)
	if raw == "" {
		return "", Options{}, false, false
	}
	var b workerBootstrap
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		return "", Options{}, false, false
	}
	return b.ChartRoot, b.Options, b.FailFast, true
}

// workerArgsOverride lets the test binary supply the flags that put it into
// worker mode, since a test binary does not accept the plugin's own flags.
var workerArgsOverride []string

func workerArgs() []string {
	if workerArgsOverride != nil {
		return workerArgsOverride
	}
	return []string{"__worker"}
}

// SetWorkerArgs overrides the arguments used to launch a worker. For tests only.
func SetWorkerArgs(args []string) { workerArgsOverride = args }

func (e *subprocessEvaluator) Run(ctx context.Context, want []SuiteKey, timeout time.Duration) ([]SuiteOutcome, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dead {
		return nil, fmt.Errorf("worker process is no longer running")
	}

	type result struct {
		resp workerResponse
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		if err := e.enc.Encode(workerRequest{Suites: want}); err != nil {
			ch <- result{err: fmt.Errorf("sending job to worker: %w", err)}
			return
		}
		var resp workerResponse
		if err := e.dec.Decode(&resp); err != nil {
			ch <- result{err: fmt.Errorf("reading worker reply: %w", err)}
			return
		}
		ch <- result{resp: resp}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		if r.err != nil {
			e.dead = true
			return nil, r.err
		}
		if r.resp.Error != "" {
			return nil, fmt.Errorf("%s", r.resp.Error)
		}
		return r.resp.Outcomes, nil
	case <-timer.C:
		// The worker is mid-render and its pipe state is now unknown, so retire it.
		e.dead = true
		return nil, errMutantTimeout
	case <-ctx.Done():
		e.dead = true
		return nil, ctx.Err()
	}
}

func (e *subprocessEvaluator) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stop != nil {
		e.stop()
		e.stop = nil
	}
	return nil
}
