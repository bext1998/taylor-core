package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/bext1998/brunel/internal/agent"
	"github.com/bext1998/brunel/internal/approval"
	"github.com/bext1998/brunel/internal/completion"
	"github.com/bext1998/brunel/internal/config"
	"github.com/bext1998/brunel/internal/pirpc"
	"github.com/bext1998/brunel/internal/safety"
	"github.com/bext1998/brunel/internal/session"
	"github.com/bext1998/brunel/internal/tui"
	"github.com/bext1998/brunel/internal/workspace"
)

// Exit codes of the interactive / one-shot entry point. They are stable:
// scripts may rely on them.
const (
	exitOK      = 0 // the run completed (or the TUI exited normally)
	exitFailed  = 1 // the run failed or ended incomplete, or a runtime error
	exitInvalid = 2 // invalid invocation (E_INVALID_ARGUMENT)
)

const errInvalidArgument = "E_INVALID_ARGUMENT"

const usageText = `Usage:
  brunel [flags]              start the interactive TUI (requires a TTY)
  brunel [flags] "<task>"     run one task in plain-text mode
  brunel login [openrouter]   save your OpenRouter key in Windows Credential Manager
  brunel logout [openrouter]  remove it (see "brunel login --help")

Flags (may appear before or after the task):
  --mode workspace|readonly   default workspace; readonly rejects writes and PowerShell
  --model <id>                model passed verbatim to pi --model, e.g. openrouter/<model>
  --name <name>               name the session (named sessions are kept)
  --resume <name|id>          resume an existing session
  --report <path>             write the CompletionReport JSON here (plain-text mode only);
                              the file must not exist and must lie inside the workspace

Providers and model ids are whatever the installed pi version supports; the
set changes with the pi version. Brunel only stores an OpenRouter key (Windows
Credential Manager, target "Brunel/OpenRouter"); for other providers pi uses
its own credential configuration.

Exit codes: 0 completed, 1 failed or incomplete, 2 invalid arguments.
`

// cliOptions are the parsed flags of the interactive / one-shot entry point
// (spec.md §4.1).
type cliOptions struct {
	task    string
	hasTask bool
	mode    *string
	model   *string
	name    *string
	resume  string
	report  string
	help    bool
}

// usageError is an invocation error: E_INVALID_ARGUMENT, exit code 2.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

// parseCLI parses args, allowing flags on either side of the task, as in
// spec.md §3.2's own example `brunel "<task>" --report out.json`. Everything
// after a literal "--" is positional.
func parseCLI(args []string) (cliOptions, error) {
	var opts cliOptions
	var mode, model, name string
	fs := flag.NewFlagSet("brunel", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&mode, "mode", "", "")
	fs.StringVar(&model, "model", "", "")
	fs.StringVar(&name, "name", "", "")
	fs.StringVar(&opts.resume, "resume", "", "")
	fs.StringVar(&opts.report, "report", "", "")

	flagArgs, tail := args, []string(nil)
	for i, a := range args {
		if a == "--" {
			flagArgs, tail = args[:i], args[i+1:]
			break
		}
	}
	var positional []string
	rest := flagArgs
	for {
		if err := fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				opts.help = true
				return opts, nil
			}
			return opts, &usageError{err.Error()}
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	positional = append(positional, tail...)

	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if set["mode"] {
		if mode != config.ModeWorkspace && mode != config.ModeReadonly {
			return opts, &usageError{fmt.Sprintf("--mode must be workspace or readonly, got %q", mode)}
		}
		opts.mode = &mode
	}
	if set["model"] {
		if strings.TrimSpace(model) == "" {
			return opts, &usageError{"--model is empty"}
		}
		opts.model = &model
	}
	if set["name"] {
		if strings.TrimSpace(name) == "" {
			return opts, &usageError{"--name is empty"}
		}
		opts.name = &name
	}
	if set["resume"] && strings.TrimSpace(opts.resume) == "" {
		return opts, &usageError{"--resume is empty"}
	}
	if set["report"] && strings.TrimSpace(opts.report) == "" {
		return opts, &usageError{"--report is empty"}
	}

	switch len(positional) {
	case 0:
	case 1:
		opts.hasTask = true
		opts.task = strings.TrimSpace(positional[0])
		if opts.task == "" {
			// EC-1: rejected before any session or provider exists.
			return opts, &usageError{"task is empty"}
		}
	default:
		return opts, &usageError{`the task must be one argument; quote it: brunel "<task>"`}
	}
	if !opts.hasTask && opts.report != "" {
		// --report never enters the TUI (spec.md §4.1); it needs a task.
		return opts, &usageError{"--report requires a task (plain-text mode)"}
	}
	return opts, nil
}

