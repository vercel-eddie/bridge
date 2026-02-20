package tunnel

import (
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Conn represents a single logical connection multiplexed over the tunnel.
type Conn struct {
	id        string
	source    string // source address (ip:port)
	dest      string // destination address (ip:port)
	client    *Client
	readBuf   chan []byte
	closed    atomic.Bool
	closeOnce sync.Once
}

// newConn creates a new tunnel connection.
func newConn(id, source, dest string, client *Client) *Conn {
	return &Conn{
		id:      id,
		source:  source,
		dest:    dest,
		client:  client,
		readBuf: make(chan []byte, 100),
	}
}

// Read reads data from the tunnel connection.
func (c *Conn) Read(b []byte) (int, error) {
	if c.closed.Load() {
		return 0, io.EOF
	}

	data, ok := <-c.readBuf
	if !ok {
		return 0, io.EOF
	}

	n := copy(b, data)
	return n, nil
}

// Write writes data to the tunnel connection.
// Every write includes source/dest addresses because the dispatcher is a
// serverless function that may be replaced between writes.
func (c *Conn) Write(b []byte) (int, error) {
	if c.closed.Load() {
		return 0, io.ErrClosedPipe
	}

	if err := c.client.sendDataWithAddresses(c.id, b, c.source, c.dest); err != nil {
		return 0, err
	}

	return len(b), nil
}

// Close closes the tunnel connection.
func (c *Conn) Close() error {
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		if err := c.client.sendClose(c.id); err != nil {
			slog.Debug("Failed to send close message", "connection_id", c.id, "error", err)
		}
		close(c.readBuf)
	})
	return nil
}

// LocalAddr returns the local address.
func (c *Conn) LocalAddr() net.Addr {
	host, portStr, _ := net.SplitHostPort(c.source)
	port, _ := strconv.Atoi(portStr)
	if host == "" {
		host = "127.0.0.1"
	}
	return &net.TCPAddr{IP: net.ParseIP(host), Port: port}
}

// RemoteAddr returns the remote address.
func (c *Conn) RemoteAddr() net.Addr {
	host, portStr, _ := net.SplitHostPort(c.dest)
	port, _ := strconv.Atoi(portStr)
	return &net.TCPAddr{IP: net.ParseIP(host), Port: port}
}

// SetDeadline sets the read and write deadlines.
func (c *Conn) SetDeadline(t time.Time) error {
	return nil // Not implemented for tunnel connections
}

// SetReadDeadline sets the read deadline.
func (c *Conn) SetReadDeadline(t time.Time) error {
	return nil // Not implemented for tunnel connections
}

// SetWriteDeadline sets the write deadline.
func (c *Conn) SetWriteDeadline(t time.Time) error {
	return nil // Not implemented for tunnel connections
}
