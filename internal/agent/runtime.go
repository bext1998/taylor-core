package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bext1998/brunel/internal/completion"
	"github.com/bext1998/brunel/internal/pirpc"
	"github.com/bext1998/brunel/internal/provider"
	"github.com/bext1998/brunel/internal/session"
)

// Run loop timing. The ack timeout bounds how long we wait for Pi to
// acknowledge the initial prompt; the run timeout bounds the whole model
// facing task so a hung Pi cannot leak the process tree (Close still
// terminates the Job Object regardless). exitCodeGrace bounds how long
// abnormalExit waits for the handle to record the process exit code.
const (
	ackTimeout    = 30 * time.Second
	runTimeout    = 30 * time.Minute
	exitCodeGrace = 5 * time.Second
	ackSource     = "pi-rpc"
)

// Runtime implements the frozen Agent contract (spec.md §5.2) by launching
// and managing the pi --mode rpc subprocess (see internal/pirpc) and
// translating Pi's RPC events into Event values for the sink and the
// session log. It is the "option b" bridge: internal/agent drives the
// lifecycle and the display/session mapping; internal/pirpc owns the
// subprocess handle and the RPC wire protocol.
type Runtime struct {
	options       pirpc.LaunchOptions
	credential    pirpc.Credential
	session       *session.Session
	workspaceRoot string
	mode          string
	now           func() time.Time
	brunelExe     string
	sink          EventSink
	// start launches the pi subprocess. It defaults to pirpc.Start; tests
	// inject a fake so the run loop can be exercised without a real
	// Node.js/npm runtime.
	start func(ctx context.Context, opts pirpc.LaunchOptions, cred pirpc.Credential, workDir string, extraEnv map[string]string) (pirpc.PiProcess, error)
}

// NewRuntime builds a Runtime. brunelExe is the absolute path to the
// running Brunel executable; the extension reads it (via BRUNEL_EXE) to
// dispatch the eight Taylor tools through the --taylor-tool subprocess.
// If brunelExe is empty it is resolved from os.Executable at Run time.
func NewRuntime(opts pirpc.LaunchOptions, cred pirpc.Credential, s *session.Session, workspaceRoot, mode, brunelExe string) *Runtime {
	return &Runtime{
		options:       opts,
		credential:    cred,
		session:       s,
		workspaceRoot: workspaceRoot,
		mode:          mode,
		now:           time.Now,
		brunelExe:     brunelExe,
		start:         pirpc.Start,
	}
}

// runState accumulates the run-level facts the completion report and the
// settle classification need.
type runState struct {
	task      string
	started   time.Time
	usage     provider.Usage
	usageSeen bool
	turns     int
	// stopReason is the stopReason of the last message_end ("stop",
	// "length", "toolUse", "error", "aborted"). agent_settled only means
	// Pi will not continue automatically (retry, compaction retry, queued
	// follow-up); it is not a success signal - the run's outcome comes
	// from here.
	stopReason string
	// finalError is the last error text Pi reported on a failed assistant
	// message (message_end errorMessage).
	finalError string
	// appendErr / appendFails track session-persist failures so a run whose
	// events.jsonl is incomplete is never reported as a clean completion.
	appendErr   error
	appendFails int
}

