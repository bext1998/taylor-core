package main

import (
	"context"
	"testing"

	"github.com/bext1998/brunel/internal/safety"
)

// blockingApprover holds Confirm open until released, so a test can look at
// the relay while a decision is outstanding.
type blockingApprover struct {
	entered chan struct{}
	release chan bool
}

func (b *blockingApprover) Confirm(ctx context.Context, _ safety.ApprovalPrompt) (bool, error) {
	close(b.entered)
	select {
	case ok := <-b.release:
		return ok, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// The report's pending_approval is whatever is waiting for a decision right
// now: present while the user has not answered, gone once they have.
func TestRelayApproverReportsPendingOnlyWhileUndecided(t *testing.T) {
	inner := &blockingApprover{entered: make(chan struct{}), release: make(chan bool)}
	relay := &relayApprover{inner: inner, sink: &eventLog{}}
	if relay.Pending() != nil {
		t.Fatal("pending before any request")
	}
	done := make(chan struct{})
	go func() {
		_, _ = relay.Confirm(context.Background(), safety.ApprovalPrompt{Command: "git push", Reason: "network write"})
		close(done)
	}()
	<-inner.entered
	p := relay.Pending()
	if p == nil || p.Command != "git push" || p.Reason != "network write" {
		t.Fatalf("Pending() = %+v while awaiting a decision", p)
	}
	inner.release <- true
	<-done
	if relay.Pending() != nil {
		t.Fatal("still pending after the user answered")
	}
}

// If the run is cancelled while the question is open, nothing was decided:
// the command stays recorded as the one that was waiting.
func TestRelayApproverKeepsPendingWhenCancelled(t *testing.T) {
	inner := &blockingApprover{entered: make(chan struct{}), release: make(chan bool)}
	relay := &relayApprover{inner: inner, sink: &eventLog{}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, _ = relay.Confirm(ctx, safety.ApprovalPrompt{Command: "git push", Reason: "r"})
		close(done)
	}()
	<-inner.entered
	cancel()
	<-done
	if relay.Pending() == nil {
		t.Fatal("a cancelled, undecided approval was dropped from the report")
	}
}