// terminals reports which standard streams are attached to a terminal.
type terminals struct {
	stdin, stdout bool
}

// cliEnv is everything runCLI touches outside its arguments, so tests can
// replace the TTY check, the agent, and the TUI.
type cliEnv struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	tty            terminals
	getwd          func() (string, error)
	executable     func() (string, error)
	userProfile    string // empty: the real profile
	credentials    config.CredentialSource
	// credentialWriter backs `brunel login` / `logout`; nil: the platform one.
	credentialWriter config.CredentialWriter
	// readSecret reads one line from the terminal without echo.
	readSecret  func() (string, error)
	sessionRoot string // empty: the default store root
	// newRunner builds the agent for one CLI invocation.
	newRunner func(launch pirpc.LaunchOptions, cred pirpc.Credential, s *session.Session, root, mode, exe string) agentRunner
	// runTUI runs the interactive TUI until the user quits.
	runTUI func(opts tui.Options, bind func(sink agent.EventSink, approver safety.Approver)) error
	// startBroker opens the approval channel (internal/approval).
	startBroker func(ctx context.Context, a safety.Approver) (approvalBroker, error)
	// signalContext returns the context cancelled by Ctrl+C in plain mode.
	signalContext func() (context.Context, context.CancelFunc)
}

// agentRunner is the agent plus the environment hook the approval channel
// needs.
type agentRunner interface {
	agent.Agent
	SetExtraEnv(map[string]string)
	// SetPendingApproval supplies the report's pending_approval fact.
	SetPendingApproval(func() *completion.ApprovalFact)
}

type approvalBroker interface {
	Env() map[string]string
	Close() error
}

func defaultCLIEnv(stdin io.Reader, stdout, stderr io.Writer, tty terminals) cliEnv {
	return cliEnv{
		stdin:      stdin,
		stdout:     stdout,
		stderr:     stderr,
		tty:        tty,
		getwd:      os.Getwd,
		executable: os.Executable,
		readSecret: readTerminalSecret,
		newRunner: func(launch pirpc.LaunchOptions, cred pirpc.Credential, s *session.Session, root, mode, exe string) agentRunner {
			return agent.NewRuntime(launch, cred, s, root, mode, exe)
		},
		runTUI: func(opts tui.Options, bind func(agent.EventSink, safety.Approver)) error {
			app := tui.New(opts)
			bind(app.Sink(), app.Approver())
			return app.Run()
		},
		startBroker: func(ctx context.Context, a safety.Approver) (approvalBroker, error) {
			return approval.StartBroker(ctx, a)
		},
		signalContext: func() (context.Context, context.CancelFunc) {
			return signal.NotifyContext(context.Background(), os.Interrupt)
		},
	}
}

// runCLI is the interactive / one-shot entry point (spec.md §4.1).
func runCLI(args []string, env cliEnv) int {
	if command, ok := authCommand(args); ok {
		return runAuth(command, args[1:], env)
	}
	opts, err := parseCLI(args)
	if opts.help {
		_, _ = io.WriteString(env.stdout, usageText)
		return exitOK
	}
	if err != nil {
		return reportError(env.stderr, err)
	}
	if !opts.hasTask && !(env.tty.stdin && env.tty.stdout) {
		return reportError(env.stderr, &usageError{`interactive mode requires a terminal; pass a task: brunel "<task>"`})
	}

	setup, err := prepareRun(context.Background(), opts, env)
	if err != nil {
		return reportError(env.stderr, err)
	}
	if opts.hasTask {
		return runPlain(opts, setup, env)
	}
	return runInteractive(setup, env)
}

// runSetup is what both modes share once the invocation is valid.
type runSetup struct {
	root    string
	mode    string
	model   string
	session *session.Session
	agent   agentRunner
	// reportPath is the resolved --report target, empty when not requested.
	reportPath string
}

