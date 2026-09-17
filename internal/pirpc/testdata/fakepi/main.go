// Package main is a minimal fake of `pi --mode rpc` for issue #9's
// real-subprocess lifecycle test (internal/pirpc/launch_windows_test.go).
//
// It mirrors Pi's JSONL wire protocol closely enough to exercise the
// Windows subprocess handle: it reads the initial prompt command from
// stdin, replies with a session header, a success command ack, one
// turn (turn_start, assistant message_update with usage, message_end
// stopReason=stop), and an agent_settled settle event, then exits with
// status 0.
//
// This source lives under testdata, so it is excluded from
// `go build ./...`, `go vet ./...`, and the INV-9 AST bash guard.
package main

import (
	"bufio"
	"encoding/json"
	"os"
)

func main() {
	// Read (and ignore) the initial prompt command Pi was sent.
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')

	// Session header: decoded as not-relevant by the run loop.
	emit(map[string]any{"type": "session", "session_id": "fake"})
	// Command ack: the run loop treats the first success response as the ack.
	emit(map[string]any{"type": "response", "success": true, "command": "prompt"})
	// One turn with a single assistant message carrying cumulative usage.
	emit(map[string]any{"type": "turn_start"})
	emit(map[string]any{
		"type": "message_update",
		"usage": map[string]any{
			"input":       10.0,
			"output":      20.0,
			"totalTokens": 30.0,
		},
		"assistantMessageEvent": map[string]any{
			"type":  "text_delta",
			"delta": "hello from fake pi",
		},
	})
	// Authoritative end of the assistant message: clean stop.
	emit(map[string]any{
		"type":    "message_end",
		"message": map[string]any{"role": "assistant", "stopReason": "stop"},
	})
	// Settle: the run loop's completion signal.
	emit(map[string]any{"type": "agent_settled"})

	os.Exit(0)
}

// emit writes one JSON object followed by a newline to stdout.
func emit(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	_, _ = os.Stdout.Write(append(b, '\n'))
}
