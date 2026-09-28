//go:build windows

package approval

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// dialPipe opens the broker's pipe. ERROR_PIPE_BUSY means every instance is
// momentarily serving another client (the broker creates the next instance
// right after accepting), so it retries until ctx ends; any other error -
// notably the pipe not existing - is returned at once.
func dialPipe(ctx context.Context, pipe string) (io.ReadWriteCloser, error) {
	for {
		f, err := os.OpenFile(pipe, os.O_RDWR, 0)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, windows.ERROR_PIPE_BUSY) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}
