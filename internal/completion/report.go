// Package completion holds the frozen completion.Report schema (spec.md §8)
// and its value types. Brunel writes a Report at the end of a run to the
// workspace; this package is a leaf with no internal dependencies.
package completion

// SchemaVersion is the frozen completion.Report schema version (spec.md §8).
const SchemaVersion = "1.0"

// Run status values a Report.Status carries. They mirror spec.md §8:
// completed = the model ended normally with every tool call terminal and no
// pending approval; incomplete = the user cancelled, declined approval, or
// the model said it cannot continue; failed = a provider/protocol/workspace
// or other unrecoverable error terminated the run.
const (
	StatusCompleted  = "completed"
	StatusIncomplete = "incomplete"
	StatusFailed     = "failed"
)

// Report is the frozen completion schema (spec.md §8). The harness records
// only observable facts; it never claims a completed report means the
// user's need was correctly met. Fields that require work #14 owns (the
// workspace diff, structured tool failures, a completion verification
// engine) are intentionally left empty by #9's bridge.
type Report struct {
	SchemaVersion   string         `json:"schema_version"` // "1.0"
	SessionID       string         `json:"session_id"`
	Task            string         `json:"task"`
	Status          string         `json:"status"` // completed | incomplete | failed
	ModifiedFiles   []string       `json:"modified_files"`
	Diff            string         `json:"diff"`
	Verifications   []Verification `json:"verifications"`
	ToolFailures    []ToolFailure  `json:"tool_failures"`
	PendingApproval *ApprovalFact  `json:"pending_approval,omitempty"`
	RemainingRisks  []string       `json:"remaining_risks"`
	Cost            CostSummary    `json:"cost"`
}

// Verification records one verification command the harness ran and its
// outcome. Spec.md §8: a non-zero exit is only recorded as a fact; it does
// not by itself decide the run status.
type Verification struct {
	Command  string `json:"command"`
	ExitCode int    `json:"exit_code"`
	Summary  string `json:"summary"`
}

// ApprovalFact records the command that is awaiting (or just resolved by)
// an approval decision. It is the display-facing fact the frozen
// EventSink surfaces; the actual Approver.Confirm decision lives in
// internal/safety, not here.
type ApprovalFact struct {
	Command string `json:"command"`
	Reason  string `json:"reason"`
}

// CostSummary is the frozen cost block of the completion Report (spec.md
// §8). Turns counts model turns observed over the run; DurationSec is the
// wall-clock seconds of the run.
type CostSummary struct {
	PromptTokens     int      `json:"prompt_tokens"`
	CompletionTokens int      `json:"completion_tokens"`
	CostUSD          *float64 `json:"cost_usd"`
	DurationSec      float64  `json:"duration_sec"`
	Turns            int      `json:"turns"`
}

// ToolFailure records one tool call that failed. Spec.md §8 leaves the
// ToolFailure shape to the harness; the minimal fields are the tool name
// and the failure message.
type ToolFailure struct {
	Tool  string `json:"tool"`
	Error string `json:"error"`
}
