//go:build windows

package approval

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/bext1998/brunel/internal/safety"
)

const (
	pipeBufferBytes  = 64 << 10
	closeWakeTimeout = 5 * time.Second
)

// Broker is the host-side end of the approval channel. It accepts Client
// connections on a per-run named pipe, checks the per-run token, and asks
// the host's own Approver (TUI modal or TTY prompt) one request at a time.
type Broker struct {
	name     string
	token    string
	approver safety.Approver
	ctx      context.Context
	cancel   context.CancelFunc
	sa       *windows.SecurityAttributes

	askMu sync.Mutex // one human prompt at a time

	mu         sync.Mutex
	closed     bool
	active     map[windows.Handle]struct{}
	wg         sync.WaitGroup
	acceptDone chan struct{}
}

// StartBroker creates the pipe and starts accepting requests. ctx bounds
// every approver call: when it is cancelled (the run was cancelled), any
// open prompt is answered with a denial. approver must not be nil.
func StartBroker(ctx context.Context, approver safety.Approver) (*Broker, error) {
	if approver == nil {
		return nil, errors.New("approval broker requires an approver")
	}
	suffix, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	token, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	sa, err := currentUserOnly()
	if err != nil {
		return nil, err
	}
	bctx, cancel := context.WithCancel(ctx)
	b := &Broker{
		name:       `\\.\pipe\brunel-approval-` + suffix,
		token:      token,
		approver:   approver,
		ctx:        bctx,
		cancel:     cancel,
		sa:         sa,
		active:     map[windows.Handle]struct{}{},
		acceptDone: make(chan struct{}),
	}
	first, err := b.newInstance(true)
	if err != nil {
		cancel()
		return nil, err
	}
	b.wg.Add(1)
	go b.acceptLoop(first)
	return b, nil
}

// Env returns the environment entries the host must pass down to the
// --taylor-tool subprocess (through pi and the extension, which inherit it).
func (b *Broker) Env() map[string]string {
	return map[string]string{EnvPipe: b.name, EnvToken: b.token}
}

// Close stops accepting, denies any open prompt, disconnects clients, and
// waits for the broker's goroutines to finish. It is idempotent.
func (b *Broker) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.cancel()
	for h := range b.active {
		// A connection blocked in ReadFile (a client that connected but
		// never wrote) returns once the pipe is disconnected.
		_ = windows.DisconnectNamedPipe(h)
	}
	b.mu.Unlock()

	// acceptLoop blocks in ConnectNamedPipe; wake it by connecting until it
	// notices closed and exits. Retrying covers the window where it is
	// between two pipe instances and a single dial finds nothing listening.
	deadline := time.Now().Add(closeWakeTimeout)
	for {
		select {
		case <-b.acceptDone:
			b.wg.Wait()
			return nil
		default:
		}
		if f, err := os.OpenFile(b.name, os.O_RDWR, 0); err == nil {
			_ = f.Close()
		}
		if time.Now().After(deadline) {
			return errors.New("approval broker: accept loop did not stop")
		}
		select {
		case <-b.acceptDone:
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (b *Broker) acceptLoop(h windows.Handle) {
	defer b.wg.Done()
	defer close(b.acceptDone)
	for {
		err := windows.ConnectNamedPipe(h, nil)
		if b.isClosed() || (err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED)) {
			_ = windows.DisconnectNamedPipe(h)
			_ = windows.CloseHandle(h)
			return
		}

		// Keep one instance listening while this connection is served, so
		// concurrent tool calls are not refused.
		next, nextErr := b.newInstance(false)

		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			_ = windows.DisconnectNamedPipe(h)
			_ = windows.CloseHandle(h)
			if nextErr == nil {
				_ = windows.CloseHandle(next)
			}
			return
		}
		b.active[h] = struct{}{}
		b.wg.Add(1)
		b.mu.Unlock()
		go b.serve(h)

		if nextErr != nil {
			return
		}
		h = next
	}
}

func (b *Broker) serve(h windows.Handle) {
	defer b.wg.Done()
	conn := os.NewFile(uintptr(h), b.name)
	defer func() {
		b.mu.Lock()
		delete(b.active, h)
		b.mu.Unlock()
		_ = windows.FlushFileBuffers(h)
		_ = windows.DisconnectNamedPipe(h)
		_ = conn.Close()
	}()

	var req request
	line, err := bufio.NewReaderSize(io.LimitReader(conn, maxMessageBytes), 4096).ReadBytes('\n')
	if err != nil && !(errors.Is(err, io.EOF) && len(line) > 0) {
		return
	}
	if err := json.Unmarshal(line, &req); err != nil {
		writeReply(conn, reply{Error: "malformed request"})
		return
	}
	if subtle.ConstantTimeCompare([]byte(req.Token), []byte(b.token)) != 1 {
		writeReply(conn, reply{Error: "invalid token"})
		return
	}

	approved, err := b.ask(safety.ApprovalPrompt{Command: req.Command, Reason: req.Reason})
	if err != nil {
		writeReply(conn, reply{Error: err.Error()})
		return
	}
	writeReply(conn, reply{Approved: approved})
}

// ask forwards one prompt to the host approver, serialized so a human only
// ever sees one prompt at a time.
func (b *Broker) ask(prompt safety.ApprovalPrompt) (bool, error) {
	b.askMu.Lock()
	defer b.askMu.Unlock()
	if err := b.ctx.Err(); err != nil {
		return false, err
	}
	return b.approver.Confirm(b.ctx, prompt)
}

func (b *Broker) newInstance(first bool) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(b.name)
	if err != nil {
		return 0, err
	}
	openMode := uint32(windows.PIPE_ACCESS_DUPLEX)
	if first {
		// Fail instead of joining a pipe someone else already created with
		// this name.
		openMode |= windows.FILE_FLAG_FIRST_PIPE_INSTANCE
	}
	pipeMode := uint32(windows.PIPE_TYPE_BYTE | windows.PIPE_READMODE_BYTE | windows.PIPE_WAIT | windows.PIPE_REJECT_REMOTE_CLIENTS)
	h, err := windows.CreateNamedPipe(name, openMode, pipeMode, windows.PIPE_UNLIMITED_INSTANCES, pipeBufferBytes, pipeBufferBytes, 0, b.sa)
	if err != nil {
		return 0, fmt.Errorf("create approval pipe: %w", err)
	}
	return h, nil
}

func (b *Broker) isClosed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}

func writeReply(w io.Writer, r reply) {
	data, err := json.Marshal(r)
	if err != nil {
		return
	}
	_, _ = w.Write(append(data, '\n'))
}

// currentUserOnly builds security attributes whose DACL grants access to
// the current user's SID only, replacing the default pipe DACL (which also
// grants Everyone read access).
func currentUserOnly() (*windows.SecurityAttributes, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("approval pipe: current user: %w", err)
	}
	sd, err := windows.SecurityDescriptorFromString(fmt.Sprintf("D:P(A;;GA;;;%s)", user.User.Sid.String()))
	if err != nil {
		return nil, fmt.Errorf("approval pipe: security descriptor: %w", err)
	}
	return &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
	}, nil
}
