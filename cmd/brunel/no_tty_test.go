//go:build windows

package main

import (
	"strings"
	"testing"

	"github.com/bext1998/brunel/internal/completion"
	"github.com/bext1998/brunel/internal/pirpc"
)

// AC-11: in pipe mode a command that needs approval ends the run with a
// non-zero status and the stable code on stderr.
func TestPlainModeNoTTYApprovalExitsNonZeroWithCode(t *testing.T) {
	h := newHarness(t, terminals{}, "")
	h.agent.err = &pirpc.Error{Code: "E_APPROVAL_REQUIRED_NO_TTY", Message: "a command needs approval but no terminal is attached"}
	h.agent.report = &completion.Report{SchemaVersion: completion.SchemaVersion, Status: completion.StatusIncomplete,
		PendingApproval: &completion.ApprovalFact{Command: "Remove-Item x", Reason: "approval required but no terminal is attached"}}
	code := runCLI([]string{"clean up", "--model", "anthropic/claude-x"}, h.env)
	if code != exitFailed {
		t.Fatalf("exit = %d, want %d", code, exitFailed)
	}
	if !strings.Contains(h.stderr.String(), "E_APPROVAL_REQUIRED_NO_TTY") {
		t.Fatalf("stderr = %q, want the stable code", h.stderr.String())
	}
}
