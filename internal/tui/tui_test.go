package tui

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bext1998/brunel/internal/agent"
	"github.com/bext1998/brunel/internal/completion"
	"github.com/bext1998/brunel/internal/safety"
)

// capture is a bridge whose messages are collected instead of sent to a
// running program.
type capture struct {
	mu   sync.Mutex
	msgs []tea.Msg
}

func newTestBridge(c *capture) *bridge {
	b := newBridge()
	b.send = func(m tea.Msg) {
		c.mu.Lock()
		c.msgs = append(c.msgs, m)
		c.mu.Unlock()
	}
	return b
}

func (c *capture) wait(t *testing.T, match func(tea.Msg) bool) tea.Msg {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		for _, m := range c.msgs {
			if match(m) {
				c.mu.Unlock()
				return m
			}
		}
		c.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("expected message was never delivered")
	return nil
}

func press(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

func update(t *testing.T, m model, msg tea.Msg) (model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	return next.(model), cmd
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func typeText(t *testing.T, m model, s string) model {
	for _, r := range s {
		m, _ = update(t, m, press(string(r)))
	}
	return m
}

// startRun submits a task whose run blocks until release is closed.
func startRun(t *testing.T) (model, *capture, chan struct{}, *bool) {
	t.Helper()
	c := &capture{}
	release := make(chan struct{})
	cancelled := new(bool)
	var mu sync.Mutex
	opts := Options{Model: "openrouter/x", Mode: "workspace", Run: func(ctx context.Context, task string, _ agent.EventSink) (*completion.Report, error) {
		select {
		case <-release:
			return &completion.Report{Status: completion.StatusCompleted}, nil
		case <-ctx.Done():
			mu.Lock()
			*cancelled = true
			mu.Unlock()
			return &completion.Report{Status: completion.StatusIncomplete}, nil
		}
	}}
	m := newModel(opts, newTestBridge(c))
	m, _ = update(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeText(t, m, "fix the bug")
	m, _ = update(t, m, press("enter"))
	if m.state != stateRunning {
		t.Fatalf("state after submit = %v, want running", m.state)
	}
	return m, c, release, cancelled
}

func TestEmptySubmitNeverStartsRun(t *testing.T) {
	called := false
	m := newModel(Options{Run: func(context.Context, string, agent.EventSink) (*completion.Report, error) {
		called = true
		return nil, nil
	}}, newTestBridge(&capture{}))
	m = typeText(t, m, "   ")
	m, _ = update(t, m, press("enter"))
	if called || m.state != stateIdle {
		t.Fatalf("empty task started a run (called=%v state=%v)", called, m.state)
	}
}

func TestResizeDoesNotCancelRun(t *testing.T) {
	m, _, release, cancelled := startRun(t)
	defer close(release)
	for _, size := range []tea.WindowSizeMsg{{Width: 30, Height: 10}, {Width: 200, Height: 60}, {Width: 10, Height: 4}} {
		m, _ = update(t, m, size)
	}
	if m.state != stateRunning || *cancelled {
		t.Fatalf("resize affected the run: state=%v cancelled=%v", m.state, *cancelled)
	}
}

func TestCtrlCCancelsRunThenQuitsWhenIdle(t *testing.T) {
	m, c, release, cancelled := startRun(t)
	defer close(release)

	m, cmd := update(t, m, press("ctrl+c"))
	if isQuit(cmd) {
		t.Fatal("Ctrl+C during a run quit instead of cancelling")
	}
	if m.state != stateCancelling {
		t.Fatalf("state = %v, want cancelling", m.state)
	}
	done := c.wait(t, func(msg tea.Msg) bool { _, ok := msg.(runDoneMsg); return ok })
	if !*cancelled {
		t.Fatal("run context was not cancelled")
	}
	m, cmd = update(t, m, done)
	if m.state != stateIdle || isQuit(cmd) {
		t.Fatalf("after run end: state=%v quit=%v, want idle and still open", m.state, isQuit(cmd))
	}
	if _, cmd = update(t, m, press("ctrl+c")); !isQuit(cmd) {
		t.Fatal("Ctrl+C while idle did not quit")
	}
}

func TestSecondCtrlCQuitsOnlyAfterRunStops(t *testing.T) {
	m, c, release, _ := startRun(t)
	defer close(release)
	m, _ = update(t, m, press("ctrl+c"))
	m, cmd := update(t, m, press("ctrl+c"))
	if isQuit(cmd) {
		t.Fatal("quit while the run was still stopping")
	}
	done := c.wait(t, func(msg tea.Msg) bool { _, ok := msg.(runDoneMsg); return ok })
	if _, cmd = update(t, m, done); !isQuit(cmd) {
		t.Fatal("did not quit once the cancelled run finished")
	}
}

// TestSinkEventsCannotApprove is TC-TUI-001 / INV-3: display events -
// including approval_needed - never open a modal, so no key press can turn
// a display event into an approval.
func TestSinkEventsCannotApprove(t *testing.T) {
	m := newModel(Options{}, newTestBridge(&capture{}))
	m, _ = update(t, m, eventMsg(agent.Event{Kind: agent.EventApprovalNeeded, Text: "git push"}))
	if len(m.approvals) != 0 {
		t.Fatal("an EventSink event opened the approval modal")
	}
	m, _ = update(t, m, press("y"))
	if len(m.approvals) != 0 {
		t.Fatal("approval state appeared after a key press")
	}
	if !strings.Contains(m.transcript.View(), "git push") {
		t.Fatal("approval_needed was not displayed in the transcript")
	}
}

func TestModalAnswersOnlyExplicitly(t *testing.T) {
	for _, tc := range []struct {
		key  string
		want bool
	}{{"y", true}, {"n", false}, {"esc", false}} {
		m := newModel(Options{}, newTestBridge(&capture{}))
		reply := make(chan bool, 1)
		m, _ = update(t, m, approvalRequestMsg{prompt: safety.ApprovalPrompt{Command: "git push", Reason: "git state change"}, reply: reply})
		// Unrelated keys do not answer.
		m, _ = update(t, m, press("x"))
		m, _ = update(t, m, press("enter"))
		select {
		case <-reply:
			t.Fatalf("modal answered on a non-answer key")
		default:
		}
		m, _ = update(t, m, press(tc.key))
		select {
		case got := <-reply:
			if got != tc.want {
				t.Fatalf("key %q answered %v, want %v", tc.key, got, tc.want)
			}
		default:
			t.Fatalf("key %q did not answer the modal", tc.key)
		}
		if len(m.approvals) != 0 {
			t.Fatal("modal still open after answering")
		}
	}
}

func TestCtrlCDeniesOpenApproval(t *testing.T) {
	m, _, release, _ := startRun(t)
	defer close(release)
	reply := make(chan bool, 1)
	m, _ = update(t, m, approvalRequestMsg{prompt: safety.ApprovalPrompt{Command: "git push", Reason: "r"}, reply: reply})
	m, _ = update(t, m, press("ctrl+c"))
	if got := <-reply; got {
		t.Fatal("Ctrl+C approved the pending command")
	}
	if m.state != stateCancelling {
		t.Fatalf("state = %v, want cancelling", m.state)
	}
}

// TestNarrowTerminalKeepsEssentials is EC-4: on a tiny terminal the
// transcript, the input, and the full approval information stay visible.
func TestNarrowTerminalKeepsEssentials(t *testing.T) {
	m := newModel(Options{Model: "m"}, newTestBridge(&capture{}))
	m, _ = update(t, m, eventMsg(agent.Event{Kind: agent.EventAssistantDelta, Text: "hello"}))
	command := "Remove-Item -Recurse -Force build"
	reason := "recursive or forced delete"
	m, _ = update(t, m, approvalRequestMsg{prompt: safety.ApprovalPrompt{Command: command, Reason: reason}, reply: make(chan bool, 1)})
	m, _ = update(t, m, tea.WindowSizeMsg{Width: 18, Height: 12})

	screen := ansi.Strip(m.View().Content)
	flat := strings.Join(strings.Fields(screen), "")
	for _, want := range []string{command, reason} {
		if !strings.Contains(flat, strings.Join(strings.Fields(want), "")) {
			t.Fatalf("narrow screen lost %q:\n%s", want, screen)
		}
	}
	if !strings.Contains(screen, "hello") {
		t.Fatalf("narrow screen lost the transcript:\n%s", screen)
	}
	if !strings.Contains(screen, ">") {
		t.Fatalf("narrow screen lost the input:\n%s", screen)
	}
	for i, line := range strings.Split(screen, "\n") {
		if w := ansi.StringWidth(line); w > 18 {
			t.Fatalf("line %d is %d cells wide on an 18-cell terminal: %q", i, w, line)
		}
	}
}

func TestStreamingAndStatusLine(t *testing.T) {
	m := newModel(Options{Model: "openrouter/x", Mode: "readonly"}, newTestBridge(&capture{}))
	m, _ = update(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})
	for _, d := range []string{"Hel", "lo ", "world"} {
		m, _ = update(t, m, eventMsg(agent.Event{Kind: agent.EventAssistantDelta, Text: d}))
	}
	cost := 0.0123
	m, _ = update(t, m, eventMsg(agent.Event{Kind: agent.EventUsageUpdated}))
	m.usage.PromptTokens, m.usage.CompletionTokens, m.usage.CostUSD = 10, 20, &cost
	screen := ansi.Strip(m.View().Content)
	for _, want := range []string{"Hello world", "openrouter/x", "readonly", "10 in / 20 out", "$0.0123", "idle"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("screen missing %q:\n%s", want, screen)
		}
	}
	if !m.View().AltScreen {
		t.Fatal("interactive TUI must use the alternate screen")
	}
}

func TestApproverWithdrawsOnCancel(t *testing.T) {
	c := &capture{}
	a := approver{newTestBridge(c)}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		ok, err := a.Confirm(ctx, safety.ApprovalPrompt{Command: "git push", Reason: "r"})
		if ok {
			t.Error("cancelled Confirm approved")
		}
		result <- err
	}()
	c.wait(t, func(m tea.Msg) bool { _, ok := m.(approvalRequestMsg); return ok })
	cancel()
	if err := <-result; err == nil {
		t.Fatal("cancelled Confirm returned no error")
	}
	c.wait(t, func(m tea.Msg) bool { _, ok := m.(approvalWithdrawnMsg); return ok })
}

