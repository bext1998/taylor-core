//go:build windows

package pirpc

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

// This file is the Windows implementation of the pi --mode rpc subprocess
// handle. It mirrors the pattern in internal/exec: a Job Object with
// KILL_ON_JOB_CLOSE so no Pi descendant outlives Close(), a suspended
// CreateProcess, and pipes for stdin/stdout. The cross-platform run loop
// that drives piProcess lives in internal/agent; the RPC wire protocol is
// decoded here (see runtime.go).

const (
	createSuspended          = 0x00000004
	createUnicodeEnvironment = 0x00000400

	jobObjectLimitKillOnJobClose = 0x00002000

	infinite = 0xFFFFFFFF

	terminationGracePeriod = 5 * time.Second
)

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procCreateJobObject          = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	procTerminateJobObject       = kernel32.NewProc("TerminateJobObject")
	procCreatePipe               = kernel32.NewProc("CreatePipe")
	procGetHandleInformation     = kernel32.NewProc("GetHandleInformation")
	procSetHandleInformation     = kernel32.NewProc("SetHandleInformation")
	procWaitForSingleObject      = kernel32.NewProc("WaitForSingleObject")
	procGetExitCodeProcess       = kernel32.NewProc("GetExitCodeProcess")
	procResumeThread             = kernel32.NewProc("ResumeThread")
	procCreateProcessW           = kernel32.NewProc("CreateProcessW")
	procWriteFile                = kernel32.NewProc("WriteFile")

	inheritableSecurityAttributes    = &syscall.SecurityAttributes{InheritHandle: 1}
	nonInheritableSecurityAttributes = &syscall.SecurityAttributes{InheritHandle: 0}
)

// jobObjectBasicLimitInformation mirrors JOBOBJECT_BASIC_LIMIT_INFORMATION
// as laid out by winbase.h: field order/types match the C ABI so Go's
// amd64 struct layout produces identical padding to the Win32 header.
type jobObjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

// jobObjectExtendedLimitInformationT mirrors JOBOJECT_EXTENDED_LIMIT_INFORMATION.
type jobObjectExtendedLimitInformationT struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IoInfo                struct {
		ReadOperationCount  uint64
		WriteOperationCount uint64
		OtherOperationCount uint64
		ReadTransferCount   uint64
		WriteTransferCount  uint64
		OtherTransferCount  uint64
	}
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

const jobObjectExtendedLimitInformation = 9 // JOBOJECT_EXTENDED_LIMIT_INFORMATION

