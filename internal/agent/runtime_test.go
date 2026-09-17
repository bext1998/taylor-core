package agent

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bext1998/brunel/internal/completion"
	"github.com/bext1998/brunel/internal/pirpc"
	"github.com/bext1998/brunel/internal/provider"
	"github.com/bext1998/brunel/internal/session"
)

// fakePiProcess implements pirpc.PiProcess for exercising the run loop
// without a real Node.js/npm runtime. It delivers a fixed event queue,
// records the sent prompt, and honours Close/Abort.
type fakePiProcess struct {
	prompt     string
	aborted    bool
	events     chan pirpc.Event
	done       chan struct{}
	exitCode   int
	stderr     string
	startErr   error
	once       sync.Once
}

func newFake(events []pirpc.Event, startErr error) *fakePiProcess {
	f := &fakePiProcess{
		events:   make(chan pirpc.Event, len(events)),
		done:     make(chan struct{}),
		startErr: startErr,
	}
	for _, e := range events {
		f.events <- e
	}
	return f
}

func (f *fakePiProcess) SendPrompt(message string) error {
	f.prompt = message
	return nil
}

func (f *fakePiProcess) terminate() {
	f.once.Do(func() {
		close(f.done)
	})
}

func (f *fakePiProcess) Abort() error {
	f.aborted = true
	f.terminate()
	return nil
}

func (f *fakePiProcess) Events() <-chan pirpc.Event { return f.events }

func (f *fakePiProcess) Done() <-chan struct{} { return f.done }

func (f *fakePiProcess) ExitCode() int     { return f.exitCode }
func (f *fakePiProcess) CapturedStderr() string { return f.stderr }

func (f *fakePiProcess) Close() { f.terminate() }

// recordingSink captures the events the run loop emits for display.
type recordingSink struct {
	events []Event
}

func (s *recordingSink) Emit(e Event) { s.events = append(s.events, e) }

func newTestSession(t *testing.T) *session.Session {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	s, err := store.Create(session.CreateOptions{WorkspaceRoot: t.TempDir(), Mode: session.ModeWorkspace})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return s
}

func (r *Runtime) withFakeStart(proc pirpc.PiProcess, startErr error) *Runtime {
	r.start = func(context.Context, pirpc.LaunchOptions, pirpc.Credential, string, map[string]string) (pirpc.PiProcess, error) {
		return proc, startErr
	}
	return r
}

// TestRunCompletesOnAgentSettled exercises the happy path: the run acks the
// prompt, streams an assistant delta plus a tool call, and settles. The
// report is completed, the sink receives the display events, and the
// session log records the assistant text and tool call.
func TestRunCompletesOnAgentSettled(t *testing.T) {
	events := []pirpc.Event{
		{Type: "response", Response: true, Success: true, Command: "prompt"},
		{Type: "message_update", AssistantType: "text_delta", DeltaText: "Hello"},
		{Type: "tool_execution_start", ToolCallID: "call_1", ExecName: "read_file"},
		{Type: "tool_execution_end", ToolCallID: "call_1", ExecName: "read_file"},
		{Type: "agent_settled"},
	}
	sink := &recordingSink{}
	sess := newTestSession(t)
	r := NewRuntime(pirpc.LaunchOptions{Provider: "openrouter", Model: "anthropic/claude-sonnet-4"}, pirpc.Credential{}, sess, t.TempDir(), "workspace", "").withFakeStart(newFake(events, nil), nil)

	report, err := r.Run(context.Background(), "add tests", sink)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if report.Status != completion.StatusCompleted {
		t.Fatalf("report.Status = %q, want %q", report.Status, completion.StatusCompleted)
	}
	if report.Task != "add tests" {
		t.Fatalf("report.Task = %q, want %q", report.Task, "add tests")
	}
	wantKinds := []EventKind{EventAssistantDelta, EventToolStarted, EventToolFinished, EventRunFinished}
	if len(sink.events) != len(wantKinds) {
		t.Fatalf("sink received %d events, want %d: %+v", len(sink.events), len(wantKinds), sink.events)
	}
	for i, want := range wantKinds {
		if sink.events[i].Kind != want {
			t.Fatalf("sink[%d].Kind = %q, want %q", i, sink.events[i].Kind, want)
		}
	}

	events2, err := sess.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents() error = %v", err)
	}
	// Assistant text + tool call start + tool result end = 3 session events.
	if got := len(events2.Events); got != 3 {
		t.Fatalf("session has %d events, want 3: %+v", got, events2.Events)
	}
	if events2.Events[0].Kind != session.EvAssistantText {
		t.Fatalf("session[0].Kind = %q, want %q", events2.Events[0].Kind, session.EvAssistantText)
	}
}

