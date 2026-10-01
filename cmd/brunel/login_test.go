package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/bext1998/brunel/internal/config"
)

const loginTestKey = "sk-or-v1-0123456789abcdef"

type fakeWriter struct {
	saved     []string
	deleted   int
	setErr    error
	deleteErr error
}

func (w *fakeWriter) SetOpenRouterAPIKey(key string) error {
	if w.setErr != nil {
		return w.setErr
	}
	w.saved = append(w.saved, key)
	return nil
}

func (w *fakeWriter) DeleteOpenRouterAPIKey() error {
	w.deleted++
	return w.deleteErr
}

type loginHarness struct {
	env    cliEnv
	stdout *bytes.Buffer
	stderr *bytes.Buffer
	writer *fakeWriter
	// secretReads counts hidden-prompt reads, so a test can tell which input
	// path supplied the key.
	secretReads int
	// secretErr, when set, is what the hidden prompt returns instead of a key.
	secretErr error
}

func newLoginHarness(stdin string, tty bool, secret string) *loginHarness {
	h := &loginHarness{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, writer: &fakeWriter{}}
	h.env = cliEnv{
		stdin:            strings.NewReader(stdin),
		stdout:           h.stdout,
		stderr:           h.stderr,
		tty:              terminals{stdin: tty, stdout: tty},
		credentialWriter: h.writer,
		readSecret: func() (string, error) {
			h.secretReads++
			if h.secretErr != nil {
				return "", h.secretErr
			}
			return secret, nil
		},
	}
	return h
}

func (h *loginHarness) output() string { return h.stdout.String() + h.stderr.String() }

// The key must reach Credential Manager exactly as typed (minus the line
// ending a pipe adds), and must never be echoed anywhere the user, a log or a
// transcript could capture it.
func TestLoginStoresPipedKeyWithoutEchoingIt(t *testing.T) {
	h := newLoginHarness(loginTestKey+"\r\n", false, "")
	if code := runCLI([]string{"login"}, h.env); code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, h.stderr.String())
	}
	if len(h.writer.saved) != 1 || h.writer.saved[0] != loginTestKey {
		t.Fatalf("saved = %q, want exactly %q", h.writer.saved, loginTestKey)
	}
	if strings.Contains(h.output(), loginTestKey) || strings.Contains(h.output(), "0123456789") {
		t.Fatalf("output echoes the key: %q", h.output())
	}
}

// At a terminal the key is read through the hidden prompt, not from stdin,
// which would echo it on screen and into scrollback.
func TestLoginAtTerminalUsesHiddenPrompt(t *testing.T) {
	h := newLoginHarness("must-not-be-read\n", true, loginTestKey)
	if code := runCLI([]string{"login"}, h.env); code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, h.stderr.String())
	}
	if h.secretReads != 1 {
		t.Fatalf("hidden prompt reads = %d, want 1", h.secretReads)
	}
	if len(h.writer.saved) != 1 || h.writer.saved[0] != loginTestKey {
		t.Fatalf("saved = %q, want the hidden-prompt key", h.writer.saved)
	}
	if strings.Contains(h.output(), loginTestKey) {
		t.Fatalf("output echoes the key: %q", h.output())
	}
}

// A key given as an argument would sit in the shell history and the process
// list. It is refused without being stored or repeated back.
func TestLoginRefusesKeyAsArgument(t *testing.T) {
	h := newLoginHarness("", false, "")
	code := runCLI([]string{"login", loginTestKey}, h.env)
	if code != exitInvalid {
		t.Fatalf("exit = %d, want %d", code, exitInvalid)
	}
	if len(h.writer.saved) != 0 {
		t.Fatalf("a key passed as an argument was stored: %q", h.writer.saved)
	}
	if strings.Contains(h.output(), loginTestKey) {
		t.Fatalf("output echoes the argument key: %q", h.output())
	}
}

// Saving an empty key would replace a working one with nothing.
func TestLoginRejectsEmptyKeyAndKeepsExistingOne(t *testing.T) {
	for name, h := range map[string]*loginHarness{
		"piped blank":  newLoginHarness("  \r\n", false, ""),
		"no stdin":     newLoginHarness("", false, ""),
		"hidden blank": newLoginHarness("", true, "   "),
	} {
		if code := runCLI([]string{"login"}, h.env); code != exitFailed {
			t.Errorf("%s: exit = %d, want %d", name, code, exitFailed)
		}
		if len(h.writer.saved) != 0 {
			t.Errorf("%s: an empty key was stored", name)
		}
		if !strings.Contains(h.stderr.String(), "E_CONFIG_CREDENTIAL") {
			t.Errorf("%s: stderr %q lacks E_CONFIG_CREDENTIAL", name, h.stderr.String())
		}
	}
}

