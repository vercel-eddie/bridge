package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/reach/pkg/bidi"
)

// TCPProxy listens for local TCP connections and forwards them through
// an HTTP CONNECT tunnel to a remote server.
type TCPProxy interface {
	Listen() error
	Serve(ctx context.Context) error
	Shutdown() error
	Addr() string
	Port() int
	ActiveConnections() int64
}

// TCPProxyConfig configures the TCP proxy.
type TCPProxyConfig struct {
	// Host to bind locally
	Host string
	// Port to listen on locally
	Port int
	// ProxyURL is the HTTP CONNECT proxy URL (e.g., "http://remote:3000")
	ProxyURL string
}

type tcpProxy struct {
	listener    net.Listener
	proxyURL    string
	addr        string
	connections atomic.Int64
}

// NewTCPProxy creates a new TCP proxy that forwards connections through HTTP CONNECT.
// If Port is 0, a random available port will be assigned.
func NewTCPProxy(cfg TCPProxyConfig) TCPProxy {
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	return &tcpProxy{
		proxyURL: cfg.ProxyURL,
		addr:     addr,
	}
}

func (p *tcpProxy) Listen() error {
	listener, err := net.Listen("tcp", p.addr)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}
	p.listener = listener
	p.addr = listener.Addr().String()
	return nil
}

func (p *tcpProxy) Serve(ctx context.Context) error {
	if p.listener == nil {
		return fmt.Errorf("must call Listen before Serve")
	}

	slog.Info("starting tcp proxy", "addr", p.addr, "proxy", p.proxyURL)

	for {
		conn, err := p.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				slog.Error("failed to accept connection", "error", err)
				continue
			}
		}

		go p.handleConn(conn)
	}
}

func (p *tcpProxy) Shutdown() error {
	slog.Info("shutting down tcp proxy")
	if p.listener != nil {
		return p.listener.Close()
	}
	return nil
}

func (p *tcpProxy) Addr() string {
	return p.addr
}

func (p *tcpProxy) Port() int {
	if p.listener == nil {
		return 0
	}
	return p.listener.Addr().(*net.TCPAddr).Port
}

func (p *tcpProxy) ActiveConnections() int64 {
	return p.connections.Load()
}

func (p *tcpProxy) handleConn(clientConn net.Conn) {
	p.connections.Add(1)
	defer p.connections.Add(-1)

	remoteAddr := clientConn.RemoteAddr().String()
	slog.Debug("client connected to tcp proxy", "remote", remoteAddr)

	defer func() {
		clientConn.Close()
		slog.Debug("client disconnected from tcp proxy", "remote", remoteAddr)
	}()

	tunnelConn, err := p.dialThroughProxy()
	if err != nil {
		slog.Error("failed to establish tunnel", "error", err, "remote", remoteAddr)
		return
	}
	defer tunnelConn.Close()

	slog.Debug("tunnel established", "remote", remoteAddr)

	bidi.New(clientConn, tunnelConn).Wait(context.Background())
}

func (p *tcpProxy) dialThroughProxy() (net.Conn, error) {
	proxyURL, err := url.Parse(p.proxyURL)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL: %w", err)
	}

	proxyAddr := proxyURL.Host
	if proxyURL.Port() == "" {
		if proxyURL.Scheme == "https" {
			proxyAddr += ":443"
		} else {
			proxyAddr += ":80"
		}
	}

	var conn net.Conn
	if proxyURL.Scheme == "https" {
		// Use TLS for HTTPS URLs
		tlsConn, err := tls.DialWithDialer(
			&net.Dialer{Timeout: 10 * time.Second},
			"tcp",
			proxyAddr,
			&tls.Config{ServerName: proxyURL.Hostname()},
		)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to proxy via TLS: %w", err)
		}
		conn = tlsConn
	} else {
		var err error
		conn, err = net.DialTimeout("tcp", proxyAddr, 10*time.Second)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to proxy: %w", err)
		}
	}

	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Host: "tunnel"},
		Header: make(http.Header),
	}
	req.Header.Set("Proxy-Connection", "keep-alive")

	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to send CONNECT request: %w", err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to read CONNECT response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("proxy returned status %d", resp.StatusCode)
	}

	return conn, nil
}