// Run launches Pi, sends the task (with the injected AGENTS.md), and
// streams events until Pi settles, the user aborts, or the run times out.
// It returns a completion.Report (spec.md §8) filled best-effort and, on
// unrecoverable failure, a Brunel error.
func (r *Runtime) Run(ctx context.Context, task string, sink EventSink) (*completion.Report, error) {
	started := r.now()
	st := &runState{task: task, started: started}
	r.sink = sink

	// Resolve the Brunel executable path for the extension (BRUNEL_EXE).
	exe := r.brunelExe
	if exe == "" {
		exe, _ = os.Executable()
	}

	// (1) Launch and manage the pi subprocess.
	proc, err := r.start(ctx, r.options, r.credential, r.workspaceRoot, map[string]string{
		"BRUNEL_EXE":  exe,
		"BRUNEL_MODE": r.mode,
	})
	if err != nil {
		return r.report(completion.StatusFailed, st, started), err
	}
	defer proc.Close()

	// (4) Read the workspace-root AGENTS.md and fold it into the prompt.
	agentFile, err := readWorkspaceAGENTSmd(r.workspaceRoot)
	if err != nil {
		return r.report(completion.StatusFailed, st, started), err
	}
	prompt := buildInitialPrompt(task, agentFile)
	if err := proc.SendPrompt(prompt); err != nil {
		return r.report(completion.StatusFailed, st, started), err
	}

	// Acknowledge the launch: Pi must ack the initial prompt before we
	// proceed, so a launch that never answers surfaces as a failure. The
	// protocol streams events asynchronously, so legitimate events can
	// arrive before the ack is written; they are buffered and replayed,
	// never dropped.
	acked, preAck, err := waitPromptAck(ctx, proc, ackTimeout)
	if err != nil {
		return r.report(completion.StatusFailed, st, r.now()), err
	}
	if !acked {
		return r.report(completion.StatusFailed, st, r.now()),
			&pirpc.Error{Code: "E_PI_RPC", Message: "pi did not acknowledge the initial prompt", Cause: nil}
	}
	for _, ev := range preAck {
		if ended, rep, runErr := r.processEvent(st, ev); ended {
			return rep, runErr
		}
	}

	// (2) + (3) Event loop: translate/emit/display and persist to the
	// session log until Pi settles, the user aborts, or the run times out.
	// The event channel is closed by the handle once the stream ends, so
	// receiving ok=false is the authoritative "process is gone" signal -
	// no in-flight event can be lost to a Done/last-event race.
	eventCh := proc.Events()
	runTimer := time.NewTimer(runTimeout)
	defer runTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			// User abort: tell Pi to stop, then terminate the tree. Cancel
			// alone is an incomplete run, but an earlier AppendEvent failure
			// makes the official log incomplete - the storage invariant below
			// must not be hidden behind the cancel status (spec §8, EC-10).
			_ = proc.Abort()
			rep := r.report(completion.StatusIncomplete, st, r.now())
			return rep, applyStorageInvariant(rep, st, nil)
		case <-runTimer.C:
			_ = proc.Abort()
			rep := r.report(completion.StatusFailed, st, r.now())
			runErr := &pirpc.Error{Code: "E_PI_RPC", Message: "pi run did not settle within the timeout", Cause: nil}
			return rep, applyStorageInvariant(rep, st, runErr)
		case ev, ok := <-eventCh:
			if !ok {
				// The stream ended without agent_settled: Pi exited (or
				// died) without settling. Classify the abnormal exit.
				return r.abnormalExit(st, r.now(), proc)
			}
			if ended, rep, runErr := r.processEvent(st, ev); ended {
				return rep, runErr
			}
		}
	}
}

// processEvent handles one decoded RPC event: it updates the run state,
// emits the display event, and appends the session-log entry. It returns
// true when the run has ended (agent_settled), together with the report
// and any run error.
func (r *Runtime) processEvent(st *runState, ev pirpc.Event) (bool, *completion.Report, error) {
	switch ev.Type {
	case "response":
		// Command acks other than the initial prompt (e.g. the ack of an
		// abort) are not agent events.
	case "turn_start":
		// A turn is one assistant response plus any resulting tool calls
		// and results (Pi RPC protocol). Counting on tool events instead
		// would over-count multi-tool turns and miss text-only turns.
		st.turns++
	case "message_end":
		// The authoritative end-of-message state: how the assistant
		// message stopped, and why, when it stopped in error.
		if ev.StopReason != "" {
			st.stopReason = ev.StopReason
		}
		if ev.ErrorMsg != "" {
			st.finalError = ev.ErrorMsg
		}
	case "agent_settled":
		if aev := r.translate(ev); aev != nil {
			r.sink.Emit(*aev)
		}
		rep, runErr := r.finishRun(st, r.now())
		return true, rep, runErr
	case "message_update":
		// Pi carries the latest cumulative usage on its message_updates;
		// surface it to the display when it changes.
		if ev.Usage != nil {
			u := toProviderUsage(*ev.Usage)
			if !st.usageSeen || usageDiffers(st.usage, u) {
				st.usage, st.usageSeen = u, true
				r.sink.Emit(Event{Kind: EventUsageUpdated, Timestamp: r.now(), Usage: u})
			}
		}
		r.emitAndAppend(st, ev)
	default:
		// tool_execution_start / tool_execution_end / agent_end: display
		// and/or session events, nothing run-level.
		r.emitAndAppend(st, ev)
	}
	return false, nil, nil
}