// prepareRun binds the workspace, resolves configuration and credentials,
// and opens the session. It runs only after the invocation is known to be
// valid, so an empty task never creates a session or reaches the provider.
func prepareRun(ctx context.Context, opts cliOptions, env cliEnv) (*runSetup, error) {
	wd, err := env.getwd()
	if err != nil {
		return nil, codedError{workspace.ErrWorkspaceInvalid.Code, "cannot determine the working directory"}
	}
	bound, err := workspace.Bind(wd)
	if err != nil {
		return nil, err
	}
	root := bound.Root()

	// Check the report target first: a path that can never be written must
	// fail before a session exists or the model is called.
	var reportPath string
	if opts.report != "" {
		if reportPath, err = resolveReportPath(bound, root, opts.report); err != nil {
			return nil, err
		}
	}

	resolved, err := config.NewLoader(root, env.userProfile, env.credentials).Load(ctx, config.CLIOverrides{Mode: opts.mode, ModelID: opts.model})
	if err != nil {
		return nil, err
	}
	mode := resolved.Config.Mode
	if mode != config.ModeWorkspace && mode != config.ModeReadonly {
		return nil, codedError{config.ErrConfigInvalid.Code, fmt.Sprintf("mode %q is not available from the CLI", mode)}
	}
	model := strings.TrimSpace(resolved.Config.ModelID)
	if model == "" {
		return nil, &usageError{`no model configured: pass --model <id> or set "model_id" in .brunel\config.json`}
	}

	exe, err := env.executable()
	if err != nil {
		return nil, codedError{"E_RUNTIME_ERROR", "cannot locate the brunel executable"}
	}
	launch := pirpc.LaunchOptions{
		Model: model,
		// The Pi extension ships next to brunel.exe, not in the user's
		// workspace.
		ExtensionPath: filepath.Join(filepath.Dir(exe), "taylor-tools.ts"),
	}

	// Only an OpenRouter run needs Brunel's stored key; any other provider
	// resolves its own credentials inside pi (spec.md §5.3).
	var cred pirpc.Credential
	if strings.EqualFold(launch.EffectiveProvider(), "openrouter") {
		key := resolved.OpenRouterAPIKey()
		if key == "" {
			return nil, codedError{config.ErrConfigCredential.Code, `no OpenRouter key in Windows Credential Manager (target "Brunel/OpenRouter")`}
		}
		cred = pirpc.OpenRouterCredential(key)
	}

	store, err := session.NewStore(env.sessionRoot)
	if err != nil {
		return nil, err
	}
	var sess *session.Session
	if opts.resume != "" {
		sess, err = store.Resume(opts.resume)
		if err == nil && opts.name != nil {
			err = sess.SetName(opts.name)
		}
	} else {
		sess, err = store.Create(session.CreateOptions{
			Name:          opts.name,
			WorkspaceRoot: root,
			Mode:          mode,
			ModelID:       model,
			IsGitRepo:     isGitRepo(root),
		})
	}
	if err != nil {
		return nil, err
	}

	runner := env.newRunner(launch, cred, sess, root, mode, exe)
	// Every tool call re-binds the workspace in its own process; hand down
	// the identity of the directory bound here so a later call can tell if
	// the path was repointed meanwhile (INV-5).
	runner.SetExtraEnv(map[string]string{"BRUNEL_WORKSPACE_ID": bound.Identity()})

	return &runSetup{
		root:    root,
		mode:    mode,
		model:   model,
		session: sess,
		agent:   runner,

		reportPath: reportPath,
	}, nil
}

func isGitRepo(root string) bool {
	_, err := os.Stat(filepath.Join(root, ".git"))
	return err == nil
}

// runPlain runs one task with plain-text streaming (no alternate screen).
func runPlain(opts cliOptions, setup *runSetup, env cliEnv) int {
	ctx, stop := env.signalContext()
	defer stop()

	sink := newPlainSink(env.stdout, env.stderr, env.tty.stdout)
	// A TTY on stdin means a human can answer approval prompts (spec.md
	// §5.2); without one no approver exists and CONFIRM fails closed with
	// E_APPROVAL_REQUIRED_NO_TTY (§6.3).
	if env.tty.stdin {
		relay := &relayApprover{inner: newTTYApprover(env.stdin, env.stderr), sink: sink}
		setup.agent.SetPendingApproval(relay.Pending)
		broker, err := env.startBroker(ctx, relay)
		if err != nil {
			sink.warn("approval prompts unavailable (" + err.Error() + "); commands that need confirmation will be refused")
		} else {
			defer broker.Close()
			setup.agent.SetExtraEnv(broker.Env())
		}
	}

	rep, runErr := setup.agent.Run(ctx, opts.task, sink)
	sink.finish()

	code := exitOK
	if rep == nil || rep.Status != completion.StatusCompleted {
		code = exitFailed
	}
	if runErr != nil {
		code = exitFailed
		reportError(env.stderr, runErr)
	}
	if setup.reportPath != "" && rep != nil {
		if err := completion.WriteFile(setup.reportPath, rep); err != nil {
			if errors.Is(err, fs.ErrExist) {
				reportError(env.stderr, codedError{"E_FILE_EXISTS", "the --report file already exists; it is not overwritten"})
			} else {
				reportError(env.stderr, codedError{"E_REPORT_WRITE", "cannot write the report: " + err.Error()})
			}
			code = exitFailed
		}
	}
	if rep != nil {
		_, _ = fmt.Fprintf(env.stderr, "brunel: run %s\n", rep.Status)
	}

	status := session.ExitStatusClean
	if ctx.Err() != nil {
		status = session.ExitStatusAborted
	}
	return closeSession(setup.session, status, code, env.stderr)
}

