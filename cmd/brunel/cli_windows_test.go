//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bext1998/brunel/internal/agent"
	"github.com/bext1998/brunel/internal/completion"
	"github.com/bext1998/brunel/internal/config"
	"github.com/bext1998/brunel/internal/pirpc"
	"github.com/bext1998/brunel/internal/safety"
	"github.com/bext1998/brunel/internal/session"
	"github.com/bext1998/brunel/internal/tui"
)

type fakeCredentials struct{ key string }

func (f fakeCredentials) OpenRouterAPIKey(context.Context) (string, error) {
	if f.key == "" {
		return "", config.ErrCredentialNotFound
	}
	return f.key, nil
}

// fakeAgent stands in for the Pi-backed runtime: it emits a fixed event
// sequence and returns a fixed report.
type fakeAgent struct {
	events []agent.Event
	report *completion.Report
	err    error

	ran  bool
	task string
	env  map[string]string
}

func (f *fakeAgent) Run(_ context.Context, task string, sink agent.EventSink) (*completion.Report, error) {
	f.ran, f.task = true, task
	for _, e := range f.events {
		sink.Emit(e)
	}
	return f.report, f.err
}

func (f *fakeAgent) SetExtraEnv(env map[string]string) { f.env = env }

type fakeBroker struct {
	approver safety.Approver
	closed   bool
}

func (b *fakeBroker) Env() map[string]string {
	return map[string]string{"BRUNEL_APPROVAL_PIPE": `\\.\pipe\test`, "BRUNEL_APPROVAL_TOKEN": "t"}
}

func (b *fakeBroker) Close() error { b.closed = true; return nil }

type harness struct {
	env      cliEnv
	stdout   *bytes.Buffer
	stderr   *bytes.Buffer
	agent    *fakeAgent
	built    int
	launch   pirpc.LaunchOptions
	cred     pirpc.Credential
	broker   *fakeBroker
	sessions string
	root     string
}

func newHarness(t *testing.T, tty terminals, key string) *harness {
	t.Helper()
	h := &harness{
		stdout:   &bytes.Buffer{},
		stderr:   &bytes.Buffer{},
		sessions: t.TempDir(),
		root:     t.TempDir(),
		agent: &fakeAgent{
			events: []agent.Event{
				{Kind: agent.EventAssistantDelta, Text: "All "},
				{Kind: agent.EventToolStarted, ToolName: "read_file"},
				{Kind: agent.EventToolFinished, ToolName: "read_file"},
				{Kind: agent.EventAssistantDelta, Text: "done."},
				{Kind: agent.EventRunFinished},
			},
			report: &completion.Report{SchemaVersion: completion.SchemaVersion, Task: "t", Status: completion.StatusCompleted},
		},
	}
	h.env = cliEnv{
		stdin:       strings.NewReader(""),
		stdout:      h.stdout,
		stderr:      h.stderr,
		tty:         tty,
		getwd:       func() (string, error) { return h.root, nil },
		executable:  func() (string, error) { return `C:\tools\brunel\brunel.exe`, nil },
		userProfile: t.TempDir(),
		credentials: fakeCredentials{key: key},
		sessionRoot: h.sessions,
		newRunner: func(launch pirpc.LaunchOptions, cred pirpc.Credential, _ *session.Session, _, _, _ string) agentRunner {
			h.built++
			h.launch, h.cred = launch, cred
			return h.agent
		},
		runTUI: func(tui.Options, func(agent.EventSink, safety.Approver)) error {
			t.Fatal("TUI started unexpectedly")
			return nil
		},
		startBroker: func(_ context.Context, a safety.Approver) (approvalBroker, error) {
			h.broker = &fakeBroker{approver: a}
			return h.broker, nil
		},
		signalContext: func() (context.Context, context.CancelFunc) {
			return context.WithCancel(context.Background())
		},
	}
	return h
}

func (h *harness) sessionDirs(t *testing.T) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(h.sessions)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// TestEmptyTaskNeverReachesProvider is EC-1 / CT-1: an empty task is
// E_INVALID_ARGUMENT, and neither a session nor the agent is created.
func TestEmptyTaskNeverReachesProvider(t *testing.T) {
	for _, task := range []string{"", "   ", "\t\n"} {
		h := newHarness(t, terminals{stdin: true, stdout: true}, "")
		code := runCLI([]string{"--model", "openrouter/x", task}, h.env)
		if code != exitInvalid {
			t.Fatalf("task %q: exit = %d, want %d", task, code, exitInvalid)
		}
		if !strings.Contains(h.stderr.String(), errInvalidArgument) {
			t.Fatalf("task %q: stderr = %q", task, h.stderr.String())
		}
		if h.built != 0 || h.agent.ran {
			t.Fatalf("task %q: agent was built or run", task)
		}
		if dirs := h.sessionDirs(t); len(dirs) != 0 {
			t.Fatalf("task %q: session created: %v", task, dirs)
		}
	}
}