// finishRun classifies a settled run. agent_settled only means Pi will not
// continue automatically (no retry, compaction retry, or queued follow-up
// remains); it is NOT a success signal. Per spec.md §8, completed requires
// positive evidence - a terminal message state (message_end) showing the
// model ended normally - so the outcome is taken from the last stopReason.
// The storage invariant (spec §8: an unrecoverable error is failed; EC-10:
// a report must not claim success when disk writes failed) is applied
// regardless of the settle classification: an incomplete events.jsonl
// makes the run failed, even when the settle itself was only incomplete.
func (r *Runtime) finishRun(st *runState, now time.Time) (*completion.Report, error) {
	status := completion.StatusCompleted
	var runErr error
	switch st.stopReason {
	case "stop":
		// Positive terminal state: the model ended its final message normally.
	case "error":
		status = completion.StatusFailed
		runErr = classifySettleError(st.finalError)
	case "":
		// No message_end was observed at all: the protocol never delivered a
		// terminal state, so there is no evidence of a normal completion.
		// A missing terminal state is a protocol anomaly, not a clean stop.
		status = completion.StatusFailed
		runErr = &pirpc.Error{Code: "E_PI_RPC", Message: "pi settled without a terminal message state (no message_end observed); completion cannot be verified", Cause: nil}
	default: // "length", "toolUse", "aborted", or an unrecognized value
		status = completion.StatusIncomplete
	}
	rep := r.report(status, st, now)
	return rep, applyStorageInvariant(rep, st, runErr)
}

// applyStorageInvariant enforces the spec.md §8 / EC-10 storage rule on
// every run exit path: when any session.AppendEvent failed, the official
// events.jsonl is incomplete, so the status becomes failed - never
// completed, and never merely incomplete - and the storage error (stable
// session code, original cause) is surfaced when no other run error is
// present. The incomplete-log fact is always kept in RemainingRisks as
// diagnostics. Early exit paths (launch, AGENTS.md, SendPrompt, ack
// failure) cannot have append failures: events are only appended after a
// successful prompt ack, so they do not need the invariant.
func applyStorageInvariant(rep *completion.Report, st *runState, runErr error) error {
	if st.appendFails == 0 {
		return runErr
	}
	rep.RemainingRisks = append(rep.RemainingRisks, fmt.Sprintf(
		"session event log incomplete: %d event(s) failed to persist (%s)", st.appendFails, st.appendErr))
	if rep.Status != completion.StatusFailed {
		rep.Status = completion.StatusFailed
	}
	if runErr == nil {
		runErr = appendStorageError(st)
	}
	return runErr
}

// classifySettleError turns a settled-in-error run into a stable Brunel
// error: the provider-error classifier over Pi's message when it has one,
// a protocol-level error otherwise.
func classifySettleError(msg string) error {
	if msg != "" {
		return pirpc.TranslateProviderError(pirpc.ProviderErrorReport{Message: msg})
	}
	return &pirpc.Error{Code: "E_PI_RPC", Message: `pi settled with stopReason "error" and no error message`, Cause: nil}
}

// appendStorageError surfaces accumulated session-persist failures as a
// Brunel error, reusing the session package's own code when present.
func appendStorageError(st *runState) error {
	code := session.ErrorCode(st.appendErr)
	if code == "" {
		code = "E_SESSION_STORAGE"
	}
	return &pirpc.Error{
		Code:    code,
		Message: fmt.Sprintf("%d session event(s) failed to persist; events.jsonl is incomplete", st.appendFails),
		Cause:   st.appendErr,
	}
}

