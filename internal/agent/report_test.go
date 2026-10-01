package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bext1998/brunel/internal/completion"
	"github.com/bext1998/brunel/internal/pirpc"
)

func toolStart(id, name, args string) pirpc.Event {
	return pirpc.Event{Type: "tool_execution_start", ToolCallID: id, ExecName: name, ToolArgs: json.RawMessage(args)}
}

func toolEnd(id, name, details string) pirpc.Event {
	return pirpc.Event{Type: "tool_execution_end", ToolCallID: id, ExecName: name, ToolDetails: json.RawMessage(details)}
}

func toolFail(id, name, text string) pirpc.Event {
	return pirpc.Event{Type: "tool_execution_end", ToolCallID: id, ExecName: name, IsError: true, ToolErrorText: text}
}

// finish wraps tool events in a protocol-faithful run that ends normally.
func finish(tool ...pirpc.Event) []pirpc.Event {
	events := []pirpc.Event{
		{Type: "response", Response: true, Success: true, Command: "prompt"},
		{Type: "turn_start"},
	}
	events = append(events, tool...)
	return append(events,
		pirpc.Event{Type: "message_end", StopReason: "stop"},
		pirpc.Event{Type: "agent_settled"},
	)
}

func runWith(t *testing.T, root string, events []pirpc.Event, setup func(*Runtime)) (*completion.Report, error) {
	t.Helper()
	r := NewRuntime(pirpc.LaunchOptions{Provider: "openrouter", Model: "m"}, pirpc.Credential{}, newTestSession(t), root, "workspace", "").
		withFakeStart(newFake(events, nil), nil)
	if setup != nil {
		setup(r)
	}
	return r.Run(context.Background(), "task", &recordingSink{})
}

