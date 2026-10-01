package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- #69 item 8: a missing path is "not found", not a generic I/O failure ---

func TestListFilesAndSearchTextReportAMissingDirectoryAsNotFound(t *testing.T) {
	root := t.TempDir()
	r := fakeResolver{root: root}
	if _, err := listFiles(r, "nope", nil, nil); ErrorCode(err) != "E_FILE_NOT_FOUND" {
		t.Errorf("listFiles(missing) code = %q, want E_FILE_NOT_FOUND (err = %v)", ErrorCode(err), err)
	}
	if _, err := searchText(r, "x", "nope", nil, nil); ErrorCode(err) != "E_FILE_NOT_FOUND" {
		t.Errorf("searchText(missing) code = %q, want E_FILE_NOT_FOUND (err = %v)", ErrorCode(err), err)
	}
}

// --- #69 items 4 and 5: search limits ---

func TestSearchTextClampsMaxResults(t *testing.T) {
	root := t.TempDir()
	var b strings.Builder
	for i := 0; i < maxSearchResults+500; i++ {
		b.WriteString("needle\n")
	}
	writeTestFile(t, filepath.Join(root, "many.txt"), b.String())
	huge := 1_000_000
	matches, err := searchText(fakeResolver{root: root}, "needle", ".", nil, &huge)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != maxSearchResults {
		t.Fatalf("got %d matches for max_results=%d, want the hard limit %d", len(matches), huge, maxSearchResults)
	}
}

// A file larger than the read limit is skipped instead of being read whole
// into memory; ordinary files next to it are still searched.
func TestSearchTextSkipsOversizedFiles(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "small.txt"), "needle small\n")
	big := strings.Repeat("x", maxSearchFileBytes+1) + "\nneedle big\n"
	writeTestFile(t, filepath.Join(root, "big.txt"), big)
	matches, err := searchText(fakeResolver{root: root}, "needle", ".", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Path != "small.txt" {
		t.Fatalf("matches = %#v, want only small.txt", matches)
	}
}

// --- #69 item 6: max_depth 0 is "no limit", the same as omitting it ---

func TestListFilesMaxDepthZeroMeansUnlimited(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "a", "b", "c.txt"), "x")
	zero := 0
	entries, err := listFiles(fakeResolver{root: root}, ".", nil, &zero)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Path != "a/b/c.txt" {
		t.Fatalf("entries = %#v, want the deep file listed", entries)
	}
}

// --- #69 item 7: a null inside a patch's line arrays is rejected, not coerced ---

func TestApplyPatchRejectsNullLineElements(t *testing.T) {
	for _, params := range []string{
		`{"path":"a.txt","expected_hash":"h","hunks":[{"start_line":1,"end_line":1,"old_lines":[null],"new_lines":["x"]}]}`,
		`{"path":"a.txt","expected_hash":"h","hunks":[{"start_line":1,"end_line":1,"old_lines":["x"],"new_lines":["a",null]}]}`,
	} {
		var p ApplyPatchParams
		if err := decodeApplyPatchParams(json.RawMessage(params), &p); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("decodeApplyPatchParams(%s) error = %v, want E_INVALID_ARGUMENT", params, err)
		}
	}
	var ok ApplyPatchParams
	good := `{"path":"a.txt","expected_hash":"h","hunks":[{"start_line":1,"end_line":1,"old_lines":["x"],"new_lines":[""]}]}`
	if err := decodeApplyPatchParams(json.RawMessage(good), &ok); err != nil {
		t.Fatalf("a legitimate empty-string line was rejected: %v", err)
	}
}

// --- #69 items 1 and 2: workspace_diff ---

func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required: ", err)
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestWorkspaceDiffSeparatesCancellationAndTimeoutFromGitFailure(t *testing.T) {
	dir := gitRepo(t)
	r := fakeResolver{root: dir}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := workspaceDiff(ctx, r, dir, ""); !errors.Is(err, context.Canceled) || ErrorCode(err) == ErrWorkspaceDiffUnavailable.Code {
		t.Errorf("cancelled diff error = %v (code %q), want context.Canceled and not E_WORKSPACE_DIFF_UNAVAILABLE", err, ErrorCode(err))
	}

	old := workspaceDiffTimeout
	workspaceDiffTimeout = time.Nanosecond
	defer func() { workspaceDiffTimeout = old }()
	if _, err := workspaceDiff(context.Background(), r, dir, ""); ErrorCode(err) != "E_TOOL_TIMEOUT" {
		t.Errorf("timed-out diff code = %q, want E_TOOL_TIMEOUT (err = %v)", ErrorCode(err), err)
	}
	workspaceDiffTimeout = old

	// A directory that is not a git work tree is the genuine "unavailable".
	notGit := t.TempDir()
	if _, err := workspaceDiff(context.Background(), fakeResolver{root: notGit}, notGit, ""); ErrorCode(err) != ErrWorkspaceDiffUnavailable.Code {
		t.Errorf("non-git diff code = %q, want %q", ErrorCode(err), ErrWorkspaceDiffUnavailable.Code)
	}
}

// git's own warnings (here the line-ending notice) go to stderr; they must not
// end up inside the diff text the model reads.
func TestWorkspaceDiffOutputExcludesStderr(t *testing.T) {
	dir := gitRepo(t)
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("config", "core.autocrlf", "true")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "init")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := workspaceDiff(context.Background(), fakeResolver{root: dir}, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "warning:") || !strings.Contains(out, "+two") {
		t.Fatalf("diff text = %q: it must hold the diff and none of git's stderr", out)
	}
}

// readTextFile's own limit (a file that grew after the size check) must hold
// exactly: the budget includes the first block already read, and an
// over-limit file is skipped rather than searched truncated (#69 item 5).
func TestReadTextFileHonoursTheExactByteBudget(t *testing.T) {
	dir := t.TempDir()
	atLimit := filepath.Join(dir, "at.txt")
	over := filepath.Join(dir, "over.txt")
	writeTestFile(t, atLimit, strings.Repeat("a", maxSearchFileBytes))
	writeTestFile(t, over, strings.Repeat("a", maxSearchFileBytes+1))

	if data, skipped, err := readTextFile(atLimit); err != nil || skipped || len(data) != maxSearchFileBytes {
		t.Fatalf("file of exactly the limit: len=%d skipped=%v err=%v", len(data), skipped, err)
	}
	if data, skipped, err := readTextFile(over); err != nil || !skipped || data != nil {
		t.Fatalf("file one byte over the limit: len=%d skipped=%v err=%v, want it skipped", len(data), skipped, err)
	}
}