// TestBubbleTeaStaysInPresentation enforces spec.md §4.2 [FROZEN]: Bubble
// Tea may not be imported by agent, pirpc, tools, or session - nor by any
// other core package.
func TestBubbleTeaStaysInPresentation(t *testing.T) {
	internalDir, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(internalDir)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "tui" {
			continue
		}
		files, _ := filepath.Glob(filepath.Join(internalDir, e.Name(), "*.go"))
		for _, f := range files {
			parsed, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("parse %s: %v", f, err)
			}
			for _, imp := range parsed.Imports {
				path, _ := strconv.Unquote(imp.Path.Value)
				if strings.HasPrefix(path, "charm.land/") || strings.Contains(path, "charmbracelet/bubbletea") {
					t.Errorf("%s imports %s; Bubble Tea belongs to internal/tui only", f, path)
				}
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no core package files were checked")
	}
}

func TestModalNeutralizesControlCharacters(t *testing.T) {
	m := newModel(Options{}, newTestBridge(&capture{}))
	m, _ = update(t, m, approvalRequestMsg{
		prompt: safety.ApprovalPrompt{Command: "echo hi\rrm -rf x\x1b[2J", Reason: "why\x1b]0;t\x07"},
		reply:  make(chan bool, 1),
	})
	view := m.renderModal()
	if strings.ContainsAny(view, "\r\x07") || strings.Contains(view, "\x1b[2J") || strings.Contains(view, "\x1b]0;") {
		t.Fatalf("modal passed raw control characters through: %q", view)
	}
	if !strings.Contains(view, `\x0d`) || !strings.Contains(view, `\x1b`) {
		t.Fatalf("modal did not show visible escapes: %q", view)
	}
}