func TestInteractiveRequiresTerminal(t *testing.T) {
	for _, tty := range []terminals{{}, {stdin: true}, {stdout: true}} {
		h := newHarness(t, tty, "")
		if code := runCLI([]string{"--model", "openrouter/x"}, h.env); code != exitInvalid {
			t.Fatalf("tty=%+v: exit = %d, want %d", tty, code, exitInvalid)
		}
		if h.built != 0 {
			t.Fatalf("tty=%+v: agent built without a terminal", tty)
		}
	}
}

// TestPlainModeStreamsAndWritesReport covers AC-3: plain-text streaming
// with no terminal control sequences, and a complete --report JSON.
func TestPlainModeStreamsAndWritesReport(t *testing.T) {
	h := newHarness(t, terminals{}, "")
	reportPath := filepath.Join(h.root, "out.json")
	code := runCLI([]string{"fix the bug", "--report", reportPath, "--model", "anthropic/claude-x"}, h.env)
	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %s", code, h.stderr.String())
	}
	if h.agent.task != "fix the bug" {
		t.Fatalf("task = %q", h.agent.task)
	}
	// A tool start ends the partial line, so the answer on stdout and the
	// tool lines on stderr do not interleave mid-sentence on a terminal.
	if h.stdout.String() != "All \ndone.\n" {
		t.Fatalf("stdout = %q", h.stdout.String())
	}
	if strings.Contains(h.stdout.String()+h.stderr.String(), "\x1b[") {
		t.Fatal("plain mode wrote terminal control sequences")
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("report not written: %v", err)
	}
	var rep completion.Report
	if err := json.Unmarshal(data, &rep); err != nil || rep.Status != completion.StatusCompleted {
		t.Fatalf("report = %s (%v)", data, err)
	}
	// Unnamed session removed on a clean exit (spec.md §7.1).
	if dirs := h.sessionDirs(t); len(dirs) != 0 {
		t.Fatalf("unnamed clean session kept: %v", dirs)
	}
}

func TestPlainModeExitCodes(t *testing.T) {
	for _, tc := range []struct {
		status string
		err    error
		want   int
	}{
		{completion.StatusCompleted, nil, exitOK},
		{completion.StatusIncomplete, nil, exitFailed},
		{completion.StatusFailed, &pirpc.Error{Code: "E_PROVIDER_AUTH", Message: "bad key"}, exitFailed},
	} {
		h := newHarness(t, terminals{}, "")
		h.agent.report.Status, h.agent.err = tc.status, tc.err
		if code := runCLI([]string{"--model", "anthropic/x", "task"}, h.env); code != tc.want {
			t.Fatalf("status %s: exit = %d, want %d", tc.status, code, tc.want)
		}
		if tc.err != nil && !strings.Contains(h.stderr.String(), "E_PROVIDER_AUTH") {
			t.Fatalf("stderr missing stable code: %q", h.stderr.String())
		}
	}
}

func TestNamedSessionIsKept(t *testing.T) {
	h := newHarness(t, terminals{}, "")
	if code := runCLI([]string{"--model", "anthropic/x", "--name", "feature-x", "task"}, h.env); code != exitOK {
		t.Fatalf("exit = %d: %s", code, h.stderr.String())
	}
	if dirs := h.sessionDirs(t); len(dirs) != 1 {
		t.Fatalf("named session not kept: %v", dirs)
	}
}

func TestOpenRouterNeedsStoredKey(t *testing.T) {
	h := newHarness(t, terminals{}, "")
	code := runCLI([]string{"--model", "openrouter/anthropic/claude", "task"}, h.env)
	if code != exitFailed || !strings.Contains(h.stderr.String(), config.ErrConfigCredential.Code) {
		t.Fatalf("exit = %d stderr = %q, want E_CONFIG_CREDENTIAL", code, h.stderr.String())
	}
	if h.built != 0 {
		t.Fatal("agent built without the required key")
	}
}

func TestCredentialOnlyInjectedForOpenRouter(t *testing.T) {
	h := newHarness(t, terminals{}, "sk-or-secret")
	if code := runCLI([]string{"--model", "openrouter/anthropic/claude", "task"}, h.env); code != exitOK {
		t.Fatalf("exit = %d: %s", code, h.stderr.String())
	}
	if h.cred != pirpc.OpenRouterCredential("sk-or-secret") {
		t.Fatalf("openrouter run got credential %+v", h.cred)
	}
	if h.launch.ExtensionPath != `C:\tools\brunel\taylor-tools.ts` {
		t.Fatalf("extension path = %q, want next to brunel.exe", h.launch.ExtensionPath)
	}

	h = newHarness(t, terminals{}, "sk-or-secret")
	if code := runCLI([]string{"--model", "anthropic/claude", "task"}, h.env); code != exitOK {
		t.Fatalf("exit = %d: %s", code, h.stderr.String())
	}
	if h.cred != (pirpc.Credential{}) {
		t.Fatalf("anthropic run got the OpenRouter credential: %+v", h.cred)
	}
	if strings.Contains(h.stdout.String()+h.stderr.String(), "sk-or-secret") {
		t.Fatal("credential leaked to output")
	}
}

