//go:build !windows

package pirpc

import (
	"context"
)

// On non-Windows platforms Brunel has no Job-Object based subprocess
// manager, so the pi --mode rpc launch cannot be performed. Start surfaces
// E_RUNTIME_REQUIRED (spec.md §11 EC-13) rather than a partial, unsafe
// attempt. The Windows implementation lives in launch_windows.go.
func startPiProcess(_ context.Context, _ string, _ []string, _ []string, _ string) (PiProcess, error) {
	return nil, codeError(ErrPiRuntimeRequired.Code, "the pi subprocess runner is only supported on Windows (spec.md §11 EC-13)", nil)
}
