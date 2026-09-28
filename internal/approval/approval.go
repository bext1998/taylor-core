// Package approval carries CONFIRM approval requests across the process
// boundary ADR-002 Route B introduces (DECISIONS.md 2026-09-28).
//
// The single safety decision entry point (safety.Gate.Decide) runs inside
// the `brunel --taylor-tool` subprocess that Pi's taylor-tools.ts extension
// spawns, which has no TTY. The TTY - and the TUI modal or plain-text
// prompt - lives in the host `brunel` process. Broker (host side) and
// Client (subprocess side) connect the two over a per-run Windows named
// pipe:
//
//	host brunel ── Broker ◄──pipe── Client ── Gate.Decide (brunel --taylor-tool)
//	   │                                            ▲
//	   └── TUI / plain-text Approver                └── spawned by pi → taylor-tools.ts
//
// Client is a safety.Approver that only the Gate calls (INV-4); Broker is a
// relay that shows the Gate's own ApprovalPrompt to the human and returns
// the answer. Neither side classifies or authorizes anything on its own, and
// every failure (broker unreachable, bad token, malformed reply) is a denial.
package approval

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
)

const (
	// EnvPipe names the environment variable carrying the broker's pipe
	// path from the host, through pi and the Node extension, to the
	// --taylor-tool subprocess.
	EnvPipe = "BRUNEL_APPROVAL_PIPE"
	// EnvToken names the environment variable carrying the per-run secret
	// the broker requires on every request.
	EnvToken = "BRUNEL_APPROVAL_TOKEN"

	// maxMessageBytes bounds one request or reply line. A command is shown
	// to the human in full, so the cap is generous; it only exists so a
	// misbehaving peer cannot make the other side buffer without limit.
	maxMessageBytes = 1 << 20
)

// ErrUnsupportedPlatform is returned where named pipes are unavailable.
// Brunel only targets Windows (spec.md §2.2); on other platforms no broker
// runs and CONFIRM decisions fail closed with E_APPROVAL_REQUIRED_NO_TTY.
var ErrUnsupportedPlatform = errors.New("approval channel requires Windows named pipes")

// request is one approval request line sent by Client.
type request struct {
	Token   string `json:"token"`
	Command string `json:"command"`
	Reason  string `json:"reason"`
}

// reply is the broker's single answer line.
type reply struct {
	Approved bool   `json:"approved"`
	Error    string `json:"error,omitempty"`
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
