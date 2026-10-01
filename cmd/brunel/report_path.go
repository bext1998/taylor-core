package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bext1998/brunel/internal/workspace"
)

// resolveReportPath turns --report into the path the report is written to
// and checks, before the run starts, everything the write will need
// (spec.md §8 / CT-8): the path is inside the workspace, its parent already
// exists, and nothing is there yet. A relative path is relative to the
// workspace root; an absolute one must lie inside it. The existence check
// is repeated atomically by completion.WriteFile, so a file that appears
// later is still not overwritten.
func resolveReportPath(bound *workspace.Workspace, root, path string) (string, error) {
	rel := path
	if filepath.IsAbs(path) {
		r, err := relativeToRoot(root, path)
		if err != nil {
			return "", err
		}
		rel = r
	}
	final, err := bound.Resolve(rel)
	if err != nil {
		return "", codedError{workspace.ErrorCode(err), "--report path is outside the workspace"}
	}
	if info, err := os.Stat(filepath.Dir(final)); err != nil || !info.IsDir() {
		return "", codedError{"E_REPORT_WRITE", "the --report directory does not exist"}
	}
	if _, err := os.Lstat(final); err == nil {
		return "", codedError{"E_FILE_EXISTS", "the --report file already exists; it is not overwritten"}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", codedError{"E_REPORT_WRITE", "cannot inspect the --report path"}
	}
	return final, nil
}

// relativeToRoot expresses an absolute --report path relative to the
// workspace root. Both sides are first resolved to their real location: the
// root has been (workspace.Bind), and the same directory can be reached as
// a symlink or an 8.3 short name, which a plain string comparison would take
// for a different place. The target itself may not exist yet, so only its
// parent directory is resolved.
func relativeToRoot(root, path string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", codedError{workspace.ErrWorkspaceInvalid.Code, "cannot resolve the workspace root"}
	}
	realParent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", codedError{"E_REPORT_WRITE", "the --report directory does not exist"}
	}
	rel, err := filepath.Rel(realRoot, filepath.Join(realParent, filepath.Base(path)))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", codedError{workspace.ErrPathEscape.Code, "--report path is outside the workspace"}
	}
	return rel, nil
}
