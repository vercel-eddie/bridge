package commands

import (
	"context"
	"fmt"
	"log/slog"
	"os/signal"
	"syscall"
	"time"

	"github.com/urfave/cli/v3"
	"github.com/vercel-eddie/bridge/pkg/mutagen"
	"github.com/vercel-eddie/bridge/pkg/proxy"
	"github.com/vercel-eddie/bridge/pkg/sshserver"
)

func Server() *cli.Command {
	return &cli.Command{
		Name:  "server",
		Usage: "Start the SSH server",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "name",
				Usage:   "Name of the sandbox",
				Sources: cli.EnvVars("REACH_NAME"),
			},
			&cli.StringFlag{
				Name:    "host",
				Usage:   "Host to bind to",
				Value:   "0.0.0.0",
				Sources: cli.EnvVars("SSH_HOST"),
			},
			&cli.IntFlag{
				Name:    "port",
				Usage:   "SSH port to listen on",
				Value:   2222,
				Sources: cli.EnvVars("SSH_PORT"),
			},
			&cli.IntFlag{
				Name:    "proxy-port",
				Usage:   "HTTP CONNECT proxy port",
				Value:   3000,
				Sources: cli.EnvVars("PROXY_PORT"),
			},
			&cli.DurationFlag{
				Name:    "idle-timeout",
				Usage:   "Idle timeout for connections",
				Value:   30 * time.Minute,
				Sources: cli.EnvVars("SSH_IDLE_TIMEOUT"),
			},
			&cli.DurationFlag{
				Name:    "max-timeout",
				Usage:   "Max timeout for connections",
				Value:   2 * time.Hour,
				Sources: cli.EnvVars("SSH_MAX_TIMEOUT"),
			},
		},
		Action: runServer,
	}
}

func runServer(ctx context.Context, c *cli.Command) error {
	// Install mutagen agent if not already installed
	// This is needed for file sync between devcontainer and sandbox
	if err := mutagen.InstallAgent(); err != nil {
		slog.Warn("Failed to install mutagen agent", "error", err)
		// Continue anyway - sync just won't work
	} else {
		slog.Info("Mutagen agent ready", "path", mutagen.AgentPath())
	}

	name := c.String("name")
	host := c.String("host")
	sshPort := c.Int("port")
	proxyPort := c.Int("proxy-port")

	cfg := sshserver.Config{
		Host:            host,
		Port:            sshPort,
		IdleTimeout:     c.Duration("idle-timeout"),
		MaxTimeout:      c.Duration("max-timeout"),
		AgentForwarding: true,
		SessionHandler:  sshserver.ShellHandler(),
	}

	if err := cfg.Validate(); err != nil {
		return err
	}

	srv, err := sshserver.New(cfg)
	if err != nil {
		return err
	}

	// Use WebSocket server for tunneling
	wsServer := proxy.NewWSServer(proxy.WSServerConfig{
		Addr:   fmt.Sprintf("%s:%d", host, proxyPort),
		Dialer: &proxy.TCPDialer{Addr: fmt.Sprintf("localhost:%d", sshPort)},
		Name:   name,
	})

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 2)
	go func() {
		errCh <- srv.Start()
	}()
	go func() {
		errCh <- wsServer.Start()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
		wsServer.Shutdown(shutdownCtx)
		return nil
	}
}
