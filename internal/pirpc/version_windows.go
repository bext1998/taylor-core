//go:build windows

package pirpc

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
)

// runVersionProbe runs the version probe inside its own Job Object, started
// suspended and bound to the job before it executes, the same way the RPC
// launcher does (INV-7). Whatever way the probe ends - normal exit, failure,
// timeout or cancellation - the job is terminated afterwards, so no
// descendant it started survives, and a grandchild holding the stdout pipe
// cannot keep the call waiting for EOF.
func runVersionProbe(ctx context.Context, path string, args []string, workDir string, stdout io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	job, err := createJobObject()
	if err != nil {
		return fmt.Errorf("create job object: %w", err)
	}
	defer closeJob(job) // KILL_ON_JOB_CLOSE: a last line of defence on every return
	if err := setJobLimits(job); err != nil {
		return fmt.Errorf("configure job object: %w", err)
	}

	stdinRead, stdinWrite, err := createPipe()
	if err != nil {
		return err
	}
	syscall.CloseHandle(stdinWrite) // the probe reads EOF immediately
	stdoutRead, stdoutWrite, err := createPipe()
	if err != nil {
		syscall.CloseHandle(stdinRead)
		return err
	}
	setInheritable(stdoutRead, false)
	stderrRead, stderrWrite, err := createPipe()
	if err != nil {
		syscall.CloseHandle(stdinRead)
		syscall.CloseHandle(stdoutRead)
		syscall.CloseHandle(stdoutWrite)
		return err
	}
	setInheritable(stderrRead, false)

	pi, err := startSuspendedProcess(workDir, buildCmdLine(path, args), stdinRead, stdoutWrite, stderrWrite, nil)
	// The child owns its copies of these ends; closing ours lets the read ends
	// see EOF when the child (and anything it handed them to) is gone.
	syscall.CloseHandle(stdinRead)
	syscall.CloseHandle(stdoutWrite)
	syscall.CloseHandle(stderrWrite)
	if err != nil {
		syscall.CloseHandle(stdoutRead)
		syscall.CloseHandle(stderrRead)
		return err
	}
	defer syscall.CloseHandle(pi.Process)

	if _, _, e := syscall.SyscallN(procAssignProcessToJobObject.Addr(), uintptr(job), uintptr(pi.Process)); e != 0 {
		syscall.TerminateProcess(pi.Process, 1)
		syscall.CloseHandle(pi.Thread)
		syscall.CloseHandle(stdoutRead)
		syscall.CloseHandle(stderrRead)
		return fmt.Errorf("bind to job object: %w", e)
	}
	if ret, _, e := syscall.SyscallN(procResumeThread.Addr(), uintptr(pi.Thread)); ret == 0xFFFFFFFF {
		terminateJobObject(job)
		syscall.CloseHandle(pi.Thread)
		syscall.CloseHandle(stdoutRead)
		syscall.CloseHandle(stderrRead)
		return fmt.Errorf("resume: %w", e)
	}
	syscall.CloseHandle(pi.Thread)

	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		f := os.NewFile(uintptr(stdoutRead), "probe-stdout")
		defer f.Close()
		_, _ = io.Copy(stdout, f)
	}()
	go func() {
		defer readers.Done()
		f := os.NewFile(uintptr(stderrRead), "probe-stderr")
		defer f.Close()
		_, _ = io.Copy(io.Discard, f) // drained so the probe never blocks; never reported
	}()

	exited := make(chan struct{})
	go func() {
		_, _ = syscall.WaitForSingleObject(pi.Process, syscall.INFINITE)
		close(exited)
	}()

	var runErr error
	select {
	case <-exited:
		var code uint32
		if err := syscall.GetExitCodeProcess(pi.Process, &code); err != nil {
			runErr = err
		} else if code != 0 {
			runErr = fmt.Errorf("exit status %d", code)
		}
	case <-ctx.Done():
		runErr = ctx.Err()
	}
	// The probe is done (or abandoned): take the whole tree down, then let the
	// readers see EOF.
	terminateJobObject(job)
	<-exited
	readers.Wait()
	return runErr
}
