package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeCredentialSource struct {
	key string
	err error
}

func (f fakeCredentialSource) OpenRouterAPIKey(context.Context) (string, error) {
	return f.key, f.err
}

func TestLoaderLayerPrecedenceAndCLIEmptyOverride(t *testing.T) {
	root := t.TempDir()
	profile := t.TempDir()
	writeConfig(t, filepath.Join(profile, ".brunel", "config.json"), `{"mode":"readonly","model_id":"global"}`)
	writeConfig(t, filepath.Join(root, ".brunel", "config.json"), `{"mode":"workspace","model_id":"project"}`)
	loader := NewLoader(root, profile, fakeCredentialSource{key: "secret-key"})
	resolved, err := loader.Load(context.Background(), CLIOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Config.Mode != ModeWorkspace || resolved.Config.ModelID != "project" || resolved.OpenRouterAPIKey() != "secret-key" {
		t.Fatalf("unexpected resolved config: %#v", resolved)
	}
	empty := ""
	if _, err := loader.Load(context.Background(), CLIOverrides{ModelID: &empty}); !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("empty CLI override error = %v, want E_CONFIG_INVALID", err)
	}
}

func TestLoaderStrictAndForbiddenConfig(t *testing.T) {
	root := t.TempDir()
	profile := t.TempDir()
	loader := NewLoader(root, profile, fakeCredentialSource{key: "key"})
	for _, tc := range []struct {
		name string
		body string
	}{
		{"unknown", `{"mode":"workspace","extra":true}`},
		{"credential", `{"mode":"workspace","api_key":"leak"}`},
		{"safety", `{"mode":"workspace","approval":"auto"}`},
		{"malformed", `{"mode":"workspace"`},
		{"bad-mode", `{"mode":"unsafe"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(profile, ".brunel", "config.json")
			writeConfig(t, path, tc.body)
			_, err := loader.Load(context.Background(), CLIOverrides{})
			if !errors.Is(err, ErrConfigInvalid) {
				t.Fatalf("Load() error = %v, want E_CONFIG_INVALID", err)
			}
			if strings.Contains(err.Error(), "leak") || strings.Contains(err.Error(), "secret-key") {
				t.Fatalf("config error leaked sensitive content: %v", err)
			}
			_ = os.Remove(path)
		})
	}
}

func TestReadLayerInvalidFieldMessageIsStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeConfig(t, path, `{"unknown":true,"api_key":"not-a-real-key"}`)

	var first string
	for i := 0; i < 100; i++ {
		_, _, err := readLayer(path, "project")
		if err == nil {
			t.Fatal("readLayer() unexpectedly succeeded")
		}
		var coded *Error
		if !errors.As(err, &coded) || coded.Cause == nil {
			t.Fatalf("readLayer() error = %v, want a coded cause", err)
		}
		message := coded.Cause.Error()
		if i == 0 {
			first = message
		}
		if message != first {
			t.Fatalf("readLayer() cause changed: first %q, got %q", first, message)
		}
	}
	if !strings.Contains(first, "forbidden security field") {
		t.Fatalf("readLayer() error = %q, want forbidden security field", first)
	}
}

func TestLoaderMissingLayersUseDefaults(t *testing.T) {
	loader := NewLoader(t.TempDir(), t.TempDir(), fakeCredentialSource{key: "key"})
	resolved, err := loader.Load(context.Background(), CLIOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Config.Mode != ModeWorkspace || resolved.Config.ModelID != "" {
		t.Fatalf("unexpected defaults: %#v", resolved.Config)
	}
}

func TestCredentialErrorsDoNotExposeSourceDetails(t *testing.T) {
	loader := NewLoader(t.TempDir(), t.TempDir(), fakeCredentialSource{err: errors.New("credential secret-value")})
	_, err := loader.Load(context.Background(), CLIOverrides{})
	if !errors.Is(err, ErrConfigCredential) {
		t.Fatalf("Load() error = %v, want E_CONFIG_CREDENTIAL", err)
	}
	if strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("credential error leaked source detail: %v", err)
	}
}

func TestMissingCredentialIsOptional(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source fakeCredentialSource
	}{
		{"not-found", fakeCredentialSource{err: ErrCredentialNotFound}},
		{"empty", fakeCredentialSource{key: "  "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeConfig(t, filepath.Join(root, ".brunel", "config.json"), `{"mode":"readonly","model_id":"anthropic/claude"}`)
			resolved, err := NewLoader(root, t.TempDir(), tc.source).Load(context.Background(), CLIOverrides{})
			if err != nil {
				t.Fatalf("Load() error = %v, want nil for an absent credential", err)
			}
			if resolved.Config.Mode != ModeReadonly || resolved.Config.ModelID != "anthropic/claude" {
				t.Fatalf("unexpected resolved config: %#v", resolved.Config)
			}
			if resolved.OpenRouterAPIKey() != "" {
				t.Fatalf("OpenRouterAPIKey() = %q, want empty", resolved.OpenRouterAPIKey())
			}
		})
	}
}

func TestResolvedFormattingNeverIncludesAPIKey(t *testing.T) {
	loader := NewLoader(t.TempDir(), t.TempDir(), fakeCredentialSource{key: "super-secret-key"})
	resolved, err := loader.Load(context.Background(), CLIOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(resolved.String(), resolved.OpenRouterAPIKey()) {
		t.Fatalf("resolved formatting leaked API key: %s", resolved.String())
	}
	encoded, err := json.Marshal(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), resolved.OpenRouterAPIKey()) || strings.Contains(fmt.Sprintf("%#v", resolved), resolved.OpenRouterAPIKey()) {
		t.Fatal("resolved serialization or formatting leaked API key")
	}
}

func writeConfig(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
