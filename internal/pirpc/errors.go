// Package pirpc implements the passthrough and translation responsibilities
// Issue #8 (F-7, ADR-002) assigns to Brunel for the Pi RPC subprocess:
// building the `pi --mode rpc` launch arguments from the user's chosen
// provider/model, injecting credentials via environment variables (never a
// project file), and translating Pi-reported provider-layer errors into
// Brunel's own stable error codes. Brunel does not re-implement SSE
// parsing, tool-call probing, or retry/backoff - that is delegated to Pi.
//
// Starting and managing the Pi RPC subprocess itself, and translating its
// RPC *events* (message/tool-call/usage) into agent.Event, is Issue #9's
// responsibility; this package only provides the pieces #9 builds on. Per
// INV-9 (spec.md §10, `[FROZEN]`), no code in this package may ever send a
// bash-type RPC command (the JSON `type` field literally set to `bash`) -
// see bash_guard_test.go.
package pirpc

import (
	"errors"
	"fmt"
)

// Error is a stable, machine-readable pirpc error.
type Error struct {
	Code    string
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Message == "" {
		return e.Code
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Cause }

func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && e != nil && t != nil && e.Code == t.Code
}

var (
	ErrInvalidArgument = &Error{Code: "E_INVALID_ARGUMENT"}

	// Provider-layer error codes a Pi RPC error is translated to
	// (spec.md §5.3/§9 CT-6): authentication, quota/rate-limit, an
	// unknown/unsupported model, and a malformed or unexpected RPC
	// protocol response. ErrPiProviderError is the fallback for a
	// provider-layer failure that does not match any of those.
	//
	// ErrProviderProtocol's code is fixed by spec.md §11 EC-11, which
	// names E_PROVIDER_PROTOCOL as the public code for a malformed SSE /
	// duplicate tool ID / unknown finish reason; it is deliberately not
	// Pi-prefixed like the others so a spec-driven consumer matches it.
	ErrPiProviderAuth   = &Error{Code: "E_PI_PROVIDER_AUTH"}
	ErrPiProviderQuota  = &Error{Code: "E_PI_PROVIDER_QUOTA"}
	ErrPiModelNotFound  = &Error{Code: "E_PI_MODEL_NOT_FOUND"}
	ErrProviderProtocol = &Error{Code: "E_PROVIDER_PROTOCOL"}
	ErrPiProviderError  = &Error{Code: "E_PI_PROVIDER_ERROR"}

	// ErrCredentialProviderMismatch is returned by InjectCredentials when
	// the credential handed in was resolved for a different provider than
	// the one being launched; the key is not injected (see env.go).
	ErrCredentialProviderMismatch = &Error{Code: "E_PI_CREDENTIAL_MISMATCH"}

	// ErrPiRuntimeRequired is returned by Start when the Pi runtime
	// (Node.js/npm and the pi executable) is missing or cannot be
	// launched. Per spec.md §11 EC-13 the code is fixed as
	// E_RUNTIME_REQUIRED (not Pi-prefixed) so a spec-driven consumer
	// matches it; EC-13 names it for a missing Node.js/npm or a pi that
	// fails to start.
	ErrPiRuntimeRequired = &Error{Code: "E_RUNTIME_REQUIRED"}
)

func codeError(code, message string, cause error) error {
	return &Error{Code: code, Message: message, Cause: cause}
}

// ErrorCode returns a stable error code when err is a pirpc Error.
func ErrorCode(err error) string {
	var coded *Error
	if errors.As(err, &coded) {
		return coded.Code
	}
	return ""
}
