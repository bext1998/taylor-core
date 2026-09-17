package pirpc

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
)

// piProcess is the platform handle to one running `pi --mode rpc`
// subprocess. The cross-platform run loop that drives it lives in
// internal/agent; this package owns the handle and the RPC wire protocol,
// so piProcess is the only place that speaks Pi's protocol. The Windows
// implementation lives in launch_windows.go; the non-Windows fallback is
// launch_nonwindows.go.
type PiProcess interface {
	// SendPrompt writes the initial RPC prompt command (with the
	// injected AGENTS.md context) to Pi's stdin.
	SendPrompt(message string) error
	// Abort tells Pi to stop the current run and then waits for the
	// process to exit, so no Pi descendant outlives the call.
	Abort() error
	// Events returns the channel of decoded RPC events; it is closed
	// when the subprocess exits.
	Events() <-chan Event
	// Done is closed when the subprocess exits (independently of Events).
	Done() <-chan struct{}
	// ExitCode returns the process exit status once Done has closed.
	ExitCode() int
	// CapturedStderr returns any stderr captured while the process ran, for
	// translating a provider/protocol failure on abnormal exit.
	CapturedStderr() string
	// Close aborts and releases every resource owned by the handle. It is
	// idempotent and safe to call on every exit path.
	Close()
}

// Event is a decoded Pi RPC event. Only the fields relevant to Brunel's
// display (agent.Event) and session log are populated; decodeRPCEvent
// decides which wire fields populate which field per event type. The
// cross-platform run loop in internal/agent maps these to the frozen
// agent.EventKind.
type Event struct {
	// Type is the RPC event's wire type field (e.g. "message_update").
	// It is the run loop's primary discriminator.
	Type string

	// Response is set when Type == "response": the command ack Pi sends
	// for an RPC command, with Success and the Command it acked.
	Response  bool
	Success   bool
	Command   string

	// Usage is the cumulative provider usage carried by a message_update.
	Usage *Usage

	// AssistantType is the assistantMessageEvent.type of a message_update
	// (e.g. "text_delta", "toolcall_start", "toolcall_end"); empty for
	// other event types.
	AssistantType string
	// CallID is the toolcall id (message_update toolcall_start/end).
	CallID string
	// ToolName is the toolName of a message_update toolcall.
	ToolName string
	// DeltaText is the text_delta payload of a message_update.
	DeltaText string

	// ToolCallID / ExecName are the tool_execution_start/end identifiers
	// (Pi's toolCallId / toolName for the Go-hosted Taylor tool).
	ToolCallID string
	ExecName   string
	// IsError is true for a tool_execution_end that reported failure.
	IsError bool

	// WillRetry is set by an agent_end that will retry the run.
	WillRetry bool
}

// Usage mirrors Pi's cumulative provider-reported usage object on a
// message_update. Brunel keeps the two token buckets and the total cost;
// the run loop maps it to provider.Usage.
type Usage struct {
	Input       float64
	Output      float64
	TotalTokens float64
	Cost        *float64
}

// wireEvent is the raw JSON shape decoded from one RPC line. Fields are
// tagged to the exact names Pi emits; unknown fields are ignored.
type wireEvent struct {
	Type       string         `json:"type"`
	Success    bool           `json:"success"`
	Command    string         `json:"command"`
	Usage      *piUsage       `json:"usage"`
	Assistant  json.RawMessage `json:"assistantMessageEvent"`
	ToolCallID string         `json:"toolCallId"`
	ToolName   string         `json:"toolName"`
	WillRetry  *bool          `json:"willRetry"`
	IsError    *bool          `json:"isError"`
	Msg        string         `json:"message"`
}

// piUsage mirrors Pi's nested usage object.
type piUsage struct {
	Input    float64 `json:"input"`
	Output   float64 `json:"output"`
	CacheRead float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total    float64 `json:"totalTokens"`
	Cost     *piCost `json:"cost"`
}

// piCost mirrors Pi's usage.cost object.
type piCost struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
	Total  float64 `json:"total"`
}

// decodeRPCEvent decodes one JSONL line from Pi's stdout. It returns the
// decoded Event and whether it is a Brunel-relevant event (true), or a
// session header / command response / unrecognized event (false, in which
// the Event is a zero value except for the header/response discriminators).
// A malformed line returns (Event{}, false) so the run loop can skip it
// without failing the run.
func decodeRPCEvent(line []byte) (Event, bool) {
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" {
		return Event{}, false
	}
	var w wireEvent
	if err := json.Unmarshal([]byte(trimmed), &w); err != nil {
		return Event{}, false
	}
	switch w.Type {
	case "", "session":
		// The first line is Pi's session header; nothing to translate.
		return Event{Type: "session"}, false
	case "response":
		return Event{Type: "response", Response: true, Success: w.Success, Command: w.Command}, true
	case "message_update":
		return Event{
			Type:      "message_update",
			Usage:     toDecodedUsage(w.Usage),
			AssistantType: decodeAssistantType(w.Assistant),
			CallID:    decodeCallID(w.Assistant),
			ToolName:  decodeToolName(w.Assistant),
			DeltaText: decodeDelta(w.Assistant),
		}, true
	case "tool_execution_start":
		return Event{
			Type:       "tool_execution_start",
			ToolCallID: w.ToolCallID,
			ExecName:   w.ToolName,
		}, true
	case "tool_execution_end":
		return Event{
			Type:       "tool_execution_end",
			ToolCallID: w.ToolCallID,
			ExecName:   w.ToolName,
			IsError:    w.IsError != nil && *w.IsError,
		}, true
	case "agent_end":
		return Event{
			Type:      "agent_end",
			WillRetry: w.WillRetry != nil && *w.WillRetry,
		}, true
	case "agent_settled":
		// The run loop's completion signal: Pi finished its final run.
		return Event{Type: "agent_settled"}, true
	default:
		// queue_update, compaction_start/end, turn_start/turn_end, etc.
		// are not displayed by #9; skip them.
		return Event{}, false
	}
}

