package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSessionLifecycleAndResume(t *testing.T) {
	store := newTestStore(t)
	name := "same-name"

	first, err := store.Create(CreateOptions{
		Name:          &name,
		WorkspaceRoot: t.TempDir(),
		Mode:          "workspace",
		ModelID:       "model-a",
		IsGitRepo:     false,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !isULID(first.Metadata().ID) {
		t.Fatalf("session id %q is not a ULID", first.Metadata().ID)
	}
	if _, err := os.Stat(filepath.Join(first.Dir(), "snapshot")); err != nil {
		t.Fatalf("non-git session must have snapshot directory: %v", err)
	}
	if _, err := first.AppendEvent(EvUserInstruction, map[string]string{"text": "hello"}, 0, "user"); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	if err := first.SaveSummary(Summary{Goal: "goal", Pending: []string{"next"}}); err != nil {
		t.Fatalf("SaveSummary() error = %v", err)
	}
	if err := first.Close(ExitStatusAborted); err != nil {
		t.Fatalf("Close(aborted) error = %v", err)
	}

	resumed, err := store.Resume(first.Metadata().ID)
	if err != nil {
		t.Fatalf("Resume(id) error = %v", err)
	}
	if resumed.Metadata().ID != first.Metadata().ID || resumed.Metadata().Name == nil || *resumed.Metadata().Name != name {
		t.Fatalf("resumed metadata mismatch: %#v", resumed.Metadata())
	}
	events, err := resumed.ReadEvents()
	if err != nil || len(events.Events) != 1 || events.Events[0].Seq != 1 {
		t.Fatalf("ReadEvents() = %#v, %v", events, err)
	}
	if events.Events[0].Payload == nil || strings.Contains(string(events.Events[0].Payload), "Authorization") {
		t.Fatalf("unexpected event payload: %s", events.Events[0].Payload)
	}
	if err := resumed.Close(ExitStatusAborted); err != nil {
		t.Fatalf("Close(resumed) error = %v", err)
	}
	byName, err := store.Resume(name)
	if err != nil {
		t.Fatalf("Resume(name) error = %v", err)
	}
	if err := byName.Close(ExitStatusAborted); err != nil {
		t.Fatalf("Close(byName) error = %v", err)
	}
}

func TestResumeRunningSessionReturnsBusy(t *testing.T) {
	store := newTestStore(t)
	s, err := store.Create(CreateOptions{WorkspaceRoot: t.TempDir(), Mode: ModeWorkspace})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Resume(s.Metadata().ID); !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("Resume(running) error = %v, want E_SESSION_BUSY", err)
	}
	if err := s.Close(ExitStatusAborted); err != nil {
		t.Fatal(err)
	}
}

func TestULIDFormatAndUniqueness(t *testing.T) {
	seen := make(map[string]struct{}, 128)
	for i := 0; i < 128; i++ {
		id, err := newULID(time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if !isULID(id) {
			t.Fatalf("generated invalid ULID %q", id)
		}
		if _, exists := seen[id]; exists {
			t.Fatalf("generated duplicate ULID %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestSessionSameNamesAndResumeErrors(t *testing.T) {
	store := newTestStore(t)
	name := "duplicate"
	for i := 0; i < 2; i++ {
		s, err := store.Create(CreateOptions{Name: &name, WorkspaceRoot: t.TempDir(), Mode: "workspace"})
		if err != nil {
			t.Fatalf("Create(%d) error = %v", i, err)
		}
		if err := s.Close(ExitStatusAborted); err != nil {
			t.Fatalf("Close(%d) error = %v", i, err)
		}
	}
	if _, err := store.Resume(name); !errors.Is(err, ErrSessionAmbiguous) {
		t.Fatalf("Resume(duplicate) error = %v, want E_SESSION_AMBIGUOUS", err)
	}
	if _, err := store.Resume("01ARZ3NDEKTSV4RRFFQ69G5FAV"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("Resume(missing id) error = %v, want E_SESSION_NOT_FOUND", err)
	}
	if _, err := store.Resume("missing"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("Resume(missing name) error = %v, want E_SESSION_NOT_FOUND", err)
	}
}

func TestResumeRejectsMetadataIDDirectoryMismatch(t *testing.T) {
	store := newTestStore(t)
	name := "tampered"
	first, err := store.Create(CreateOptions{Name: &name, WorkspaceRoot: t.TempDir(), Mode: ModeWorkspace})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Create(CreateOptions{WorkspaceRoot: t.TempDir(), Mode: ModeWorkspace})
	if err != nil {
		t.Fatal(err)
	}
	meta := first.Metadata()
	meta.ID = second.Metadata().ID
	if err := writeJSONAtomic(filepath.Join(first.Dir(), "meta.json"), meta); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Resume(first.Metadata().ID); !errors.Is(err, ErrSessionCorrupt) {
		t.Fatalf("Resume(id) error = %v, want E_SESSION_CORRUPT", err)
	}
	if _, err := store.Resume(name); !errors.Is(err, ErrSessionCorrupt) {
		t.Fatalf("Resume(name) error = %v, want E_SESSION_CORRUPT", err)
	}
}

func TestSessionAppendOnlyAndSecretMask(t *testing.T) {
	store := newTestStore(t)
	s, err := store.Create(CreateOptions{WorkspaceRoot: t.TempDir(), Mode: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Dir(), "events.jsonl")
	if _, err := s.AppendEvent(EvToolResult, map[string]string{
		"authorization": "Bearer super-secret-token",
		"env":           "OPENROUTER_API_KEY=sk-or-v1-secret-value",
	}, 0, "tool_output"); err != nil {
		t.Fatal(err)
	}
	prefix, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(prefix), "super-secret-token") || strings.Contains(string(prefix), "sk-or-v1-secret-value") {
		t.Fatalf("known secret was persisted: %s", prefix)
	}
	if _, err := s.AppendEvent(EvAssistantText, "second", 0, "assistant"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(after), string(prefix)) {
		t.Fatal("events.jsonl prefix was rewritten")
	}
}

func TestSessionMetadataConcurrentWithAppendEvent(t *testing.T) {
	store := newTestStore(t)
	s, err := store.Create(CreateOptions{WorkspaceRoot: t.TempDir(), Mode: ModeWorkspace})
	if err != nil {
		t.Fatal(err)
	}

	const appendCount = 100
	var wg sync.WaitGroup
	errs := make(chan error, appendCount)
	for i := 0; i < appendCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.AppendEvent(EvToolResult, map[string]string{"value": "test"}, 0, "test")
			errs <- err
		}()
	}
	for i := 0; i < appendCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.Metadata()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("AppendEvent() error = %v", err)
		}
	}
	result, err := s.ReadEvents()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Events) != appendCount {
		t.Fatalf("event count = %d, want %d", len(result.Events), appendCount)
	}
}

