//go:build !windows

package pirpc

import (
	"context"
	"io"
	"os/exec"
	"time"
)

// runVersionProbe runs the probe with os/exec. Brunel supports Windows only;
// this keeps the non-Windows build and its tests working, without the Job
// Object the Windows implementation uses.
func runVersionProbe(ctx context.Context, path string, args []string, workDir string, stdout io.Writer) error {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = workDir
	cmd.WaitDelay = time.Second
	cmd.Stdout = stdout
	return cmd.Run()
}
