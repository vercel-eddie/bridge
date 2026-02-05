package proxy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WSDialer dials WebSocket connections to a tunnel server.
type WSDialer struct {
	tunnelURL string
	dialer    *websocket.Dialer
}

var _ Dialer = (*WSDialer)(nil)

// NewWSDialer creates a new WebSocket dialer for the given tunnel URL.
func NewWSDialer(tunnelURL string) *WSDialer {
	// Convert HTTP URL to WebSocket URL
	if strings.HasPrefix(tunnelURL, "https://") {
		tunnelURL = "wss://" + strings.TrimPrefix(tunnelURL, "https://")
	} else if strings.HasPrefix(tunnelURL, "http://") {
		tunnelURL = "ws://" + strings.TrimPrefix(tunnelURL, "http://")
	}

	// Ensure /ssh path
	if !strings.HasSuffix(tunnelURL, "/ssh") {
		tunnelURL = strings.TrimSuffix(tunnelURL, "/") + "/ssh"
	}

	return &WSDialer{
		tunnelURL: tunnelURL,
		dialer: &websocket.Dialer{
			HandshakeTimeout: 30 * time.Second,
			ReadBufferSize:   32 * 1024,
			WriteBufferSize:  32 * 1024,
		},
	}
}

// Dial connects to the WebSocket tunnel server and returns an io.ReadWriteCloser.
func (d *WSDialer) Dial(ctx context.Context) (io.ReadWriteCloser, error) {
	u, err := url.Parse(d.tunnelURL)
	if err != nil {
		return nil, fmt.Errorf("invalid tunnel URL: %w", err)
	}

	header := http.Header{}
	header.Set("Origin", fmt.Sprintf("https://%s", u.Host))

	conn, resp, err := d.dialer.DialContext(ctx, d.tunnelURL, header)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("websocket dial failed with status %d: %w", resp.StatusCode, err)
		}
		return nil, fmt.Errorf("websocket dial failed: %w", err)
	}

	wsConn := &wsConn{conn: conn}

	// Start ping loop to keep connection alive
	go wsConn.pingLoop()

	return wsConn, nil
}

// URL returns the tunnel URL.
func (d *WSDialer) URL() string {
	return d.tunnelURL
}

// wsConn wraps a websocket.Conn to implement io.ReadWriteCloser.
type wsConn struct {
	conn    *websocket.Conn
	readMu  sync.Mutex
	writeMu sync.Mutex
	buf     []byte
	offset  int
	closed  bool
}

func (c *wsConn) Read(p []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()

	if c.closed {
		return 0, io.EOF
	}

	// Return buffered data first
	if c.offset < len(c.buf) {
		n := copy(p, c.buf[c.offset:])
		c.offset += n
		return n, nil
	}

	// Read next message
	messageType, data, err := c.conn.ReadMessage()
	if err != nil {
		return 0, err
	}

	if messageType != websocket.BinaryMessage {
		// Skip non-binary messages, try again
		return c.Read(p)
	}

	c.buf = data
	c.offset = 0

	n := copy(p, c.buf)
	c.offset = n
	return n, nil
}

func (c *wsConn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if c.closed {
		return 0, io.ErrClosedPipe
	}

	err := c.conn.WriteMessage(websocket.BinaryMessage, p)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *wsConn) Close() error {
	c.closed = true
	return c.conn.Close()
}

func (c *wsConn) pingLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		c.writeMu.Lock()
		if c.closed {
			c.writeMu.Unlock()
			return
		}
		err := c.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second))
		c.writeMu.Unlock()
		if err != nil {
			return
		}
	}
}