// TestRunReportsFailureWhenLaunchFails covers EC-13: a start error surfaces
// as a failed report without emitting events.
func TestRunReportsFailureWhenLaunchFails(t *testing.T) {
	sink := &recordingSink{}
	sess := newTestSession(t)
	startErr := &pirpc.Error{Code: "E_RUNTIME_REQUIRED", Message: "pi not found"}
	r := NewRuntime(pirpc.LaunchOptions{Model: "m"}, pirpc.Credential{}, sess, t.TempDir(), "workspace", "").withFakeStart(nil, startErr)

	report, err := r.Run(context.Background(), "task", sink)
	if err == nil {
		t.Fatal("Run() error = nil, want launch error")
	}
	if report.Status != completion.StatusFailed {
		t.Fatalf("report.Status = %q, want %q", report.Status, completion.StatusFailed)
	}
	if len(sink.events) != 0 {
		t.Fatalf("sink received %d events on launch failure, want 0", len(sink.events))
	}
}

// TestRunAbortsOnContextCancel covers the user-abort path: cancelling the
// context sends an abort and reports an incomplete run.
func TestRunAbortsOnContextCancel(t *testing.T) {
	events := []pirpc.Event{
		{Type: "response", Response: true, Success: true, Command: "prompt"},
		{Type: "message_update", AssistantType: "text_delta", DeltaText: "partial"},
	}
	proc := newFake(events, nil)
	sink := &recordingSink{}
	sess := newTestSession(t)
	r := NewRuntime(pirpc.LaunchOptions{Model: "m"}, pirpc.Credential{}, sess, t.TempDir(), "workspace", "").withFakeStart(proc, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, _ = r.Run(ctx, "task", sink)
		close(done)
	}()
	// Let the run acknowledge the prompt, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run() did not return after context cancel")
	}
	if !proc.aborted {
		t.Fatal("Abort() was not called on context cancel")
	}
}

// TestBuildInitialPrompt covers issue #9 §4's prompt folding.
func TestBuildInitialPrompt(t *testing.T) {
	t.Run("no AGENTS.md returns the task unchanged", func(t *testing.T) {
		got := buildInitialPrompt("do work", "")
		if got != "do work" {
			t.Fatalf("buildInitialPrompt(empty) = %q, want %q", got, "do work")
		}
	})

	t.Run("folds a trimmed AGENTS.md", func(t *testing.T) {
		got := buildInitialPrompt("do work", "  instructions\n")
		if got != "do work\n\n---Workspace agent instructions (AGENTS.md)---\ninstructions\n---end of AGENTS.md---" {
			t.Fatalf("buildInitialPrompt = %q", got)
		}
	})
}

// TestReadWorkspaceAGENTSmd covers the minimal workspace-root read: a
// missing file is not an error, an existing file is returned.
func TestReadWorkspaceAGENTSmd(t *testing.T) {
	empty := t.TempDir()
	got, err := readWorkspaceAGENTSmd(empty)
	if err != nil {
		t.Fatalf("readWorkspaceAGENTSmd(missing) error = %v", err)
	}
	if got != "" {
		t.Fatalf("readWorkspaceAGENTSmd(missing) = %q, want empty", got)
	}

	withFile := t.TempDir()
	if err := os.WriteFile(filepath.Join(withFile, "AGENTS.md"), []byte("run"), 0o600); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	got, err = readWorkspaceAGENTSmd(withFile)
	if err != nil {
		t.Fatalf("readWorkspaceAGENTSmd(existing) error = %v", err)
	}
	if got != "run" {
		t.Fatalf("readWorkspaceAGENTSmd(existing) = %q, want %q", got, "run")
	}
}