// The report must carry what the tools actually did: files written, commands
// run with their exit codes, and calls that failed with the reason. A
// verification that exits non-zero is a recorded fact and does not by itself
// change the status (spec §8).
func TestReportRecordsToolFacts(t *testing.T) {
	events := finish(
		toolStart("1", "create_file", `{"path":"b.txt","content":"hi"}`),
		toolEnd("1", "create_file", `{"tool":"create_file","hash":{"hash":"x"}}`),
		toolStart("2", "run_powershell", `{"command":"go test ./..."}`),
		toolEnd("2", "run_powershell", `{"tool":"run_powershell","run":{"stdout":"","stderr":"FAIL pkg","exit_code":1,"truncated":false}}`),
		toolStart("3", "apply_patch", `{"path":"a.txt","expected_hash":"h","hunks":[]}`),
		toolEnd("3", "apply_patch", `{"tool":"apply_patch","hash":{"hash":"y"}}`),
		toolStart("4", "write_file", `{"path":"a.txt","expected_hash":"h","content":"x"}`),
		toolFail("4", "write_file", "E_FILE_HASH_MISMATCH: file changed"),
		toolStart("5", "write_file", `{"path":"c.txt","expected_hash":"h","content":"x"}`),
		toolFail("5", "write_file", "E_FILE_HASH_MISMATCH: file changed"),
	)
	rep, err := runWith(t, t.TempDir(), events, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if rep.Status != completion.StatusCompleted {
		t.Fatalf("Status = %q, want completed: a failing verification or tool call does not decide it", rep.Status)
	}
	if got := strings.Join(rep.ModifiedFiles, ","); got != "b.txt,a.txt" {
		t.Fatalf("ModifiedFiles = %q, want b.txt,a.txt (successful writes only, once each, in order)", got)
	}
	if len(rep.Verifications) != 1 || rep.Verifications[0].Command != "go test ./..." || rep.Verifications[0].ExitCode != 1 {
		t.Fatalf("Verifications = %+v, want the one run_powershell with exit 1", rep.Verifications)
	}
	if !strings.Contains(rep.Verifications[0].Summary, "FAIL pkg") {
		t.Fatalf("Summary = %q, want the stderr that explains the exit code", rep.Verifications[0].Summary)
	}
	if len(rep.ToolFailures) != 2 || rep.ToolFailures[0].Tool != "write_file" || !strings.Contains(rep.ToolFailures[0].Error, "E_FILE_HASH_MISMATCH") {
		t.Fatalf("ToolFailures = %+v, want both failed writes with their reason", rep.ToolFailures)
	}
}

// INV-8: a completed report only contains tool calls that reached a terminal
// state. A call that never ended must stop the run from being "completed".
func TestReportNotCompletedWhileToolCallIsUnfinished(t *testing.T) {
	events := finish(
		toolStart("1", "create_file", `{"path":"b.txt","content":"hi"}`),
		toolEnd("1", "create_file", `{"tool":"create_file","hash":{"hash":"x"}}`),
		toolStart("2", "run_powershell", `{"command":"npm test"}`),
	)
	rep, _ := runWith(t, t.TempDir(), events, nil)
	if rep.Status == completion.StatusCompleted {
		t.Fatal("report claims completed while a tool call never reached a terminal state")
	}
	if !strings.Contains(strings.Join(rep.RemainingRisks, "\n"), "run_powershell") {
		t.Fatalf("RemainingRisks = %q, want the unfinished call named", rep.RemainingRisks)
	}
	if len(rep.Verifications) != 0 {
		t.Fatalf("Verifications = %+v, an unfinished command has no exit code to record", rep.Verifications)
	}
}

// Declining approval is "incomplete" (spec §8) even if the model then carries
// on and ends normally.
func TestReportDeclinedApprovalIsIncomplete(t *testing.T) {
	for _, code := range []string{"E_APPROVAL_DENIED", "E_APPROVAL_REQUIRED_NO_TTY"} {
		events := finish(
			toolStart("1", "run_powershell", `{"command":"Remove-Item x"}`),
			toolFail("1", "run_powershell", code+": not approved"),
		)
		rep, _ := runWith(t, t.TempDir(), events, nil)
		if rep.Status != completion.StatusIncomplete {
			t.Errorf("%s: Status = %q, want incomplete", code, rep.Status)
		}
	}
}

// A command still waiting for approval when the run ends is a fact of the
// report and prevents "completed".
func TestReportPendingApproval(t *testing.T) {
	events := finish(toolStart("1", "run_powershell", `{"command":"Remove-Item x"}`))
	rep, _ := runWith(t, t.TempDir(), events, func(r *Runtime) {
		r.SetPendingApproval(func() *completion.ApprovalFact {
			return &completion.ApprovalFact{Command: "Remove-Item x", Reason: "deletes files"}
		})
	})
	if rep.PendingApproval == nil || rep.PendingApproval.Command != "Remove-Item x" || rep.PendingApproval.Reason != "deletes files" {
		t.Fatalf("PendingApproval = %+v", rep.PendingApproval)
	}
	if rep.Status == completion.StatusCompleted {
		t.Fatal("report claims completed with an approval still pending")
	}
}

func TestReportWithoutPendingApprovalOmitsIt(t *testing.T) {
	rep, _ := runWith(t, t.TempDir(), finish(), func(r *Runtime) {
		r.SetPendingApproval(func() *completion.ApprovalFact { return nil })
	})
	if rep.PendingApproval != nil {
		t.Fatalf("PendingApproval = %+v, want nil", rep.PendingApproval)
	}
}

// Commands and output can contain credentials the model was handed or
// invented; the report is written to disk and must not repeat them.
func TestReportMasksSecrets(t *testing.T) {
	const key = "sk-or-v1-0123456789abcdef0123456789abcdef"
	events := finish(
		toolStart("1", "run_powershell", `{"command":"curl -H 'Authorization: Bearer `+key+`' x"}`),
		toolEnd("1", "run_powershell", `{"tool":"run_powershell","run":{"stdout":"token `+key+`","stderr":"","exit_code":0,"truncated":false}}`),
		toolFail("2", "read_file", "E_X: bad "+key),
	)
	rep, _ := runWith(t, t.TempDir(), events, nil)
	data, _ := json.Marshal(rep)
	if strings.Contains(string(data), key) {
		t.Fatalf("report repeats the secret: %s", data)
	}
}

// JSON consumers index these arrays; null would break them (spec §8 types).
func TestReportArraysAreNeverNull(t *testing.T) {
	rep, _ := runWith(t, t.TempDir(), finish(), nil)
	data, _ := json.Marshal(rep)
	for _, field := range []string{"modified_files", "verifications", "tool_failures", "remaining_risks"} {
		if !strings.Contains(string(data), `"`+field+`":[]`) {
			t.Errorf("%s is not an empty array in %s", field, data)
		}
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "t@example.com")
	git(t, dir, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "init")
	return dir
}

// The diff is what the workspace now differs by, taken by Brunel at the end
// of the run rather than by asking the model: edits and files the agent
// created must both show up.
func TestReportDiffCoversEditedAndCreatedFiles(t *testing.T) {
	dir := newRepo(t)
	events := finish(
		toolStart("1", "write_file", `{"path":"a.txt"}`),
		toolEnd("1", "write_file", `{"tool":"write_file","hash":{"hash":"x"}}`),
		toolStart("2", "create_file", `{"path":"new.txt"}`),
		toolEnd("2", "create_file", `{"tool":"create_file","hash":{"hash":"y"}}`),
	)
	r := NewRuntime(pirpc.LaunchOptions{Model: "m"}, pirpc.Credential{}, newTestSession(t), dir, "workspace", "").
		withFakeStart(newFake(events, nil), nil)
	// The fake tools do not touch disk, so make the changes the real tools would.
	r.start = func(context.Context, pirpc.LaunchOptions, pirpc.Credential, string, map[string]string) (pirpc.PiProcess, error) {
		_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "new.txt"), []byte("brand new\n"), 0o644)
		return newFake(events, nil), nil
	}
	rep, err := r.Run(context.Background(), "task", &recordingSink{})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, want := range []string{"-one", "+two", "new.txt", "+brand new"} {
		if !strings.Contains(rep.Diff, want) {
			t.Errorf("Diff lacks %q:\n%s", want, rep.Diff)
		}
	}
}