// abnormalExit reports a failed run when Pi's event stream ends without an
// agent_settled event.
func (r *Runtime) abnormalExit(st *runState, now time.Time, proc pirpc.PiProcess) (*completion.Report, error) {
	// The stream is closed, so the process has exited; wait (bounded) for
	// the handle to record the exit code before reading it.
	select {
	case <-proc.Done():
	case <-time.After(exitCodeGrace):
	}
	var runErr error
	switch {
	case proc.ExitCode() != 0:
		if msg := strings.TrimSpace(proc.CapturedStderr()); msg != "" {
			runErr = pirpc.TranslateProviderError(pirpc.ProviderErrorReport{Message: msg})
		} else {
			runErr = &pirpc.Error{Code: "E_RUNTIME_ERROR", Message: "pi exited with a non-zero status", Cause: nil}
		}
	case st.finalError != "":
		runErr = classifySettleError(st.finalError)
	default:
		runErr = &pirpc.Error{Code: "E_PI_RPC", Message: "pi exited without settling", Cause: nil}
	}
	rep := r.report(completion.StatusFailed, st, now)
	return rep, applyStorageInvariant(rep, st, runErr)
}

// emitAndAppend translates one RPC event, emits it to the sink (display),
// and appends it to the session log. A failed append is tracked in st so
// the run is never reported as a clean completion when the official record
// is missing events.
func (r *Runtime) emitAndAppend(st *runState, ev pirpc.Event) {
	if aev := r.translate(ev); aev != nil {
		r.sink.Emit(*aev)
	}
	kind, payload := sessionEventFor(ev)
	if kind == "" {
		return
	}
	if _, err := r.session.AppendEvent(kind, payload, 0, ackSource); err != nil {
		if st.appendErr == nil {
			st.appendErr = err
		}
		st.appendFails++
	}
}

// sessionEventFor maps an RPC event to the session-log entry it records.
// Each tool call is recorded exactly once: as EvToolCall at toolcall_end
// (the model's completed call) and as EvToolResult at tool_execution_end
// (the host's execution result). toolcall_start and tool_execution_start
// record nothing - they are the start of streaming / start of execution of
// the same call, and logging them would duplicate the entry.
func sessionEventFor(ev pirpc.Event) (session.EventKind, map[string]string) {
	switch ev.Type {
	case "message_update":
		switch ev.AssistantType {
		case "text_delta":
			return session.EvAssistantText, map[string]string{"text": ev.DeltaText}
		case "toolcall_end":
			return session.EvToolCall, map[string]string{"tool": ev.ToolName, "id": ev.CallID}
		}
	case "tool_execution_end":
		return session.EvToolResult, map[string]string{"tool": ev.ExecName, "id": ev.ToolCallID}
	}
	return "", nil
}

// translate maps a Pi RPC event to the frozen agent.Event. It returns nil
// for events the display does not surface: session header, response acks,
// message_end, turn_start, agent_end, a usage-only message_update (its
// usage is surfaced separately as EventUsageUpdated), and the toolcall_*
// streaming events - the model assembling a call is not the tool running;
// the display's tool boundaries are tool_execution_start / tool_execution_end.
func (r *Runtime) translate(ev pirpc.Event) *Event {
	ts := r.now()
	switch ev.Type {
	case "message_update":
		if ev.AssistantType == "text_delta" && ev.DeltaText != "" {
			return &Event{Kind: EventAssistantDelta, Timestamp: ts, Text: ev.DeltaText}
		}
	case "tool_execution_start":
		return &Event{Kind: EventToolStarted, Timestamp: ts, ToolCallID: ev.ToolCallID, ToolName: ev.ExecName}
	case "tool_execution_end":
		return &Event{Kind: EventToolFinished, Timestamp: ts, ToolCallID: ev.ToolCallID, ToolName: ev.ExecName}
	case "agent_settled":
		return &Event{Kind: EventRunFinished, Timestamp: ts}
	}
	return nil
}

