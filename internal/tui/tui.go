// Package tui is Brunel's thin interactive presentation layer (spec.md §4.2
// [FROZEN]): a scrollable transcript, a multi-line input, a status line
// (model, tokens, cost, run state), and an approve/deny modal that shows the
// command and the reason. Nothing else.
//
// Bubble Tea lives only here. The agent core never imports this package; it
// talks to it through the UI-neutral ports - agent.EventSink for display
// and safety.Approver for CONFIRM prompts - which is the same boundary the
// plain-text mode uses (spec.md §5.2).
package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bext1998/brunel/internal/agent"
	"github.com/bext1998/brunel/internal/approval"
	"github.com/bext1998/brunel/internal/completion"
	"github.com/bext1998/brunel/internal/provider"
	"github.com/bext1998/brunel/internal/safety"
)

// RunFunc starts one agent run. The TUI calls it on its own goroutine for
// each submitted task and cancels ctx when the user presses Ctrl+C while
// the run is active.
type RunFunc func(ctx context.Context, task string, sink agent.EventSink) (*completion.Report, error)

// Options configure the TUI.
type Options struct {
	Model string // shown in the status line
	Mode  string // workspace or readonly, shown in the status line
	Run   RunFunc
}

type runState int

const (
	stateIdle runState = iota
	stateRunning
	stateCancelling
)

func (s runState) String() string {
	switch s {
	case stateRunning:
		return "running"
	case stateCancelling:
		return "cancelling"
	default:
		return "idle"
	}
}

// Messages delivered to the Bubble Tea loop.
type (
	eventMsg agent.Event

	// approvalRequestMsg opens the modal. It only ever comes from
	// Approver.Confirm - which only the safety gate calls (INV-4) - never
	// from an EventSink event, so no display event can open a modal that
	// approves anything (INV-3, TC-TUI-001).
	approvalRequestMsg struct {
		prompt safety.ApprovalPrompt
		reply  chan<- bool
	}
	approvalWithdrawnMsg struct{ reply chan<- bool }

	runDoneMsg struct {
		report *completion.Report
		err    error
	}
)

// bridge carries messages from other goroutines (the run, the approval
// broker) into the Bubble Tea loop. It is shared by pointer so every copy of
// the model reaches the same program.
type bridge struct {
	mu      sync.Mutex
	send    func(tea.Msg)
	stopped chan struct{}
	once    sync.Once
}

func newBridge() *bridge { return &bridge{stopped: make(chan struct{})} }

func (b *bridge) deliver(msg tea.Msg) bool {
	b.mu.Lock()
	send := b.send
	b.mu.Unlock()
	select {
	case <-b.stopped:
		return false
	default:
	}
	if send == nil {
		return false
	}
	send(msg)
	return true
}

func (b *bridge) stop() { b.once.Do(func() { close(b.stopped) }) }

// sink is the TUI's agent.EventSink: it only forwards events for display.
type sink struct{ b *bridge }

func (s sink) Emit(e agent.Event) { s.b.deliver(eventMsg(e)) }

// approver is the TUI's safety.Approver: it opens the modal and waits for
// the user's answer. Only an explicit approve key returns true.
type approver struct{ b *bridge }

func (a approver) Confirm(ctx context.Context, prompt safety.ApprovalPrompt) (bool, error) {
	reply := make(chan bool, 1)
	if !a.b.deliver(approvalRequestMsg{prompt: prompt, reply: reply}) {
		return false, errors.New("tui is not running")
	}
	select {
	case ok := <-reply:
		return ok, nil
	case <-ctx.Done():
		a.b.deliver(approvalWithdrawnMsg{reply: reply})
		return false, ctx.Err()
	case <-a.b.stopped:
		return false, errors.New("tui exited")
	}
}

// App wires the model to a Bubble Tea program.
type App struct {
	opts    Options
	bridge  *bridge
	program *tea.Program
}

