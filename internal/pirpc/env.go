package pirpc

import (
	"strings"
	"unicode/utf8"
)

// providerCredentialEnv maps a provider name to the environment variable
// Pi itself already recognizes for that provider's API key (confirmed
// against @earendil-works/pi-coding-agent by Issue #24's compatibility
// spike, spikes/pi-compatibility/recon-credentials.ps1 on the
// agent/pi-spike-issue-24 branch). Brunel injects into this existing
// mechanism rather than inventing its own (spec.md §5.3: "透過環境變數或 Pi
// 既有的 credential 機制傳入"). Names are matched case-insensitively.
var providerCredentialEnv = map[string]string{
	"openrouter": "OPENROUTER_API_KEY",
	"anthropic":  "ANTHROPIC_API_KEY",
	"openai":     "OPENAI_API_KEY",
	"gemini":     "GEMINI_API_KEY",
	"google":     "GEMINI_API_KEY",
}

// CredentialEnvVar returns the environment variable name Pi recognizes for
// provider's API key, and whether provider is one Brunel knows about.
// Brunel's own credential storage today (Issue #12) only ever resolves an
// OpenRouter key; other providers are listed here so a caller with its own
// key source can still inject correctly, and so this mapping has one place
// to grow as Brunel's config layer gains more providers.
func CredentialEnvVar(provider string) (string, bool) {
	name, ok := providerCredentialEnv[strings.ToLower(strings.TrimSpace(provider))]
	return name, ok
}

// Credential is an API key together with the provider it authenticates.
// Keeping the two bound means a key resolved for one provider can never be
// injected into another provider's environment variable: InjectCredentials
// rejects the pair when Provider does not resolve to the same variable as
// the provider being launched. Today Brunel's config layer only produces an
// OpenRouter credential (see OpenRouterCredential); other providers require
// the caller to supply a Credential with the matching Provider.
type Credential struct {
	// Provider is the provider this key authenticates, e.g. "openrouter".
	// Matched case-insensitively and via providerCredentialEnv aliases
	// (so "google" and "gemini" are equivalent).
	Provider string
	// APIKey is the secret value. An empty APIKey means "no key to inject".
	APIKey string
}

// OpenRouterCredential builds the one Credential Brunel's config layer can
// resolve today (Issue #12: Windows Credential Manager -> OpenRouter key).
// It exists so callers do not hand-write the provider string and risk a
// mismatch.
func OpenRouterCredential(apiKey string) Credential {
	return Credential{Provider: "openrouter", APIKey: apiKey}
}

// InjectCredentials returns a copy of base (an os.Environ()-shaped slice of
// "KEY=VALUE" strings) with the environment variable Pi recognizes for
// provider's API key set to cred.APIKey, replacing any existing entry for
// that variable. base is never modified in place.
//
// The credential is only injected when cred.Provider resolves to the same
// Pi environment variable as provider; otherwise the environment is
// returned unchanged together with ErrCredentialProviderMismatch, so a key
// resolved for one provider is never placed in a different provider's
// variable (which would ship it to the wrong endpoint and defeat the
// spec.md §9 "no unmasked secret in a public error" guarantee once that
// endpoint echoed it back).
//
// An unrecognized provider or an empty cred.APIKey leaves the environment
// unchanged with no error - the subprocess then falls back to whatever
// credential Pi can find on its own (its settings.json, or a variable the
// user already has set), which is an accepted limitation for providers
// Brunel's own config layer does not yet resolve a key for (spec.md §5.3:
// "須在 README／CLI help 明確揭露為「隨 Pi 版本變動」").
func InjectCredentials(base []string, provider string, cred Credential) ([]string, error) {
	out := make([]string, len(base))
	copy(out, base)

	name, ok := CredentialEnvVar(provider)
	if !ok || strings.TrimSpace(cred.APIKey) == "" {
		return out, nil
	}

	// A NUL would truncate or invalidate the environment block, and invalid
	// UTF-8 would be silently rewritten to U+FFFD when the block is encoded;
	// either way the subprocess would not get the key the user stored. Refuse
	// it here, before launch, instead of failing inside CreateProcess.
	if strings.IndexByte(cred.APIKey, 0) >= 0 || !utf8.ValidString(cred.APIKey) {
		return out, codeError(ErrCredentialInvalid.Code,
			`the OpenRouter key is not usable text (contains NUL or invalid UTF-8); save it again with "brunel login"`, nil)
	}

	credName, credOK := CredentialEnvVar(cred.Provider)
	if !credOK || credName != name {
		return out, codeError(ErrCredentialProviderMismatch.Code,
			"credential resolved for a different provider was not injected", nil)
	}

	// Windows environment variable names are case-insensitive, so an
	// existing "OpenRouter_Api_Key=old" must be treated as the same
	// variable and dropped, not left in place next to the new entry. Match
	// on the name left of the first "=" with EqualFold and re-emit the
	// canonical name. Any later case-variant duplicates collapse into the
	// one canonical entry.
	result := make([]string, 0, len(base)+1)
	canonical := name + "=" + cred.APIKey
	replaced := false
	for _, entry := range base {
		entryName, _, hasEq := strings.Cut(entry, "=")
		if hasEq && strings.EqualFold(entryName, name) {
			if !replaced {
				result = append(result, canonical)
				replaced = true
			}
			continue
		}
		result = append(result, entry)
	}
	if !replaced {
		result = append(result, canonical)
	}
	return result, nil
}

// InjectCredentialsForLaunch injects cred for the provider opts actually
// target (opts.EffectiveProvider()), so a provider-prefixed model with no
// explicit LaunchOptions.Provider still receives the Credential Manager
// key. It is the entry point Issue #9's subprocess launcher should call;
// InjectCredentials remains available when the provider is already known
// independently of a LaunchOptions.
func InjectCredentialsForLaunch(base []string, opts LaunchOptions, cred Credential) ([]string, error) {
	return InjectCredentials(base, opts.EffectiveProvider(), cred)
}
