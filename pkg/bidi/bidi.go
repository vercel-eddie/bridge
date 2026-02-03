package bidi

import (
	"context"
	"io"
)

// Pipe manages bidirectional copying between two ReadWriteClosers.
type Pipe struct {
	a     io.Closer
	b     io.Closer
	errCh chan error
}

// New creates a new bidirectional pipe between a and b.
// Data is copied in both directions concurrently.
func New(a, b io.ReadWriteCloser) *Pipe {
	p := &Pipe{
		a:     a,
		b:     b,
		errCh: make(chan error, 2),
	}

	go func() {
		_, err := io.Copy(a, b)
		p.errCh <- err
	}()

	go func() {
		_, err := io.Copy(b, a)
		p.errCh <- err
	}()

	return p
}

// Wait blocks until one direction completes or the context is canceled.
// If the context is canceled, both connections are closed.
// Returns the first error encountered, or the context error.
func (p *Pipe) Wait(ctx context.Context) error {
	select {
	case err := <-p.errCh:
		return err
	case <-ctx.Done():
		p.a.Close()
		p.b.Close()
		return ctx.Err()
	}
}
