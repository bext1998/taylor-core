//go:build windows

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bext1998/brunel/internal/workspace"
)

func mklinkJunction(t *testing.T, link, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("cannot create junction: %v (%s)", err, out)
	}
}

func callTool(t *testing.T, cwd, id, name, params string) (taylorToolResponse, int) {
	t.Helper()
	args := []string{"--taylor-tool", name, "--cwd", cwd, "--mode", "workspace"}
	if id != "" {
		args = append(args, "--workspace-id", id)
	}
	var out, diag bytes.Buffer
	code := run(args, bytes.NewBufferString(params), &out, &diag)
	var resp taylorToolResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", out.String(), err)
	}
	return resp, code
}

// Every tool call is a fresh process that binds the workspace again. If the
// directory behind the workspace path is swapped between two calls (INV-5), a
// later call must not quietly start operating on the other location: the
// identity the session started with is passed down and checked.
func TestToolCallRejectsWorkspaceSwappedBetweenCalls(t *testing.T) {
	base := t.TempDir()
	a, b := filepath.Join(base, "a"), filepath.Join(base, "b")
	for _, d := range []string{a, b} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(base, "ws")
	mklinkJunction(t, link, a)

	bound, err := workspace.Bind(link)
	if err != nil {
		t.Fatal(err)
	}
	id := bound.Identity()

	if resp, code := callTool(t, link, id, "create_file", `{"path":"first.txt","content":"1"}`); code != 0 || resp.Status != "ok" {
		t.Fatalf("call before the swap = %d/%+v", code, resp)
	}

	// Swap the directory behind the same path.
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	mklinkJunction(t, link, b)

	resp, code := callTool(t, link, id, "create_file", `{"path":"second.txt","content":"2"}`)
	if code == 0 || resp.ErrorCode != "E_WORKSPACE_UNBOUND" {
		t.Fatalf("call after the swap = %d/%+v, want E_WORKSPACE_UNBOUND", code, resp)
	}
	if _, err := os.Stat(filepath.Join(b, "second.txt")); !os.IsNotExist(err) {
		t.Fatal("the call wrote into the swapped-in directory")
	}
}

// The check is only as strong as the host passing the identity, so it must
// also hold when nothing changed, and an old caller that passes no identity
// keeps working.
func TestToolCallWithUnchangedWorkspaceAndWithoutIdentity(t *testing.T) {
	root := t.TempDir()
	bound, err := workspace.Bind(root)
	if err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"same identity": bound.Identity(), "no identity given": ""} {
		resp, code := callTool(t, root, id, "list_files", `{"path":"."}`)
		if code != 0 || resp.Status != "ok" {
			t.Errorf("%s: %d/%+v", name, code, resp)
		}
	}
}

// The identity only protects the session if every run actually hands it to
// the tool processes: the runner gets the identity of the directory the
// session bound, whether or not an approval channel exists.
func TestRunPassesWorkspaceIdentityToTheRunner(t *testing.T) {
	for _, tty := range []terminals{{}, {stdin: true}} {
		h := newHarness(t, tty, "")
		if code := runCLI([]string{"--model", "anthropic/x", "task"}, h.env); code != exitOK {
			t.Fatalf("exit = %d: %s", code, h.stderr.String())
		}
		bound, err := workspace.Bind(h.root)
		if err != nil {
			t.Fatal(err)
		}
		if got := h.agent.env["BRUNEL_WORKSPACE_ID"]; got == "" || got != bound.Identity() {
			t.Fatalf("tty=%+v: BRUNEL_WORKSPACE_ID = %q, want %q", tty, got, bound.Identity())
		}
	}
}