// A file the user already had untracked is not the agent's work and must not
// be presented as part of its diff.
func TestReportDiffIgnoresUntrackedFilesTheAgentDidNotCreate(t *testing.T) {
	dir := newRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "mine.txt"), []byte("private\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	events := finish(
		toolStart("1", "list_files", `{"path":"."}`),
		toolEnd("1", "list_files", `{"tool":"list_files","list":{"entries":[]}}`),
	)
	rep, _ := runWith(t, dir, events, nil)
	if strings.Contains(rep.Diff, "private") {
		t.Fatalf("Diff includes an untracked file the agent never touched:\n%s", rep.Diff)
	}
	if len(rep.RemainingRisks) != 0 {
		t.Fatalf("RemainingRisks = %q: a pre-existing untracked file is not in the diff, so no warning is due", rep.RemainingRisks)
	}
}

// If the workspace already had uncommitted changes, the diff cannot tell them
// apart from the agent's; the report says so instead of implying otherwise.
func TestReportWarnsWhenWorkspaceWasAlreadyDirty(t *testing.T) {
	dir := newRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("user edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	events := finish(
		toolStart("1", "list_files", `{"path":"."}`),
		toolEnd("1", "list_files", `{"tool":"list_files","list":{"entries":[]}}`),
	)
	rep, _ := runWith(t, dir, events, nil)
	if !strings.Contains(strings.Join(rep.RemainingRisks, "\n"), "uncommitted changes before the run") {
		t.Fatalf("RemainingRisks = %q, want the pre-existing changes disclosed", rep.RemainingRisks)
	}
}

// Without git there is no diff to take; the report states that rather than
// leaving an empty string that reads as "nothing changed".
func TestReportDiffUnavailableOutsideGit(t *testing.T) {
	events := finish(
		toolStart("1", "create_file", `{"path":"b.txt"}`),
		toolEnd("1", "create_file", `{"tool":"create_file","hash":{"hash":"x"}}`),
	)
	rep, _ := runWith(t, t.TempDir(), events, nil)
	if rep.Diff != "" {
		t.Fatalf("Diff = %q, want empty outside a git repository", rep.Diff)
	}
	if !strings.Contains(strings.Join(rep.RemainingRisks, "\n"), "diff unavailable") {
		t.Fatalf("RemainingRisks = %q, want the missing diff disclosed", rep.RemainingRisks)
	}
}

// A run that never executed a tool changed nothing, so no git is run and no
// diff warning is raised.
func TestReportWithoutToolsHasNoDiffNote(t *testing.T) {
	rep, _ := runWith(t, t.TempDir(), finish(), nil)
	if len(rep.RemainingRisks) != 0 {
		t.Fatalf("RemainingRisks = %q, want none", rep.RemainingRisks)
	}
}

// A key shape the pattern-based masking does not know; only the exact match
// against the injected credential can remove it, and that match fails if the
// key is cut in half first.
const oddKey = "ZZtopKEY0123456789abcdefGHIJKLMN"

func withKey(r *Runtime) { r.credential = pirpc.OpenRouterCredential(oddKey) }

func leaksKeyFragment(text string) bool {
	return strings.Contains(text, oddKey[:10]) || strings.Contains(text, oddKey[len(oddKey)-10:])
}

