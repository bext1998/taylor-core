package agent

import (
	"context"
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
// terminates the Job Object regardless).
const (
	ackTimeout    = 30 * time.Second
	runTimeout    = 30 * time.Minute
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

// Run launches Pi, sends the task (with the injected AGENTS.md), and
// streams events until Pi settles, the user aborts, or the run times out.
// It returns a completion.Report (spec.md §8) filled best-effort and, on
// unrecoverable failure, a Brunel error.
func (r *Runtime) Run(ctx context.Context, task string, sink EventSink) (*completion.Report, error) {
	started := r.now()
	var usage provider.Usage
	turns := 0
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
		return r.report(completion.StatusFailed, task, usage, turns, started, started), err
	}
	defer proc.Close()

	// (4) Read the workspace-root AGENTS.md and fold it into the prompt.
	agentFile, err := readWorkspaceAGENTSmd(r.workspaceRoot)
	if err != nil {
		return r.report(completion.StatusFailed, task, usage, turns, started, started), err
	}
	prompt := buildInitialPrompt(task, agentFile)
	if err := proc.SendPrompt(prompt); err != nil {
		return r.report(completion.StatusFailed, task, usage, turns, started, started), err
	}

	// Acknowledge the launch: Pi must ack the initial prompt before we
	// proceed, so a launch that never answers surfaces as a failure.
	acked, err := waitPromptAck(ctx, proc, ackTimeout)
	if err != nil {
		return r.report(completion.StatusFailed, task, usage, turns, started, started), err
	}
	if !acked {
		return r.report(completion.StatusFailed, task, usage, turns, started, started),
			&pirpc.Error{Code: "E_PI_RPC", Message: "pi did not acknowledge the initial prompt", Cause: nil}
	}

	// (2) + (3) Event loop: translate/emit/display and persist to the
	// session log until Pi settles, the user aborts, or the run times out.
	eventCh := proc.Events()
	doneCh := proc.Done()
	runTimer := time.NewTimer(runTimeout)
	defer runTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			// User abort: tell Pi to stop, then terminate the tree.
			_ = proc.Abort()
			return r.report(completion.StatusIncomplete, task, usage, turns, started, r.now()), nil
		case <-runTimer.C:
			_ = proc.Abort()
			return r.report(completion.StatusFailed, task, usage, turns, started, r.now()),
				&pirpc.Error{Code: "E_PI_RPC", Message: "pi run did not settle within the timeout", Cause: nil}
		case <-doneCh:
			// The process exited. Drain any pending agent_settled event
			// before declaring an abnormal exit (a select race can close
			// Done and deliver the settle event together).
			for {
				select {
				case ev := <-eventCh:
					if ev.Type == "agent_settled" {
						r.emitAndAppend(ev)
						return r.report(completion.StatusCompleted, task, usage, turns, started, r.now()), nil
					}
					r.emitAndAppend(ev)
				default:
					return r.abnormalExit(task, usage, turns, started, r.now(), proc)
				}
			}
		case ev, ok := <-eventCh:
			if !ok {
				return r.abnormalExit(task, usage, turns, started, r.now(), proc)
			}
			switch ev.Type {
			case "response":
				// Command acks other than the initial prompt (e.g. tool
				// result echoes) are not agent events.
			case "message_update":
				if ev.Usage != nil {
					usage = toProviderUsage(*ev.Usage)
				}
				r.emitAndAppend(ev)
			case "tool_execution_start":
				turns++
				r.emitAndAppend(ev)
			case "tool_execution_end":
				r.emitAndAppend(ev)
			case "agent_settled":
				r.emitAndAppend(ev)
				return r.report(completion.StatusCompleted, task, usage, turns, started, r.now()), nil
			}
		}
	}
}

// abnormalExit reports a failed run when Pi exits without settling. It
// translates Pi's captured stderr into a provider/protocol error when
// possible, otherwise surfaces a generic runtime failure.
func (r *Runtime) abnormalExit(task string, usage provider.Usage, turns int, started, now time.Time, proc pirpc.PiProcess) (*completion.Report, error) {
	if code := proc.ExitCode(); code != 0 {
		if msg := strings.TrimSpace(proc.CapturedStderr()); msg != "" {
			if terr := pirpc.TranslateProviderError(pirpc.ProviderErrorReport{Message: msg}); terr != nil {
				return r.report(completion.StatusFailed, task, usage, turns, started, now), terr
			}
		}
		return r.report(completion.StatusFailed, task, usage, turns, started, now),
			&pirpc.Error{Code: "E_RUNTIME_ERROR", Message: "pi exited with a non-zero status", Cause: nil}
	}
	return r.report(completion.StatusFailed, task, usage, turns, started, now),
		&pirpc.Error{Code: "E_PI_RPC", Message: "pi exited without settling", Cause: nil}
}

