// Package agent defines the frozen model-facing agent contract (spec.md
// §5.2). The Agent interface is implemented by a bridge that launches and
// manages the pi --mode rpc subprocess (see internal/pirpc) and turns Pi's
// RPC events into Event values; the display layer (TUI or plain text)
// consumes the same Event stream through EventSink. The Agent interface
// itself never changes, so a sink or a future implementation can be
// swapped without touching the presentation layer.
package agent

import (
	"context"
	"time"

	"github.com/bext1998/brunel/internal/completion"
	"github.com/bext1998/brunel/internal/provider"
)

// Agent runs one model-facing task and streams the resulting events to sink.
// The concrete implementation lives in the bridge (internal/pirpc launches
// the subprocess; this package only holds the frozen contract). sink is
// display-only: it never authorizes a tool or changes the agent's decision
// (spec.md §5.2, INV-3).
type Agent interface {
	Run(ctx context.Context, task string, sink EventSink) (*completion.Report, error)
}

// EventSink receives translated, UI-neutral events for display only. TUI
// and plain-text output must consume the same event source.
type EventSink interface {
	Emit(Event)
}

// Event is the frozen, UI-neutral event the sink consumes (spec.md §5.2).
// Its Kind selects which display path handles it; the remaining fields are
// only meaningful for the kinds that use them.
type Event struct {
	Kind       EventKind
	Timestamp  time.Time
	Text       string
	ToolCallID string
	ToolName   string
	Usage      provider.Usage
}

// EventKind enumerates the display events a sink may receive. The approval
// kinds only mark the "needs approval" and "approval resolved" timepoints
// for display; the actual Approver.Confirm decision is made by the safety
// decision entry point, not here.
type EventKind string

const (
	EventAssistantDelta   EventKind = "assistant_delta"
	EventToolStarted      EventKind = "tool_started"
	EventToolFinished     EventKind = "tool_finished"
	EventApprovalNeeded   EventKind = "approval_needed"
	EventApprovalResolved EventKind = "approval_resolved"
	EventUsageUpdated     EventKind = "usage_updated"
	EventRunFinished      EventKind = "run_finished"
)
