package config

import (
	"errors"
	"fmt"
)

type Error struct {
	Code   string
	Source string
	// Message is an optional secret-free hint appended to the error text.
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	text := e.Code
	if e.Source != "" {
		text = fmt.Sprintf("%s (%s)", e.Code, e.Source)
	}
	if e.Message != "" {
		text += ": " + e.Message
	}
	return text
}

func (e *Error) Unwrap() error { return e.Cause }

func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && e != nil && t != nil && e.Code == t.Code
}

var (
	ErrConfigInvalid       = &Error{Code: "E_CONFIG_INVALID"}
	ErrConfigCredential    = &Error{Code: "E_CONFIG_CREDENTIAL"}
	ErrUnsupportedPlatform = &Error{Code: "E_UNSUPPORTED_PLATFORM"}
)

// ErrCredentialNotFound is returned by a CredentialSource when no OpenRouter
// credential is stored at all. Load treats it as "no key" rather than a
// failure: since spec v1.3 §5.3 Pi resolves credentials for providers Brunel
// holds no key for, so a missing OpenRouter key only matters when the run
// actually targets OpenRouter (the CLI enforces that). Any other read error
// is still E_CONFIG_CREDENTIAL.
var ErrCredentialNotFound = errors.New("OpenRouter credential not found")

// ErrCredentialInvalid is returned by a CredentialSource when a credential is
// stored but is not usable key text (for example a blob that is neither UTF-8
// nor UTF-16 without NUL). It is distinct from ErrCredentialNotFound: the user
// has a key saved, it just cannot be read, so Load reports E_CONFIG_CREDENTIAL
// with a hint instead of silently treating the key as absent.
var ErrCredentialInvalid = errors.New("OpenRouter credential is not readable text")

func configError(code, source string, cause error) error {
	return &Error{Code: code, Source: source, Cause: cause}
}

func ErrorCode(err error) string {
	var coded *Error
	if errors.As(err, &coded) {
		return coded.Code
	}
	return ""
}
