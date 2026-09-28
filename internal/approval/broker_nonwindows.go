//go:build !windows

package approval

import (
	"context"
	"io"

	"github.com/bext1998/brunel/internal/safety"
)

// Broker is unavailable off Windows; StartBroker always fails, so the host
// provides no approval channel and CONFIRM decisions fail closed.
type Broker struct{}

func StartBroker(context.Context, safety.Approver) (*Broker, error) {
	return nil, ErrUnsupportedPlatform
}

func (*Broker) Env() map[string]string { return nil }

func (*Broker) Close() error { return nil }

func dialPipe(context.Context, string) (io.ReadWriteCloser, error) {
	return nil, ErrUnsupportedPlatform
}
