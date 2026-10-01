package completion

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// WriteFile writes report as JSON to path without ever leaving a partial
// file at path: the JSON goes to a temporary file in the same directory, is
// flushed, and is then linked into place (spec.md §9 CT-8, EC-5).
//
// It never replaces an existing file: until OQ-3 is ruled on, the
// pre-ruling behaviour is to refuse (spec.md §16), reported as an error
// wrapping fs.ErrExist. Linking, unlike renaming, fails atomically when the
// target already exists, so a file created after the caller's own check is
// still not overwritten. The caller is responsible for the path being inside
// the workspace.
func WriteFile(path string, report *Report) error {
	if report == nil {
		return errors.New("completion: nil report")
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), ".brunel-report-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	linkErr := os.Link(tmpName, path)
	_ = os.Remove(tmpName)
	return linkErr
}
