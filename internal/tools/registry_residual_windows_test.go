//go:build windows

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	brunelexec "github.com/bext1998/brunel/internal/exec"
	"github.com/bext1998/brunel/internal/safety"
)

func powerShellRegistry(t *testing.T, root string, approver safety.Approver) *Registry {
	t.Helper()
	runner, err := brunelexec.NewRunner()
	if err != nil {
		t.Skip("pwsh is required: ", err)
	}
	return newTestRegistry(t, root, safety.ModeWorkspace, approver, runner)
}

// AC-6 only exercised AUTO commands. A CONFIRM command the user approves must
// actually run through the registry (#69 item 9), and be asked about once.
func TestRegistryApprovedConfirmCommandRuns(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "x"), "present\n")
	approver := &fakeApprover{approve: true}
	registry := powerShellRegistry(t, root, approver)

	result, err := registry.Call(context.Background(), "run_powershell", json.RawMessage(`{"command":"Remove-Item .\\x -Recurse"}`))
	if err != nil || result.Run == nil || result.Run.ExitCode != 0 {
		t.Fatalf("approved command result = %#v, err = %v", result, err)
	}
	if approver.calls != 1 {
		t.Fatalf("approver calls = %d, want 1", approver.calls)
	}
	if _, err := os.Stat(filepath.Join(root, "x")); !os.IsNotExist(err) {
		t.Fatalf("the approved command did not run: stat err = %v", err)
	}
}

// A cwd that leaves the workspace is refused with the stable path-escape code
// and nothing is run (#69 item 9).
func TestRegistryPowerShellCwdEscapeIsRejected(t *testing.T) {
	root := t.TempDir()
	registry := powerShellRegistry(t, root, nil)
	_, err := registry.Call(context.Background(), "run_powershell", json.RawMessage(`{"command":"Write-Output hi","cwd":"..\\.."}`))
	if ErrorCode(err) != "E_PATH_ESCAPE" {
		t.Fatalf("cwd escape code = %q, want E_PATH_ESCAPE (err = %v)", ErrorCode(err), err)
	}
}

// timeout_sec must reach the runner: a one-second timeout cuts short a command
// that the generous default limit would have let finish (#69 item 9).
func TestRegistryPowerShellTimeoutSecIsPassedToTheRunner(t *testing.T) {
	registry := powerShellRegistry(t, t.TempDir(), nil)
	start := time.Now()
	_, err := registry.Call(context.Background(), "run_powershell", json.RawMessage(`{"command":"Start-Sleep -Seconds 20","timeout_sec":1}`))
	if ErrorCode(err) != "E_TOOL_TIMEOUT" {
		t.Fatalf("timeout code = %q, want E_TOOL_TIMEOUT (err = %v)", ErrorCode(err), err)
	}
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Fatalf("returned after %v: timeout_sec was not applied (the default limit is 10s)", elapsed)
	}
}