// TestTranslate maps each Pi RPC event to the frozen agent.Event the sink
// consumes.
func TestTranslate(t *testing.T) {
	r := &Runtime{now: time.Now}
	tests := []struct {
		name string
		ev   pirpc.Event
		want *Event
	}{
		{
			name: "text_delta",
			ev:   pirpc.Event{Type: "message_update", AssistantType: "text_delta", DeltaText: "hi"},
			want: &Event{Kind: EventAssistantDelta, Text: "hi"},
		},
		{
			name: "toolcall_start",
			ev:   pirpc.Event{Type: "message_update", AssistantType: "toolcall_start", CallID: "c1", ToolName: "read_file"},
			want: &Event{Kind: EventToolStarted, ToolCallID: "c1", ToolName: "read_file"},
		},
		{
			name: "tool_execution_start",
			ev:   pirpc.Event{Type: "tool_execution_start", ToolCallID: "c1", ExecName: "read_file"},
			want: &Event{Kind: EventToolStarted, ToolCallID: "c1", ToolName: "read_file"},
		},
		{
			name: "tool_execution_end",
			ev:   pirpc.Event{Type: "tool_execution_end", ToolCallID: "c1", ExecName: "read_file"},
			want: &Event{Kind: EventToolFinished, ToolCallID: "c1", ToolName: "read_file"},
		},
		{
			name: "agent_settled",
			ev:   pirpc.Event{Type: "agent_settled"},
			want: &Event{Kind: EventRunFinished},
		},
		{
			name: "bare usage message_update is not surfaced",
			ev:   pirpc.Event{Type: "message_update", AssistantType: ""},
			want: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := r.translate(tc.ev)
			if err != nil {
				t.Fatalf("translate() error = %v", err)
			}
			if tc.want == nil {
				if got != nil {
					t.Fatalf("translate() = %+v, want nil", got)
				}
				return
			}
			if got == nil || got.Kind != tc.want.Kind || got.Text != tc.want.Text || got.ToolCallID != tc.want.ToolCallID || got.ToolName != tc.want.ToolName {
				t.Fatalf("translate() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestAbnormalExitClassifiesProviderError covers the abnormal-exit path:
// a non-zero exit with provider-style stderr is translated to the matching
// Brunel code rather than a generic runtime failure.
func TestAbnormalExitClassifiesProviderError(t *testing.T) {
	proc := newFake(nil, nil)
	proc.exitCode = 1
	proc.stderr = "rate limit exceeded"
	r := &Runtime{session: newTestSession(t)}

	report, err := r.abnormalExit("task", provider.Usage{}, 0, time.Now(), time.Now(), proc)
	if report.Status != completion.StatusFailed {
		t.Fatalf("report.Status = %q, want %q", report.Status, completion.StatusFailed)
	}
	if err == nil {
		t.Fatal("abnormalExit() error = nil, want provider error")
	}
	if ErrorCode := pirpc.ErrorCode(err); ErrorCode != pirpc.ErrPiProviderQuota.Code {
		t.Fatalf("abnormalExit() code = %q, want %q", ErrorCode, pirpc.ErrPiProviderQuota.Code)
	}
}

// TestToProviderUsage maps a decoded Pi usage snapshot to provider.Usage.
func TestToProviderUsage(t *testing.T) {
	cost := 0.003
	got := toProviderUsage(pirpc.Usage{Input: 100, Output: 50, TotalTokens: 150, Cost: &cost})
	if got.PromptTokens != 100 || got.CompletionTokens != 50 {
		t.Fatalf("tokens = %+v, want Prompt 100 Completion 50", got)
	}
	if got.CostUSD == nil || *got.CostUSD != 0.003 {
		t.Fatalf("CostUSD = %v, want 0.003", got.CostUSD)
	}

	blind := toProviderUsage(pirpc.Usage{Input: 1, Output: 2})
	if blind.CostUSD != nil {
		t.Fatalf("CostUSD = %v, want nil when Pi reports no cost", blind.CostUSD)
	}
}