func TestLongCommandMustBeReadBeforeApproval(t *testing.T) {
	m := newModel(Options{}, newTestBridge(&capture{}))
	var parts []string
	for i := 0; i < 40; i++ {
		parts = append(parts, fmt.Sprintf("Write-Host line%02d", i))
	}
	command := strings.Join(parts, "\n")
	reply := make(chan bool, 1)
	m, _ = update(t, m, tea.WindowSizeMsg{Width: 40, Height: 10})
	m, _ = update(t, m, approvalRequestMsg{prompt: safety.ApprovalPrompt{Command: command, Reason: "why"}, reply: reply})

	if h := len(strings.Split(m.View().Content, "\n")); h > 10 {
		t.Fatalf("view is %d lines on a 10-line terminal", h)
	}
	seen := map[string]bool{}
	collect := func() {
		for _, l := range strings.Split(ansi.Strip(m.View().Content), "\n") {
			if f := strings.Fields(l); len(f) >= 2 && f[len(f)-2] == "Write-Host" {
				seen[f[len(f)-1]] = true
			}
		}
	}
	collect()
	if seen["line39"] {
		t.Fatal("test setup: the end of the command is already visible")
	}
	m, _ = update(t, m, press("y"))
	select {
	case <-reply:
		t.Fatal("approved a command whose end was never shown")
	default:
	}
	for i := 0; i < 100 && !seen["line39"]; i++ {
		m, _ = update(t, m, press("pgdown"))
		collect()
	}
	if !seen["line39"] {
		t.Fatal("could not scroll to the end of the command")
	}
	for i := 0; i < 40; i++ {
		if !seen[fmt.Sprintf("line%02d", i)] {
			t.Fatalf("line%02d was never shown while scrolling", i)
		}
	}
	m, _ = update(t, m, press("y"))
	select {
	case ok := <-reply:
		if !ok {
			t.Fatal("y after reading everything did not approve")
		}
	default:
		t.Fatal("y after reading everything did not answer")
	}
}

