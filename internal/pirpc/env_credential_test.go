package pirpc

import (
	"strings"
	"testing"
)

// A key with a NUL (what a UTF-16 blob read as UTF-8 looks like) must never
// reach the environment block: CreateProcess rejects it with an error that
// hides the real cause. It is refused before launch, with a code the user can
// act on, and the key text is not echoed back.
func TestInjectCredentialsRejectsUnusableKeys(t *testing.T) {
	cases := map[string]string{
		"nul between characters": "s\x00k\x00-\x00o\x00r\x00",
		"trailing nul":           "sk-or-v1-secretvalue\x00",
		"invalid utf-8":          "sk-or-v1-secret\xff\xfevalue",
	}
	base := []string{"PATH=base-path"}
	for name, key := range cases {
		out, err := InjectCredentials(base, "openrouter", OpenRouterCredential(key))
		if ErrorCode(err) != ErrCredentialInvalid.Code {
			t.Errorf("%s: ErrorCode(err) = %q, want %q", name, ErrorCode(err), ErrCredentialInvalid.Code)
			continue
		}
		if len(out) != 1 || out[0] != "PATH=base-path" {
			t.Errorf("%s: out = %q, want environment unchanged", name, out)
		}
		if strings.Contains(err.Error(), "sk-or-v1") {
			t.Errorf("%s: error %q echoes the key", name, err)
		}
	}
}
