package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/bext1998/brunel/internal/agent"
	"github.com/bext1998/brunel/internal/safety"
)

func TestParseCLIFlagsAroundTask(t *testing.T) {
	for _, args := range [][]string{
		{"fix the bug", "--report", "out.json", "--mode", "readonly"},
		{"--mode", "readonly", "fix the bug", "--report", "out.json"},
		{"--report=out.json", "--mode=readonly", "--", "fix the bug"},
	} {
		opts, err := parseCLI(args)
		if err != nil {
			t.Fatalf("parseCLI(%q) error = %v", args, err)
		}
		if !opts.hasTask || opts.task != "fix the bug" || opts.report != "out.json" || opts.mode == nil || *opts.mode != "readonly" {
			t.Fatalf("parseCLI(%q) = %+v", args, opts)
		}
	}
}

func TestParseCLIDoubleDashKeepsFlagLikeTask(t *testing.T) {
	opts, err := parseCLI([]string{"--model", "m", "--", "--mode is a word here"})
	if err != nil || opts.task != "--mode is a word here" || opts.mode != nil {
		t.Fatalf("parseCLI = %+v, %v", opts, err)
	}
}

func TestParseCLIRejectsInvalidInvocations(t *testing.T) {
	for name, args := range map[string][]string{
		"empty task":          {""},
		"blank task":          {"  \t "},
		"two positionals":     {"fix", "bug"},
		"bad mode":            {"--mode", "benchmark", "task"},
		"empty model":         {"--model", " ", "task"},
		"report without task": {"--report", "out.json"},
		"unknown flag":        {"--yolo", "task"},
	} {
		if _, err := parseCLI(args); err == nil {
			t.Errorf("%s: parseCLI(%q) succeeded", name, args)
		} else if code, exit := errorCode(err); code != errInvalidArgument || exit != exitInvalid {
			t.Errorf("%s: error code = %s/%d, want %s/%d", name, code, exit, errInvalidArgument, exitInvalid)
		}
	}
}

func TestParseCLIOmittedFlagsStayNil(t *testing.T) {
	opts, err := parseCLI(nil)
	if err != nil || opts.hasTask || opts.mode != nil || opts.model != nil || opts.name != nil {
		t.Fatalf("parseCLI(nil) = %+v, %v", opts, err)
	}
}

func TestHelpPrintsUsage(t *testing.T) {
	var out bytes.Buffer
	env := cliEnv{stdout: &out, stderr: &out}
	if code := runCLI([]string{"--help"}, env); code != exitOK {
		t.Fatalf("--help exit = %d", code)
	}
	for _, want := range []string{"brunel [flags]", "--report", "changes with the pi version", "Exit codes"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("usage missing %q:\n%s", want, out.String())
		}
	}
}

func TestPlainSinkSeparatesAnswerFromActivity(t *testing.T) {
	var out, log bytes.Buffer
	s := newPlainSink(&out, &log)
	s.Emit(agent.Event{Kind: agent.EventAssistantDelta, Text: "Looking"})
	s.Emit(agent.Event{Kind: agent.EventToolStarted, ToolName: "read_file"})
	s.Emit(agent.Event{Kind: agent.EventToolFinished, ToolName: "read_file"})
	s.Emit(agent.Event{Kind: agent.EventAssistantDelta, Text: "Done."})
	s.Emit(agent.Event{Kind: agent.EventRunFinished})
	s.finish()
	if out.String() != "Looking\nDone.\n" {
		t.Fatalf("stdout = %q", out.String())
	}
	if !strings.Contains(log.String(), "[tool] read_file started") || !strings.Contains(log.String(), "[tool] read_file finished") {
		t.Fatalf("stderr = %q", log.String())
	}
	if strings.Contains(out.String()+log.String(), "\x1b[") {
		t.Fatal("plain-text mode wrote a terminal control sequence")
	}
}

func TestTTYApproverApprovesOnlyYes(t *testing.T) {
	for input, want := range map[string]bool{
		"y\n": true, "YES\n": true, "n\n": false, "\n": false, "yep\n": false, "": false,
	} {
		var out bytes.Buffer
		a := newTTYApprover(strings.NewReader(input), &out)
		got, err := a.Confirm(context.Background(), safety.ApprovalPrompt{Command: "git push", Reason: "git state change"})
		if err != nil || got != want {
			t.Errorf("answer %q: Confirm = (%v, %v), want %v", input, got, err, want)
		}
		if !strings.Contains(out.String(), "git push") || !strings.Contains(out.String(), "git state change") {
			t.Errorf("prompt did not show command and reason: %q", out.String())
		}
	}
}

func TestTTYApproverCancel(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ok, err := newTTYApprover(r, &bytes.Buffer{}).Confirm(ctx, safety.ApprovalPrompt{Command: "x", Reason: "y"})
	if ok || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Confirm = (%v, %v)", ok, err)
	}
}

type recordingApprover struct {
	answer bool
	calls  int
}

func (r *recordingApprover) Confirm(context.Context, safety.ApprovalPrompt) (bool, error) {
	r.calls++
	return r.answer, nil
}

type eventLog struct{ events []agent.Event }

func (l *eventLog) Emit(e agent.Event) { l.events = append(l.events, e) }

func TestRelayApproverEmitsDisplayEventsAndKeepsDecision(t *testing.T) {
	for _, answer := range []bool{true, false} {
		inner := &recordingApprover{answer: answer}
		log := &eventLog{}
		got, err := (&relayApprover{inner: inner, sink: log}).Confirm(context.Background(), safety.ApprovalPrompt{Command: "git push", Reason: "r"})
		if err != nil || got != answer || inner.calls != 1 {
			t.Fatalf("relay = (%v, %v) calls=%d, want %v once", got, err, inner.calls, answer)
		}
		if len(log.events) != 2 || log.events[0].Kind != agent.EventApprovalNeeded || log.events[1].Kind != agent.EventApprovalResolved {
			t.Fatalf("relay events = %+v", log.events)
		}
	}
}