// decodeAssistantEvent extracts the assistantMessageEvent payload as a
// map. Pi emits assistantMessageEvent as a JSON object whose "type" is one
// of text_start/text_delta/text_end (text) or toolcall_start/toolcall_end
// (a tool call); the exact sub-fields are read from the raw JSON so the
// decoder never assumes an internal session shape. If Pi wraps the object
// in a JSON string, the decoder unwraps it.
func decodeAssistantEvent(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err == nil {
		return m
	}
	// Fallback: the payload may be a JSON-encoded string.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil && s != "" {
		var m2 map[string]any
		if json.Unmarshal([]byte(s), &m2) == nil {
			return m2
		}
	}
	return nil
}

func decodeAssistantType(raw json.RawMessage) string {
	m := decodeAssistantEvent(raw)
	if m == nil {
		return ""
	}
	if t, ok := m["type"].(string); ok {
		return t
	}
	return ""
}

func decodeCallID(raw json.RawMessage) string {
	m := decodeAssistantEvent(raw)
	if m == nil {
		return ""
	}
	if id, ok := m["id"].(string); ok {
		return id
	}
	return ""
}

func decodeToolName(raw json.RawMessage) string {
	m := decodeAssistantEvent(raw)
	if m == nil {
		return ""
	}
	if tn, ok := m["toolName"].(string); ok {
		return tn
	}
	return ""
}

func decodeDelta(raw json.RawMessage) string {
	m := decodeAssistantEvent(raw)
	if m == nil {
		return ""
	}
	if d, ok := m["delta"].(string); ok {
		return d
	}
	return ""
}

// toDecodedUsage converts Pi's nested usage object to the flat Usage the
// run loop maps to provider.Usage. Cost is a pointer so a missing cost
// stays distinguishable from a reported zero.
func toDecodedUsage(u *piUsage) *Usage {
	if u == nil {
		return nil
	}
	out := &Usage{Input: u.Input, Output: u.Output, TotalTokens: u.Total}
	if u.Cost != nil {
		total := u.Cost.Total
		out.Cost = &total
	}
	return out
}

// command is the base JSON command shape all RPC commands share. Keeping
// the type field here (and never the literal bash command) is what makes
// INV-9's AST check meaningful - see bash_guard_test.go.
type command struct {
	Type string `json:"type"`
}

// promptCommand is the initial RPC command Brunel sends to Pi. Its message
// carries the task plus the injected workspace AGENTS.md (issue #9 §4).
type promptCommand struct {
	command
	Message string `json:"message"`
}

// abortCommand is the RPC command sent to stop Pi's current run.
type abortCommand struct {
	command
}

// RPCPrompt builds the initial prompt command. It is the only place the
// "prompt" RPC type is constructed, and it never emits a bash command.
func RPCPrompt(message string) *promptCommand {
	return &promptCommand{command: command{Type: "prompt"}, Message: message}
}

// RPCAbort builds the abort command.
func RPCAbort() *abortCommand {
	return &abortCommand{command: command{Type: "abort"}}
}

// Start launches the pi --mode rpc subprocess and returns a handle the
// internal/agent run loop drives. It resolves the pi executable (EC-13),
// builds the frozen launch args (spec.md §5.1), injects credentials and
// the Brunel extension's required environment (BRUNEL_EXE / BRUNEL_MODE),
// and binds the whole process tree to a Windows Job Object so no Pi
// descendant outlives Close(). Only internal/pirpc speaks Pi's protocol.
func Start(ctx context.Context, opts LaunchOptions, cred Credential, workDir string, extraEnv map[string]string) (PiProcess, error) {
	if strings.TrimSpace(opts.Model) == "" {
		return nil, codeError(ErrInvalidArgument.Code, "model is required", nil)
	}
	if strings.TrimSpace(workDir) == "" {
		return nil, codeError(ErrInvalidArgument.Code, "work dir is required", nil)
	}

	piPath, err := resolvePiPath()
	if err != nil {
		return nil, err
	}
	args, err := BuildArgs(opts)
	if err != nil {
		return nil, err
	}

	base := os.Environ()
	env, err := InjectCredentialsForLaunch(base, opts, cred)
	if err != nil {
		return nil, err
	}
	env = mergeEnv(env, extraEnv)

	return startPiProcess(ctx, piPath, args, env, workDir)
}

// mergeEnv adds or overrides entries from add onto base, returning a new
// slice. Keys are matched case-insensitively on Windows.
func mergeEnv(base []string, add map[string]string) []string {
	if len(add) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(add))
	out = append(out, base...)
	for k, v := range add {
		replaced := false
		for i := range out {
			if name, _, ok := strings.Cut(out[i], "="); ok && strings.EqualFold(name, k) {
				out[i] = k + "=" + v
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, k + "=" + v)
		}
	}
	return out
}

// resolvePiPath locates the pi executable on PATH. It never falls back to
// npx or a project-local install: a missing pi means the Node.js/npm
// runtime requirement (spec.md §11 EC-13) is unmet, which must surface as
// E_RUNTIME_REQUIRED, not a silent no-op.
func resolvePiPath() (string, error) {
	path, err := exec.LookPath("pi")
	if err != nil {
		return "", codeError(ErrPiRuntimeRequired.Code, "pi (Node.js/npm runtime) was not found on PATH", err)
	}
	return path, nil
}
