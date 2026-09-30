package pirpc

import (
	"strings"
	"testing"
)

func TestTranslateProviderErrorByKind(t *testing.T) {
	cases := map[string]string{
		"auth":            ErrPiProviderAuth.Code,
		"authentication":  ErrPiProviderAuth.Code,
		"unauthorized":    ErrPiProviderAuth.Code,
		"quota":           ErrPiProviderQuota.Code,
		"rate_limit":      ErrPiProviderQuota.Code,
		"model_not_found": ErrPiModelNotFound.Code,
		"invalid_model":   ErrPiModelNotFound.Code,
		"protocol":        ErrProviderProtocol.Code,
		"malformed":       ErrProviderProtocol.Code,
	}
	for kind, want := range cases {
		err := TranslateProviderError(ProviderErrorReport{Kind: kind, Message: "some message"})
		if ErrorCode(err) != want {
			t.Errorf("TranslateProviderError(Kind=%q) = %q, want %q", kind, ErrorCode(err), want)
		}
	}
}

func TestTranslateProviderErrorFallsBackToMessageKeywords(t *testing.T) {
	cases := map[string]string{
		"401 Unauthorized: invalid api key":        ErrPiProviderAuth.Code,
		"rate limit exceeded, try again later":     ErrPiProviderQuota.Code,
		"model not found: gpt-9000":                ErrPiModelNotFound.Code,
		"protocol error: unexpected response type": ErrProviderProtocol.Code,
	}
	for message, want := range cases {
		err := TranslateProviderError(ProviderErrorReport{Message: message})
		if ErrorCode(err) != want {
			t.Errorf("TranslateProviderError(Message=%q) = %q, want %q", message, ErrorCode(err), want)
		}
	}
}

// TestTranslateProviderErrorRealWorldMessages pins the exact texts observed
// from a real pi 0.85.1 + OpenRouter/OpenAI run (Issue #8 verification,
// 2026-10-01). A user who mistypes a model id or has no key configured must
// see the specific code, not the generic fallback, to know what to fix.
func TestTranslateProviderErrorRealWorldMessages(t *testing.T) {
	cases := map[string]string{
		// OpenRouter, unknown model id.
		`400: {"message":"z-ai/does-not-exist-xyz-999 is not a valid model ID","code":400}`: ErrPiModelNotFound.Code,
		// Pi itself, provider selected but no credential discovered.
		"No API key found for anthropic.\n\nUse /login to log into a provider via OAuth or API key.": ErrPiProviderAuth.Code,
		// OpenAI, wrong key (already classified; guards against regression).
		`OpenAI API error (401): {"message":"Incorrect API key provided","code":"invalid_api_key"}`: ErrPiProviderAuth.Code,
	}
	for message, want := range cases {
		err := TranslateProviderError(ProviderErrorReport{Message: message})
		if ErrorCode(err) != want {
			t.Errorf("TranslateProviderError(Message=%q) = %q, want %q", message, ErrorCode(err), want)
		}
	}
}

func TestTranslateProviderErrorDefaultsToGenericProviderError(t *testing.T) {
	err := TranslateProviderError(ProviderErrorReport{Message: "the model server had a hiccup"})
	if ErrorCode(err) != ErrPiProviderError.Code {
		t.Fatalf("ErrorCode() = %q, want %q", ErrorCode(err), ErrPiProviderError.Code)
	}
}

func TestTranslateProviderErrorPreservesMessage(t *testing.T) {
	err := TranslateProviderError(ProviderErrorReport{Kind: "quota", Message: "insufficient credits on account acct_123"})
	if err.Error() != ErrPiProviderQuota.Code+": insufficient credits on account acct_123" {
		t.Fatalf("err.Error() = %q, want message preserved", err.Error())
	}
}

func TestTranslateProviderErrorUsesSpecMandatedProtocolCode(t *testing.T) {
	// spec.md §11 EC-11 fixes the public code for a malformed/unexpected
	// provider protocol response as E_PROVIDER_PROTOCOL (not Pi-prefixed).
	err := TranslateProviderError(ProviderErrorReport{Kind: "protocol", Message: "bad frame"})
	if ErrorCode(err) != "E_PROVIDER_PROTOCOL" {
		t.Fatalf("ErrorCode() = %q, want E_PROVIDER_PROTOCOL (spec.md EC-11)", ErrorCode(err))
	}
}