// startPiProcess launches pi (issue #9 §1) and returns a handle the
// internal/agent run loop drives. It builds the frozen args, injects
// credentials and the extension's required environment (BRUNEL_EXE /
// BRUNEL_MODE), binds the process tree to a Job Object, and resumes the
// suspended process.
func startPiProcess(_ context.Context, piPath string, args []string, env []string, workDir string) (PiProcess, error) {
	cmdLine := buildCmdLine(piPath, args)
	envBlock, err := buildEnvBlock(env)
	if err != nil {
		return nil, codeError(ErrPiRuntimeRequired.Code, "failed to build Pi environment", err)
	}

	job, err := createJobObject()
	if err != nil {
		return nil, codeError(ErrPiRuntimeRequired.Code, "failed to create a job object for the pi process", err)
	}
	if err := setJobLimits(job); err != nil {
		syscall.CloseHandle(job)
		return nil, codeError(ErrPiRuntimeRequired.Code, "failed to configure job object limits", err)
	}

	stdinRead, stdinWrite, err := createPipe()
	if err != nil {
		closeJob(job)
		return nil, codeError(ErrPiRuntimeRequired.Code, "failed to create the pi stdin pipe", err)
	}
	// The child reads stdin (inheritable); the parent keeps the write end
	// to send RPC commands, so it must not be inherited.
	setInheritable(stdinWrite, false)

	stdoutRead, stdoutWrite, err := createPipe()
	if err != nil {
		closeJob(job)
		syscall.CloseHandle(stdinRead)
		syscall.CloseHandle(stdinWrite)
		return nil, codeError(ErrPiRuntimeRequired.Code, "failed to create the pi stdout pipe", err)
	}
	// The child writes stdout (inheritable); the parent reads the other
	// end, which must not be inherited.
	setInheritable(stdoutRead, false)

	stderrRead, stderrWrite, err := createPipe()
	if err != nil {
		closeJob(job)
		syscall.CloseHandle(stdinRead)
		syscall.CloseHandle(stdinWrite)
		syscall.CloseHandle(stdoutRead)
		syscall.CloseHandle(stdoutWrite)
		return nil, codeError(ErrPiRuntimeRequired.Code, "failed to create the pi stderr pipe", err)
	}
	setInheritable(stderrWrite, false)

	pi, err := startSuspendedProcess(workDir, cmdLine, stdinRead, stdoutWrite, stderrWrite, envBlock)
	if err != nil {
		closeJob(job)
		syscall.CloseHandle(stdinRead)
		syscall.CloseHandle(stdinWrite)
		syscall.CloseHandle(stdoutRead)
		syscall.CloseHandle(stdoutWrite)
		syscall.CloseHandle(stderrRead)
		syscall.CloseHandle(stderrWrite)
		return nil, codeError(ErrPiRuntimeRequired.Code, "failed to start the pi subprocess", err)
	}

	// Bind the whole process tree to the Job Object before resuming.
	if _, _, err := syscall.SyscallN(procAssignProcessToJobObject.Addr(), uintptr(job), uintptr(pi.Process)); err != 0 {
		syscall.TerminateProcess(pi.Process, 1)
		syscall.CloseHandle(pi.Process)
		syscall.CloseHandle(pi.Thread)
		closeJob(job)
		syscall.CloseHandle(stdinRead)
		syscall.CloseHandle(stdinWrite)
		syscall.CloseHandle(stdoutRead)
		syscall.CloseHandle(stdoutWrite)
		syscall.CloseHandle(stderrRead)
		syscall.CloseHandle(stderrWrite)
		return nil, codeError(ErrPiRuntimeRequired.Code, "failed to bind the pi process to a job object", err)
	}

	// Resume the child; it now runs inside the bounded job. ResumeThread
	// returns the previous suspend count; 0xFFFFFFFF signals failure.
	if ret, _, callErr := syscall.SyscallN(procResumeThread.Addr(), uintptr(pi.Thread)); ret == 0xFFFFFFFF {
		syscall.TerminateProcess(pi.Process, 1)
		syscall.CloseHandle(pi.Process)
		syscall.CloseHandle(pi.Thread)
		closeJob(job)
		syscall.CloseHandle(stdinRead)
		syscall.CloseHandle(stdinWrite)
		syscall.CloseHandle(stdoutRead)
		syscall.CloseHandle(stdoutWrite)
		syscall.CloseHandle(stderrRead)
		syscall.CloseHandle(stderrWrite)
		return nil, codeError(ErrPiRuntimeRequired.Code, "failed to resume the pi process", callErr)
	}

	// The thread is now running; the handle is no longer needed.
	syscall.CloseHandle(pi.Thread)

	// The child owns its own copies of the stdio ends it was given; the
	// parent closes its redundant copies so the read ends see EOF when the
	// child exits. The parent keeps stdinWrite (to send commands),
	// stdoutRead (to decode events) and stderrRead (for diagnostics).
	syscall.CloseHandle(stdinRead)
	syscall.CloseHandle(stdoutWrite)
	syscall.CloseHandle(stderrWrite)

	p := &windowsPiProcess{
		pi:         pi.Process,
		job:        job,
		stdinWrite: stdinWrite,
		stdoutRead: stdoutRead,
		stderrRead: stderrRead,
		stdoutCh:   make(chan Event),
		doneCh:     make(chan struct{}),
		closedCh:   make(chan struct{}),
		readDone:   make(chan struct{}),
		stderrBuf:  &bytes.Buffer{},
	}
	go p.waitLoop()
	go p.readLoop()
	go p.stderrDrain()
	return p, nil
}

// windowsPiProcess is the Windows piProcess handle.
type windowsPiProcess struct {
	pi         syscall.Handle
	job        syscall.Handle
	stdinWrite syscall.Handle
	stdoutRead syscall.Handle
	stderrRead syscall.Handle

	stdoutCh chan Event
	doneCh   chan struct{}
	closedCh chan struct{}
	readDone chan struct{}

	stderrMu  sync.Mutex
	stderrBuf *bytes.Buffer

	exitMu   sync.Mutex
	exitCode int

	closeOnce sync.Once
}

// waitLoop waits for the process to exit and closes Done.
func (p *windowsPiProcess) waitLoop() {
	defer close(p.doneCh)
	syscall.WaitForSingleObject(p.pi, infinite)
	var code uint32
	syscall.GetExitCodeProcess(p.pi, &code)
	p.exitMu.Lock()
	p.exitCode = int(code)
	p.exitMu.Unlock()
}

// stderrDrain captures Pi's stderr (bounded) for translating a
// provider/protocol failure on abnormal exit.
func (p *windowsPiProcess) stderrDrain() {
	reader := &jsonlReader{h: p.stderrRead}
	for {
		line, err := reader.readLine()
		if err != nil {
			return
		}
		p.stderrMu.Lock()
		if p.stderrBuf.Len() < 16*1024 {
			p.stderrBuf.Write(line)
			p.stderrBuf.WriteByte('\n')
		}
		p.stderrMu.Unlock()
	}
}

