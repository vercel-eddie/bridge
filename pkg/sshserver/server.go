package sshserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"time"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/creack/pty"
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

	// Create basic server options
	opts := []ssh.Option{
		wish.WithAddress(addr),
		wish.WithIdleTimeout(cfg.IdleTimeout),
		wish.WithMaxTimeout(cfg.MaxTimeout),
	}

	if cfg.HostKeyPath != "" {
		opts = append(opts, wish.WithHostKeyPath(cfg.HostKeyPath))
	}

	// SFTP subsystem support (needed for mutagen file sync)
	// Note: Disabled temporarily to debug chmod issue
	// opts = append(opts, wish.WithSubsystem("sftp", sftpSubsystem))

	// Create server first
	srv, err := wish.NewServer(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create server: %w", err)
	}

	// Now set up the handler chain manually
	// Start with the session handler as the base
	var handler ssh.Handler
	if cfg.SessionHandler != nil {
		handler = cfg.SessionHandler
	} else {
		// Default handler that does nothing
		handler = func(s ssh.Session) {
			s.Exit(0)
		}
	}

	// Note: SCP middleware removed - mutagen uses SFTP for file transfers

	// Add custom middleware if any
	for i := len(cfg.Middleware) - 1; i >= 0; i-- {
		handler = cfg.Middleware[i](handler)
	}

	// Add tracing middleware as the outermost layer
	tracingHandler := handler
	handler = func(s ssh.Session) {
		slog.Info("SSH session",
			"user", s.User(),
			"command", s.Command(),
			"raw_command", s.RawCommand(),
			"remote", s.RemoteAddr().String(),
		)
		tracingHandler(s)
	}

	// Set the handler on the server
	srv.Handler = handler

	// Set up agent forwarding
	if cfg.AgentForwarding {
		if srv.RequestHandlers == nil {
			srv.RequestHandlers = map[string]ssh.RequestHandler{}
		}
		srv.RequestHandlers["auth-agent-req@openssh.com"] = func(ctx ssh.Context, srv *ssh.Server, req *gossh.Request) (bool, []byte) {
			ssh.SetAgentRequested(ctx)
			return true, nil
		}
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

// ShellHandler returns a handler that provides shell access
// The SSH server runs as the vercel-sandbox user, so commands are executed directly
func ShellHandler() ssh.Handler {
	return func(s ssh.Session) {
		cmd := s.Command()
		ptyReq, winCh, isPty := s.Pty()

		// Get current user info (server runs as this user)
		currentUser, err := user.Current()
		if err != nil {
			slog.Error("Failed to get current user", "error", err)
			s.Exit(1)
			return
		}

		if isPty {
			// Interactive shell with PTY
			var shell *exec.Cmd
			if len(cmd) > 0 {
				shell = exec.Command(cmd[0], cmd[1:]...)
			} else {
				shell = exec.Command("/bin/sh")
			}

			shell.Env = append(os.Environ(), fmt.Sprintf("TERM=%s", ptyReq.Term))
			shell.Dir = currentUser.HomeDir

			ptmx, err := pty.Start(shell)
			if err != nil {
				slog.Error("failed to start pty", "error", err)
				s.Exit(1)
				return
			}
			defer ptmx.Close()

			// Handle window size changes
			go func() {
				for win := range winCh {
					pty.Setsize(ptmx, &pty.Winsize{
						Rows: uint16(win.Height),
						Cols: uint16(win.Width),
					})
				}
			}()

			// Copy data between session and PTY
			go io.Copy(ptmx, s)
			io.Copy(s, ptmx)

			shell.Wait()
		} else {
			// Non-interactive command execution
			rawCmd := s.RawCommand()
			if rawCmd == "" {
				io.WriteString(s, "No command provided\n")
				s.Exit(1)
				return
			}

			slog.Info("SSH executing command", "user", s.User(), "rawCmd", rawCmd)

			// Check if command contains shell metacharacters that require shell interpretation
			needsShell := strings.ContainsAny(rawCmd, "|&;<>()$`\\\"'*?[]#~%")

			var shell *exec.Cmd
			if needsShell {
				// Complex command - run through shell
				slog.Info("SSH using shell for command")
				shell = exec.Command("/bin/sh", "-c", rawCmd)
			} else {
				// Simple command - parse and run directly
				parts := strings.Fields(rawCmd)
				if len(parts) == 0 {
					io.WriteString(s, "No command provided\n")
					s.Exit(1)
					return
				}

				slog.Info("SSH running command directly", "parts", parts)
				shell = exec.Command(parts[0], parts[1:]...)
			}

			shell.Stdin = s
			shell.Stdout = s
			shell.Stderr = s.Stderr()
			shell.Dir = currentUser.HomeDir
			shell.Env = os.Environ()

			slog.Info("SSH command executing", "dir", currentUser.HomeDir)

			if err := shell.Run(); err != nil {
				var exitErr *exec.ExitError
				if errors.As(err, &exitErr) {
					slog.Info("SSH command exited", "exit_code", exitErr.ExitCode())
					s.Exit(exitErr.ExitCode())
					return
				}
				slog.Error("command failed", "error", err)
				s.Exit(1)
				return
			}
			slog.Info("SSH command completed")
		}
	}
}