// New builds the TUI. Sink and Approver may be handed to the agent side
// before Run is called; they only deliver once the program is running.
func New(opts Options, programOptions ...tea.ProgramOption) *App {
	b := newBridge()
	app := &App{opts: opts, bridge: b}
	app.program = tea.NewProgram(newModel(opts, b), programOptions...)
	b.mu.Lock()
	b.send = app.program.Send
	b.mu.Unlock()
	return app
}

// Sink returns the display port the agent emits to.
func (a *App) Sink() agent.EventSink { return sink{a.bridge} }

// Approver returns the TUI's approval port.
func (a *App) Approver() safety.Approver { return approver{a.bridge} }

// Run blocks until the user quits. It never returns while an agent run is
// still active: quitting is only possible when idle, and a Ctrl+C during a
// run first cancels it and waits for it to finish.
func (a *App) Run() error {
	defer a.bridge.stop()
	_, err := a.program.Run()
	return err
}

// Key bindings.
var (
	keySubmit  = key.NewBinding(key.WithKeys("enter"))
	keyNewline = key.NewBinding(key.WithKeys("shift+enter", "ctrl+j", "alt+enter"))
	keyCancel  = key.NewBinding(key.WithKeys("ctrl+c"))
	keyApprove = key.NewBinding(key.WithKeys("y", "Y"))
	keyDeny    = key.NewBinding(key.WithKeys("n", "N", "esc"))
	keyScroll  = key.NewBinding(key.WithKeys("pgup", "pgdown", "ctrl+home", "ctrl+end"))
)

type pendingApproval struct {
	prompt safety.ApprovalPrompt
	reply  chan<- bool
}

type model struct {
	opts   Options
	bridge *bridge

	width, height int
	transcript    viewport.Model
	input         textarea.Model

	lines      []string // finished transcript entries
	assistant  strings.Builder
	state      runState
	usage      provider.Usage
	usageSeen  bool
	cancel     context.CancelFunc
	quitOnDone bool

	approvals []pendingApproval // head is shown; the broker sends one at a time
}

func newModel(opts Options, b *bridge) model {
	in := textarea.New()
	in.Placeholder = "Describe a task. Enter to send, Ctrl+J for a new line."
	in.ShowLineNumbers = false
	in.Prompt = "> "
	in.KeyMap.InsertNewline = keyNewline
	in.SetHeight(3)
	in.Focus()

	vp := viewport.New()
	vp.KeyMap = viewport.KeyMap{
		PageUp:   key.NewBinding(key.WithKeys("pgup")),
		PageDown: key.NewBinding(key.WithKeys("pgdown")),
	}
	return model{opts: opts, bridge: b, transcript: vp, input: in, width: 80, height: 24}
}

func (m model) Init() tea.Cmd { return textarea.Blink }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Resizing only re-lays out the screen; it never touches the run.
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil

	case tea.InterruptMsg:
		return m.handleCtrlC()

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case eventMsg:
		m.applyEvent(agent.Event(msg))
		return m, nil

	case approvalRequestMsg:
		m.approvals = append(m.approvals, pendingApproval(msg))
		m.layout()
		return m, nil

	case approvalWithdrawnMsg:
		m.dropApproval(msg.reply)
		return m, nil

	case runDoneMsg:
		return m.finishRun(msg)
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, keyCancel) {
		return m.handleCtrlC()
	}
	if len(m.approvals) > 0 {
		// The modal owns the keyboard: only an explicit y approves.
		switch {
		case key.Matches(msg, keyApprove):
			m.answerApproval(true)
		case key.Matches(msg, keyDeny):
			m.answerApproval(false)
		}
		return m, nil
	}
	if key.Matches(msg, keyScroll) {
		var cmd tea.Cmd
		switch msg.String() {
		case "ctrl+home":
			m.transcript.GotoTop()
		case "ctrl+end":
			m.transcript.GotoBottom()
		default:
			m.transcript, cmd = m.transcript.Update(msg)
		}
		return m, cmd
	}
	if key.Matches(msg, keySubmit) {
		return m.submit()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.layout()
	return m, cmd
}

