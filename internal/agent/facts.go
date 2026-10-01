package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bext1998/brunel/internal/completion"
	"github.com/bext1998/brunel/internal/redact"
)

const (
	gitTimeout       = 30 * time.Second
	maxDiffBytes     = 1 << 20
	maxSummaryRunes  = 200
	approvalDenied   = "E_APPROVAL_DENIED"
	approvalNoTTY    = "E_APPROVAL_REQUIRED_NO_TTY"
	noGitDiffMessage = "workspace is not a usable git repository; diff unavailable"
)

// toolFacts accumulates what the tools actually did during a run. It is fed
// from Pi's tool_execution_start (the arguments the model passed) and
// tool_execution_end (the structured result the Go tool returned), so the
// completion report states observed facts rather than asking the model.
type toolFacts struct {
	// open maps a tool call that started but has not ended to its start event.
	open     map[string]toolCall
	started  int
	modified []string
	verified []completion.Verification
	failures []completion.ToolFailure
	// approvalDeclined is set when a call failed because the user declined
	// (or no approval channel existed): spec §8 makes such a run incomplete.
	approvalDeclined bool
}

type toolCall struct {
	name string
	args json.RawMessage
}

func (f *toolFacts) start(id, name string, args json.RawMessage) {
	if f.open == nil {
		f.open = map[string]toolCall{}
	}
	f.open[id] = toolCall{name: name, args: args}
	f.started++
}

// end records the terminal state of a call. A successful write adds its path
// to the modified files, and every run_powershell is recorded as a
// verification: the harness does not understand the task, so it does not try
// to tell a test run from any other command (the exit code is only a fact).
func (f *toolFacts) end(id, name string, isError bool, details json.RawMessage, errText string) {
	call, known := f.open[id]
	delete(f.open, id)
	if !known {
		call.name = name
	}
	if isError {
		f.failures = append(f.failures, completion.ToolFailure{Tool: name, Error: errText})
		if strings.Contains(errText, approvalDenied) || strings.Contains(errText, approvalNoTTY) {
			f.approvalDeclined = true
		}
		return
	}
	switch name {
	case "create_file", "write_file", "apply_patch":
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(call.args, &args) == nil && args.Path != "" {
			f.addModified(args.Path)
		}
	case "run_powershell":
		var args struct {
			Command string `json:"command"`
		}
		var res struct {
			Run struct {
				Stdout   string `json:"stdout"`
				Stderr   string `json:"stderr"`
				ExitCode int    `json:"exit_code"`
			} `json:"run"`
		}
		if json.Unmarshal(call.args, &args) != nil || json.Unmarshal(details, &res) != nil {
			return
		}
		f.verified = append(f.verified, completion.Verification{
			Command:  args.Command,
			ExitCode: res.Run.ExitCode,
			Summary:  summarizeOutput(res.Run.Stdout, res.Run.Stderr),
		})
	}
}

func (f *toolFacts) addModified(path string) {
	for _, p := range f.modified {
		if p == path {
			return
		}
	}
	f.modified = append(f.modified, path)
}

// summarizeOutput keeps the tail of stderr (or stdout when stderr is empty)
// on one line: the end of a failing command's output is where it explains
// itself.
func summarizeOutput(stdout, stderr string) string {
	text := strings.TrimSpace(stderr)
	if text == "" {
		text = strings.TrimSpace(stdout)
	}
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) > maxSummaryRunes {
		runes := []rune(text)
		text = "..." + string(runes[len(runes)-maxSummaryRunes:])
	}
	return text
}

// openCallNames lists the tools that never reached a terminal state.
func (f *toolFacts) openCallNames() []string {
	var names []string
	for _, c := range f.open {
		names = append(names, c.name)
	}
	return names
}

