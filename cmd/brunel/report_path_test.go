//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The report target is checked before the run starts: finding out after a
// long task that the report cannot be written would waste the whole run.
func TestReportPathProblemsFailBeforeTheRunStarts(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "out.json")
	cases := map[string]struct {
		prepare func(h *harness) string
		code    string
	}{
		"existing file": {func(h *harness) string {
			p := filepath.Join(h.root, "out.json")
			_ = os.WriteFile(p, []byte("earlier"), 0o600)
			return p
		}, "E_FILE_EXISTS"},
		"outside workspace": {func(h *harness) string { return outside }, "E_PATH_ESCAPE"},
		"parent escape":     {func(h *harness) string { return `..\out.json` }, "E_PATH_ESCAPE"},
		"missing parent":    {func(h *harness) string { return `nope\out.json` }, "E_REPORT_WRITE"},
	}
	for name, c := range cases {
		h := newHarness(t, terminals{}, "")
		path := c.prepare(h)
		code := runCLI([]string{"fix the bug", "--report", path, "--model", "anthropic/claude-x"}, h.env)
		if code != exitFailed {
			t.Errorf("%s: exit = %d, want %d", name, code, exitFailed)
		}
		if !strings.Contains(h.stderr.String(), c.code) {
			t.Errorf("%s: stderr %q lacks %s", name, h.stderr.String(), c.code)
		}
		if h.built != 0 || h.agent.ran {
			t.Errorf("%s: the run started despite an unusable report path", name)
		}
	}
}

// A relative --report is relative to the workspace root, as in the spec's
// own example `brunel "<task>" --report out.json`.
func TestRelativeReportPathIsInsideWorkspace(t *testing.T) {
	h := newHarness(t, terminals{}, "")
	code := runCLI([]string{"fix the bug", "--report", "out.json", "--model", "anthropic/claude-x"}, h.env)
	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %s", code, h.stderr.String())
	}
	if _, err := os.Stat(filepath.Join(h.root, "out.json")); err != nil {
		t.Fatalf("report not written inside the workspace: %v", err)
	}
}