// emitAndAppend translates one RPC event, emits it to the sink (display)
// and appends it to the session log.
func (r *Runtime) emitAndAppend(ev pirpc.Event) {
	if aev, _ := r.translate(ev); aev != nil {
		r.sink.Emit(*aev)
	}
	r.append(ev)
}

// translate maps a Pi RPC event to the frozen agent.Event. It returns nil
// for events the display does not surface (e.g. a bare usage-only
// message_update with no assistant text/tool).
func (r *Runtime) translate(ev pirpc.Event) (*Event, error) {
	ts := r.now()
	switch ev.Type {
	case "message_update":
		switch ev.AssistantType {
		case "text_delta":
			if ev.DeltaText != "" {
				return &Event{Kind: EventAssistantDelta, Timestamp: ts, Text: ev.DeltaText}, nil
			}
		case "toolcall_start":
			return &Event{Kind: EventToolStarted, Timestamp: ts, ToolCallID: ev.CallID, ToolName: ev.ToolName}, nil
		case "toolcall_end":
			return &Event{Kind: EventToolFinished, Timestamp: ts, ToolCallID: ev.CallID}, nil
		}
	case "tool_execution_start":
		return &Event{Kind: EventToolStarted, Timestamp: ts, ToolCallID: ev.ToolCallID, ToolName: ev.ExecName}, nil
	case "tool_execution_end":
		return &Event{Kind: EventToolFinished, Timestamp: ts, ToolCallID: ev.ToolCallID, ToolName: ev.ExecName}, nil
	case "agent_settled":
		return &Event{Kind: EventRunFinished, Timestamp: ts}, nil
	}
	return nil, nil
}

// append persists a Pi RPC event to the session log (issue #9 §3). The
// mapping to the frozen session.EventKinds is documented in the spec.
func (r *Runtime) append(ev pirpc.Event) {
	var kind session.EventKind
	var payload any
	switch ev.Type {
	case "message_update":
		switch ev.AssistantType {
		case "text_delta":
			kind = session.EvAssistantText
			payload = map[string]string{"text": ev.DeltaText}
		case "toolcall_start":
			kind = session.EvToolCall
			payload = map[string]string{"tool": ev.ToolName, "id": ev.CallID}
		case "toolcall_end":
			kind = session.EvToolResult
			payload = map[string]string{"tool": ev.ToolName, "id": ev.CallID}
		}
	case "tool_execution_start":
		kind = session.EvToolCall
		payload = map[string]string{"tool": ev.ExecName, "id": ev.ToolCallID}
	case "tool_execution_end":
		kind = session.EvToolResult
		payload = map[string]string{"tool": ev.ExecName, "id": ev.ToolCallID}
	}
	if kind == "" {
		return
	}
	if _, err := r.session.AppendEvent(kind, payload, 0, ackSource); err != nil {
		// A session write error does not fail the run; the display still
		// received the event.
	}
}

// report builds the frozen completion.Report (spec.md §8) best-effort.
// Only the fields #9 can observe are filled; the workspace diff,
// structured tool failures and a verification engine are work #14 owns.
func (r *Runtime) report(status, task string, usage provider.Usage, turns int, started, now time.Time) *completion.Report {
	return &completion.Report{
		SchemaVersion: completion.SchemaVersion,
		SessionID:     r.session.Metadata().ID,
		Task:          task,
		Status:        status,
		Cost: completion.CostSummary{
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			CostUSD:          usage.CostUSD,
			DurationSec:      now.Sub(started).Seconds(),
			Turns:            turns,
		},
	}
}

// waitPromptAck blocks until Pi acknowledges the initial prompt (a
// "response" command ack) or the run fails. A non-success ack means Pi
// rejected the prompt.
func waitPromptAck(ctx context.Context, proc pirpc.PiProcess, timeout time.Duration) (bool, error) {
	eventCh := proc.Events()
	doneCh := proc.Done()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-doneCh:
			return false, &pirpc.Error{Code: "E_PI_RPC", Message: "pi exited before acknowledging the initial prompt", Cause: nil}
		case <-timer.C:
			return false, &pirpc.Error{Code: "E_PI_RPC", Message: "pi did not acknowledge the initial prompt within the timeout", Cause: nil}
		case ev, ok := <-eventCh:
			if !ok {
				return false, &pirpc.Error{Code: "E_PI_RPC", Message: "pi closed the event stream before acknowledging the initial prompt", Cause: nil}
			}
			if ev.Type == "response" {
				return ev.Success, nil
			}
			// An event arriving before the ack is unexpected; keep waiting.
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
