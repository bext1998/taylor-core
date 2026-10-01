//go:build windows

package exec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func newTestRunner(t *testing.T) *Runner {
	t.Helper()
	r, err := NewRunner()
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	return r
}

func baseOptions(command string) Options {
	return Options{
		Command:        command,
		Timeout:        10 * time.Second,
		MaxProcesses:   16,
		MaxMemoryBytes: 512 * 1024 * 1024,
		MaxOutputBytes: 64 * 1024,
	}
}

// TestWaitForExitAndOutput exercises the bounded wait in isolation, with
// fake channels instead of a real process/job, so the "a descendant
// process outlives the primary and keeps a pipe write end open" failure
// mode from spec AC-10 can be simulated deterministically and fast,
// rather than needing real Job Object fault injection.
func TestWaitForExitAndOutput(t *testing.T) {
	const shortDeadline = 50 * time.Millisecond

	t.Run("success", func(t *testing.T) {
		exitCh := make(chan struct{})
		close(exitCh)
		stdoutCh := make(chan pipeResult, 1)
		stderrCh := make(chan pipeResult, 1)
		stdoutCh <- pipeResult{data: []byte("out")}
		stderrCh <- pipeResult{data: []byte("err")}

		stdout, stderr, err := waitForExitAndOutput(exitCh, stdoutCh, stderrCh, shortDeadline, nil)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if string(stdout.data) != "out" || string(stderr.data) != "err" {
			t.Fatalf("stdout/stderr = %q/%q, want %q/%q", stdout.data, stderr.data, "out", "err")
		}
	})

	t.Run("exit never confirmed", func(t *testing.T) {
		exitCh := make(chan struct{}) // never closed
		stdoutCh := make(chan pipeResult)
		stderrCh := make(chan pipeResult)
		cause := errors.New("terminate failed")

		start := time.Now()
		_, _, err := waitForExitAndOutput(exitCh, stdoutCh, stderrCh, shortDeadline, cause)
		elapsed := time.Since(start)

		if !errors.Is(err, ErrExecInternal) {
			t.Fatalf("err = %v, want E_EXEC_INTERNAL", err)
		}
		if !errors.Is(err, cause) {
			t.Fatalf("err = %v, want it to wrap cause %v", err, cause)
		}
		if elapsed > time.Second {
			t.Fatalf("took %v, want close to the %v deadline", elapsed, shortDeadline)
		}
	})

	// This is the scenario from the review finding: exit is confirmed
	// promptly (e.g. a fallback TerminateProcess killed the primary
	// process), but a grandchild outside the job's reach still holds a
	// pipe write end open, so that pipe alone never reaches EOF. The
	// overall wait must still return within the shared deadline instead
	// of hanging on the unconditional pipe receive.
	t.Run("stdout never drains after exit confirmed", func(t *testing.T) {
		exitCh := make(chan struct{})
		close(exitCh)
		stdoutCh := make(chan pipeResult) // never delivers
		stderrCh := make(chan pipeResult, 1)
		stderrCh <- pipeResult{}

		start := time.Now()
		_, _, err := waitForExitAndOutput(exitCh, stdoutCh, stderrCh, shortDeadline, nil)
		elapsed := time.Since(start)

		if !errors.Is(err, ErrExecInternal) {
			t.Fatalf("err = %v, want E_EXEC_INTERNAL", err)
		}
		if elapsed > time.Second {
			t.Fatalf("took %v, want close to the %v deadline", elapsed, shortDeadline)
		}
	})

	t.Run("stderr never drains after exit confirmed", func(t *testing.T) {
		exitCh := make(chan struct{})
		close(exitCh)
		stdoutCh := make(chan pipeResult, 1)
		stdoutCh <- pipeResult{}
		stderrCh := make(chan pipeResult) // never delivers

		start := time.Now()
		_, _, err := waitForExitAndOutput(exitCh, stdoutCh, stderrCh, shortDeadline, nil)
		elapsed := time.Since(start)

		if !errors.Is(err, ErrExecInternal) {
			t.Fatalf("err = %v, want E_EXEC_INTERNAL", err)
		}
		if elapsed > time.Second {
			t.Fatalf("took %v, want close to the %v deadline", elapsed, shortDeadline)
		}
	})
}

