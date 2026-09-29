package completion

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// WriteFile writes report as JSON to path without ever leaving a partial
// file at path: the JSON goes to a temporary file in the same directory,
// is flushed, and is then renamed over path (spec.md §9 CT-8, EC-5).
//
// This is the minimal writer the CLI's --report flag needs (issue #2); the
// full CT-8 contract (report path inside the workspace, writable parent,
// failure-case facts) is issue #14's scope and hardens this function.
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
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