// handleCtrlC implements spec.md §4.2: while a run is active Ctrl+C cancels
// it; when idle it exits. A pending approval is denied first, so the run is
// never left waiting on a modal that is gone.
func (m model) handleCtrlC() (tea.Model, tea.Cmd) {
	for len(m.approvals) > 0 {
		m.answerApproval(false)
	}
	switch m.state {
	case stateRunning:
		m.state = stateCancelling
		if m.cancel != nil {
			m.cancel()
		}
		m.addLine(styleNotice.Render("Cancelling… (Ctrl+C again exits once the run has stopped)"))
		return m, nil
	case stateCancelling:
		m.quitOnDone = true
		return m, nil
	default:
		return m, tea.Quit
	}
}

func (m model) submit() (tea.Model, tea.Cmd) {
	if m.state != stateIdle {
		return m, nil
	}
	task := strings.TrimSpace(m.input.Value())
	if task == "" {
		// An empty task never reaches the provider (spec.md §9 CT-1).
		return m, nil
	}
	m.input.Reset()
	m.addLine(styleUser.Render("› " + task))
	m.state = stateRunning

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	run := m.opts.Run
	b := m.bridge
	go func() {
		defer cancel()
		rep, err := run(ctx, task, sink{b})
		b.deliver(runDoneMsg{report: rep, err: err})
	}()
	m.layout()
	return m, nil
}

func (m model) finishRun(msg runDoneMsg) (tea.Model, tea.Cmd) {
	m.flushAssistant()
	m.state = stateIdle
	m.cancel = nil
	for len(m.approvals) > 0 {
		m.answerApproval(false)
	}
	status := "unknown"
	if msg.report != nil {
		status = msg.report.Status
	}
	line := fmt.Sprintf("Run %s", status)
	if msg.err != nil {
		line += ": " + msg.err.Error()
		m.addLine(styleError.Render(line))
	} else {
		m.addLine(styleNotice.Render(line))
	}
	if msg.report != nil && len(msg.report.RemainingRisks) > 0 {
		m.addLine(styleNotice.Render("Remaining risks: " + strings.Join(msg.report.RemainingRisks, "; ")))
	}
	if m.quitOnDone {
		return m, tea.Quit
	}
	return m, nil
}

func (m *model) applyEvent(e agent.Event) {
	switch e.Kind {
	case agent.EventAssistantDelta:
		m.assistant.WriteString(e.Text)
	case agent.EventToolStarted:
		m.flushAssistant()
		m.addLine(styleTool.Render("⚙ " + e.ToolName + " …"))
	case agent.EventToolFinished:
		m.addLine(styleTool.Render("✓ " + e.ToolName))
	case agent.EventApprovalNeeded:
		// Display only: the modal is opened by the Approver port, not here.
		m.flushAssistant()
		m.addLine(styleNotice.Render("Approval requested: " + e.Text))
	case agent.EventApprovalResolved:
		m.addLine(styleNotice.Render("Approval: " + e.Text))
	case agent.EventUsageUpdated:
		m.usage, m.usageSeen = e.Usage, true
	case agent.EventRunFinished:
		m.flushAssistant()
	}
	m.refreshTranscript()
}

func (m *model) answerApproval(ok bool) {
	if len(m.approvals) == 0 {
		return
	}
	head := m.approvals[0]
	m.approvals = m.approvals[1:]
	head.reply <- ok // buffered by the approver; never blocks
	m.layout()
}

func (m *model) dropApproval(reply chan<- bool) {
	for i, p := range m.approvals {
		if p.reply == reply {
			m.approvals = append(m.approvals[:i], m.approvals[i+1:]...)
			break
		}
	}
	m.layout()
}