// report builds the frozen completion.Report (spec.md §8) best-effort. Only
// the fields #9 can observe are filled; the workspace diff, structured tool
// failures and a verification engine are work #14 owns.
func (r *Runtime) report(status string, st *runState, now time.Time) *completion.Report {
	return &completion.Report{
		SchemaVersion: completion.SchemaVersion,
		SessionID:     r.session.Metadata().ID,
		Task:          st.task,
		Status:        status,
		Cost: completion.CostSummary{
			PromptTokens:     st.usage.PromptTokens,
			CompletionTokens: st.usage.CompletionTokens,
			CostUSD:          st.usage.CostUSD,
			DurationSec:      now.Sub(st.started).Seconds(),
			Turns:            st.turns,
		},
	}
}

// waitPromptAck blocks until Pi acknowledges the initial prompt (a
// "response" command ack) or the run fails. Events that arrive before the
// ack are returned for the caller to process, not dropped: the protocol
// streams events asynchronously, so they are legitimate. A non-success ack
// means Pi rejected the prompt.
func waitPromptAck(ctx context.Context, proc pirpc.PiProcess, timeout time.Duration) (bool, []pirpc.Event, error) {
	eventCh := proc.Events()
	doneCh := proc.Done()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var preAck []pirpc.Event
	for {
		select {
		case <-ctx.Done():
			return false, preAck, ctx.Err()
		case <-doneCh:
			return false, preAck, &pirpc.Error{Code: "E_PI_RPC", Message: "pi exited before acknowledging the initial prompt", Cause: nil}
		case <-timer.C:
			return false, preAck, &pirpc.Error{Code: "E_PI_RPC", Message: "pi did not acknowledge the initial prompt within the timeout", Cause: nil}
		case ev, ok := <-eventCh:
			if !ok {
				return false, preAck, &pirpc.Error{Code: "E_PI_RPC", Message: "pi closed the event stream before acknowledging the initial prompt", Cause: nil}
			}
			if ev.Type == "response" {
				return ev.Success, preAck, nil
			}
			preAck = append(preAck, ev)
		}
	}
}

// readWorkspaceAGENTSmd reads the workspace-root AGENTS.md that Brunel
// injects into Pi's initial prompt (issue #9 §4). A missing file is not an
// error; a read failure is surfaced so the caller can decide whether to
// fail the launch. The minimal version is #9's scope; the near-directory
// version is #11.
func readWorkspaceAGENTSmd(workspaceRoot string) (string, error) {
	path := filepath.Join(workspaceRoot, "AGENTS.md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// buildInitialPrompt folds the workspace AGENTS.md into the initial prompt
// message Pi receives. Without AGENTS.md the message is just the task.
func buildInitialPrompt(task, agentFile string) string {
	trimmed := strings.TrimSpace(agentFile)
	if trimmed == "" {
		return task
	}
	return task + "\n\n---Workspace agent instructions (AGENTS.md)---\n" + trimmed + "\n---end of AGENTS.md---"
}

// toProviderUsage maps a decoded Pi usage snapshot to provider.Usage.
func toProviderUsage(u pirpc.Usage) provider.Usage {
	var cost *float64
	if u.Cost != nil {
		c := *u.Cost
		cost = &c
	}
	return provider.Usage{
		PromptTokens:     int(u.Input),
		CompletionTokens: int(u.Output),
		CostUSD:          cost,
	}
}

// usageDiffers reports whether two cumulative usage snapshots differ.
// CostUSD is a pointer so an unknown cost (nil) stays distinguishable from
// a reported zero, and pointer identity is meaningless - compare by value.
func usageDiffers(a, b provider.Usage) bool {
	if a.PromptTokens != b.PromptTokens || a.CompletionTokens != b.CompletionTokens {
		return true
	}
	if (a.CostUSD == nil) != (b.CostUSD == nil) {
		return true
	}
	if a.CostUSD != nil && *a.CostUSD != *b.CostUSD {
		return true
	}
	return false
}
