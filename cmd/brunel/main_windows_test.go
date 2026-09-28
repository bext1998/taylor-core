//go:build windows

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bext1998/brunel/internal/approval"
	brunelexec "github.com/bext1998/brunel/internal/exec"
	"github.com/bext1998/brunel/internal/safety"
)

func TestTaylorToolAcceptsExactlyFrozenNames(t *testing.T) {
	root := t.TempDir()
	const original = "original\n"
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "write.txt"), []byte(original), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	hash := sha256.Sum256([]byte(original))
	expectedHash := hex.EncodeToString(hash[:])

	cases := []struct {
		name   string
		params string
	}{
		{"list_files", `{"path":"."}`},
		{"search_text", `{"pattern":"original"}`},
		{"read_file", `{"path":"note.txt"}`},
		{"apply_patch", `{"path":"note.txt","expected_hash":"` + expectedHash + `","hunks":[{"start_line":1,"end_line":1,"old_lines":["original"],"new_lines":["patched"]}]}`},
		{"create_file", `{"path":"new.txt","content":"new"}`},
		{"write_file", `{"path":"write.txt","expected_hash":"` + expectedHash + `","content":"changed"}`},
		{"run_powershell", `{"command":"Write-Output ok"}`},
		{"workspace_diff", `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response, exitCode := invokeTaylorTool(t, root, "workspace", tc.name, tc.params)
			if response.ErrorCode == "E_INVALID_ARGUMENT" {
				t.Fatalf("%s was rejected as an invalid tool: %#v", tc.name, response)
			}
			if exitCode == 0 && response.Status != "ok" {
				t.Fatalf("%s exit code/status = %d/%q, want success status", tc.name, exitCode, response.Status)
			}
		})
	}

	response, exitCode := invokeTaylorTool(t, root, "workspace", "not_a_tool", `{}`)
	if exitCode == 0 || response.ErrorCode != "E_INVALID_ARGUMENT" {
		t.Fatalf("unknown tool response = %#v, exit=%d", response, exitCode)
	}
}

func TestTaylorToolMissingExpectedHashHasNoSideEffect(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "note.txt")
	const original = "original\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	for _, tc := range []struct {
		name   string
		params string
	}{
		{"apply_patch", `{"path":"note.txt","hunks":[]}`},
		{"write_file", `{"path":"note.txt","content":"changed"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response, exitCode := invokeTaylorTool(t, root, "workspace", tc.name, tc.params)
			if exitCode == 0 || response.ErrorCode != "E_INVALID_ARGUMENT" {
				t.Fatalf("response = %#v, exit=%d", response, exitCode)
			}
			assertTaylorFile(t, path, original)
		})
	}
}

