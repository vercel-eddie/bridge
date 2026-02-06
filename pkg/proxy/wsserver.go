package proxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/puzpuzpuz/xsync/v3"
	bridgev1 "github.com/vercel-eddie/bridge/api/go/bridge/v1"
	"github.com/vercel-eddie/bridge/pkg/bidi"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	// registrationTimeout is how long to wait for a registration message
	registrationTimeout = 30 * time.Second
)

// pendingTunnel represents a client connection waiting for its server pair
type pendingTunnel struct {
	clientConn *websocket.Conn
	ready      chan *websocket.Conn // receives the server connection when matched
	done       chan struct{}        // closed when the tunnel is finished
	cancel     context.CancelFunc
}

// WSServer is a WebSocket server that tunnels connections to a target.
type WSServer struct {
	httpServer *http.Server
	addr       string
	dialer     Dialer
	name       string
	upgrader   websocket.Upgrader
	conns      *xsync.MapOf[*websocket.Conn, struct{}]

	// pendingTunnels tracks client connections waiting for their server pair
	// keyed by connection_key
	pendingTunnels *xsync.MapOf[string, *pendingTunnel]

	pairingTimeout time.Duration
}

// WSServerConfig configures the WebSocket server.
type WSServerConfig struct {
	Addr           string        // Listen address (e.g., ":3000" or "0.0.0.0:3000")
	Dialer         Dialer        // Dialer for establishing connections to the target
	Name           string        // Name of the sandbox
	PairingTimeout time.Duration // How long to wait for server to pair with client (default 60s)
}

// NewWSServer creates a new WebSocket tunnel server.
func NewWSServer(cfg WSServerConfig) *WSServer {
	addr := cfg.Addr
	if addr == "" {
		addr = ":3000"
	}

	pairingTimeout := cfg.PairingTimeout
	if pairingTimeout == 0 {
		pairingTimeout = 60 * time.Second
	}

	s := &WSServer{
		addr:           addr,
		dialer:         cfg.Dialer,
		name:           cfg.Name,
		conns:          xsync.NewMapOf[*websocket.Conn, struct{}](),
		pendingTunnels: xsync.NewMapOf[string, *pendingTunnel](),
		pairingTimeout: pairingTimeout,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  32 * 1024,
			WriteBufferSize: 32 * 1024,
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins for tunnel
			},
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/ssh", s.handleSSH)
	mux.HandleFunc("/tunnel", s.handleTunnel)

	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  0, // No timeout for WebSocket
		WriteTimeout: 0,
	}

	return s
}

func (s *WSServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Bridge-Name", s.name)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (s *WSServer) handleSSH(w http.ResponseWriter, r *http.Request) {
	wsConn, err := s.upgrader.Upgrade(w, r, http.Header{
		"X-Bridge-Name": []string{s.name},
	})
	if err != nil {
		slog.Error("failed to upgrade websocket", "error", err, "remote", r.RemoteAddr)
		return
	}

	s.conns.Store(wsConn, struct{}{})
	defer func() {
		s.conns.Delete(wsConn)
		wsConn.Close()
	}()

	remoteAddr := r.RemoteAddr
	slog.Info("SSH websocket tunnel connected", "remote", remoteAddr)

	// Dial the target
	targetConn, err := s.dialer.Dial(r.Context())
	if err != nil {
		slog.Error("failed to dial target", "error", err, "remote", remoteAddr)
		return
	}
	defer targetConn.Close()

	slog.Info("connected to SSH target", "remote", remoteAddr)

	// Create adapters for bidirectional copy
	wsAdapter := &wsConnAdapter{conn: wsConn}

	bidi.New(wsAdapter, targetConn).Wait(context.Background())

	slog.Info("SSH websocket tunnel disconnected", "remote", remoteAddr)
}