// readLoop decodes Pi's stdout into Events until the stream ends, then
// closes the channel so a blocked receiver is guaranteed to wake: the run
// loop treats the closed channel as the authoritative "process is gone"
// signal, so no in-flight event is lost to a Done/last-event race.
func (p *windowsPiProcess) readLoop() {
	defer close(p.readDone)
	defer close(p.stdoutCh)
	reader := &jsonlReader{h: p.stdoutRead}
	for {
		line, err := reader.readLine()
		if err != nil {
			// EOF or read error: the process is gone.
			return
		}
		if ev, ok := decodeRPCEvent(line); ok {
			select {
			case p.stdoutCh <- ev:
			case <-p.closedCh:
				return
			}
		}
	}
}

// SendPrompt writes the initial prompt command to Pi's stdin.
func (p *windowsPiProcess) SendPrompt(message string) error {
	data, err := json.Marshal(RPCPrompt(message))
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFile(p.stdinWrite, data)
}

// Abort tells Pi to stop (best-effort RPC command), terminates the whole
// process tree via the Job Object, and waits (bounded) for the process to
// die so no Pi descendant outlives the call.
func (p *windowsPiProcess) Abort() error {
	_ = p.send(RPCAbort())
	terminateJobObject(p.job)
	select {
	case <-p.doneCh:
	case <-time.After(terminationGracePeriod):
	}
	return nil
}

// Events returns the decoded event stream.
func (p *windowsPiProcess) Events() <-chan Event { return p.stdoutCh }

// Done is closed when the subprocess exits.
func (p *windowsPiProcess) Done() <-chan struct{} { return p.doneCh }

// ExitCode returns the process exit status once Done has closed.
func (p *windowsPiProcess) ExitCode() int {
	p.exitMu.Lock()
	defer p.exitMu.Unlock()
	return p.exitCode
}

// CapturedStderr returns the stderr captured while the process ran, for
// translating a provider/protocol failure on abnormal exit.
func (p *windowsPiProcess) CapturedStderr() string {
	p.stderrMu.Lock()
	defer p.stderrMu.Unlock()
	return p.stderrBuf.String()
}

// Close aborts and releases every resource: kills the job, waits for the
// process and the reader goroutine to drain, then closes all handles. It
// is idempotent (closeOnce) and safe to call on every exit path.
func (p *windowsPiProcess) Close() {
	p.closeOnce.Do(func() {
		close(p.closedCh)
		terminateJobObject(p.job)
		select {
		case <-p.doneCh:
		case <-time.After(terminationGracePeriod):
		}
		select {
		case <-p.readDone:
		case <-time.After(terminationGracePeriod):
		}
		syscall.CloseHandle(p.stdinWrite)
		syscall.CloseHandle(p.stdoutRead)
		syscall.CloseHandle(p.stderrRead)
		syscall.CloseHandle(p.pi)
		closeJob(p.job)
	})
}

// send writes a raw command line to Pi's stdin.
func (p *windowsPiProcess) send(command any) error {
	data, err := json.Marshal(command)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFile(p.stdinWrite, data)
}

// writeFile writes the whole buffer to a pipe handle.
func writeFile(h syscall.Handle, buf []byte) error {
	for len(buf) > 0 {
		var n uint32
		_, _, err := syscall.SyscallN(procWriteFile.Addr(), uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&n)), 0)
		if err != 0 {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		buf = buf[n:]
	}
	return nil
}

// --- Job Object helpers ---

func createJobObject() (syscall.Handle, error) {
	h, _, err := syscall.SyscallN(procCreateJobObject.Addr(), 0, 0)
	if err != 0 || h == 0 {
		return 0, err
	}
	return syscall.Handle(h), nil
}

func setJobLimits(h syscall.Handle) error {
	// JOBOJECT_EXTENDED_LIMIT_INFORMATION: the only documented path to set
	// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE. We impose no process-count or
	// memory bounds on the pi subprocess — only kill-on-job-close, so no
	// descendant outlives the handle.
	limits := jobObjectExtendedLimitInformationT{
		BasicLimitInformation: jobObjectBasicLimitInformation{
			LimitFlags: jobObjectLimitKillOnJobClose,
		},
	}
	ret, _, callErr := syscall.SyscallN(procSetInformationJobObject.Addr(), uintptr(h), jobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits))
	// Win32: a non-zero return value means success (callErr is 0); a zero
	// return means failure with the error in callErr.
	if ret == 0 {
		return callErr
	}
	return nil
}

