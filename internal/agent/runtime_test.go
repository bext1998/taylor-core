package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
// records the sent prompt, and honours Close/Abort. Like the real
// handle, the events channel is closed on termination so the run loop
// observes a clean stream end.
type fakePiProcess struct {
	prompt   string
	aborted  bool
	events   chan pirpc.Event
	done     chan struct{}
	exitCode int
	stderr   string
	startErr error
	once     sync.Once
}

func newFake(events []pirpc.Event, startErr error) *fakePiProcess {
	f := &fakePiProcess{
		events:   make(chan pirpc.Event, len(events)+1),
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
		close(f.events)
	})
}

func (f *fakePiProcess) Abort() error {
	f.aborted = true
	f.terminate()
	return nil
}

func (f *fakePiProcess) Events() <-chan pirpc.Event { return f.events }

func (f *fakePiProcess) Done() <-chan struct{} { return f.done }

func (f *fakePiProcess) ExitCode() int          { return f.exitCode }
func (f *fakePiProcess) CapturedStderr() string { return f.stderr }

func (f *fakePiProcess) Close() { f.terminate() }

// recordingSink captures the events the run loop emits for display.
type recordingSink struct {
	events []Event
}

func (s *recordingSink) Emit(e Event) { s.events = append(s.events, e) }

func kindsOf(s *recordingSink) []EventKind {
	out := make([]EventKind, len(s.events))
	for i, e := range s.events {
		out[i] = e.Kind
	}
	return out
}

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

// TestRunCompletesOnAgentSettled exercises the happy path with a
// protocol-faithful event sequence: ack, one turn with a text delta,
// toolcall_end (model finished the call), tool execution start/end,
// message_end stopReason=stop, then agent_settled. The report is
// completed with turns counted per turn_start, the sink receives the
// display events (including the usage update), and the session log
// records exactly one tool_call and one tool_result per tool.
func TestRunCompletesOnAgentSettled(t *testing.T) {
	events := []pirpc.Event{
		{Type: "response", Response: true, Success: true, Command: "prompt"},
		{Type: "turn_start"},
		{Type: "message_update", AssistantType: "text_delta", DeltaText: "Hello", Usage: &pirpc.Usage{Input: 10, Output: 20, TotalTokens: 30}},
		{Type: "message_update", AssistantType: "toolcall_start", CallID: "call_1", ToolName: "read_file"},
		{Type: "message_update", AssistantType: "toolcall_end", CallID: "call_1", ToolName: "read_file"},
		{Type: "message_end", StopReason: "toolUse"},
		{Type: "tool_execution_start", ToolCallID: "call_1", ExecName: "read_file"},
		{Type: "tool_execution_end", ToolCallID: "call_1", ExecName: "read_file"},
		{Type: "turn_start"},
		{Type: "message_update", AssistantType: "text_delta", DeltaText: "Done."},
		{Type: "message_end", StopReason: "stop"},
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
	if report.Cost.Turns != 2 {
		t.Fatalf("report.Cost.Turns = %d, want 2 (one per turn_start, not per tool)", report.Cost.Turns)
	}
	if report.Cost.PromptTokens != 10 || report.Cost.CompletionTokens != 20 {
		t.Fatalf("report.Cost = %+v, want Prompt 10 Completion 20", report.Cost)
	}

	wantKinds := []EventKind{
		EventUsageUpdated, EventAssistantDelta,
		EventToolStarted, EventToolFinished,
		EventAssistantDelta, EventRunFinished,
	}
	if got := kindsOf(sink); len(got) != len(wantKinds) {
		t.Fatalf("sink received %d events, want %d: %+v", len(got), len(wantKinds), got)
	}
	for i, want := range wantKinds {
		if got := kindsOf(sink); got[i] != want {
			t.Fatalf("sink[%d].Kind = %q, want %q (full: %v)", i, got[i], want, got)
		}
	}

	events2, err := sess.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents() error = %v", err)
	}
	// assistant text (2 deltas) + one tool_call (toolcall_end) + one
	// tool_result (tool_execution_end) = 4 session events.
	if got := len(events2.Events); got != 4 {
		t.Fatalf("session has %d events, want 4: %+v", got, events2.Events)
	}
	if events2.Events[0].Kind != session.EvAssistantText {
		t.Fatalf("session[0].Kind = %q, want %q", events2.Events[0].Kind, session.EvAssistantText)
	}
	if events2.Events[1].Kind != session.EvToolCall {
		t.Fatalf("session[1].Kind = %q, want %q", events2.Events[1].Kind, session.EvToolCall)
	}
	// The tool_call payload must carry the real call id and tool name - a
	// real toolcall_end wire event only yields these when the nested
	// toolCall object is decoded (regression: empty id/tool).
	var callPayload map[string]string
	if err := json.Unmarshal(events2.Events[1].Payload, &callPayload); err != nil {
		t.Fatalf("tool_call payload not JSON: %v", err)
	}
	if callPayload["id"] != "call_1" || callPayload["tool"] != "read_file" {
		t.Fatalf("tool_call payload = %v, want id=call_1 tool=read_file", callPayload)
	}
	if events2.Events[2].Kind != session.EvToolResult {
		t.Fatalf("session[2].Kind = %q, want %q", events2.Events[2].Kind, session.EvToolResult)
	}
}

