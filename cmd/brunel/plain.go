package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/bext1998/brunel/internal/agent"
	"github.com/bext1998/brunel/internal/safety"
)

// plainSink is the plain-text agent.EventSink. The model's text streams to
// stdout unchanged, so `brunel "<task>" > out.txt` captures just the
// answer; tool activity and notices go to stderr. It never writes terminal
// control sequences (no alternate screen, spec.md §3.2). Emit may be called
// from the run loop and the approval broker concurrently.
type plainSink struct {
	mu       sync.Mutex
	out, log io.Writer
	midLine  bool // stdout has text without a trailing newline
}

func newPlainSink(out, log io.Writer) *plainSink {
	return &plainSink{out: out, log: log}
}

func (s *plainSink) Emit(e agent.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch e.Kind {
	case agent.EventAssistantDelta:
		if e.Text == "" {
			return
		}
		_, _ = io.WriteString(s.out, e.Text)
		s.midLine = !strings.HasSuffix(e.Text, "\n")
	case agent.EventToolStarted:
		s.endLineLocked()
		_, _ = fmt.Fprintf(s.log, "[tool] %s started\n", e.ToolName)
	case agent.EventToolFinished:
		_, _ = fmt.Fprintf(s.log, "[tool] %s finished\n", e.ToolName)
	case agent.EventApprovalResolved:
		// EventApprovalNeeded is not printed: the TTY approver's prompt
		// already shows the command and the reason.
		_, _ = fmt.Fprintf(s.log, "[approval] %s\n", e.Text)
	case agent.EventRunFinished:
		s.endLineLocked()
	}
}

// warn prints a notice line on stderr.
func (s *plainSink) warn(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.endLineLocked()
	_, _ = fmt.Fprintf(s.log, "brunel: %s\n", msg)
}

// finish terminates a trailing partial line of model output.
func (s *plainSink) finish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.endLineLocked()
}

func (s *plainSink) endLineLocked() {
	if s.midLine {
		_, _ = io.WriteString(s.out, "\n")
		s.midLine = false
	}
}

// ttyApprover is the plain-text safety.Approver for a run with a TTY on
// stdin: it prints the command and the reason and reads one answer line.
// Only "y" or "yes" approves; anything else, EOF, or cancellation denies.
// Nothing is remembered between prompts (spec.md §6.2).
type ttyApprover struct {
	in  *bufio.Reader
	out io.Writer
}

func newTTYApprover(in io.Reader, out io.Writer) *ttyApprover {
	return &ttyApprover{in: bufio.NewReader(in), out: out}
}

func (a *ttyApprover) Confirm(ctx context.Context, p safety.ApprovalPrompt) (bool, error) {
	_, _ = fmt.Fprintf(a.out, "\n[approval required] %s\n  command: %s\nApprove this command once? [y/N] ", p.Reason, p.Command)

	answer := make(chan string, 1)
	go func() {
		line, err := a.in.ReadString('\n')
		if err != nil && line == "" {
			answer <- ""
			return
		}
		answer <- line
	}()
	select {
	case line := <-answer:
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			return true, nil
		}
		return false, nil
	case <-ctx.Done():
		_, _ = fmt.Fprintln(a.out)
		return false, ctx.Err()
	}
}

// relayApprover wraps the host approver the approval broker calls. It only
// adds the display events spec.md §5.2 asks the sink to show (request and
// result); the decision is still the wrapped Approver's, called on behalf of
// the safety gate in the --taylor-tool subprocess.
type relayApprover struct {
	inner safety.Approver
	sink  agent.EventSink
}

func (r *relayApprover) Confirm(ctx context.Context, p safety.ApprovalPrompt) (bool, error) {
	r.sink.Emit(agent.Event{Kind: agent.EventApprovalNeeded, Timestamp: time.Now(), Text: p.Command})
	ok, err := r.inner.Confirm(ctx, p)
	result := "denied: " + p.Command
	if err == nil && ok {
		result = "approved once: " + p.Command
	}
	r.sink.Emit(agent.Event{Kind: agent.EventApprovalResolved, Timestamp: time.Now(), Text: result})
	return ok, err
}