func (s *WSServer) handleTunnel(w http.ResponseWriter, r *http.Request) {
	wsConn, err := s.upgrader.Upgrade(w, r, http.Header{
		"X-Bridge-Name": []string{s.name},
	})
	if err != nil {
		slog.Error("failed to upgrade websocket for tunnel", "error", err, "remote", r.RemoteAddr)
		return
	}

	s.conns.Store(wsConn, struct{}{})
	defer func() {
		s.conns.Delete(wsConn)
		_ = wsConn.Close()
	}()

	remoteAddr := r.RemoteAddr
	slog.Debug("tunnel connection established", "remote", remoteAddr)

	// Set read deadline for registration message
	_ = wsConn.SetReadDeadline(time.Now().Add(registrationTimeout))

	// Wait for registration message
	messageType, data, err := wsConn.ReadMessage()
	if err != nil {
		slog.Error("failed to read registration message", "error", err, "remote", remoteAddr)
		return
	}

	// Clear the deadline after successful read
	_ = wsConn.SetReadDeadline(time.Time{})

	if messageType != websocket.BinaryMessage && messageType != websocket.TextMessage {
		slog.Error("unexpected message type for registration", "type", messageType, "remote", remoteAddr)
		return
	}

	// Parse the registration message
	var msg bridgev1.Message
	if err := proto.Unmarshal(data, &msg); err != nil {
		slog.Error("failed to parse registration message", "error", err, "remote", remoteAddr)
		return
	}

	reg := msg.GetRegistration()
	if reg == nil {
		slog.Error("registration message missing registration field", "remote", remoteAddr)
		return
	}

	slog.Debug("received tunnel registration",
		"remote", remoteAddr,
		"is_server", reg.GetIsServer(),
		"connection_key", reg.GetConnectionKey(),
		"has_bypass_secret", reg.GetProtectionBypassSecret() != "",
	)

	// Derive the public sandbox URL from the Host header so the dispatcher
	// receives a routable URL rather than the server's bind address.
	sandboxURL := "https://" + r.Host

	if reg.GetIsServer() {
		s.handleServerRegistration(wsConn, reg, remoteAddr)
	} else {
		s.handleClientRegistration(r.Context(), wsConn, reg, remoteAddr, sandboxURL)
	}
}

func (s *WSServer) handleClientRegistration(ctx context.Context, wsConn *websocket.Conn, reg *bridgev1.Message_Registration, remoteAddr string, sandboxURL string) {
	functionURL := reg.GetFunctionUrl()

	if functionURL == "" {
		slog.Error("client registration missing function_url", "remote", remoteAddr)
		s.sendError(wsConn, "registration missing function_url")
		return
	}

	// Generate a random connection key for pairing
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		slog.Error("failed to generate connection key", "error", err, "remote", remoteAddr)
		s.sendError(wsConn, "internal error generating connection key")
		return
	}
	connectionKey := hex.EncodeToString(keyBytes)

	// Create a context with timeout for the entire pairing process
	pairCtx, cancel := context.WithTimeout(ctx, s.pairingTimeout)
	defer cancel()

	// Create pending tunnel entry
	pending := &pendingTunnel{
		clientConn: wsConn,
		ready:      make(chan *websocket.Conn, 1),
		done:       make(chan struct{}),
		cancel:     cancel,
	}

	s.pendingTunnels.Store(connectionKey, pending)

	defer func() {
		// Only delete if this is still our pending entry
		s.pendingTunnels.Compute(connectionKey, func(oldValue *pendingTunnel, loaded bool) (*pendingTunnel, bool) {
			if loaded && oldValue == pending {
				return nil, true // delete
			}
			return oldValue, false // keep existing
		})
	}()

	// POST to the dispatcher to trigger server connection, including connection_key
	if err := s.notifyDispatcher(pairCtx, functionURL, sandboxURL, connectionKey, reg.GetProtectionBypassSecret()); err != nil {
		slog.Error("failed to notify dispatcher", "error", err, "function_url", functionURL, "remote", remoteAddr)
		s.sendError(wsConn, fmt.Sprintf("failed to connect to dispatcher: %v", err))
		return
	}

	slog.Debug("notified dispatcher, waiting for server connection",
		"connection_key", connectionKey,
		"function_url", functionURL,
		"remote", remoteAddr,
	)

	// Wait for server connection
	select {
	case serverConn := <-pending.ready:
		slog.Info("tunnel paired",
			"connection_key", connectionKey,
			"client", remoteAddr,
		)

		// Relay messages between client and server, preserving message boundaries
		relayMessages(wsConn, serverConn)

		// Signal that we're done so the server handler can exit
		close(pending.done)

		slog.Debug("tunnel closed", "connection_key", connectionKey)

	case <-pairCtx.Done():
		slog.Error("timeout waiting for server connection",
			"connection_key", connectionKey,
			"remote", remoteAddr,
		)
		s.sendError(wsConn, fmt.Sprintf("timeout waiting for server connection for connection_key %s", connectionKey))
		close(pending.done)
	}
}

func (s *WSServer) handleServerRegistration(wsConn *websocket.Conn, reg *bridgev1.Message_Registration, remoteAddr string) {
	connectionKey := reg.GetConnectionKey()

	if connectionKey == "" {
		slog.Error("server registration missing connection_key", "remote", remoteAddr)
		s.sendError(wsConn, "registration missing connection_key")
		return
	}

	pending, ok := s.pendingTunnels.LoadAndDelete(connectionKey)
	if !ok {
		slog.Error("no pending client for server registration",
			"connection_key", connectionKey,
			"remote", remoteAddr,
		)
		s.sendError(wsConn, fmt.Sprintf("no pending client for connection_key %s", connectionKey))
		return
	}

	slog.Debug("server registered, pairing with client",
		"connection_key", connectionKey,
		"remote", remoteAddr,
	)

	// Send server connection to the waiting client handler
	select {
	case pending.ready <- wsConn:
		// Successfully paired - wait for the tunnel to complete
		// The client handler will close the done channel when bidi copy finishes
		<-pending.done
		slog.Debug("server handler exiting after tunnel closed", "connection_key", connectionKey)
	default:
		slog.Error("failed to pair server with client",
			"connection_key", connectionKey,
			"remote", remoteAddr,
		)
		s.sendError(wsConn, fmt.Sprintf("failed to pair server with client for connection_key %s", connectionKey))
	}
}

