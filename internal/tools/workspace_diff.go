package tools

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"time"

	brunelexec "github.com/bext1998/brunel/internal/exec"
	"github.com/bext1998/brunel/internal/filetools"
)

// workspaceDiffTimeout bounds one git diff; a variable so a test can shorten it.
var workspaceDiffTimeout = 30 * time.Second

func workspaceDiff(ctx context.Context, r filetools.Resolver, root, path string) (string, error) {
	// Resolve before spawning git so a path escape is rejected without any I/O.
	if _, err := r.Resolve(path); err != nil {
		return "", err
	}
	pathspec := "."
	if path != "" {
		pathspec = filepath.ToSlash(filepath.Clean(path))
	}
	commandCtx, cancel := context.WithTimeout(ctx, workspaceDiffTimeout)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, "git", "-C", root, "diff", "--", pathspec)
	// Output, not CombinedOutput: git's own warnings go to stderr and must not
	// end up inside the diff text.
	output, err := cmd.Output()
	if err != nil {
		// A cancelled call and a timed-out call are not "git is unavailable":
		// cancellation (Ctrl+C, EC-5) is passed back as such, a timeout gets
		// its own code, and only a real git failure is "unavailable".
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
			return "", codeError(brunelexec.ErrToolTimeout.Code, "git diff timed out", commandCtx.Err())
		}
		return "", codeError(ErrWorkspaceDiffUnavailable.Code, "git diff is unavailable for this workspace", err)
	}
	return string(output), nil
}