// TestRunSettledWithErrorIsNotCompleted covers the review finding that
// agent_settled is NOT a success signal: a message_end with
// stopReason=error must produce a failed report with the provider error
// classified from the message.
func TestRunSettledWithErrorIsNotCompleted(t *testing.T) {
	events := []pirpc.Event{
		{Type: "response", Response: true, Success: true, Command: "prompt"},
		{Type: "turn_start"},
		{Type: "message_update", AssistantType: "text_delta", DeltaText: "Sorry, something went wrong."},
		{Type: "message_end", StopReason: "error", ErrorMsg: "rate limit exceeded for this key"},
		{Type: "agent_settled"},
	}
	sink := &recordingSink{}
	sess := newTestSession(t)
	r := NewRuntime(pirpc.LaunchOptions{Model: "m"}, pirpc.Credential{}, sess, t.TempDir(), "workspace", "").withFakeStart(newFake(events, nil), nil)

	report, err := r.Run(context.Background(), "task", sink)
	if err == nil {
		t.Fatal("Run() error = nil, want provider error from stopReason=error")
	}
	if code := pirpc.ErrorCode(err); code != pirpc.ErrPiProviderQuota.Code {
		t.Fatalf("ErrorCode = %q, want %q", code, pirpc.ErrPiProviderQuota.Code)
	}
	if report.Status != completion.StatusFailed {
		t.Fatalf("report.Status = %q, want %q (settled is not success)", report.Status, completion.StatusFailed)
	}
}

// TestRunSettledWithoutTerminalStateIsFailed covers the review finding that
// agent_settled is not success evidence: settling with no message_end at
// all means the protocol never delivered a terminal state, so the run must
// not be completed - it fails with a protocol-level error.
func TestRunSettledWithoutTerminalStateIsFailed(t *testing.T) {
	events := []pirpc.Event{
		{Type: "response", Response: true, Success: true, Command: "prompt"},
		{Type: "message_update", AssistantType: "text_delta", DeltaText: "ok"},
		{Type: "agent_settled"},
	}
	sink := &recordingSink{}
	sess := newTestSession(t)
	r := NewRuntime(pirpc.LaunchOptions{Model: "m"}, pirpc.Credential{}, sess, t.TempDir(), "workspace", "").withFakeStart(newFake(events, nil), nil)

	report, err := r.Run(context.Background(), "task", sink)
	if err == nil {
		t.Fatal("Run() error = nil, want E_PI_RPC for a settle without any message_end terminal state")
	}
	if code := pirpc.ErrorCode(err); code != "E_PI_RPC" {
		t.Fatalf("ErrorCode = %q, want E_PI_RPC", code)
	}
	if report.Status != completion.StatusFailed {
		t.Fatalf("report.Status = %q, want %q (settled without terminal state is not completed)", report.Status, completion.StatusFailed)
	}
}

// TestRunSettledAbortedIsIncomplete: stopReason=aborted is not a clean
// completion and not a provider failure.
func TestRunSettledAbortedIsIncomplete(t *testing.T) {
	events := []pirpc.Event{
		{Type: "response", Response: true, Success: true, Command: "prompt"},
		{Type: "message_end", StopReason: "aborted"},
		{Type: "agent_settled"},
	}
	sink := &recordingSink{}
	sess := newTestSession(t)
	r := NewRuntime(pirpc.LaunchOptions{Model: "m"}, pirpc.Credential{}, sess, t.TempDir(), "workspace", "").withFakeStart(newFake(events, nil), nil)

	report, err := r.Run(context.Background(), "task", sink)
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if report.Status != completion.StatusIncomplete {
		t.Fatalf("report.Status = %q, want %q", report.Status, completion.StatusIncomplete)
	}
}

