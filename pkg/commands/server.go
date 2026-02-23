package commands

import (
	"context"
	"fmt"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/urfave/cli/v3"
	"github.com/vercel/bridge/pkg/proxy"
)

func Server() *cli.Command {
	return &cli.Command{
		Name:   "server",
		Usage:  "Start the bridge gRPC proxy server",
		Hidden: true,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "addr",
				Usage:   "Address to bind the server to",
				Value:   ":9090",
				Sources: cli.EnvVars("BRIDGE_ADDR"),
			},
			&cli.StringSliceFlag{
				Name:    "listen-ports",
				Aliases: []string{"l"},
				Usage:   `L4 port specs for ingress listeners (e.g. "8080/tcp", "9090/udp", "8080")`,
				Sources: cli.EnvVars("BRIDGE_LISTEN_PORTS"),
			},
		},
		Action: runServer,
	}
}

func runServer(ctx context.Context, c *cli.Command) error {
	addr := c.String("addr")

	// Parse listen-ports flag.
	var listenPorts []proxy.ListenPort
	for _, spec := range c.StringSlice("listen-ports") {
		for _, part := range strings.Split(spec, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			lp, err := proxy.ParseListenPort(part)
			if err != nil {
				return fmt.Errorf("invalid listen-port %q: %w", part, err)
			}
			listenPorts = append(listenPorts, lp)
		}
	}

	grpcServer := proxy.NewGRPCServer(addr, listenPorts)

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- grpcServer.Start()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		grpcServer.Shutdown(shutdownCtx)
		return nil
	}
}