// The summary keeps only the tail of the output; the key must be masked
// before that cut, not after, or its end can survive.
func TestReportSummaryMasksKeyBeforeTruncating(t *testing.T) {
	out := oddKey + strings.Repeat("x", 190)
	events := finish(
		toolStart("1", "run_powershell", `{"command":"echo"}`),
		toolEnd("1", "run_powershell", `{"tool":"run_powershell","run":{"stdout":"`+out+`","stderr":"","exit_code":0,"truncated":false}}`),
	)
	rep, _ := runWith(t, t.TempDir(), events, withKey)
	if len(rep.Verifications) != 1 || leaksKeyFragment(rep.Verifications[0].Summary) {
		t.Fatalf("Verifications = %+v, a piece of the key survived the truncation", rep.Verifications)
	}
}

// Same for the diff, whose size limit can fall in the middle of a key.
func TestReportDiffMasksKeyBeforeTruncating(t *testing.T) {
	dir := newRepo(t)
	// Try several alignments so the size limit lands inside the key for at
	// least one of them whichever way the diff header is sized.
	for pad := maxDiffBytes - 220; pad < maxDiffBytes-100; pad += 7 {
		content := strings.Repeat("a", pad) + oddKey + strings.Repeat("b", 400) + "\n"
		if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		diff, _ := workspaceDiffFact(dir, []string{"big.txt"}, oddKey)
		if leaksKeyFragment(diff) {
			t.Fatalf("pad %d: a piece of the key survived the diff truncation", pad)
		}
	}
}

// Without a terminal nobody can approve a command that needs it (spec §6.3,
// AC-11). The run must stop at once, not carry on while the model tries other
// commands: Pi is aborted, the stable code is returned, and the report says
// which command was waiting.
func TestRunStopsImmediatelyWhenApprovalIsUnavailable(t *testing.T) {
	events := finish(
		toolStart("1", "run_powershell", `{"command":"Remove-Item important.txt"}`),
		toolFail("1", "run_powershell", "E_APPROVAL_REQUIRED_NO_TTY: command requires confirmation but no terminal is attached"),
		// What the model would do next if the run were allowed to continue.
		toolStart("2", "create_file", `{"path":"after.txt","content":"x"}`),
		toolEnd("2", "create_file", `{"tool":"create_file","hash":{"hash":"x"}}`),
	)
	proc := newFake(events, nil)
	r := NewRuntime(pirpc.LaunchOptions{Model: "m"}, pirpc.Credential{}, newTestSession(t), t.TempDir(), "workspace", "").withFakeStart(proc, nil)
	rep, err := r.Run(context.Background(), "task", &recordingSink{})

	if pirpc.ErrorCode(err) != "E_APPROVAL_REQUIRED_NO_TTY" {
		t.Fatalf("error = %v, want E_APPROVAL_REQUIRED_NO_TTY", err)
	}
	if !proc.aborted {
		t.Fatal("Pi was not aborted")
	}
	if rep.Status == completion.StatusCompleted {
		t.Fatalf("Status = %q, want a non-completed run", rep.Status)
	}
	if rep.PendingApproval == nil || rep.PendingApproval.Command != "Remove-Item important.txt" {
		t.Fatalf("PendingApproval = %+v, want the command that could not be approved", rep.PendingApproval)
	}
	if len(rep.ModifiedFiles) != 0 {
		t.Fatalf("ModifiedFiles = %v: the run kept going after the refusal", rep.ModifiedFiles)
	}
}

// A user declining at the prompt is different: the model may carry on and the
// run is not cut short (it only ends up incomplete, spec §8).
func TestRunContinuesAfterUserDeclinesApproval(t *testing.T) {
	events := finish(
		toolStart("1", "run_powershell", `{"command":"Remove-Item x"}`),
		toolFail("1", "run_powershell", "E_APPROVAL_DENIED: user declined confirmation"),
		toolStart("2", "create_file", `{"path":"after.txt","content":"x"}`),
		toolEnd("2", "create_file", `{"tool":"create_file","hash":{"hash":"x"}}`),
	)
	proc := newFake(events, nil)
	r := NewRuntime(pirpc.LaunchOptions{Model: "m"}, pirpc.Credential{}, newTestSession(t), t.TempDir(), "workspace", "").withFakeStart(proc, nil)
	rep, err := r.Run(context.Background(), "task", &recordingSink{})
	if err != nil || proc.aborted {
		t.Fatalf("a user decline stopped the run: err=%v aborted=%v", err, proc.aborted)
	}
	if len(rep.ModifiedFiles) != 1 {
		t.Fatalf("ModifiedFiles = %v, want the later write recorded", rep.ModifiedFiles)
	}
}