func TestTranslateProviderErrorMasksCredentialsInMessage(t *testing.T) {
	// A provider is free to echo the offending request back in its error
	// text. spec.md §9: a public error must not carry an API key,
	// Authorization header, or unmasked known secret.
	cases := []struct {
		name string
		in   string
		leak string
	}{
		{
			name: "authorization bearer header",
			in:   "upstream 401: Authorization: Bearer sk-or-v1-0123456789abcdef rejected",
			leak: "sk-or-v1-0123456789abcdef",
		},
		{
			name: "echoed api key assignment",
			in:   "provider rejected api key OPENROUTER_API_KEY=sk-or-v1-SECRETSECRETSECRET: unauthorized",
			leak: "sk-or-v1-SECRETSECRETSECRET",
		},
		{
			name: "json api_key field",
			in:   `provider error {"api_key":"sk-live-abcd1234efgh5678","code":"invalid_api_key"}`,
			leak: "sk-live-abcd1234efgh5678",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := TranslateProviderError(ProviderErrorReport{Message: tc.in})
			if got := err.Error(); strings.Contains(got, tc.leak) {
				t.Fatalf("translated error %q still contains secret %q", got, tc.leak)
			}
			// Classification runs on the raw report, so the auth category
			// survives even when masking later rewrites that part.
			if ErrorCode(err) != ErrPiProviderAuth.Code {
				t.Fatalf("ErrorCode() = %q, want %q", ErrorCode(err), ErrPiProviderAuth.Code)
			}
		})
	}
}

func TestTranslateProviderErrorClassifiesFromRawBeforeMasking(t *testing.T) {
	// The credential assignment and the "unauthorized" keyword share a
	// line, so envSecretRE eats the whole line when masking. Classifying
	// the raw message first keeps the auth category; only the displayed
	// message is masked.
	report := ProviderErrorReport{Message: "OPENROUTER_API_KEY=sk-or-v1-secretsecret: unauthorized"}
	err := TranslateProviderError(report)
	if ErrorCode(err) != ErrPiProviderAuth.Code {
		t.Fatalf("ErrorCode() = %q, want %q (must classify from the raw message)", ErrorCode(err), ErrPiProviderAuth.Code)
	}
	if strings.Contains(err.Error(), "sk-or-v1-secretsecret") {
		t.Fatalf("err.Error() = %q, secret survived masking", err.Error())
	}
}

func TestTranslateProviderErrorRedactsKnownSecretValue(t *testing.T) {
	// A provider key whose format the heuristics do not recognize (here a
	// Google "AIza..." key) is still removed when the caller passes its
	// exact value, which #9 holds after injecting it.
	const key = "AIzaSyD-ExampleKey-000111222333444555666"
	report := ProviderErrorReport{Message: "authentication failed for API key " + key}

	err := TranslateProviderError(report, key)
	if strings.Contains(err.Error(), key) {
		t.Fatalf("err.Error() = %q, known secret was not redacted", err.Error())
	}
	if ErrorCode(err) != ErrPiProviderAuth.Code {
		t.Fatalf("ErrorCode() = %q, want %q", ErrorCode(err), ErrPiProviderAuth.Code)
	}

	// Documented limitation: without the known value the heuristics alone
	// do not catch this format.
	blind := TranslateProviderError(report)
	if !strings.Contains(blind.Error(), key) {
		t.Fatalf("heuristics unexpectedly masked %q; update this test and the spec §9 boundary note", key)
	}
}

func TestTranslateProviderErrorNeverRetriesJustClassifies(t *testing.T) {
	// TranslateProviderError has no retry/backoff of its own: calling it
	// repeatedly with the same report always yields the same classification.
	report := ProviderErrorReport{Kind: "quota", Message: "rate limited"}
	first := TranslateProviderError(report)
	second := TranslateProviderError(report)
	if ErrorCode(first) != ErrorCode(second) {
		t.Fatalf("classification is not stable across calls: %q vs %q", ErrorCode(first), ErrorCode(second))
	}
}
