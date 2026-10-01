package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/bext1998/brunel/internal/config"
)

// maxKeyInput bounds what login reads from a pipe; an API key is a few
// hundred bytes at most.
const maxKeyInput = 64 << 10

// authCommand reports whether args invoke `brunel login` or `brunel logout`.
// Only the very first argument counts, so `brunel -- login` and
// `brunel --mode readonly login` still run "login" as a task.
func authCommand(args []string) (string, bool) {
	if len(args) > 0 && (args[0] == "login" || args[0] == "logout") {
		return args[0], true
	}
	return "", false
}

// runAuth runs `brunel login [openrouter]` or `brunel logout [openrouter]`,
// the counterpart of pi's /login and /logout for the one key Brunel stores
// itself. The key is only ever read from a hidden prompt or stdin, never from
// an argument, and is never printed: arguments end up in shell history and
// process listings.
func runAuth(command string, args []string, env cliEnv) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		_, _ = io.WriteString(env.stdout, authUsageText)
		return exitOK
	}
	if len(args) > 1 || (len(args) == 1 && !strings.EqualFold(args[0], "openrouter")) {
		// The argument is deliberately not echoed: someone who pasted a key
		// here must not have it printed back.
		return reportError(env.stderr, &usageError{
			`brunel ` + command + ` takes no key argument and only the "openrouter" provider; ` +
				`other providers use pi's own login (run pi, then /login)`,
		})
	}

	writer := env.credentialWriter
	if writer == nil {
		writer = config.NewPlatformCredentialWriter()
	}
	if command == "logout" {
		return runLogout(writer, env)
	}
	return runLogin(writer, env)
}

func runLogin(writer config.CredentialWriter, env cliEnv) int {
	key, err := readKey(env)
	if err != nil {
		return reportError(env.stderr, err)
	}
	if err := writer.SetOpenRouterAPIKey(key); err != nil {
		return reportError(env.stderr, credentialWriteError("could not save the OpenRouter key", err))
	}
	_, _ = fmt.Fprintf(env.stdout, "OpenRouter key saved to Windows Credential Manager (target %q).\n", config.OpenRouterCredentialTarget)
	return exitOK
}

func runLogout(writer config.CredentialWriter, env cliEnv) int {
	err := writer.DeleteOpenRouterAPIKey()
	if errors.Is(err, config.ErrCredentialNotFound) {
		_, _ = io.WriteString(env.stdout, "No OpenRouter key was stored.\n")
		return exitOK
	}
	if err != nil {
		return reportError(env.stderr, credentialWriteError("could not remove the OpenRouter key", err))
	}
	_, _ = io.WriteString(env.stdout, "OpenRouter key removed from Windows Credential Manager.\n")
	return exitOK
}

// credentialWriteError keeps the platform-unsupported code and reports every
// other writer failure as E_CONFIG_CREDENTIAL. Writer errors carry Win32 or
// validation text only, never the key.
func credentialWriteError(what string, err error) error {
	if errors.Is(err, config.ErrUnsupportedPlatform) {
		return codedError{config.ErrUnsupportedPlatform.Code, what + ": Windows Credential Manager is not available on this platform"}
	}
	return codedError{config.ErrConfigCredential.Code, what + ": " + err.Error()}
}

// readKey reads the key from the hidden prompt when stdin is a terminal and
// from the first line of stdin otherwise (for a password manager or a pipe).
func readKey(env cliEnv) (string, error) {
	var key string
	if env.tty.stdin && env.readSecret != nil {
		_, _ = io.WriteString(env.stderr, "OpenRouter API key (input is hidden): ")
		secret, err := env.readSecret()
		_, _ = io.WriteString(env.stderr, "\n")
		if err != nil {
			return "", codedError{config.ErrConfigCredential.Code, "could not read the key from the terminal"}
		}
		key = secret
	} else {
		line, err := bufio.NewReader(io.LimitReader(env.stdin, maxKeyInput)).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", codedError{config.ErrConfigCredential.Code, "could not read the key from stdin"}
		}
		key = line
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", codedError{config.ErrConfigCredential.Code, "no key was entered; nothing was changed"}
	}
	return key, nil
}

// readTerminalSecret reads one line from the console without echoing it.
func readTerminalSecret() (string, error) {
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	return string(b), err
}

const authUsageText = `Usage:
  brunel login [openrouter]    save your OpenRouter API key in Windows Credential Manager
  brunel logout [openrouter]   remove it

The key is read from a hidden prompt at a terminal, or from the first line of
stdin when piped (for example from a password manager). It is never taken from
an argument and never printed. Other providers use pi's own login.
`