func TestJumpingToEndDoesNotUnlockApproval(t *testing.T) {
	m := newModel(Options{}, newTestBridge(&capture{}))
	var parts []string
	for i := 0; i < 40; i++ {
		parts = append(parts, fmt.Sprintf("Write-Host line%02d", i))
	}
	reply := make(chan bool, 1)
	m, _ = update(t, m, tea.WindowSizeMsg{Width: 40, Height: 10})
	m, _ = update(t, m, approvalRequestMsg{prompt: safety.ApprovalPrompt{Command: strings.Join(parts, "\n"), Reason: "why"}, reply: reply})

	m, _ = update(t, m, press("end"))
	m, _ = update(t, m, press("y"))
	select {
	case <-reply:
		t.Fatal("End then y approved a command whose middle was never shown")
	default:
	}
	// Reading the skipped middle (paging back up, then down) unlocks it.
	m, _ = update(t, m, press("home"))
	for i := 0; i < 100; i++ {
		m, _ = update(t, m, press("pgdown"))
	}
	m, _ = update(t, m, press("y"))
	select {
	case ok := <-reply:
		if !ok {
			t.Fatal("expected approval after reading every page")
		}
	default:
		t.Fatal("still locked after paging through the whole command")
	}
}

func TestResizeVoidsApprovalReadingProgress(t *testing.T) {
	m := newModel(Options{}, newTestBridge(&capture{}))
	var parts []string
	for i := 0; i < 40; i++ {
		parts = append(parts, fmt.Sprintf("Write-Host line%02d aaaaaaaaaaaaaaaaaaaaaaaa", i))
	}
	reply := make(chan bool, 1)
	m, _ = update(t, m, tea.WindowSizeMsg{Width: 20, Height: 10})
	m, _ = update(t, m, approvalRequestMsg{prompt: safety.ApprovalPrompt{Command: strings.Join(parts, "\n"), Reason: "why"}, reply: reply})
	// Read most of it at the narrow width, but not to the end.
	for i := 0; i < 500 && m.modalSeen < 50; i++ {
		m, _ = update(t, m, press("pgdown"))
	}
	if m.modalSeen < 50 {
		t.Fatalf("test setup: reading progress only reached %d of %d lines", m.modalSeen, len(m.modalText()))
	}
	if m.modalFullySeen() {
		t.Fatal("test setup: the whole command was read at the narrow width")
	}
	m, _ = update(t, m, tea.WindowSizeMsg{Width: 120, Height: 10})
	if wide := len(m.modalText()); wide > 50 {
		t.Fatalf("test setup: %d wrapped lines at the wide width, want at most 50", wide)
	}
	m, _ = update(t, m, press("end"))
	m, _ = update(t, m, press("y"))
	select {
	case <-reply:
		t.Fatal("approved after a resize using reading progress from the old width")
	default:
	}
	if m.modalSeen > m.modalBodyHeight(len(m.modalText())) {
		t.Fatalf("reading progress %d survived the resize", m.modalSeen)
	}
	// Same-width resize keeps the progress.
	m, _ = update(t, m, press("home"))
	m, _ = update(t, m, press("pgdown"))
	seen := m.modalSeen
	m, _ = update(t, m, tea.WindowSizeMsg{Width: 120, Height: 10})
	if m.modalSeen != seen {
		t.Fatal("a resize that kept the width reset the reading progress")
	}
}