func terminateJobObject(h syscall.Handle) {
	if h == 0 {
		return
	}
	_, _, _ = syscall.SyscallN(procTerminateJobObject.Addr(), uintptr(h), 1)
}

func closeJob(h syscall.Handle) {
	if h != 0 {
		syscall.CloseHandle(h)
	}
}

// --- Pipe / process helpers ---

func createPipe() (readEnd, writeEnd syscall.Handle, err error) {
	_, _, e := syscall.SyscallN(procCreatePipe.Addr(),
		uintptr(unsafe.Pointer(&readEnd)),
		uintptr(unsafe.Pointer(&writeEnd)),
		uintptr(unsafe.Pointer(inheritableSecurityAttributes)),
		0)
	if e != 0 {
		return 0, 0, e
	}
	return readEnd, writeEnd, nil
}

func setInheritable(h syscall.Handle, inheritable bool) {
	var flags uint32
	if inheritable {
		flags = 1
	}
	_, _, _ = syscall.SyscallN(procSetHandleInformation.Addr(), uintptr(h), 1, uintptr(flags))
}

func buildCmdLine(piPath string, args []string) string {
	cmdLine := syscall.EscapeArg(piPath)
	for _, arg := range args {
		cmdLine += " " + syscall.EscapeArg(arg)
	}
	return cmdLine
}

// buildEnvBlock converts a KEY=VALUE slice into a double-NUL-terminated
// array for CreateProcessW's lpEnvironment.
func buildEnvBlock(env []string) ([]uint16, error) {
	var u16 []uint16
	for _, e := range env {
		enc := utf16.Encode([]rune(e))
		u16 = append(u16, enc...)
		u16 = append(u16, 0)
	}
	return append(u16, 0), nil
}

// startSuspendedProcess starts pi suspended, assigns it to the Job Object
// (already bound by the caller), then resumes it. It returns the process
// handle.
func startSuspendedProcess(workDir, cmdLine string, stdin, stdout, stderr syscall.Handle, env []uint16) (syscall.ProcessInformation, error) {
	// Mirrors internal/exec: use the syscall.CreateProcess wrapper, which
	// correctly fills PROCESS_INFORMATION (handles + IDs) and returns the
	// Win32 error directly.
	cmdLinePtr, err := syscall.UTF16PtrFromString(cmdLine)
	if err != nil {
		return syscall.ProcessInformation{}, err
	}
	workDirPtr, err := syscall.UTF16PtrFromString(workDir)
	if err != nil {
		return syscall.ProcessInformation{}, err
	}

	si := &syscall.StartupInfo{
		Flags:     syscall.STARTF_USESTDHANDLES,
		StdInput:  stdin,
		StdOutput: stdout,
		StdErr:    stderr,
	}
	si.Cb = uint32(unsafe.Sizeof(*si))

	var pi syscall.ProcessInformation
	var envPtr *uint16
	flags := uint32(createSuspended) // STARTED_SUSPENDED, not running until assigned to the job
	if len(env) > 0 {
		envPtr = &env[0]
		// buildEnvBlock always encodes a UTF-16 block; without this flag
		// CreateProcess reinterprets lpEnvironment as an ANSI multi-string
		// and rejects it with ERROR_INVALID_PARAMETER.
		flags |= createUnicodeEnvironment
	}
	err = syscall.CreateProcess(
		nil,        // lpApplicationName: quoted path in cmdLine is argv[0]
		cmdLinePtr, // lpCommandLine
		nil,        // process security attributes
		nil,        // thread security attributes
		true,       // inherit handles
		flags,
		envPtr,     // environment block (nil when empty)
		workDirPtr, // current directory
		si,
		&pi,
	)
	if err != nil {
		return syscall.ProcessInformation{}, err
	}
	return pi, nil
}

// jsonlReader reads newline-delimited JSON from a pipe handle, buffering
// partial lines across reads so a split event never breaks decoding.
type jsonlReader struct {
	h       syscall.Handle
	partial []byte
}

// readLine returns the next complete line (without the trailing newline)
// or an error once the stream ends.
func (r *jsonlReader) readLine() ([]byte, error) {
	for {
		if i := bytes.IndexByte(r.partial, '\n'); i >= 0 {
			line := r.partial[:i]
			r.partial = append([]byte{}, r.partial[i+1:]...)
			return line, nil
		}
		buf := make([]byte, 64*1024)
		n, err := syscall.Read(r.h, buf)
		if n > 0 {
			r.partial = append(r.partial, buf[:n]...)
		}
		if err != nil || n == 0 {
			if len(r.partial) > 0 {
				line := r.partial
				r.partial = nil
				return line, err
			}
			return nil, err
		}
	}
}
