package netutil

import (
	"context"
	"net"
)

// AcceptLoop accepts connections on the listener and calls handler in a
// goroutine for each connection. It blocks until the context is canceled
// or the listener is closed.
func AcceptLoop(ctx context.Context, ln net.Listener, handler func(net.Conn)) error {
	// Close the listener when context is canceled
	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			// Check if context was canceled
			select {
			case <-ctx.Done():
				return context.Cause(ctx)
			default:
			}
			// Listener was closed or other error
			return err
		}

		go handler(conn)
	}
}
