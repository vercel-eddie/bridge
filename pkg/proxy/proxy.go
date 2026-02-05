package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Conn represents a hijacked HTTP CONNECT connection.
type Conn struct {
	Net net.Conn
}

// Server is an HTTP CONNECT proxy that forwards all connections to a configured target.
type Server struct {
	httpServer *http.Server
	addr       string
	target     string
	name       string
	connCh     chan Conn
}

// Config configures the proxy server.
type Config struct {
	Host   string
	Port   int
	Target string // Target address to forward all connections to
	Name   string // Name of the sandbox (returned in x-bridge-name header)
}

// New creates a new HTTP CONNECT proxy server.
func New(cfg Config) *Server {
	if cfg.Host == "" {
		cfg.Host = "0.0.0.0"
	}
	if cfg.Port == 0 {
		cfg.Port = 3000
	}

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	p := &Server{
		addr:   addr,
		target: cfg.Target,
		name:   cfg.Name,
		connCh: make(chan Conn, 100),
	}

	p.httpServer = &http.Server{
		Addr:         addr,
		Handler:      p,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	return p
}

// Conns returns a channel that produces hijacked connections.
func (p *Server) Conns() <-chan Conn {
	return p.connCh
}

// Target returns the configured target address.
func (p *Server) Target() string {
	return p.target
}

func (p *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}

	if r.URL.Path == "/health" {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (p *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}

	if p.name != "" {
		w.Header().Set("X-Bridge-Name", p.name)
	}
	w.WriteHeader(http.StatusOK)

	conn, _, err := hijacker.Hijack()
	if err != nil {
		slog.Error("failed to hijack connection", "error", err)
		return
	}

	slog.Debug("http connect request", "remote", r.RemoteAddr, "target", p.target)

	select {
	case p.connCh <- Conn{Net: conn}:
	default:
		slog.Warn("connection channel full, dropping connection", "remote", r.RemoteAddr)
		conn.Close()
	}
}

// Start starts the proxy server.
func (p *Server) Start() error {
	slog.Info("starting http connect proxy", "addr", p.addr, "target", p.target)
	return p.httpServer.ListenAndServe()
}

// Shutdown gracefully shuts down the proxy server.
func (p *Server) Shutdown(ctx context.Context) error {
	slog.Info("shutting down http connect proxy")
	close(p.connCh)
	return p.httpServer.Shutdown(ctx)
}

// Addr returns the address the server is listening on.
func (p *Server) Addr() string {
	return p.addr
}