// TestRunPreservesEventsBeforeAck covers the review finding that events
// arriving before the prompt ack were dropped. The protocol streams
// events asynchronously, so a message_update can precede the response
// line; it must still be displayed and persisted.
func TestRunPreservesEventsBeforeAck(t *testing.T) {
	events := []pirpc.Event{
		{Type: "message_update", AssistantType: "text_delta", DeltaText: "early"},
		{Type: "response", Response: true, Success: true, Command: "prompt"},
		{Type: "message_end", StopReason: "stop"},
		{Type: "agent_settled"},
	}
	sink := &recordingSink{}
	sess := newTestSession(t)
	r := NewRuntime(pirpc.LaunchOptions{Model: "m"}, pirpc.Credential{}, sess, t.TempDir(), "workspace", "").withFakeStart(newFake(events, nil), nil)

	report, err := r.Run(context.Background(), "task", sink)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if report.Status != completion.StatusCompleted {
		t.Fatalf("report.Status = %q, want completed", report.Status)
	}
	if len(sink.events) != 2 || sink.events[0].Kind != EventAssistantDelta || sink.events[0].Text != "early" {
		t.Fatalf("pre-ack event was dropped: %+v", sink.events)
	}
	got, _ := sess.ReadEvents()
	if len(got.Events) != 1 || got.Events[0].Kind != session.EvAssistantText {
		t.Fatalf("pre-ack event not persisted: %+v", got.Events)
	}
}

// TestRunAppendFailureIsReportedFailed covers the review finding that a
// session.AppendEvent failure must not be reported as a clean completion.
// Per spec.md §8 an unrecoverable storage error is a failed run (not
// merely incomplete), and EC-10 requires the report to not claim success.
func TestRunAppendFailureIsReportedFailed(t *testing.T) {
	sess := newTestSession(t)
	// Closing the session makes every AppendEvent fail with E_SESSION_CLOSED.
	if err := sess.Close(session.ExitStatusAborted); err != nil {
		t.Fatalf("session close: %v", err)
	}
	events := []pirpc.Event{
		{Type: "response", Response: true, Success: true, Command: "prompt"},
		{Type: "message_update", AssistantType: "text_delta", DeltaText: "hi"},
		{Type: "message_end", StopReason: "stop"},
		{Type: "agent_settled"},
	}
	sink := &recordingSink{}
	r := NewRuntime(pirpc.LaunchOptions{Model: "m"}, pirpc.Credential{}, sess, t.TempDir(), "workspace", "").withFakeStart(newFake(events, nil), nil)

	report, err := r.Run(context.Background(), "task", sink)
	if err == nil {
		t.Fatal("Run() error = nil, want the session persistence error surfaced")
	}
	if code := session.ErrorCode(err); code != session.ErrSessionClosed.Code {
		t.Fatalf("ErrorCode = %q, want %q", code, session.ErrSessionClosed.Code)
	}
	if report.Status != completion.StatusFailed {
		t.Fatalf("report.Status = %q, want %q (unrecoverable storage error is failed per spec §8)", report.Status, completion.StatusFailed)
	}
	if len(report.RemainingRisks) == 0 || len(report.RemainingRisks[0]) == 0 {
		t.Fatalf("RemainingRisks empty, want the persistence note: %+v", report.RemainingRisks)
	}
}

