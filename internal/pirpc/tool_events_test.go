package pirpc

import (
	"encoding/json"
	"testing"
)

// Lines captured from a real `pi --mode rpc` run (Pi 0.85.1) with the Taylor
// extension. The completion report is built from these two shapes: the
// arguments the model passed (start) and the structured result the Go tool
// returned (end), so decoding must keep both.
const (
	realToolStart = `{"type":"tool_execution_start","toolCallId":"call_1","toolName":"run_powershell","args":{"command":"exit 3"}}`
	realToolEnd   = `{"type":"tool_execution_end","toolCallId":"call_1","toolName":"run_powershell","result":{"content":[{"type":"text","text":"{}"}],"details":{"tool":"run_powershell","run":{"stdout":"","stderr":"","exit_code":3,"truncated":false}}},"isError":false}`
	failedToolEnd = `{"type":"tool_execution_end","toolCallId":"call_2","toolName":"run_powershell","result":{"content":[{"type":"text","text":"E_APPROVAL_DENIED: user declined confirmation"}]},"isError":true}`
)

func TestDecodeToolStartKeepsArguments(t *testing.T) {
	ev, ok := decodeRPCEvent([]byte(realToolStart))
	if !ok || ev.Type != "tool_execution_start" {
		t.Fatalf("decode = %+v, %v", ev, ok)
	}
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(ev.ToolArgs, &args); err != nil || args.Command != "exit 3" {
		t.Fatalf("ToolArgs = %s (%v), want the command the model passed", ev.ToolArgs, err)
	}
}

func TestDecodeToolEndKeepsStructuredResult(t *testing.T) {
	ev, ok := decodeRPCEvent([]byte(realToolEnd))
	if !ok || ev.IsError {
		t.Fatalf("decode = %+v, %v", ev, ok)
	}
	var details struct {
		Run struct {
			ExitCode int `json:"exit_code"`
		} `json:"run"`
	}
	if err := json.Unmarshal(ev.ToolDetails, &details); err != nil || details.Run.ExitCode != 3 {
		t.Fatalf("ToolDetails = %s (%v), want exit_code 3", ev.ToolDetails, err)
	}
}

// A failed call carries its stable error code and message only in the result
// text; without it a tool failure could not be reported with a reason.
func TestDecodeFailedToolEndKeepsErrorText(t *testing.T) {
	ev, ok := decodeRPCEvent([]byte(failedToolEnd))
	if !ok || !ev.IsError {
		t.Fatalf("decode = %+v, %v", ev, ok)
	}
	if ev.ToolErrorText != "E_APPROVAL_DENIED: user declined confirmation" {
		t.Fatalf("ToolErrorText = %q", ev.ToolErrorText)
	}
}