func TestMissingModelIsInvalidArgument(t *testing.T) {
	h := newHarness(t, terminals{}, "")
	if code := runCLI([]string{"task"}, h.env); code != exitInvalid {
		t.Fatalf("exit = %d, want %d: %s", code, exitInvalid, h.stderr.String())
	}
	if h.built != 0 {
		t.Fatal("agent built without a model")
	}
}

func TestConfigModelUsedWithoutFlag(t *testing.T) {
	h := newHarness(t, terminals{}, "")
	if err := os.MkdirAll(filepath.Join(h.root, ".brunel"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.root, ".brunel", "config.json"), []byte(`{"model_id":"anthropic/from-config","mode":"readonly"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := runCLI([]string{"task"}, h.env); code != exitOK {
		t.Fatalf("exit = %d: %s", code, h.stderr.String())
	}
	if h.launch.Model != "anthropic/from-config" {
		t.Fatalf("model = %q", h.launch.Model)
	}
}

// TestApprovalChannelOnlyWithTTY: with a TTY on stdin the host opens the
// approval channel and hands it to the agent's environment; without one no
// channel exists, so CONFIRM fails closed in the subprocess (spec.md §6.3).
func TestApprovalChannelOnlyWithTTY(t *testing.T) {
	h := newHarness(t, terminals{stdin: true}, "")
	if code := runCLI([]string{"--model", "anthropic/x", "task"}, h.env); code != exitOK {
		t.Fatalf("exit = %d: %s", code, h.stderr.String())
	}
	if h.broker == nil || h.agent.env["BRUNEL_APPROVAL_PIPE"] == "" || !h.broker.closed {
		t.Fatalf("broker=%+v env=%v: channel not opened, passed down, and closed", h.broker, h.agent.env)
	}

	h = newHarness(t, terminals{}, "")
	if code := runCLI([]string{"--model", "anthropic/x", "task"}, h.env); code != exitOK {
		t.Fatalf("exit = %d: %s", code, h.stderr.String())
	}
	if h.broker != nil || h.agent.env != nil {
		t.Fatal("approval channel opened without a TTY")
	}
}

func TestInteractiveWiresTUI(t *testing.T) {
	h := newHarness(t, terminals{stdin: true, stdout: true}, "")
	var sawOpts tui.Options
	h.env.runTUI = func(opts tui.Options, bind func(agent.EventSink, safety.Approver)) error {
		sawOpts = opts
		bind(&eventLog{}, &recordingApprover{answer: false})
		rep, err := opts.Run(context.Background(), "interactive task", &eventLog{})
		if err != nil || rep.Status != completion.StatusCompleted {
			return errors.New("run through the TUI failed")
		}
		return nil
	}
	if code := runCLI([]string{"--model", "anthropic/x", "--mode", "readonly"}, h.env); code != exitOK {
		t.Fatalf("exit = %d: %s", code, h.stderr.String())
	}
	if sawOpts.Model != "anthropic/x" || sawOpts.Mode != "readonly" || h.agent.task != "interactive task" {
		t.Fatalf("tui opts = %+v, task = %q", sawOpts, h.agent.task)
	}
	if h.broker == nil || !h.broker.closed || h.agent.env["BRUNEL_APPROVAL_PIPE"] == "" {
		t.Fatal("TUI approval channel not wired")
	}
}

func TestInvalidWorkspace(t *testing.T) {
	h := newHarness(t, terminals{}, "")
	h.env.getwd = func() (string, error) { return filepath.Join(h.root, "does-not-exist"), nil }
	code := runCLI([]string{"--model", "anthropic/x", "task"}, h.env)
	if code != exitFailed || !strings.Contains(h.stderr.String(), "E_WORKSPACE_INVALID") {
		t.Fatalf("exit = %d stderr = %q", code, h.stderr.String())
	}
}

// A run cancelled with Ctrl+C in the TUI must leave the unnamed session as
// aborted recovery evidence instead of deleting it as a clean exit.
func TestInteractiveCancelKeepsSession(t *testing.T) {
	h := newHarness(t, terminals{stdin: true, stdout: true}, "")
	h.env.runTUI = func(opts tui.Options, bind func(agent.EventSink, safety.Approver)) error {
		bind(&eventLog{}, &recordingApprover{})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _ = opts.Run(ctx, "cancelled task", &eventLog{})
		return nil
	}
	if code := runCLI([]string{"--model", "anthropic/x"}, h.env); code != exitOK {
		t.Fatalf("exit = %d: %s", code, h.stderr.String())
	}
	if n := len(h.sessionDirs(t)); n != 1 {
		t.Fatalf("%d session dirs after a cancelled TUI run, want the aborted one kept", n)
	}
}