// applyFacts fills the facts-derived fields of rep and lowers a "completed"
// status when the facts contradict it (spec §8, INV-8): every tool call must
// have a terminal state, no approval may be pending, and a declined approval
// makes the run incomplete. Only completed is ever lowered; failed and
// incomplete already say the run did not finish cleanly.
func (r *Runtime) applyFacts(rep *completion.Report, st *runState) {
	f := &st.facts
	rep.ModifiedFiles = append(rep.ModifiedFiles, f.modified...)
	rep.Verifications = append(rep.Verifications, f.verified...)
	rep.ToolFailures = append(rep.ToolFailures, f.failures...)

	secret := r.credential.APIKey
	for i := range rep.Verifications {
		rep.Verifications[i].Command = redact.Secrets(rep.Verifications[i].Command, secret)
		rep.Verifications[i].Summary = redact.Secrets(rep.Verifications[i].Summary, secret)
	}
	for i := range rep.ToolFailures {
		rep.ToolFailures[i].Error = redact.Secrets(rep.ToolFailures[i].Error, secret)
	}

	if r.pendingApproval != nil {
		if p := r.pendingApproval(); p != nil {
			rep.PendingApproval = &completion.ApprovalFact{
				Command: redact.Secrets(p.Command, secret),
				Reason:  redact.Secrets(p.Reason, secret),
			}
		}
	}

	if open := f.openCallNames(); len(open) > 0 {
		rep.RemainingRisks = append(rep.RemainingRisks, fmt.Sprintf(
			"%d tool call(s) never reached a terminal state: %s", len(open), strings.Join(open, ", ")))
	}
	if rep.Status == completion.StatusCompleted {
		switch {
		case len(f.open) > 0, rep.PendingApproval != nil:
			rep.Status = completion.StatusIncomplete
		case f.approvalDeclined:
			rep.Status = completion.StatusIncomplete
		}
	}

	if f.started > 0 {
		diff, note := workspaceDiffFact(r.workspaceRoot, f.modified)
		rep.Diff = redact.Secrets(diff, secret)
		if note != "" {
			rep.RemainingRisks = append(rep.RemainingRisks, note)
		}
		if st.dirtyAtStart && diff != "" {
			rep.RemainingRisks = append(rep.RemainingRisks,
				"workspace had uncommitted changes before the run; the diff includes them")
		}
	}
}

// runGit runs git in root and returns stdout. Exit status 1 is accepted for
// `diff --no-index`, which uses it to mean "files differ".
func runGit(root string, okExit1 bool, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	var exit *exec.ExitError
	if err != nil && okExit1 && errors.As(err, &exit) && exit.ExitCode() == 1 {
		err = nil
	}
	return out.String(), err
}

// workspaceDirty reports whether root is a git work tree with uncommitted
// changes to tracked files. Untracked files are ignored on purpose: the diff
// only ever includes the untracked files the agent created, so a file the
// user already had cannot be mistaken for the agent's work. Any git failure
// reads as "not dirty": the later diff attempt reports the unavailability.
func workspaceDirty(root string) bool {
	out, err := runGit(root, false, "status", "--porcelain", "--untracked-files=no")
	return err == nil && strings.TrimSpace(out) != ""
}

// workspaceDiffFact returns the diff of the work tree against HEAD plus a
// diff of each file the agent created that git does not track yet. A note is
// returned when no diff could be taken or it was cut short.
func workspaceDiffFact(root string, modified []string) (diff, note string) {
	if _, err := runGit(root, false, "rev-parse", "--is-inside-work-tree"); err != nil {
		return "", noGitDiffMessage
	}
	tracked, err := runGit(root, false, "diff", "HEAD")
	if err != nil {
		// No commits yet: HEAD does not exist, diff the index and work tree.
		tracked, err = runGit(root, false, "diff")
		if err != nil {
			return "", noGitDiffMessage
		}
	}
	var b strings.Builder
	b.WriteString(tracked)

	untracked, err := runGit(root, false, "ls-files", "--others", "--exclude-standard", "-z")
	if err == nil {
		created := map[string]bool{}
		for _, p := range modified {
			created[filepath.ToSlash(filepath.Clean(p))] = true
		}
		for _, p := range strings.Split(untracked, "\x00") {
			if p == "" || !created[filepath.ToSlash(p)] {
				continue
			}
			part, err := runGit(root, true, "diff", "--no-index", "--", "/dev/null", p)
			if err == nil {
				b.WriteString(part)
			}
		}
	}

	diff = b.String()
	if len(diff) > maxDiffBytes {
		cut := diff[:maxDiffBytes]
		for !utf8.ValidString(cut) {
			cut = cut[:len(cut)-1]
		}
		return cut, fmt.Sprintf("diff truncated to %d bytes", maxDiffBytes)
	}
	return diff, ""
}
