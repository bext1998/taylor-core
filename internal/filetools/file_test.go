package filetools

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadFileReturnsWholeFileHashAndRequestedRange(t *testing.T) {
	dir := t.TempDir()
	content := "one\ntwo\nthree\n"
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	r := fakeResolver{root: dir}

	full, err := ReadFile(r, "a.txt", 0, 0)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got := hashBytes([]byte(content)); full.Hash != got {
		t.Fatalf("Hash = %q, want %q", full.Hash, got)
	}
	if len(full.Lines) != 3 || full.Lines[0].Text != "one" || full.Lines[2].Text != "three" {
		t.Fatalf("unexpected full read: %+v", full.Lines)
	}

	partial, err := ReadFile(r, "a.txt", 2, 2)
	if err != nil {
		t.Fatalf("ReadFile(range) error = %v", err)
	}
	if partial.Hash != full.Hash {
		t.Fatalf("ranged read hash = %q, want whole-file hash %q", partial.Hash, full.Hash)
	}
	if len(partial.Lines) != 1 || partial.Lines[0].Number != 2 || partial.Lines[0].Text != "two" {
		t.Fatalf("unexpected ranged read: %+v", partial.Lines)
	}
}

func TestReadFileMissingReturnsNotFound(t *testing.T) {
	r := fakeResolver{root: t.TempDir()}
	if _, err := ReadFile(r, "missing.txt", 0, 0); ErrorCode(err) != ErrNotFound.Code {
		t.Fatalf("ErrorCode() = %q, want %q (err=%v)", ErrorCode(err), ErrNotFound.Code, err)
	}
}

func TestReadFileRejectsInvertedRange(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := fakeResolver{root: dir}
	if _, err := ReadFile(r, "a.txt", 5, 2); ErrorCode(err) != ErrInvalidArgument.Code {
		t.Fatalf("ErrorCode() = %q, want %q (err=%v)", ErrorCode(err), ErrInvalidArgument.Code, err)
	}
}

func TestCreateFileFailsWhenTargetExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := fakeResolver{root: dir}

	if _, err := CreateFile(r, "a.txt", "new"); ErrorCode(err) != ErrFileExists.Code {
		t.Fatalf("ErrorCode() = %q, want %q (err=%v)", ErrorCode(err), ErrFileExists.Code, err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "old" {
		t.Fatalf("existing file changed after failed create_file: %q", content)
	}
}

// installNewFile is the final no-overwrite operation after CreateFile has
// staged content. Exercise it directly so this test covers a file created
// after CreateFile's initial Lstat check.
func TestInstallNewFileDoesNotOverwriteExistingTarget(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "staged.txt")
	target := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(source, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := installNewFile(source, target)
	if !isAlreadyExists(err) {
		t.Fatalf("installNewFile() error = %v, want destination-exists error", err)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "old" {
		t.Fatalf("existing file changed after failed install: %q", content)
	}
}

func TestCreateFileWritesNewFileAndReturnsMatchingHash(t *testing.T) {
	dir := t.TempDir()
	r := fakeResolver{root: dir}

	hash, err := CreateFile(r, "new.txt", "hello")
	if err != nil {
		t.Fatalf("CreateFile() error = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "new.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello" {
		t.Fatalf("content = %q, want %q", content, "hello")
	}
	if hash != hashBytes([]byte("hello")) {
		t.Fatalf("hash = %q, does not match written content", hash)
	}
	if _, err := hex.DecodeString(hash); err != nil {
		t.Fatalf("hash %q is not hex: %v", hash, err)
	}
}

// CreateFile does not create intermediate directories - that is a concern
// for whichever tool call wires this package to a resolved workspace path
// (Issue #4), not for the hash/atomic-write layer itself.
func TestCreateFileFailsWhenParentDirectoryMissing(t *testing.T) {
	dir := t.TempDir()
	r := fakeResolver{root: dir}
	if _, err := CreateFile(r, "sub/new.txt", "hello"); err == nil {
		t.Fatal("CreateFile() unexpectedly succeeded with a missing parent directory")
	}
	if _, err := os.Stat(filepath.Join(dir, "sub")); !os.IsNotExist(err) {
		t.Fatalf("parent directory should not have been created, stat err = %v", err)
	}
}

func TestWriteFileRejectsStaleHashAndPreservesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := fakeResolver{root: dir}
	read, err := ReadFile(r, "a.txt", 0, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate an external modification between read and write.
	if err := os.WriteFile(path, []byte("changed externally"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := WriteFile(r, "a.txt", read.Hash, "overwrite"); ErrorCode(err) != ErrStaleHash.Code {
		t.Fatalf("ErrorCode() = %q, want %q (err=%v)", ErrorCode(err), ErrStaleHash.Code, err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "changed externally" {
		t.Fatalf("file changed after rejected stale write: %q", content)
	}
}

func TestWriteFileSucceedsWithMatchingHash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := fakeResolver{root: dir}
	read, err := ReadFile(r, "a.txt", 0, 0)
	if err != nil {
		t.Fatal(err)
	}

	newHash, err := WriteFile(r, "a.txt", read.Hash, "replaced")
	if err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "replaced" {
		t.Fatalf("content = %q, want %q", content, "replaced")
	}
	if newHash != hashBytes([]byte("replaced")) {
		t.Fatalf("newHash = %q, does not match written content", newHash)
	}

	// The returned hash must be usable as expected_hash for the next write.
	if _, err := WriteFile(r, "a.txt", newHash, "replaced again"); err != nil {
		t.Fatalf("second WriteFile() with returned hash failed: %v", err)
	}
}

func TestWriteFileRejectsEmptyExpectedHash(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := fakeResolver{root: dir}
	if _, err := WriteFile(r, "a.txt", "", "y"); ErrorCode(err) != ErrInvalidArgument.Code {
		t.Fatalf("ErrorCode() = %q, want %q (err=%v)", ErrorCode(err), ErrInvalidArgument.Code, err)
	}
}

func TestWriteFileMissingTargetReturnsNotFound(t *testing.T) {
	r := fakeResolver{root: t.TempDir()}
	if _, err := WriteFile(r, "missing.txt", "deadbeef", "content"); ErrorCode(err) != ErrNotFound.Code {
		t.Fatalf("ErrorCode() = %q, want %q (err=%v)", ErrorCode(err), ErrNotFound.Code, err)
	}
}

// CreateFile stages content in a temporary file and installs it
// atomically; a failed create must not leave any temporary file behind in
// the target directory.
func TestCreateFileLeavesNoTempFileBehind(t *testing.T) {
	dir := t.TempDir()
	r := fakeResolver{root: dir}
	if _, err := CreateFile(r, "new.txt", "hello"); err != nil {
		t.Fatalf("CreateFile() error = %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "new.txt" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("directory should contain only new.txt, got %v", names)
	}
}

// An external lock must cause the public WriteFile API to fail rather than
// wait without limit. Windows byte-range locks can reject the outer read
// before WriteFile reaches lockTargetForReplace; the direct test below
// covers that retry path on every platform.
func TestWriteFileReturnsBoundedErrorWhenTargetIsLocked(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := fakeResolver{root: dir}
	read, err := ReadFile(r, "a.txt", 0, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Take the same exclusive lock the replace path uses, then attempt a
	// WriteFile: while the target is externally locked, WriteFile must
	// not be able to verify-and-swap the file - it either blocks on the
	// lock or fails without touching the file. It must never silently
	// replace content it could not verify.
	locked, err := openLockable(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := lockFileExclusive(locked); err != nil {
		_ = locked.Close()
		t.Fatal(err)
	}
	release := func() {
		if locked == nil {
			return
		}
		_ = unlockFile(locked)
		_ = locked.Close()
		locked = nil
	}
	defer release()

	started := time.Now()
	_, err = WriteFile(r, "a.txt", read.Hash, "replaced")
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("WriteFile() waited %s while target was locked, want bounded failure", elapsed)
	}
	if ErrorCode(err) != ErrFileIO.Code {
		t.Fatalf("ErrorCode() = %q, want %q (err=%v)", ErrorCode(err), ErrFileIO.Code, err)
	}
	release()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "original" {
		t.Fatalf("file was replaced despite external lock: %q", content)
	}
	if _, err := WriteFile(r, "a.txt", read.Hash, "replaced"); err != nil {
		t.Fatalf("WriteFile() error after unlock = %v", err)
	}
	content, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "replaced" {
		t.Fatalf("content = %q, want %q", content, "replaced")
	}
}

func TestLockTargetForReplaceReturnsBoundedErrorWhenLocked(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	held, err := openLockable(path)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := lockFileExclusive(held); err != nil {
		t.Fatal(err)
	}
	heldLocked := true
	defer func() {
		if heldLocked {
			_ = unlockFile(held)
		}
	}()

	contender, err := openLockable(path)
	if err != nil {
		t.Fatal(err)
	}
	defer contender.Close()

	started := time.Now()
	err = lockTargetForReplace(contender)
	elapsed := time.Since(started)
	if !isLockUnavailable(err) {
		t.Fatalf("lockTargetForReplace() error = %v, want lock-unavailable error", err)
	}
	if elapsed < lockRetryWindow || elapsed > time.Second {
		t.Fatalf("lockTargetForReplace() waited %s, want [%s, 1s]", elapsed, lockRetryWindow)
	}

	if err := unlockFile(held); err != nil {
		t.Fatal(err)
	}
	heldLocked = false
	if err := lockTargetForReplace(contender); err != nil {
		t.Fatalf("lockTargetForReplace() after unlock error = %v", err)
	}
	defer unlockFile(contender)
}

// A stale-hash rejection must happen inside the lock and must not replace
// the file even when the external modification races with the write.
func TestWriteFileStaleDetectionUnderLockPreservesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := fakeResolver{root: dir}
	read, err := ReadFile(r, "a.txt", 0, 0)
	if err != nil {
		t.Fatal(err)
	}

	// External modification after the read: the locked re-check must
	// catch it and leave the new version intact.
	if err := os.WriteFile(path, []byte("changed externally"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteFile(r, "a.txt", read.Hash, "overwrite"); ErrorCode(err) != ErrStaleHash.Code {
		t.Fatalf("ErrorCode() = %q, want %q (err=%v)", ErrorCode(err), ErrStaleHash.Code, err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "changed externally" {
		t.Fatalf("file changed after rejected stale write: %q", content)
	}
}

// The missing parent is reported as "not found" with its own message, not as a
// generic I/O failure the model cannot act on (#69 item 11).
func TestCreateFileMissingParentIsReportedAsNotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := CreateFile(fakeResolver{root: dir}, "sub/new.txt", "hello")
	if ErrorCode(err) != ErrNotFound.Code {
		t.Fatalf("CreateFile() code = %q, want %q (err = %v)", ErrorCode(err), ErrNotFound.Code, err)
	}
}
