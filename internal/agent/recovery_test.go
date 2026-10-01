package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/bext1998/brunel/internal/pirpc"
	"github.com/bext1998/brunel/internal/session"
)

// runTask runs one task on sess with the given Pi events and returns the
// prompt the (fake) Pi received.
func runTask(t *testing.T, sess *session.Session, root, task string, events []pirpc.Event) string {
	t.Helper()
	proc := newFake(events, nil)
	r := NewRuntime(pirpc.LaunchOptions{Model: "m"}, pirpc.Credential{}, sess, root, "workspace", "").withFakeStart(proc, nil)
	if _, err := r.Run(context.Background(), task, &recordingSink{}); err != nil {
		t.Fatalf("Run(%q) error = %v", task, err)
	}
	return proc.prompt
}

func newNamedSession(t *testing.T, name string) (*session.Store, *session.Session, string) {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	root := t.TempDir()
	sess, err := store.Create(session.CreateOptions{Name: &name, WorkspaceRoot: root, Mode: session.ModeWorkspace})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return store, sess, root
}

// A first task's work: a new file, a failing command, and a command the user
// declined. Used by several tests below.
func firstTaskEvents() []pirpc.Event {
	return finish(
		toolStart("1", "create_file", `{"path":"b.txt","content":"hi"}`),
		toolEnd("1", "create_file", `{"tool":"create_file","hash":{"hash":"x"}}`),
		toolStart("2", "run_powershell", `{"command":"go test ./..."}`),
		toolEnd("2", "run_powershell", `{"tool":"run_powershell","run":{"stdout":"","stderr":"FAIL pkg","exit_code":1,"truncated":false}}`),
		toolStart("3", "run_powershell", `{"command":"Remove-Item old -Recurse"}`),
		toolFail("3", "run_powershell", "E_APPROVAL_DENIED: user declined confirmation"),
	)
}

// Each finished run leaves a summary of what it established (spec §7.1): the
// goal, the user's instructions and declines, what changed, what was run and
// where it stopped.
func TestRunSavesASummaryOfWhatItEstablished(t *testing.T) {
	_, sess, root := newNamedSession(t, "work")
	runTask(t, sess, root, "add b.txt and run the tests", firstTaskEvents())

	sum, err := sess.LoadSummary()
	if err != nil {
		t.Fatalf("LoadSummary: %v", err)
	}
	if sum.Goal != "add b.txt and run the tests" {
		t.Errorf("Goal = %q", sum.Goal)
	}
	joined := strings.Join(sum.Decisions, "\n")
	if !strings.Contains(joined, "add b.txt and run the tests") || !strings.Contains(joined, "declined to run: Remove-Item old -Recurse") {
		t.Errorf("Decisions = %q, want the instruction and the declined command", sum.Decisions)
	}
	if len(sum.ModifiedFiles) != 1 || sum.ModifiedFiles[0] != "b.txt" {
		t.Errorf("ModifiedFiles = %v", sum.ModifiedFiles)
	}
	if len(sum.Verifications) != 1 || sum.Verifications[0].Command != "go test ./..." || sum.Verifications[0].ExitCode != 1 {
		t.Errorf("Verifications = %+v", sum.Verifications)
	}
	if len(sum.Pending) == 0 {
		t.Errorf("Pending is empty although the run ended incomplete (a declined approval)")
	}
}

// #52: the second task of the same (TUI) session starts a new Pi process; it
// must be told what the first task did, or it cannot refer to it.
func TestNextTaskInTheSameSessionReceivesTheEarlierWork(t *testing.T) {
	_, sess, root := newNamedSession(t, "work")
	runTask(t, sess, root, "add b.txt and run the tests", firstTaskEvents())
	prompt := runTask(t, sess, root, "now fix the failing test", finish())

	if !strings.HasPrefix(prompt, "now fix the failing test") {
		t.Fatalf("the new task must come first, prompt = %q", prompt)
	}
	for _, want := range []string{
		"Previous goal: add b.txt and run the tests",
		"b.txt",
		"go test ./... (exit 1): FAIL pkg",
		"user declined to run: Remove-Item old -Recurse",
		"approvals are never carried over",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the second task's prompt lacks %q:\n%s", want, prompt)
		}
	}
}

// #53 / AC-13: a resumed session is a new Brunel process with a new Session
// object; the saved summary has to reach the new Pi.
func TestResumeDeliversTheSavedSummaryToTheNewPi(t *testing.T) {
	store, sess, root := newNamedSession(t, "work")
	runTask(t, sess, root, "add b.txt and run the tests", firstTaskEvents())

	if err := sess.Close(session.ExitStatusClean); err != nil { // the first process has exited
		t.Fatalf("Close: %v", err)
	}
	resumed, err := store.Resume("work")
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	prompt := runTask(t, resumed, root, "continue", finish())
	for _, want := range []string{"Previous goal: add b.txt and run the tests", "b.txt", "go test ./... (exit 1)", "declined to run: Remove-Item old -Recurse"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the resumed session's prompt lacks %q:\n%s", want, prompt)
		}
	}
}

// A decline is remembered so the model does not assume the command ran, but
// no approval is ever restored: nothing may read as "already approved".
func TestResumeNeverRestoresAnApproval(t *testing.T) {
	_, sess, root := newNamedSession(t, "work")
	events := finish(
		toolStart("1", "run_powershell", `{"command":"git push origin main"}`),
		toolEnd("1", "run_powershell", `{"tool":"run_powershell","run":{"stdout":"pushed","stderr":"","exit_code":0,"truncated":false}}`),
	)
	runTask(t, sess, root, "publish", events) // the user approved and it ran
	prompt := runTask(t, sess, root, "publish again", finish())

	if strings.Contains(strings.ToLower(prompt), "approved") && !strings.Contains(prompt, "approvals are never carried over") {
		t.Errorf("the prompt mentions an approval without the carry-over disclaimer:\n%s", prompt)
	}
	if strings.Contains(prompt, "user approved") || strings.Contains(prompt, "already approved") {
		t.Errorf("the prompt restores an approval:\n%s", prompt)
	}
}

func TestBrandNewSessionAddsNoRecoverySection(t *testing.T) {
	_, sess, root := newNamedSession(t, "work")
	prompt := runTask(t, sess, root, "first task", finish())
	if strings.Contains(prompt, "Earlier work in this session") {
		t.Fatalf("a new session got a recovery section:\n%s", prompt)
	}
}

// Missing data is disclosed, never invented (§7.2): a session that has events
// but no saved summary must say so instead of looking like a clean start.
func TestResumeWithoutASummaryDisclosesTheGap(t *testing.T) {
	_, sess, root := newNamedSession(t, "work")
	if _, err := sess.AppendEvent(session.EvAssistantText, map[string]string{"text": "earlier"}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	prompt := runTask(t, sess, root, "continue", finish())
	if !strings.Contains(prompt, "no summary of it was saved") {
		t.Fatalf("the gap was not disclosed:\n%s", prompt)
	}
}

// What is saved and handed over passes through the session's masking: a key
// that appeared in a task must not come back out in a later prompt.
func TestRecoveryContextDoesNotRepeatSecrets(t *testing.T) {
	const key = "sk-or-v1-0123456789abcdef0123456789abcdef"
	_, sess, root := newNamedSession(t, "work")
	runTask(t, sess, root, "use the key "+key+" to call the api", finish())
	prompt := runTask(t, sess, root, "continue", finish())
	if strings.Contains(prompt, key) {
		t.Fatalf("the earlier task's secret came back in the prompt:\n%s", prompt)
	}
}