func TestUnnamedCleanExitDeletesAndAbortedRetains(t *testing.T) {
	store := newTestStore(t)
	clean, err := store.Create(CreateOptions{WorkspaceRoot: t.TempDir(), Mode: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	cleanDir := clean.Dir()
	if err := clean.Close(ExitStatusClean); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cleanDir); !os.IsNotExist(err) {
		t.Fatalf("unnamed clean session still exists, stat error = %v", err)
	}

	aborted, err := store.Create(CreateOptions{WorkspaceRoot: t.TempDir(), Mode: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	abortedDir := aborted.Dir()
	if err := aborted.Close(ExitStatusAborted); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(abortedDir); err != nil {
		t.Fatalf("unnamed aborted session was not retained: %v", err)
	}
}

func TestNamedCleanExitPersistsAndUpdatesStatus(t *testing.T) {
	store := newTestStore(t)
	name := "keep-me"
	s, err := store.Create(CreateOptions{Name: &name, WorkspaceRoot: t.TempDir(), Mode: ModeReadonly, IsGitRepo: true})
	if err != nil {
		t.Fatal(err)
	}
	dir := s.Dir()
	if _, err := os.Stat(filepath.Join(dir, "snapshot")); !os.IsNotExist(err) {
		t.Fatalf("git session should not create snapshot directory, stat error = %v", err)
	}
	if err := s.Close(ExitStatusClean); err != nil {
		t.Fatal(err)
	}
	resumed, err := store.Resume(name)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Metadata().ExitStatus != ExitStatusClean {
		t.Fatalf("exit status = %q, want clean", resumed.Metadata().ExitStatus)
	}
}

func TestEventsTrailingFragmentIsNotRewritten(t *testing.T) {
	store := newTestStore(t)
	s, err := store.Create(CreateOptions{WorkspaceRoot: t.TempDir(), Mode: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Dir(), "events.jsonl")
	if err := os.WriteFile(path, []byte(`{"seq":1,"ts":"2026-01-01T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ExitStatusAborted); err != nil {
		t.Fatal(err)
	}
	resumed, err := store.Resume(s.Metadata().ID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := resumed.ReadEvents()
	if err != nil {
		t.Fatal(err)
	}
	if !result.TrailingFragment {
		t.Fatal("expected trailing fragment warning")
	}
	original, _ := os.ReadFile(path)
	if _, err := resumed.AppendEvent(EvError, "must not append", 0, "system"); !errors.Is(err, ErrEventLogTail) {
		t.Fatalf("AppendEvent() error = %v, want E_EVENT_LOG_TAIL", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatal("trailing event fragment was rewritten")
	}
	if err := resumed.Close(ExitStatusAborted); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupExpiredUnnamedAborted(t *testing.T) {
	store := newTestStore(t)
	s, err := store.Create(CreateOptions{WorkspaceRoot: t.TempDir(), Mode: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ExitStatusAborted); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-73 * time.Hour)
	meta := s.Metadata()
	meta.UpdatedAt = old
	if err := writeJSONAtomic(filepath.Join(s.Dir(), "meta.json"), meta); err != nil {
		t.Fatal(err)
	}
	removed, err := store.CleanupExpired(time.Now())
	if err != nil || removed != 1 {
		t.Fatalf("CleanupExpired() = %d, %v", removed, err)
	}
}

func TestCleanupExpiredRunningOnlyWhenOwnerLockIsFree(t *testing.T) {
	store := newTestStore(t)
	active, err := store.Create(CreateOptions{WorkspaceRoot: t.TempDir(), Mode: ModeWorkspace})
	if err != nil {
		t.Fatal(err)
	}
	activeMeta := active.Metadata()
	activeMeta.UpdatedAt = time.Now().Add(-73 * time.Hour)
	if err := writeJSONAtomic(filepath.Join(active.Dir(), "meta.json"), activeMeta); err != nil {
		t.Fatal(err)
	}
	removed, err := store.CleanupExpired(time.Now())
	if err != nil || removed != 0 {
		t.Fatalf("CleanupExpired(active) = %d, %v", removed, err)
	}
	if err := active.lock.Close(); err != nil {
		t.Fatal(err)
	}
	removed, err = store.CleanupExpired(time.Now())
	if err != nil || removed != 1 {
		t.Fatalf("CleanupExpired(orphan) = %d, %v", removed, err)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	return store
}

// AC-14: after the session is summarized, the original events can still be
// found and the bytes already in events.jsonl are untouched.
func TestSummarizingDoesNotChangeOrLoseTheOriginalEvents(t *testing.T) {
	store := newTestStore(t)
	s, err := store.Create(CreateOptions{WorkspaceRoot: t.TempDir(), Mode: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Dir(), "events.jsonl")
	if _, err := s.AppendEvent(EvUserInstruction, "fix the bug", 0, "user"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendEvent(EvAssistantText, "done", 0, "assistant"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.SaveSummary(Summary{Goal: "fix the bug", Pending: []string{"none"}}); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("saving a summary changed events.jsonl")
	}
	read, err := s.ReadEvents()
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Events) != 2 || read.Events[0].Seq != 1 || read.Events[0].Kind != EvUserInstruction ||
		!strings.Contains(string(read.Events[0].Payload), "fix the bug") || read.Events[1].Kind != EvAssistantText {
		t.Fatalf("original events were not recoverable after summarizing: %+v", read.Events)
	}
}
