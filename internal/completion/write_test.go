package completion

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestWriteFileCreatesCompleteJSONAndLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
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

// Until OQ-3 is ruled on, an existing file is never overwritten (spec §16):
// the user's earlier report must survive, and no temp file may be left.
func TestWriteFileNeverOverwritesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	if err := os.WriteFile(path, []byte("earlier report"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := WriteFile(path, &Report{SchemaVersion: SchemaVersion, Status: StatusCompleted})
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("WriteFile over an existing file: error = %v, want fs.ErrExist", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "earlier report" {
		t.Fatalf("existing report was changed: %q", data)
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

// EC-5 (the safety property behind "cancel while the report is written"): a
// report must never be visible half-written. It is staged in a temporary file
// and published in one step, so a sampled reader polling the target while a
// large report is written may only ever see the file absent or complete -
// never a prefix of it, and never empty. This does not interrupt the writer;
// it checks the published state, not cancellation propagation (#55).
func TestWriteFileNeverExposesAPartialReport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	want := strings.Repeat("x", 8<<20)
	rep := &Report{SchemaVersion: SchemaVersion, Status: StatusCompleted, Diff: want}

	var violated atomic.Bool
	var badLen, samples atomic.Int64
	started := make(chan struct{})
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		first := true
		for {
			select {
			case <-stop:
				return
			default:
			}
			if data, err := os.ReadFile(path); err == nil {
				samples.Add(1)
				if !json.Valid(data) { // includes an empty file
					violated.Store(true)
					badLen.Store(int64(len(data)))
				}
			}
			if first {
				first = false
				close(started)
			}
		}
	}()
	<-started // the reader is polling before the write begins
	err := WriteFile(path, rep)
	close(stop)
	<-done

	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if violated.Load() {
		t.Fatalf("a reader saw an invalid report of %d bytes at the target path", badLen.Load())
	}
	t.Logf("reader took %d samples of the target while the report was written", samples.Load())
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got Report
	if err := json.Unmarshal(data, &got); err != nil || got.Diff != want {
		t.Fatalf("final report is incomplete or invalid: err=%v, diff bytes=%d, want %d", err, len(got.Diff), len(want))
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}