func (s *WSServer) sendError(wsConn *websocket.Conn, errMsg string) {
	msg := &bridgev1.Message{
		Error: errMsg,
		Close: true,
	}
	data, err := proto.Marshal(msg)
	if err != nil {
		slog.Error("failed to marshal error message", "error", err)
		return
	}
	if err := wsConn.WriteMessage(websocket.BinaryMessage, data); err != nil {
		slog.Error("failed to send error message", "error", err)
	}
}

// relayMessages relays WebSocket messages between two connections,
// preserving message boundaries for proper protobuf parsing.
func relayMessages(conn1, conn2 *websocket.Conn) {
	done := make(chan struct{}, 2)

	// conn1 -> conn2
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			messageType, data, err := conn1.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					slog.Debug("relay read error from conn1", "error", err)
				}
				return
			}
			if err := conn2.WriteMessage(messageType, data); err != nil {
				slog.Debug("relay write error to conn2", "error", err)
				return
			}
		}
	}()

	// conn2 -> conn1
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			messageType, data, err := conn2.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					slog.Debug("relay read error from conn2", "error", err)
				}
				return
			}
			if err := conn1.WriteMessage(messageType, data); err != nil {
				slog.Debug("relay write error to conn1", "error", err)
				return
			}
		}
	}()

	// Wait for one direction to finish
	<-done
}

func (s *WSServer) notifyDispatcher(ctx context.Context, functionURL string, sandboxURL string, connectionKey string, protectionBypassSecret string) error {
	// Build the connect URL
	connectURL := functionURL + "/__tunnel/connect"

	slog.Debug("notifying dispatcher",
		"connect_url", connectURL,
		"connection_key", connectionKey,
		"has_bypass_secret", protectionBypassSecret != "",
	)

	// Create the ServerConnection payload
	payload := &bridgev1.ServerConnection{
		SandboxUrl:    sandboxURL,
		ConnectionKey: connectionKey,
	}

	jsonData, err := protojson.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal ServerConnection: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, connectURL, bytes.NewReader(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if protectionBypassSecret != "" {
		req.Header.Set("x-vercel-protection-bypass", protectionBypassSecret)
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to POST to dispatcher: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("dispatcher returned status %d", resp.StatusCode)
	}

	return nil
}

// Start starts the WebSocket server.
func (s *WSServer) Start() error {
	slog.Info("starting websocket tunnel server", "addr", s.addr)
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully shuts down the server.
func (s *WSServer) Shutdown(ctx context.Context) error {
	slog.Info("shutting down websocket tunnel server")

	// Close all active WebSocket connections
	s.conns.Range(func(conn *websocket.Conn, _ struct{}) bool {
		conn.Close()
		return true
	})

	return s.httpServer.Shutdown(ctx)
}

// Addr returns the address the server is listening on.
func (s *WSServer) Addr() string {
	return s.addr
}

// wsConnAdapter adapts a websocket.Conn to io.ReadWriteCloser
type wsConnAdapter struct {
	conn    *websocket.Conn
	readMu  sync.Mutex
	writeMu sync.Mutex
	buf     []byte
	offset  int
}

func (a *wsConnAdapter) Read(p []byte) (int, error) {
	a.readMu.Lock()
	defer a.readMu.Unlock()

	// If we have buffered data, return it
	if a.offset < len(a.buf) {
		n := copy(p, a.buf[a.offset:])
		a.offset += n
		return n, nil
	}

	// Read next message
	messageType, data, err := a.conn.ReadMessage()
	if err != nil {
		return 0, err
	}

	if messageType != websocket.BinaryMessage {
		// Skip non-binary messages, try again
		return a.Read(p)
	}

	a.buf = data
	a.offset = 0

	n := copy(p, a.buf)
	a.offset = n
	return n, nil
}

func (a *wsConnAdapter) Write(p []byte) (int, error) {
	a.writeMu.Lock()
	defer a.writeMu.Unlock()

	err := a.conn.WriteMessage(websocket.BinaryMessage, p)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func (a *wsConnAdapter) Close() error {
	return a.conn.Close()
}