func TestPSRunner_HappyPath(t *testing.T) {
	r := newTestRunner(t)
	out, err := r.Run(context.Background(), baseOptions("Write-Output 'hello'; exit 3"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out.ExitCode != 3 {
		t.Fatalf("ExitCode = %d, want 3", out.ExitCode)
	}
	if strings.TrimSpace(string(out.Stdout)) != "hello" {
		t.Fatalf("Stdout = %q, want %q", out.Stdout, "hello")
	}
	if out.StdoutTruncated || out.StderrTruncated {
		t.Fatalf("unexpected truncation: stdout=%v stderr=%v", out.StdoutTruncated, out.StderrTruncated)
	}
}

func TestPSRunner_InvalidOptions(t *testing.T) {
	r := newTestRunner(t)
	base := baseOptions("Write-Output 'x'")

	cases := []struct {
		name string
		mut  func(*Options)
	}{
		{"empty command", func(o *Options) { o.Command = "   " }},
		{"zero timeout", func(o *Options) { o.Timeout = 0 }},
		{"zero max processes", func(o *Options) { o.MaxProcesses = 0 }},
		{"zero max memory", func(o *Options) { o.MaxMemoryBytes = 0 }},
		{"zero max output", func(o *Options) { o.MaxOutputBytes = 0 }},
		{"relative work dir", func(o *Options) { o.WorkDir = `relative\path` }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opts := base
			c.mut(&opts)
			if _, err := r.Run(context.Background(), opts); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("Run() error = %v, want E_INVALID_ARGUMENT", err)
			}
		})
	}
}

// TestPSRunner_ConcurrentRunsAcrossRunners exercises many concurrent Run
// calls spread across separate Runner instances. Before createProcessMu
// was made process-wide (rather than per-Runner), CreateProcess's
// bInheritHandles=true could let one Runner's child inherit another
// still-open Runner's pipe write end, which would either hang that
// other Run call waiting for EOF or never happen deterministically
// enough to reproduce -- so this asserts every call finishes within a
// bounded time with its own correct, uncontaminated output.
func TestPSRunner_ConcurrentRunsAcrossRunners(t *testing.T) {
	const n = 12
	var wg sync.WaitGroup
	errs := make([]error, n)
	outs := make([]string, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := newTestRunner(t)
			out, err := r.Run(context.Background(), baseOptions(fmt.Sprintf("Write-Output 'run-%d'", i)))
			errs[i] = err
			outs[i] = strings.TrimSpace(string(out.Stdout))
		}(i)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("concurrent Run calls did not all finish within 30s; likely a handle-inheritance hang")
	}

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Errorf("run %d: error = %v", i, errs[i])
		}
		want := fmt.Sprintf("run-%d", i)
		if outs[i] != want {
			t.Errorf("run %d: Stdout = %q, want %q", i, outs[i], want)
		}
	}
}

func TestPSRunner_OutputTruncated(t *testing.T) {
	r := newTestRunner(t)
	opts := baseOptions(`1..20000 | ForEach-Object { Write-Output 'line of output padding to grow the buffer past the cap' }`)
	opts.MaxOutputBytes = 4096

	out, err := r.Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !out.StdoutTruncated {
		t.Fatalf("expected StdoutTruncated = true")
	}
	if int64(len(out.Stdout)) != opts.MaxOutputBytes {
		t.Fatalf("len(Stdout) = %d, want %d", len(out.Stdout), opts.MaxOutputBytes)
	}
	if out.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0 (truncation must not escalate to failure)", out.ExitCode)
	}
}

// TestPSRunner_Timeout_KillsProcessTree covers AC-10 / TC-EXEC: a
// grandchild process must die along with its parent when the tool call
// times out, not just the directly-spawned pwsh.
//
// The runner's clock starts when the parent pwsh starts, and the grandchild
// is a second pwsh cold start (about 0.9s on an idle machine, much more on a
// loaded CI runner). If the timeout fires before the grandchild has written
// anything, nothing was tested: the premise (a live grandchild) never held.
// That attempt is retried with a longer timeout instead of being reported as
// a Job Object failure. A grandchild that started and is still advancing after
// the timeout always fails the test.
func TestPSRunner_Timeout_KillsProcessTree(t *testing.T) {
	for _, timeout := range []time.Duration{2 * time.Second, 5 * time.Second, 12 * time.Second} {
		if started := runTimeoutKillsProcessTree(t, timeout); started {
			return
		}
		t.Logf("grandchild had not started within %v; retrying with a longer timeout", timeout)
	}
	t.Fatalf("grandchild never wrote a counter value even with a 12s timeout; process tree may not have started")
}

