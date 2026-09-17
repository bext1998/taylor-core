// Package provider carries the provider-layer facts Brunel keeps about a Pi
// run. Per spec.md §5.3 (ADR-002) the provider abstraction itself is
// delegated to Pi; Brunel only retains the smallest facts it needs to fill
// the frozen completion.Report (spec.md §8) and to surface usage updates to
// a display layer. This package is a leaf with no internal dependencies.
package provider

// Usage is the minimal provider-reported usage snapshot for a Pi run. Pi
// reports cumulative usage on every message_update; Brunel keeps the two
// token buckets and the (optional) cost so the completion Report's
// CostSummary can be filled best-effort. CostUSD is a pointer so an
// unknown/zero cost (Pi may report cost as null for some providers) stays
// distinguishable from a reported zero.
type Usage struct {
	// PromptTokens is Pi's usage.input (the input token count).
	PromptTokens int
	// CompletionTokens is Pi's usage.output (the output token count).
	CompletionTokens int
	// CostUSD is Pi's usage.cost.total, when the provider reports one.
	CostUSD *float64
}