// TestRunCancelAfterAppendFailure covers the case where the user cancels
// after an AppendEvent has already failed: the report must be failed
// (not incomplete) and the storage error must be surfaced.
func TestRunCancelAfterAppendFailure(t *testing.T) {
	sess := newTestSession(t)
	if err := sess.Close(session.ExitStatusAborted); err != nil {
		t.Fatalf("session close: %v", err)
	}
	events := []pirpc.Event{
		{Type: "response", Response: true, Success: true, Command: "prompt"},
		{Type: "message_update", AssistantType: "text_delta", DeltaText: "partial"},
	}
	proc := newFake(events, nil)
	sink := &recordingSink{}
	r := NewRuntime(pirpc.LaunchOptions{Model: "m"}, pirpc.Credential{}, sess, t.TempDir(), "workspace", "").withFakeStart(proc, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var report *completion.Report
	var runErr error
	go func() {
		report, runErr = r.Run(ctx, "task", sink)
		close(done)
	}()
	// Let the run process the text delta (append will fail), then cancel.
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
	if runErr == nil {
		t.Fatal("Run() error = nil, want storage error surfaced on cancel")
	}
	if code := session.ErrorCode(runErr); code != session.ErrSessionClosed.Code {
		t.Fatalf("ErrorCode = %q, want %q", code, session.ErrSessionClosed.Code)
	}
	if report.Status != completion.StatusFailed {
		t.Fatalf("report.Status = %q, want %q (storage failure overrides cancel/incomplete)", report.Status, completion.StatusFailed)
	}
	if len(report.RemainingRisks) == 0 {
		t.Fatal("RemainingRisks empty, want the persistence note")
	}
}

// TestRunAbortedSettleWithAppendFailureIsFailed: the run settles with
// stopReason=aborted (which would normally be incomplete), but an earlier
// AppendEvent failure must escalate the status to failed per spec §8.
func TestRunAbortedSettleWithAppendFailureIsFailed(t *testing.T) {
	sess := newTestSession(t)
	if err := sess.Close(session.ExitStatusAborted); err != nil {
		t.Fatalf("session close: %v", err)
	}
	events := []pirpc.Event{
		{Type: "response", Response: true, Success: true, Command: "prompt"},
		{Type: "message_update", AssistantType: "text_delta", DeltaText: "partial"},
		{Type: "message_end", StopReason: "aborted"},
		{Type: "agent_settled"},
	}
	sink := &recordingSink{}
	r := NewRuntime(pirpc.LaunchOptions{Model: "m"}, pirpc.Credential{}, sess, t.TempDir(), "workspace", "").withFakeStart(newFake(events, nil), nil)

	report, err := r.Run(context.Background(), "task", sink)
	if err == nil {
		t.Fatal("Run() error = nil, want storage error surfaced")
	}
	if code := session.ErrorCode(err); code != session.ErrSessionClosed.Code {
		t.Fatalf("ErrorCode = %q, want %q", code, session.ErrSessionClosed.Code)
	}
	if report.Status != completion.StatusFailed {
		t.Fatalf("report.Status = %q, want %q (aborted + storage failure = failed, not incomplete)", report.Status, completion.StatusFailed)
	}
	if len(report.RemainingRisks) == 0 {
		t.Fatal("RemainingRisks empty, want the persistence note")
	}
}

// TestRunAbnormalExitWithoutSettle: the event stream ends (channel closed)
// without agent_settled; a clean exit code with no error must still fail
// the run as an unexplained non-settle.
func TestRunAbnormalExitWithoutSettle(t *testing.T) {
	events := []pirpc.Event{
		{Type: "response", Response: true, Success: true, Command: "prompt"},
		{Type: "message_update", AssistantType: "text_delta", DeltaText: "partial"},
	}
	proc := newFake(events, nil)
	// Simulate the process exiting without settling: close the stream.
	go func() {
		time.Sleep(50 * time.Millisecond)
		proc.terminate()
	}()
	sink := &recordingSink{}
	sess := newTestSession(t)
	r := NewRuntime(pirpc.LaunchOptions{Model: "m"}, pirpc.Credential{}, sess, t.TempDir(), "workspace", "").withFakeStart(proc, nil)

	report, err := r.Run(context.Background(), "task", sink)
	if err == nil {
		t.Fatal("Run() error = nil, want E_PI_RPC for exit without settle")
	}
	if report.Status != completion.StatusFailed {
		t.Fatalf("report.Status = %q, want %q", report.Status, completion.StatusFailed)
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

// TestBuildInitialPrompt covers issue #9 §4's prompt folding and the issue #47
// probe hint.
func TestBuildInitialPrompt(t *testing.T) {
	t.Run("no AGENTS.md returns the task plus the probe hint", func(t *testing.T) {
		got := buildInitialPrompt("do work", "")
		want := "do work\n\n" + nearAgentsMDProbeGuidance
		if got != want {
			t.Fatalf("buildInitialPrompt(empty) = %q, want %q", got, want)
		}
	})

	t.Run("folds a trimmed AGENTS.md after the probe hint", func(t *testing.T) {
		got := buildInitialPrompt("do work", "  instructions\n")
		want := "do work\n\n" + nearAgentsMDProbeGuidance +
			"\n\n---Workspace agent instructions (AGENTS.md)---\ninstructions\n---end of AGENTS.md---"
		if got != want {
			t.Fatalf("buildInitialPrompt = %q, want %q", got, want)
		}
	})

	// Issue #47: the hint must name the tool the model has to reach for and
	// the directory it is probing; a gutted sentence would pass the
	// exact-string subtests above while guiding nobody.
	t.Run("probe hint names list_files and the directory rules", func(t *testing.T) {
		for _, want := range []string{"list_files", "AGENTS.md", "directory"} {
			if !strings.Contains(nearAgentsMDProbeGuidance, want) {
				t.Errorf("nearAgentsMDProbeGuidance missing %q: %q", want, nearAgentsMDProbeGuidance)
			}
		}
		// One line, appended to the task: no extra section heading.
		if strings.Contains(nearAgentsMDProbeGuidance, "---") {
			t.Errorf("nearAgentsMDProbeGuidance introduces a section: %q", nearAgentsMDProbeGuidance)
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
// consumes. toolcall_start/toolcall_end are the model assembling the call
// and must NOT surface as tool started/finished - only the actual
// tool_execution_start/end do (review finding: avoid duplicating the
// tool_execution_end result).
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
			name: "toolcall_start is not tool execution start",
			ev:   pirpc.Event{Type: "message_update", AssistantType: "toolcall_start", CallID: "c1", ToolName: "read_file"},
			want: nil,
		},
		{
			name: "toolcall_end is model finishing the call, not the tool finishing",
			ev:   pirpc.Event{Type: "message_update", AssistantType: "toolcall_end", CallID: "c1", ToolName: "read_file"},
			want: nil,
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
			name: "message_end is state, not display",
			ev:   pirpc.Event{Type: "message_end", StopReason: "stop"},
			want: nil,
		},
		{
			name: "turn_start is state, not display",
			ev:   pirpc.Event{Type: "turn_start"},
			want: nil,
		},
		{
			name: "agent_settled",
			ev:   pirpc.Event{Type: "agent_settled"},
			want: &Event{Kind: EventRunFinished},
		},
		{
			name: "bare usage message_update is not surfaced as text",
			ev:   pirpc.Event{Type: "message_update", AssistantType: ""},
			want: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := r.translate(tc.ev)
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
	proc.terminate()
	r := &Runtime{session: newTestSession(t), now: time.Now}
	st := &runState{task: "task", started: time.Now()}

	report, err := r.abnormalExit(st, time.Now(), proc)
	if report.Status != completion.StatusFailed {
		t.Fatalf("report.Status = %q, want %q", report.Status, completion.StatusFailed)
	}
	if err == nil {
		t.Fatal("abnormalExit() error = nil, want provider error")
	}
	if code := pirpc.ErrorCode(err); code != pirpc.ErrPiProviderQuota.Code {
		t.Fatalf("abnormalExit() code = %q, want %q", code, pirpc.ErrPiProviderQuota.Code)
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

// TestUsageDiffers guards the dedup used before emitting EventUsageUpdated:
// identical snapshots do not re-emit, any change does.
func TestUsageDiffers(t *testing.T) {
	a := provider.Usage{PromptTokens: 10, CompletionTokens: 5}
	if usageDiffers(a, a) {
		t.Fatal("identical usage must not differ")
	}
	b := a
	b.CompletionTokens = 6
	if !usageDiffers(a, b) {
		t.Fatal("changed completion tokens must differ")
	}
	// a has no cost; setting c's cost to nil leaves both nil -> equal.
	c := a
	c.CostUSD = nil
	if usageDiffers(a, c) {
		t.Fatal("nil cost vs nil cost must not differ")
	}
	d := provider.Usage{PromptTokens: 10, CompletionTokens: 5, CostUSD: func() *float64 { v := 1.0; return &v }()}
	if !usageDiffers(a, d) {
		t.Fatal("cost appeared vs no cost must differ")
	}
}
