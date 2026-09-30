package pirpc

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPiVersionPinMatchesManifests(t *testing.T) {
	for _, filename := range []string{"package.json", "package-lock.json"} {
		data, err := os.ReadFile(filepath.Join("..", "..", filename))
		if err != nil {
			t.Fatal(err)
		}
		var manifest struct {
			Dependencies     map[string]string `json:"dependencies"`
			PeerDependencies map[string]string `json:"peerDependencies"`
			Packages         map[string]struct {
				Dependencies map[string]string `json:"dependencies"`
				Version      string            `json:"version"`
			} `json:"packages"`
		}
		if err := json.Unmarshal(data, &manifest); err != nil {
			t.Fatal(err)
		}
		deps := manifest.Dependencies
		if filename == "package-lock.json" {
			deps = manifest.Packages[""].Dependencies
			if got := manifest.Packages["node_modules/@earendil-works/pi-coding-agent"].Version; got != SupportedPiVersion {
				t.Fatalf("locked version = %q", got)
			}
		}
		if got := deps["@earendil-works/pi-coding-agent"]; got != SupportedPiVersion {
			t.Fatalf("%s dependency = %q", filename, got)
		}
		if _, ok := manifest.PeerDependencies["@earendil-works/pi-coding-agent"]; ok {
			t.Fatal("Pi must be a direct dependency")
		}
	}
}

func installTestPi(t *testing.T, root, bin string) string {
	t.Helper()
	dir := filepath.Join(root, "node_modules", "@earendil-works", "pi-coding-agent")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"bin": map[string]string{"pi": bin}})
	if err := os.WriteFile(filepath.Join(dir, "package.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, bin)
}

func TestResolvePiLocalBeforePATH(t *testing.T) {
	root := filepath.Join(t.TempDir(), "install with spaces")
	entry := installTestPi(t, root, "cli.js")
	if err := os.WriteFile(entry, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	global := t.TempDir()
	name := "pi"
	if runtime.GOOS == "windows" {
		name = "pi.exe"
	}
	if err := os.WriteFile(filepath.Join(global, name), []byte("different global version"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", global)
	for _, opts := range []LaunchOptions{{ExtensionPath: filepath.Join(root, "taylor-tools.ts")}, {ExtensionPath: "taylor-tools.ts"}, {}} {
		got, err := resolvePiPath(opts, root)
		if err != nil || got != entry {
			t.Fatalf("local resolution = %q, %v; want %q", got, err, entry)
		}
	}
	// No .bin directory exists: the package manifest alone is sufficient.
	if err := os.Remove(entry); err != nil {
		t.Fatal(err)
	}
	if _, err := resolvePiPath(LaunchOptions{}, root); ErrorCode(err) != ErrPiRuntimeRequired.Code {
		t.Fatalf("broken local install fell back: %v", err)
	}
}

func TestResolvePiGlobalAndMissing(t *testing.T) {
	global := t.TempDir()
	name := "pi"
	if runtime.GOOS == "windows" {
		name = "pi.exe"
	}
	path := filepath.Join(global, name)
	if err := os.WriteFile(path, []byte("placeholder"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", global)
	got, err := resolvePiPath(LaunchOptions{}, t.TempDir())
	if err != nil || got != path {
		t.Fatalf("fallback = %q, %v", got, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := resolvePiPath(LaunchOptions{}, t.TempDir()); ErrorCode(err) != ErrPiRuntimeRequired.Code {
		t.Fatalf("missing Pi = %v", err)
	}
}

func TestStartRejectsUnverifiedPiBeforeRPC(t *testing.T) {
	root := t.TempDir()
	bin := "fakepi"
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	entry := installTestPi(t, root, bin)
	build := exec.Command("go", "build", "-o", entry, "./testdata/fakepi")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, output)
	}
	marker := filepath.Join(root, "rpc-started")
	t.Setenv("FAKE_PI_RPC_MARKER", marker)
	for _, version := range []string{"0.85.2", "", "0.85.1-extra", "secret-value", strings.Repeat("x", 300)} {
		t.Run("reject "+version[:min(len(version), 20)], func(t *testing.T) {
			t.Setenv("FAKE_PI_VERSION", version)
			proc, err := Start(context.Background(), LaunchOptions{Model: "test"}, Credential{}, root, nil)
			if proc != nil {
				proc.Close()
				t.Fatal("started RPC for unverified version")
			}
			if ErrorCode(err) != ErrPiVersionMismatch.Code {
				t.Fatalf("error = %v", err)
			}
			if strings.Contains(err.Error(), "secret-value") {
				t.Fatal("probe output leaked")
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("RPC was entered")
			}
		})
	}
	t.Setenv("FAKE_PI_VERSION", SupportedPiVersion)
	if err := checkPiVersion(context.Background(), entry, nil, root); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_PI_VERSION_EXIT", "1")
	if err := checkPiVersion(context.Background(), entry, nil, root); ErrorCode(err) != ErrPiRuntimeRequired.Code {
		t.Fatalf("failed probe = %v", err)
	}
	t.Setenv("FAKE_PI_VERSION_EXIT", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := checkPiVersion(ctx, entry, nil, root); ErrorCode(err) != ErrPiRuntimeRequired.Code {
		t.Fatalf("cancelled probe = %v", err)
	}
}
