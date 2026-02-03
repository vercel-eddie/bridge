package sshserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/charmbracelet/wish/logging"
	gossh "golang.org/x/crypto/ssh"
)

type Server struct {
	srv  *ssh.Server
	addr string
}

type Config struct {
	Host            string
	Port            int
	HostKeyPath     string
	IdleTimeout     time.Duration
	MaxTimeout      time.Duration
	AgentForwarding bool
	SessionHandler  ssh.Handler
	Middleware      []wish.Middleware
}

func DefaultConfig() Config {
	return Config{
		Host:            "0.0.0.0",
		Port:            2222,
		IdleTimeout:     30 * time.Minute,
		MaxTimeout:      2 * time.Hour,
		AgentForwarding: true,
	}
}

func (c Config) Validate() error {
	if net.ParseIP(c.Host) == nil {
		return fmt.Errorf("invalid host IP: %s", c.Host)
	}

	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("invalid port: %d (must be 1-65535)", c.Port)
	}

	addr := fmt.Sprintf("%s:%d", c.Host, c.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("port %d is not available: %w", c.Port, err)
	}
	ln.Close()

	if c.HostKeyPath != "" {
		if _, err := os.Stat(c.HostKeyPath); errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("host key file not found: %s", c.HostKeyPath)
		}
	}

	if c.IdleTimeout < 0 {
		return errors.New("idle-timeout must be positive")
	}

	if c.MaxTimeout < 0 {
		return errors.New("max-timeout must be positive")
	}

	if c.MaxTimeout > 0 && c.IdleTimeout > 0 && c.IdleTimeout > c.MaxTimeout {
		return errors.New("idle-timeout cannot exceed max-timeout")
	}

	return nil
}

func New(cfg Config) (*Server, error) {
	if cfg.Host == "" {
		cfg.Host = "0.0.0.0"
	}
	if cfg.Port == 0 {
		cfg.Port = 2222
	}

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	opts := []ssh.Option{
		wish.WithAddress(addr),
		wish.WithIdleTimeout(cfg.IdleTimeout),
		wish.WithMaxTimeout(cfg.MaxTimeout),
	}

	if cfg.HostKeyPath != "" {
		opts = append(opts, wish.WithHostKeyPath(cfg.HostKeyPath))
	}

	middleware := []wish.Middleware{logging.Middleware()}
	if len(cfg.Middleware) > 0 {
		middleware = append(cfg.Middleware, middleware...)
	}
	opts = append(opts, wish.WithMiddleware(middleware...))

	srv, err := wish.NewServer(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create server: %w", err)
	}

	if cfg.AgentForwarding {
		srv.RequestHandlers = map[string]ssh.RequestHandler{
			"auth-agent-req@openssh.com": func(ctx ssh.Context, srv *ssh.Server, req *gossh.Request) (bool, []byte) {
				ssh.SetAgentRequested(ctx)
				return true, nil
			},
		}
	}

	if cfg.SessionHandler != nil {
		srv.Handler = cfg.SessionHandler
	}

	return &Server{
		srv:  srv,
		addr: addr,
	}, nil
}

func (s *Server) Start() error {
	slog.Info("starting ssh server", "addr", s.addr)
	return s.srv.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	slog.Info("shutting down ssh server")
	return s.srv.Shutdown(ctx)
}

func (s *Server) Addr() string {
	return s.addr
}
