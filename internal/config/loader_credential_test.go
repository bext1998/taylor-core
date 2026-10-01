package config

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A stored-but-unreadable key must not read as "no key" and must not fall
// into the generic "unavailable" failure: the user needs the code and the way
// out (save it again) in the message itself.
func TestLoadReportsUnreadableCredentialWithRemedy(t *testing.T) {
	loader := NewLoader(t.TempDir(), t.TempDir(), fakeCredentialSource{err: ErrCredentialInvalid})
	resolved, err := loader.Load(context.Background(), CLIOverrides{})
	if !errors.Is(err, ErrConfigCredential) {
		t.Fatalf("Load() error = %v, want E_CONFIG_CREDENTIAL", err)
	}
	if resolved.OpenRouterAPIKey() != "" {
		t.Fatal("an unreadable credential produced a key")
	}
	if !strings.Contains(err.Error(), "brunel login") {
		t.Fatalf("error %q does not say how to fix it", err)
	}
}
