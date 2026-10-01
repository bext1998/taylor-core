//go:build !windows

package config

import (
	"context"
	"errors"
	"testing"
)

func TestPlatformCredentialSourceUnsupported(t *testing.T) {
	loader := NewLoader(t.TempDir(), t.TempDir(), nil)
	_, err := loader.Load(context.Background(), CLIOverrides{})
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("Load() error = %v, want E_UNSUPPORTED_PLATFORM", err)
	}
}

func TestPlatformCredentialWriterUnsupported(t *testing.T) {
	if err := NewPlatformCredentialWriter().SetOpenRouterAPIKey("key"); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("SetOpenRouterAPIKey() error = %v, want E_UNSUPPORTED_PLATFORM", err)
	}
}

func TestPlatformCredentialWriterDeleteUnsupported(t *testing.T) {
	if err := NewPlatformCredentialWriter().DeleteOpenRouterAPIKey(); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("DeleteOpenRouterAPIKey() error = %v, want E_UNSUPPORTED_PLATFORM", err)
	}
}
