package commands

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"
	"time"

	"github.com/reach/pkg/proxy"
	"github.com/reach/pkg/sshserver"
	"github.com/urfave/cli/v3"
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
			&cli.StringFlag{
				Name:    "host-key",
				Usage:   "Path to host key file",
				Sources: cli.EnvVars("SSH_HOST_KEY"),
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
	name := c.String("name")
	host := c.String("host")
	sshPort := c.Int("port")
	proxyPort := c.Int("proxy-port")

	cfg := sshserver.Config{
		Host:            host,
		Port:            sshPort,
		HostKeyPath:     c.String("host-key"),
		IdleTimeout:     c.Duration("idle-timeout"),
		MaxTimeout:      c.Duration("max-timeout"),
		AgentForwarding: true,
	}

	if err := cfg.Validate(); err != nil {
		return err
	}

	srv, err := sshserver.New(cfg)
	if err != nil {
		return err
	}

	// Target is the local SSH server
	sshTarget := fmt.Sprintf("localhost:%d", sshPort)

	proxyServer := proxy.New(proxy.Config{
		Host:   host,
		Port:   proxyPort,
		Target: sshTarget,
		Name:   name,
	})

	tunnel := proxy.NewTunnel(sshTarget)

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 2)
	go func() {
		errCh <- srv.Start()
	}()
	go func() {
		errCh <- proxyServer.Start()
	}()

	// Handle incoming proxy connections via the tunnel
	go func() {
		for conn := range proxyServer.Conns() {
			go tunnel.Handle(ctx, conn)
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
		proxyServer.Shutdown(shutdownCtx)
		return nil
	}
}
