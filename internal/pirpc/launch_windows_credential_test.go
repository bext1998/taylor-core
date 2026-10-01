//go:build windows

package pirpc

import (
	"context"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Defence in depth behind InjectCredentials: whatever builds the environment,
// an entry with a NUL must not be encoded into the block.
func TestBuildEnvBlockRejectsNUL(t *testing.T) {
	block, err := buildEnvBlock([]string{"PATH=base-path", "OPENROUTER_API_KEY=a\x00b"})
	if err == nil {
		t.Fatalf("buildEnvBlock() = %v, want an error for an entry containing NUL", block)
	}
	if strings.Contains(err.Error(), "OPENROUTER_API_KEY=a") {
		t.Fatalf("error %q echoes the environment entry", err)
	}
}

// The user only ever saw "failed to start the pi subprocess" for a failed
// CreateProcess. The Win32 reason (public, no secret) must be in the message.
func TestStartPiProcessFailureNamesWin32Reason(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-pi.exe")
	_, err := startPiProcess(context.Background(), missing, nil, nil, ".")
	if err == nil {
		t.Fatal("startPiProcess() succeeded for a missing executable")
	}
	if ErrorCode(err) != ErrPiRuntimeRequired.Code {
		t.Fatalf("ErrorCode(err) = %q, want %q", ErrorCode(err), ErrPiRuntimeRequired.Code)
	}
	want := syscall.Errno(syscall.ERROR_FILE_NOT_FOUND).Error()
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want it to contain the Win32 reason %q", err, want)
	}
}
