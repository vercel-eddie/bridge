package main

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/reach/pkg/commands"
	"github.com/urfave/cli/v3"
)

var version = "dev"

func main() {
	commands.Version = version

	app := &cli.Command{
		Name:    "reach",
		Usage:   "Reach CLI",
		Version: version,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "log-level",
				Usage:   "Log level (debug, info, warn, error)",
				Value:   "info",
				Sources: cli.EnvVars("LOG_LEVEL"),
				Action: func(ctx context.Context, c *cli.Command, v string) error {
					level := parseLogLevel(v)
					slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
						Level: level,
					})))
					return nil
				},
			},
		},
		Commands: []*cli.Command{
			commands.Create(),
			commands.Connect(),
			commands.Server(),
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