func TestTaylorToolReadonlyRejectsMutationsAndPowerShell(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "note.txt")
	const original = "original\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	for _, tc := range []struct {
		name   string
		params string
	}{
		{"apply_patch", `{"path":"note.txt","expected_hash":"hash","hunks":[]}`},
		{"create_file", `{"path":"new.txt","content":"new"}`},
		{"write_file", `{"path":"note.txt","expected_hash":"hash","content":"changed"}`},
		{"run_powershell", `{"command":"Set-Content -LiteralPath note.txt changed"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response, exitCode := invokeTaylorTool(t, root, "readonly", tc.name, tc.params)
			if exitCode == 0 || response.ErrorCode != "E_READONLY_MODE" {
				t.Fatalf("response = %#v, exit=%d", response, exitCode)
			}
		})
	}
	assertTaylorFile(t, path, original)
	if _, err := os.Stat(filepath.Join(root, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("readonly create_file created new.txt: %v", err)
	}
}

func TestTaylorToolConfirmWithoutTTYFailsClosed(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sentinel.txt")
	if err := os.WriteFile(path, []byte("present\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	response, exitCode := invokeTaylorTool(t, root, "workspace", "run_powershell", `{"command":"Remove-Item .\\sentinel.txt -Recurse"}`)
	if exitCode == 0 || response.ErrorCode != "E_APPROVAL_REQUIRED_NO_TTY" {
		t.Fatalf("response = %#v, exit=%d", response, exitCode)
	}
	assertTaylorFile(t, path, "present\n")
}

func TestTaylorToolResponseSnapshot(t *testing.T) {
	var output bytes.Buffer
	exitCode := runTaylorTool(context.Background(), testTaylorConfig(t.TempDir(), "workspace", "list_files"), strings.NewReader(`{"path":"."}`), &output)
	if exitCode != 0 {
		t.Fatalf("list_files exit code = %d, output=%s", exitCode, output.String())
	}
	want, err := os.ReadFile(filepath.Join("testdata", "list_files_response.json"))
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	// The snapshot's contract is the JSON shape, not line endings: a
	// core.autocrlf checkout leaves the committed file CRLF on Windows
	// while json.Encoder always emits LF.
	if normalizeNewlines(output.String()) != normalizeNewlines(string(want)) {
		t.Fatalf("response changed\nwant:\n%s\ngot:\n%s", want, output.String())
	}
}

func invokeTaylorTool(t *testing.T, cwd, mode, name, params string) (taylorToolResponse, int) {
	t.Helper()
	var output bytes.Buffer
	var diagnostics bytes.Buffer
	exitCode := run([]string{"--taylor-tool", name, "--cwd", cwd, "--mode", mode}, bytes.NewBufferString(params), &output, &diagnostics)
	var response taylorToolResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatalf("decode response %q: %v", output.String(), err)
	}
	return response, exitCode
}

func testTaylorConfig(cwd, mode, name string) taylorToolConfig {
	return taylorToolConfig{
		name:           name,
		cwd:            cwd,
		mode:           mode,
		timeout:        time.Second,
		maxProcesses:   uint(defaultMaxProcesses),
		maxMemoryBytes: defaultMaxMemoryBytes,
		maxOutputBytes: defaultMaxOutputBytes,
	}
}

func normalizeNewlines(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

func assertTaylorFile(t *testing.T, path, want string) {
	t.Helper()
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(actual) != want {
		t.Fatalf("content of %s = %q, want %q", path, actual, want)
	}
}

type answeringApprover struct {
	answer  bool
	prompts []safety.ApprovalPrompt
}

func (a *answeringApprover) Confirm(_ context.Context, p safety.ApprovalPrompt) (bool, error) {
	a.prompts = append(a.prompts, p)
	return a.answer, nil
}

// TestTaylorToolConfirmUsesHostApprovalChannel drives the real --taylor-tool
// path against a real approval broker (issue #2): the subprocess Gate asks
// the host through the named pipe, a denial has no side effect, and an
// approval lets the command run once.
func TestTaylorToolConfirmUsesHostApprovalChannel(t *testing.T) {
	for _, approve := range []bool{false, true} {
		root := t.TempDir()
		path := filepath.Join(root, "sentinel.txt")
		if err := os.WriteFile(path, []byte("present\n"), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		host := &answeringApprover{answer: approve}
		broker, err := approval.StartBroker(context.Background(), host)
		if err != nil {
			t.Fatalf("StartBroker: %v", err)
		}
		for k, v := range broker.Env() {
			t.Setenv(k, v)
		}

		response, exitCode := invokeTaylorTool(t, root, "workspace", "run_powershell", `{"command":"Remove-Item .\\sentinel.txt -Recurse"}`)
		_ = broker.Close()

		if len(host.prompts) != 1 || !strings.Contains(host.prompts[0].Command, "Remove-Item") || host.prompts[0].Reason == "" {
			t.Fatalf("approve=%v: host saw %#v, want one prompt with command and reason", approve, host.prompts)
		}
		if !approve {
			if exitCode == 0 || response.ErrorCode != safety.ErrApprovalDenied.Code {
				t.Fatalf("denied: response = %#v, exit=%d", response, exitCode)
			}
			assertTaylorFile(t, path, "present\n")
			continue
		}
		if response.ErrorCode == brunelexec.ErrPwshRequired.Code {
			t.Skip("pwsh 7 is not installed")
		}
		if exitCode != 0 || response.Status != "ok" {
			t.Fatalf("approved: response = %#v, exit=%d", response, exitCode)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("approved command did not run: stat err = %v", err)
		}
	}
}
