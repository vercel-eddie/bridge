package plumbing

import (
	"io"
	"net"
	"sync"
	"time"
)

// TunnelConn wraps one side of a multiplexed tunnel connection as a net.Conn.
//
// Reads come from a channel fed by a central read loop (demuxed by connection
// ID). Writes go through a caller-provided send function that handles stream
// serialization and any mutex.
type TunnelConn struct {
	readCh    chan []byte
	closeCh   chan struct{}
	closeOnce sync.Once
	sendFn    func([]byte) error
	onClose   func()

	localAddr  net.Addr
	remoteAddr net.Addr

	buf []byte // leftover from a partial Read
}

// TunnelConnConfig configures a TunnelConn.
type TunnelConnConfig struct {
	// SendFn writes data onto the tunnel stream. The caller is responsible for
	// any mutex and message framing.
	SendFn func([]byte) error

	// OnClose is called once when Close is invoked (e.g. to remove the conn
	// from a tracking map). May be nil.
	OnClose func()

	LocalAddr  net.Addr
	RemoteAddr net.Addr
}

// NewTunnelConn creates a TunnelConn ready for use with bidi.New.
func NewTunnelConn(cfg TunnelConnConfig) *TunnelConn {
	return &TunnelConn{
		readCh:     make(chan []byte, 64),
		closeCh:    make(chan struct{}),
		sendFn:     cfg.SendFn,
		onClose:    cfg.OnClose,
		localAddr:  cfg.LocalAddr,
		remoteAddr: cfg.RemoteAddr,
	}
}

// Deliver pushes data into the read buffer. Called by the central read loop
// when a message arrives for this connection's ID.
func (tc *TunnelConn) Deliver(data []byte) {
	select {
	case tc.readCh <- data:
	case <-tc.closeCh:
	}
}

// SignalClose tears down the connection from the remote side.
func (tc *TunnelConn) SignalClose() {
	tc.closeOnce.Do(func() { close(tc.closeCh) })
}

// --- net.Conn implementation ---

func (tc *TunnelConn) Read(b []byte) (int, error) {
	if len(tc.buf) > 0 {
		n := copy(b, tc.buf)
		tc.buf = tc.buf[n:]
		return n, nil
	}
	select {
	case data, ok := <-tc.readCh:
		if !ok {
			return 0, io.EOF
		}
		n := copy(b, data)
		if n < len(data) {
			tc.buf = data[n:]
		}
		return n, nil
	case <-tc.closeCh:
		return 0, io.EOF
	}
}

func (tc *TunnelConn) Write(b []byte) (int, error) {
	if err := tc.sendFn(b); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (tc *TunnelConn) Close() error {
	tc.closeOnce.Do(func() { close(tc.closeCh) })
	if tc.onClose != nil {
		tc.onClose()
	}
	return nil
}

func (tc *TunnelConn) LocalAddr() net.Addr                { return tc.localAddr }
func (tc *TunnelConn) RemoteAddr() net.Addr               { return tc.remoteAddr }
func (tc *TunnelConn) SetDeadline(_ time.Time) error      { return nil }
func (tc *TunnelConn) SetReadDeadline(_ time.Time) error  { return nil }
func (tc *TunnelConn) SetWriteDeadline(_ time.Time) error { return nil }

var _ net.Conn = (*TunnelConn)(nil)