// If the terminal cannot be read the command must fail without storing
// anything, and the error must not carry the partial input.
func TestLoginTerminalReadFailureStoresNothing(t *testing.T) {
	h := newLoginHarness("", true, loginTestKey)
	h.secretErr = errors.New("The handle is invalid.")
	if code := runCLI([]string{"login"}, h.env); code != exitFailed {
		t.Fatalf("exit = %d, want %d", code, exitFailed)
	}
	if len(h.writer.saved) != 0 {
		t.Fatalf("a key was stored after a failed read: %q", h.writer.saved)
	}
	if !strings.Contains(h.stderr.String(), "E_CONFIG_CREDENTIAL") {
		t.Fatalf("stderr %q lacks E_CONFIG_CREDENTIAL", h.stderr.String())
	}
	if strings.Contains(h.output(), loginTestKey) {
		t.Fatalf("output echoes the key: %q", h.output())
	}
}

// Brunel only stores an OpenRouter key; any other provider is Pi's business
// and the user is pointed there instead of silently getting OpenRouter.
func TestLoginOtherProviderPointsToPi(t *testing.T) {
	h := newLoginHarness(loginTestKey+"\n", false, "")
	code := runCLI([]string{"login", "anthropic"}, h.env)
	if code != exitInvalid {
		t.Fatalf("exit = %d, want %d", code, exitInvalid)
	}
	if len(h.writer.saved) != 0 {
		t.Fatal("a key was stored for an unsupported provider")
	}
	if !strings.Contains(h.stderr.String(), "pi") {
		t.Fatalf("stderr %q does not point to pi", h.stderr.String())
	}
}

func TestLoginAcceptsOpenRouterProviderName(t *testing.T) {
	h := newLoginHarness(loginTestKey+"\n", false, "")
	if code := runCLI([]string{"login", "OpenRouter"}, h.env); code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, h.stderr.String())
	}
	if len(h.writer.saved) != 1 {
		t.Fatalf("saved = %q, want one key", h.writer.saved)
	}
}

// When Credential Manager refuses the write the user must see a failure, not
// a success message, and the key must not appear in the error.
func TestLoginReportsWriteFailureWithoutKey(t *testing.T) {
	h := newLoginHarness(loginTestKey+"\n", false, "")
	h.writer.setErr = errors.New("Access is denied.")
	if code := runCLI([]string{"login"}, h.env); code != exitFailed {
		t.Fatalf("exit = %d, want %d", code, exitFailed)
	}
	if !strings.Contains(h.stderr.String(), "E_CONFIG_CREDENTIAL") {
		t.Fatalf("stderr %q lacks E_CONFIG_CREDENTIAL", h.stderr.String())
	}
	if strings.Contains(h.stdout.String(), "saved") || strings.Contains(h.output(), loginTestKey) {
		t.Fatalf("failure reported as success or echoed the key: %q", h.output())
	}
}

func TestLoginUnsupportedPlatform(t *testing.T) {
	h := newLoginHarness(loginTestKey+"\n", false, "")
	h.writer.setErr = config.ErrUnsupportedPlatform
	if code := runCLI([]string{"login"}, h.env); code != exitFailed {
		t.Fatalf("exit = %d, want %d", code, exitFailed)
	}
	if !strings.Contains(h.stderr.String(), "E_UNSUPPORTED_PLATFORM") {
		t.Fatalf("stderr %q lacks E_UNSUPPORTED_PLATFORM", h.stderr.String())
	}
}

func TestLogoutRemovesKey(t *testing.T) {
	h := newLoginHarness("", false, "")
	if code := runCLI([]string{"logout"}, h.env); code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, h.stderr.String())
	}
	if h.writer.deleted != 1 {
		t.Fatalf("deletes = %d, want 1", h.writer.deleted)
	}
}

// Logging out twice is not an error: the end state the user asked for holds.
func TestLogoutWithNothingStoredSucceeds(t *testing.T) {
	h := newLoginHarness("", false, "")
	h.writer.deleteErr = config.ErrCredentialNotFound
	if code := runCLI([]string{"logout"}, h.env); code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, h.stderr.String())
	}
	if !strings.Contains(h.stdout.String(), "No OpenRouter key") {
		t.Fatalf("stdout %q does not say nothing was stored", h.stdout.String())
	}
}

func TestLogoutReportsDeleteFailure(t *testing.T) {
	h := newLoginHarness("", false, "")
	h.writer.deleteErr = errors.New("Access is denied.")
	if code := runCLI([]string{"logout"}, h.env); code != exitFailed {
		t.Fatalf("exit = %d, want %d", code, exitFailed)
	}
}

// A one-word task that happens to be "login" is still reachable after `--`.
func TestLoginWordAfterDoubleDashStaysATask(t *testing.T) {
	if _, ok := authCommand([]string{"--", "login"}); ok {
		t.Fatal(`"-- login" was taken as the login command`)
	}
	if _, ok := authCommand([]string{"--mode", "readonly", "login"}); ok {
		t.Fatal(`"login" after a flag was taken as the login command`)
	}
	if name, ok := authCommand([]string{"login"}); !ok || name != "login" {
		t.Fatalf("authCommand(login) = %q, %v", name, ok)
	}
	if name, ok := authCommand([]string{"logout"}); !ok || name != "logout" {
		t.Fatalf("authCommand(logout) = %q, %v", name, ok)
	}
}
