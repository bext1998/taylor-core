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
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"
)

func main() {
	// --hold is the grandchild of the version-probe tree test: it records its
	// pid and then lives until it is killed.
	if len(os.Args) > 1 && os.Args[1] == "--hold" {
		_ = os.WriteFile(os.Getenv("FAKE_PI_PIDFILE"), []byte(strconv.Itoa(os.Getpid())), 0600)
		time.Sleep(10 * time.Minute)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		// FAKE_PI_VERSION_TREE starts a grandchild that inherits stdout, then
		// either hangs ("hang") or prints the version and exits ("exit"),
		// leaving the grandchild behind.
		if mode := os.Getenv("FAKE_PI_VERSION_TREE"); mode != "" {
			grandchild := exec.Command(os.Args[0], "--hold")
			grandchild.Stdout, grandchild.Stderr = os.Stdout, os.Stderr
			if err := grandchild.Start(); err != nil {
				os.Exit(2)
			}
			for i := 0; i < 500; i++ {
				if _, err := os.Stat(os.Getenv("FAKE_PI_PIDFILE")); err == nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if mode == "hang" {
				time.Sleep(10 * time.Minute)
			}
		}
		if os.Getenv("FAKE_PI_VERSION_EXIT") == "1" {
			os.Exit(1)
		}
		version, ok := os.LookupEnv("FAKE_PI_VERSION")
		if !ok {
			version = "0.85.1"
		}
		fmt.Println(version)
		return
	}
	if marker := os.Getenv("FAKE_PI_RPC_MARKER"); marker != "" {
		_ = os.WriteFile(marker, []byte("started"), 0600)
	}
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