// runTimeoutKillsProcessTree runs one attempt and reports whether the
// grandchild was seen running before the timeout. It fails the test if the
// timeout does not return promptly or if a started grandchild survives.
func runTimeoutKillsProcessTree(t *testing.T, timeout time.Duration) bool {
	t.Helper()
	r := newTestRunner(t)
	counterPath := filepath.Join(t.TempDir(), "counter.txt")
	t.Setenv("BRUNEL_TEST_COUNTER_FILE", counterPath)

	script := `
$psi = New-Object System.Diagnostics.ProcessStartInfo
$psi.FileName = 'pwsh'
$psi.ArgumentList.Add('-NoProfile')
$psi.ArgumentList.Add('-NonInteractive')
$psi.ArgumentList.Add('-Command')
$psi.ArgumentList.Add('$i=0; while ($true) { Set-Content -Path $env:BRUNEL_TEST_COUNTER_FILE -Value $i; $i++; Start-Sleep -Milliseconds 100 }')
$psi.UseShellExecute = $false
[System.Diagnostics.Process]::Start($psi) | Out-Null
Start-Sleep -Seconds 30
`
	opts := baseOptions(script)
	opts.Timeout = timeout

	start := time.Now()
	_, err := r.Run(context.Background(), opts)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrToolTimeout) {
		t.Fatalf("Run() error = %v, want E_TOOL_TIMEOUT", err)
	}
	if elapsed > timeout+8*time.Second {
		t.Fatalf("Run() took %v, expected to return shortly after the %v timeout", elapsed, timeout)
	}

	v0, ok := readCounter(t, counterPath)
	if !ok {
		return false
	}
	time.Sleep(700 * time.Millisecond)
	v1, _ := readCounter(t, counterPath)
	if v1 != v0 {
		t.Fatalf("counter still advancing after timeout (v0=%d v1=%d): grandchild survived Job Object termination", v0, v1)
	}
	return true
}

func readCounter(t *testing.T, path string) (int, bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false
	}
	return v, true
}

// TestPSRunner_JobBoundBeforeResume covers the escape-window requirement
// from spec §6.4: AssignProcessToJobObject must have already completed
// before the suspended thread ever runs a single instruction.
func TestPSRunner_JobBoundBeforeResume(t *testing.T) {
	r := newTestRunner(t)
	markerPath := filepath.Join(t.TempDir(), "marker.txt")
	t.Setenv("BRUNEL_TEST_MARKER_FILE", markerPath)

	var mu sync.Mutex
	var hookErr error
	var sawMember bool

	testHookAfterAssignBeforeResume = func(job, process syscall.Handle) {
		mu.Lock()
		defer mu.Unlock()
		// Independent evidence the thread truly hasn't run yet: its
		// first scripted action (writing the marker file) must not
		// have happened at the point this hook fires.
		if _, statErr := os.Stat(markerPath); !os.IsNotExist(statErr) {
			hookErr = errors.New("marker file already exists before ResumeThread; child ran before job assignment")
			return
		}
		member, err := isProcessInJob(process, job)
		if err != nil {
			hookErr = err
			return
		}
		sawMember = member
	}
	defer func() { testHookAfterAssignBeforeResume = nil }()

	_, err := r.Run(context.Background(), baseOptions(`Set-Content -Path $env:BRUNEL_TEST_MARKER_FILE -Value '1'`))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if hookErr != nil {
		t.Fatalf("hook observed a problem: %v", hookErr)
	}
	if !sawMember {
		t.Fatalf("process was not a job member at the point AssignProcessToJobObject returned, before ResumeThread")
	}
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("marker file was not written after Run() returned: %v", err)
	}
}
