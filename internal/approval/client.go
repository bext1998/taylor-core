package approval

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/bext1998/brunel/internal/safety"
)

// Client is the subprocess-side safety.Approver: Gate.Decide calls Confirm
// for a CONFIRM decision and Client relays the Gate's ApprovalPrompt to the
// host's Broker. It never decides on its own - any failure to reach the
// broker or to read a well-formed answer is returned as an error, which the
// Gate turns into E_APPROVAL_DENIED (fail closed).
type Client struct {
	pipe  string
	token string
	// dial opens the connection; tests may replace it.
	dial func(ctx context.Context, pipe string) (io.ReadWriteCloser, error)
}

var _ safety.Approver = (*Client)(nil)

// ClientFromEnv builds a Client from EnvPipe/EnvToken and removes both
// variables from this process's environment, so the pwsh children that
// run_powershell starts (which inherit the environment) never see the
// token. It returns nil when the host provided no approval channel (no
// TTY): the caller then passes a nil Approver to the Gate, and CONFIRM
// fails with E_APPROVAL_REQUIRED_NO_TTY exactly as before.
func ClientFromEnv() *Client {
	pipe := strings.TrimSpace(os.Getenv(EnvPipe))
	token := strings.TrimSpace(os.Getenv(EnvToken))
	_ = os.Unsetenv(EnvPipe)
	_ = os.Unsetenv(EnvToken)
	if pipe == "" || token == "" {
		return nil
	}
	return &Client{pipe: pipe, token: token, dial: dialPipe}
}

// Confirm sends one approval request and waits for the host's answer.
func (c *Client) Confirm(ctx context.Context, prompt safety.ApprovalPrompt) (bool, error) {
	conn, err := c.dial(ctx, c.pipe)
	if err != nil {
		return false, fmt.Errorf("approval channel unavailable: %w", err)
	}

	type result struct {
		approved bool
		err      error
	}
	done := make(chan result, 1)
	go func() {
		approved, err := exchange(conn, request{Token: c.token, Command: prompt.Command, Reason: prompt.Reason})
		done <- result{approved, err}
	}()

	select {
	case r := <-done:
		_ = conn.Close()
		return r.approved, r.err
	case <-ctx.Done():
		// The blocked read may not return until the host answers or the
		// pipe breaks; this process is a one-shot tool call, so the
		// goroutine ends with it.
		_ = conn.Close()
		return false, ctx.Err()
	}
}

func exchange(conn io.ReadWriter, req request) (bool, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return false, err
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return false, fmt.Errorf("approval channel write: %w", err)
	}
	line, err := bufio.NewReader(io.LimitReader(conn, maxMessageBytes)).ReadBytes('\n')
	if err != nil && !(errors.Is(err, io.EOF) && len(line) > 0) {
		return false, fmt.Errorf("approval channel read: %w", err)
	}
	var rep reply
	if err := json.Unmarshal(line, &rep); err != nil {
		return false, errors.New("approval channel: malformed reply")
	}
	if rep.Error != "" {
		return false, fmt.Errorf("approval channel: %s", rep.Error)
	}
	return rep.Approved, nil
}
