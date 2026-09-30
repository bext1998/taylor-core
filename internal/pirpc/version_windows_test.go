//go:build windows

package pirpc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in smoke test: no prompt, provider call, credentials or paid model access.
// npm ci installs the pinned package before this test is enabled in CI.
func TestStartLocalInstalledPi(t *testing.T) {
	if os.Getenv("BRUNEL_TEST_REAL_PI") != "1" {
		t.Skip("requires npm ci and BRUNEL_TEST_REAL_PI=1")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// A different global shim must not affect Start's selected local version.
	global := t.TempDir()
	if err := os.WriteFile(filepath.Join(global, "pi.cmd"), []byte("@echo 99.0.0\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", global+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	proc, err := Start(ctx, LaunchOptions{Model: "openai/gpt-4o", ExtensionPath: filepath.Join(root, "taylor-tools.ts")}, Credential{}, t.TempDir(), map[string]string{"PI_CODING_AGENT_DIR": t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer proc.Close()
	if err := proc.(*windowsPiProcess).send(command{Type: "get_state"}); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case ev, ok := <-proc.Events():
			if !ok {
				t.Fatalf("Pi exited before ready: %s", proc.CapturedStderr())
			}
			if ev.Response && ev.Command == "get_state" {
				if !ev.Success {
					t.Fatal("get_state failed")
				}
				if stderr := strings.TrimSpace(proc.CapturedStderr()); stderr != "" {
					t.Fatalf("Pi startup stderr: %q", stderr)
				}
				return
			}
		case <-ctx.Done():
			t.Fatalf("Pi startup timed out: %s", proc.CapturedStderr())
		}
	}
}

func TestPiInvocationGlobalNpmShim(t *testing.T) {
	dir := t.TempDir()
	entry := installTestPi(t, dir, "cli.js")
	if err := os.WriteFile(entry, nil, 0600); err != nil {
		t.Fatal(err)
	}
	// Merely a marker: it must never be interpreted as a batch program.
	shim := filepath.Join(dir, "pi.cmd")
	if err := os.WriteFile(shim, []byte("exit /b 99"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node.exe"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	path, prefix, err := piInvocation(shim)
	if err != nil || path != filepath.Join(dir, "node.exe") || len(prefix) != 1 || prefix[0] != entry {
		t.Fatalf("npm shim invocation = %q, %v, %v", path, prefix, err)
	}
}
