package completion

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileReplacesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep := &Report{SchemaVersion: SchemaVersion, Task: "t", Status: StatusCompleted}
	if err := WriteFile(path, rep); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got Report
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("report is not valid JSON: %v\n%s", err, data)
	}
	if got.Status != StatusCompleted || got.SchemaVersion != SchemaVersion {
		t.Fatalf("unexpected report: %#v", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}

func TestWriteFileMissingParentLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing", "report.json")
	if err := WriteFile(path, &Report{SchemaVersion: SchemaVersion}); err == nil {
		t.Fatal("WriteFile into a missing directory succeeded")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("partial report exists: %v", err)
	}
}
