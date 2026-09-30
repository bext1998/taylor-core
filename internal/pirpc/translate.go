package pirpc

import (
	"strings"

	"github.com/bext1998/brunel/internal/redact"
)

// ProviderErrorReport is the minimal shape of a provider-layer failure
// reported over Pi's RPC protocol that #9's event loop will decode: an
// optional machine-readable Kind (whatever short category string, if any,
// Pi's own error event carries) and the human-readable Message it reported.
// Neither field's exact wire format is specified by Pi - this package does
// not parse RPC JSON itself (that is Issue #9's responsibility) - so
// TranslateProviderError is deliberately best-effort, the same spirit as
// internal/safety's command classification (spec.md §5.3/§9 CT-6: "Brunel
// 不重新實作重試邏輯，也不得覆蓋或攔截 Pi 已決定的重試／放棄行為" - this
// package only labels a failure for display, it never retries or hides it).
type ProviderErrorReport struct {
	Kind    string
	Message string
}

// knownKinds maps normalized, Pi-reported category strings that are
// reasonably expected for an error event to Brunel's stable codes. Real
// values are confirmed against Pi's actual RPC protocol as #9 implements
// the event decoder; unrecognized or empty Kind falls back to scanning
// Message.
var knownKinds = map[string]*Error{
	"auth":               ErrPiProviderAuth,
	"authentication":     ErrPiProviderAuth,
	"unauthorized":       ErrPiProviderAuth,
	"invalid_api_key":    ErrPiProviderAuth,
	"quota":              ErrPiProviderQuota,
	"rate_limit":         ErrPiProviderQuota,
	"insufficient_quota": ErrPiProviderQuota,
	"model_not_found":    ErrPiModelNotFound,
	"invalid_model":      ErrPiModelNotFound,
	"protocol":           ErrProviderProtocol,
	"malformed":          ErrProviderProtocol,
}

// messageKeywords is the fallback used when Kind is empty or unrecognized:
// a best-effort substring scan over Message, checked in this order.
var messageKeywords = []struct {
	code     *Error
	keywords []string
}{
	{ErrPiProviderAuth, []string{"unauthorized", "invalid api key", "invalid_api_key", "no api key", "authentication failed", "forbidden", "401", "403"}},
	{ErrPiProviderQuota, []string{"quota", "rate limit", "rate_limit", "insufficient credits", "429", "too many requests"}},
	{ErrPiModelNotFound, []string{"model not found", "unknown model", "unsupported model", "no such model", "not a valid model", "invalid model"}},
	{ErrProviderProtocol, []string{"malformed", "unexpected response", "protocol error", "invalid json", "parse error"}},
}

// TranslateProviderError maps one provider-layer failure Pi reported over
// RPC to a stable Brunel error code.
//
// Classification runs on the *raw* report (Kind, then a keyword scan over
// the original Message): a provider may echo a credential assignment on the
// same line as the error category ("OPENROUTER_API_KEY=... : unauthorized"),
// and masking that line first would delete the category with it.
//
// Only the message placed in the returned (public) error is masked, via
// redact.Secrets: a provider is free to echo an Authorization header or API
// key back in its error text, and spec.md §9 forbids a public error from
// carrying an API key, Authorization header, or unmasked known secret.
// knownSecrets are exact credential values the caller already holds for
// this launch (e.g. the injected API key); they are redacted by exact
// match, covering provider key formats the heuristics do not recognize.
// For providers whose key Brunel never sees (Pi discovers it itself) only
// the heuristics apply - see redact.Secrets.
//
// It never retries and never suppresses the failure - the caller (Issue
// #9's event loop) surfaces the returned error as-is.
func TranslateProviderError(report ProviderErrorReport, knownSecrets ...string) error {
	coded := classifyProviderError(report)
	return codeError(coded.Code, redact.Secrets(report.Message, knownSecrets...), nil)
}

// classifyProviderError picks the stable code for report from its raw
// (unmasked) fields: Kind first, then an ordered keyword scan over Message,
// falling back to the generic provider error.
func classifyProviderError(report ProviderErrorReport) *Error {
	if coded, ok := knownKinds[normalizeKind(report.Kind)]; ok {
		return coded
	}
	lower := strings.ToLower(report.Message)
	for _, entry := range messageKeywords {
		for _, keyword := range entry.keywords {
			if strings.Contains(lower, keyword) {
				return entry.code
			}
		}
	}
	return ErrPiProviderError
}

func normalizeKind(kind string) string {
	return strings.ToLower(strings.TrimSpace(kind))
}