func (m *model) flushAssistant() {
	if m.assistant.Len() == 0 {
		return
	}
	m.lines = append(m.lines, m.assistant.String())
	m.assistant.Reset()
}

func (m *model) addLine(s string) {
	m.lines = append(m.lines, s)
	m.refreshTranscript()
}

func (m *model) refreshTranscript() {
	atBottom := m.transcript.AtBottom()
	entries := m.lines
	if m.assistant.Len() > 0 {
		entries = append(append([]string(nil), m.lines...), m.assistant.String())
	}
	w := max(m.width, 1)
	wrapped := make([]string, len(entries))
	for i, e := range entries {
		wrapped[i] = lipgloss.NewStyle().Width(w).Render(e)
	}
	m.transcript.SetContent(strings.Join(wrapped, "\n"))
	if atBottom {
		m.transcript.GotoBottom()
	}
}

// layout sizes the regions. On a small terminal the status line is dropped
// first; the transcript, the input, and the approval information always keep
// at least one line each (spec.md §4.2, EC-4).
func (m *model) layout() {
	w := max(m.width, 1)
	m.input.SetWidth(w)
	modal := m.modalHeight()
	inputH := 3
	status := 1
	for status+inputH+modal+1 > m.height && inputH > 1 {
		inputH--
	}
	if status+inputH+modal+1 > m.height {
		status = 0
	}
	m.input.SetHeight(inputH)
	m.transcript.SetWidth(w)
	m.transcript.SetHeight(max(m.height-status-inputH-modal, 1))
	m.refreshTranscript()
}

func (m model) modalHeight() int {
	if len(m.approvals) == 0 {
		return 0
	}
	return lipgloss.Height(m.renderModal())
}

func (m model) renderModal() string {
	p := m.approvals[0].prompt
	w := max(m.width, 1)
	body := strings.Join([]string{
		styleModalTitle.Render("Approval required"),
		"Reason:  " + approval.SanitizeForDisplay(p.Reason),
		"Command: " + approval.SanitizeForDisplay(p.Command),
		styleModalKeys.Render("[y] approve once   [n] deny"),
	}, "\n")
	// The modal never truncates the command or the reason: it wraps them to
	// the terminal width so the full text stays visible when narrow.
	return styleModal.Width(w).Render(body)
}

func (m model) renderStatus() string {
	tokens := "tokens -"
	cost := "cost -"
	if m.usageSeen {
		tokens = fmt.Sprintf("tokens %d in / %d out", m.usage.PromptTokens, m.usage.CompletionTokens)
		if m.usage.CostUSD != nil {
			cost = fmt.Sprintf("cost $%.4f", *m.usage.CostUSD)
		}
	}
	state := m.state.String()
	if len(m.approvals) > 0 {
		state = "awaiting approval"
	}
	line := strings.Join([]string{"model " + m.opts.Model, m.opts.Mode, tokens, cost, state}, " │ ")
	return styleStatus.Width(max(m.width, 1)).Render(ansi.Truncate(line, max(m.width, 1), "…"))
}

func (m model) View() tea.View {
	parts := []string{m.transcript.View()}
	if len(m.approvals) > 0 {
		parts = append(parts, m.renderModal())
	}
	if m.height >= 3+m.modalHeight()+m.input.Height() {
		parts = append(parts, m.renderStatus())
	}
	parts = append(parts, m.input.View())
	v := tea.NewView(strings.Join(parts, "\n"))
	v.AltScreen = true
	return v
}

var (
	styleUser       = lipgloss.NewStyle().Bold(true)
	styleTool       = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleNotice     = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	styleError      = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styleStatus     = lipgloss.NewStyle().Reverse(true)
	styleModal      = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleModalTitle = lipgloss.NewStyle().Bold(true)
	styleModalKeys  = lipgloss.NewStyle().Bold(true)
)
