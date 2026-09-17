//go:build windows

package pirpc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// buildFakePi compiles the fake pi program into a temp dir and returns its
// absolute path.
func buildFakePi(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "fakepi.exe")
	cmd := exec.Command("go", "build", "-o", out, "./testdata/fakepi")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build fake pi: %v", err)
	}
	return out
}

// TestStartPiProcessLifecycle exercises the real Windows subprocess handle
// (issue #9 §1): it starts the fake pi, sends a prompt, decodes the event
// stream through the Windows pipe, verifies the ack and the settle event,
// and confirms the process exits cleanly with status 0.
func TestStartPiProcessLifecycle(t *testing.T) {
	piPath := buildFakePi(t)

	proc, err := startPiProcess(context.Background(), piPath, []string{"--mode", "rpc"}, nil, ".")
	if err != nil {
		t.Fatalf("startPiProcess() error = %v", err)
	}
	defer proc.Close()

	if err := proc.SendPrompt("do work"); err != nil {
		t.Fatalf("SendPrompt() error = %v", err)
	}

	if !awaitAck(proc) {
		t.Fatal("did not receive a success command ack")
	}

	var deltas []string
	settled := false
	for !settled {
		select {
		case ev := <-proc.Events():
			switch ev.Type {
			case "message_update":
				deltas = append(deltas, ev.DeltaText)
				if ev.Usage == nil || ev.Usage.Input != 10 || ev.Usage.Output != 20 {
					t.Fatalf("usage = %+v, want Input 10 Output 20", ev.Usage)
				}
			case "agent_settled":
				settled = true
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("timed out waiting for events; stderr=%q", proc.CapturedStderr())
		}
	}
	if len(deltas) != 1 || deltas[0] != "hello from fake pi" {
		t.Fatalf("delta = %v, want [hello from fake pi]", deltas)
	}

	select {
	case <-proc.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("process did not exit after settling")
	}
	if code := proc.ExitCode(); code != 0 {
		t.Fatalf("ExitCode() = %d, want 0", code)
	}
}

// TestCloseIsIdempotent covers the review finding that the Windows handle
// leaked its process/thread handles and that Close was not idempotent. It
// starts a real subprocess, then calls Close twice: the second call must be
// a no-op (no panic / double-handle-close), and the process must be gone.
func TestCloseIsIdempotent(t *testing.T) {
	piPath := buildFakePi(t)

	proc, err := startPiProcess(context.Background(), piPath, []string{"--mode", "rpc"}, nil, ".")
	if err != nil {
		t.Fatalf("startPiProcess() error = %v", err)
	}

	proc.Close()
	proc.Close() // must not panic or double-close handles

	select {
	case <-proc.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("process did not exit after Close")
	}
}

// awaitAck returns true when the first success command ack arrives.
func awaitAck(proc PiProcess) bool {
	timer := time.After(10 * time.Second)
	for {
		select {
		case <-timer:
			return false
		case ev := <-proc.Events():
			if ev.Type == "response" && ev.Success {
				return true
			}
		}
	}
}
