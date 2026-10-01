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
		r, err := filepath.Rel(root, path)
		if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return "", codedError{workspace.ErrPathEscape.Code, "--report path is outside the workspace"}
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