// sessionCloser is the part of *session.Session the exit path needs.
type sessionCloser interface {
	Close(status string) error
}

// closeSession finalizes the session and folds a failure into the exit
// code: if the session metadata could not be written or cleaned up, the
// caller must not see a success status (the recovery evidence is unreliable).
func closeSession(s sessionCloser, status string, code int, stderr io.Writer) int {
	if err := s.Close(status); err != nil {
		reportError(stderr, err)
		return exitFailed
	}
	return code
}

// runInteractive runs the TUI. Each submitted task is one agent run in the
// same session; Ctrl+C cancels the active run, and exits when idle.
func runInteractive(setup *runSetup, env cliEnv) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var broker approvalBroker
	// lastRunCancelled records whether the most recent run was cancelled
	// (Ctrl+C). Quitting after a cancelled run must keep the session as
	// aborted recovery evidence, like plain-text mode does.
	var lastRunCancelled atomic.Bool
	opts := tui.Options{
		Model: setup.model,
		Mode:  setup.mode,
		Run: func(runCtx context.Context, task string, sink agent.EventSink) (*completion.Report, error) {
			rep, err := setup.agent.Run(runCtx, task, sink)
			lastRunCancelled.Store(runCtx.Err() != nil)
			return rep, err
		},
	}
	err := env.runTUI(opts, func(sink agent.EventSink, approver safety.Approver) {
		relay := &relayApprover{inner: approver, sink: sink}
		setup.agent.SetPendingApproval(relay.Pending)
		b, err := env.startBroker(ctx, relay)
		if err != nil {
			sink.Emit(agent.Event{Kind: agent.EventApprovalResolved, Text: "channel unavailable (" + err.Error() + "); commands that need confirmation will be refused"})
			return
		}
		broker = b
		setup.agent.SetExtraEnv(b.Env())
	})
	cancel()
	if broker != nil {
		_ = broker.Close()
	}

	code := exitOK
	if err != nil {
		code = exitFailed
		reportError(env.stderr, codedError{"E_RUNTIME_ERROR", "tui: " + err.Error()})
	}
	status := session.ExitStatusClean
	if lastRunCancelled.Load() {
		status = session.ExitStatusAborted
	}
	return closeSession(setup.session, status, code, env.stderr)
}

// codedError is a CLI-level error with a stable code.
type codedError struct {
	code, msg string
}

func (e codedError) Error() string { return e.msg }

// reportError prints "brunel: <CODE>: <message>" and returns the exit code
// for err. Messages come from errors that are already secret-free (the
// pirpc errors are redacted; config errors never carry the key).
func reportError(w io.Writer, err error) int {
	code, exit := errorCode(err)
	msg := err.Error()
	if strings.HasPrefix(msg, code) {
		_, _ = fmt.Fprintf(w, "brunel: %s\n", msg)
	} else {
		_, _ = fmt.Fprintf(w, "brunel: %s: %s\n", code, msg)
	}
	if _, isUsage := err.(*usageError); isUsage {
		_, _ = fmt.Fprintln(w, `run "brunel --help" for usage`)
	}
	return exit
}

func errorCode(err error) (string, int) {
	var usage *usageError
	if errors.As(err, &usage) {
		return errInvalidArgument, exitInvalid
	}
	var coded codedError
	if errors.As(err, &coded) {
		return coded.code, exitFailed
	}
	var piErr *pirpc.Error
	if errors.As(err, &piErr) && piErr.Code != "" {
		return piErr.Code, exitFailed
	}
	for _, code := range []string{config.ErrorCode(err), workspace.ErrorCode(err), session.ErrorCode(err)} {
		if code != "" {
			if code == errInvalidArgument {
				return code, exitInvalid
			}
			return code, exitFailed
		}
	}
	return "E_RUNTIME_ERROR", exitFailed
}
