//go:build windows

package approval

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bext1998/brunel/internal/safety"
)

// fakeApprover records every prompt it is shown and answers with answer.
// It also tracks how many Confirm calls overlap.
type fakeApprover struct {
	mu      sync.Mutex
	prompts []safety.ApprovalPrompt
	answer  bool
	hold    chan struct{} // when non-nil, Confirm waits for it or ctx

	inFlight atomic.Int32
	maxSeen  atomic.Int32
}

func (f *fakeApprover) Confirm(ctx context.Context, p safety.ApprovalPrompt) (bool, error) {
	n := f.inFlight.Add(1)
	defer f.inFlight.Add(-1)
	for {
		m := f.maxSeen.Load()
		if n <= m || f.maxSeen.CompareAndSwap(m, n) {
			break
		}
	}
	f.mu.Lock()
	f.prompts = append(f.prompts, p)
	f.mu.Unlock()
	if f.hold != nil {
		select {
		case <-f.hold:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	return f.answer, nil
}

func (f *fakeApprover) seen() []safety.ApprovalPrompt {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]safety.ApprovalPrompt(nil), f.prompts...)
}

func startTestBroker(t *testing.T, approver safety.Approver) (*Broker, *Client) {
	t.Helper()
	b, err := StartBroker(context.Background(), approver)
	if err != nil {
		t.Fatalf("StartBroker: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	env := b.Env()
	return b, &Client{pipe: env[EnvPipe], token: env[EnvToken], dial: dialPipe}
}

func TestBrokerRelaysPromptAndAnswer(t *testing.T) {
	for _, answer := range []bool{true, false} {
		fake := &fakeApprover{answer: answer}
		_, client := startTestBroker(t, fake)
		prompt := safety.ApprovalPrompt{Command: "git push origin main", Reason: "git state change"}
		got, err := client.Confirm(context.Background(), prompt)
		if err != nil {
			t.Fatalf("Confirm: %v", err)
		}
		if got != answer {
			t.Fatalf("Confirm = %v, want %v", got, answer)
		}
		if seen := fake.seen(); len(seen) != 1 || seen[0] != prompt {
			t.Fatalf("host approver saw %#v, want exactly %#v", seen, prompt)
		}
	}
}

func TestBrokerRejectsWrongTokenWithoutPrompting(t *testing.T) {
	fake := &fakeApprover{answer: true}
	_, client := startTestBroker(t, fake)
	client.token = "not-the-token"
	approved, err := client.Confirm(context.Background(), safety.ApprovalPrompt{Command: "git push", Reason: "r"})
	if err == nil || approved {
		t.Fatalf("Confirm with wrong token = (%v, %v), want denial with error", approved, err)
	}
	if len(fake.seen()) != 0 {
		t.Fatalf("host approver was prompted for an unauthenticated request")
	}
}

func TestBrokerSerializesConcurrentPrompts(t *testing.T) {
	fake := &fakeApprover{answer: true, hold: make(chan struct{})}
	_, client := startTestBroker(t, fake)

	const n = 4
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := client.Confirm(context.Background(), safety.ApprovalPrompt{Command: "git push", Reason: "r"})
			if err == nil && !ok {
				err = errors.New("unexpected denial")
			}
			errs <- err
		}()
	}
	// Release prompts one at a time.
	for i := 0; i < n; i++ {
		deadline := time.Now().Add(5 * time.Second)
		for len(fake.seen()) <= i {
			if time.Now().After(deadline) {
				t.Fatalf("prompt %d never reached the host approver", i+1)
			}
			time.Sleep(5 * time.Millisecond)
		}
		fake.hold <- struct{}{}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Confirm: %v", err)
		}
	}
	if max := fake.maxSeen.Load(); max != 1 {
		t.Fatalf("host approver saw %d overlapping prompts, want 1", max)
	}
}

func TestBrokerCloseDeniesOpenPrompt(t *testing.T) {
	fake := &fakeApprover{answer: true, hold: make(chan struct{})}
	b, client := startTestBroker(t, fake)

	result := make(chan error, 1)
	go func() {
		ok, err := client.Confirm(context.Background(), safety.ApprovalPrompt{Command: "git push", Reason: "r"})
		if err == nil && ok {
			err = errors.New("prompt was approved after Close")
		}
		result <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(fake.seen()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("prompt never reached the host approver")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("Confirm returned no error after the broker closed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Confirm did not return after the broker closed")
	}
	// After Close nobody is listening: a new request fails closed.
	if ok, err := client.Confirm(context.Background(), safety.ApprovalPrompt{Command: "x", Reason: "r"}); err == nil || ok {
		t.Fatalf("Confirm after Close = (%v, %v), want denial with error", ok, err)
	}
}

func TestClientFromEnvConsumesVariables(t *testing.T) {
	t.Setenv(EnvPipe, `\\.\pipe\brunel-approval-test`)
	t.Setenv(EnvToken, "secret-token")
	c := ClientFromEnv()
	if c == nil || c.pipe != `\\.\pipe\brunel-approval-test` || c.token != "secret-token" {
		t.Fatalf("ClientFromEnv = %#v", c)
	}
	for _, name := range []string{EnvPipe, EnvToken} {
		if _, ok := os.LookupEnv(name); ok {
			t.Fatalf("%s still set after ClientFromEnv; pwsh children would inherit it", name)
		}
	}
	if c := ClientFromEnv(); c != nil {
		t.Fatalf("ClientFromEnv without variables = %#v, want nil (no TTY)", c)
	}
}

// TestGateAsksHostThroughChannel is the INV-4 path end to end: only the
// Gate calls the Client, the host approver sees the Gate's own command and
// reason, and a host decline is E_APPROVAL_DENIED.
func TestGateAsksHostThroughChannel(t *testing.T) {
	for _, answer := range []bool{true, false} {
		fake := &fakeApprover{answer: answer}
		_, client := startTestBroker(t, fake)
		gate := safety.NewGate(safety.ModeWorkspace, client, t.TempDir())
		decision, err := gate.Decide(context.Background(), safety.ToolCall{Tool: "run_powershell", Command: "git push origin main"})
		seen := fake.seen()
		if len(seen) != 1 || seen[0].Command != "git push origin main" || seen[0].Reason == "" {
			t.Fatalf("host approver saw %#v, want the gate's command with a reason", seen)
		}
		if answer {
			if err != nil || decision.Risk != safety.RiskConfirm {
				t.Fatalf("approved Decide = (%#v, %v), want RiskConfirm", decision, err)
			}
		} else if !errors.Is(err, safety.ErrApprovalDenied) {
			t.Fatalf("declined Decide error = %v, want E_APPROVAL_DENIED", err)
		}
	}
}

// TestGateAutoNeverUsesChannel keeps AC-9: an AUTO decision makes no
// approval request at all.
func TestGateAutoNeverUsesChannel(t *testing.T) {
	fake := &fakeApprover{answer: true}
	_, client := startTestBroker(t, fake)
	gate := safety.NewGate(safety.ModeWorkspace, client, t.TempDir())
	if _, err := gate.Decide(context.Background(), safety.ToolCall{Tool: "run_powershell", Command: "go test ./..."}); err != nil {
		t.Fatalf("Decide(AUTO): %v", err)
	}
	if len(fake.seen()) != 0 {
		t.Fatalf("AUTO decision prompted the host: %#v", fake.seen())
	}
}
