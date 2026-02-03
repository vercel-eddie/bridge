package commands

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/reach/pkg/sandbox"
	"github.com/reach/pkg/sessions"
	"github.com/reach/pkg/vercel"
	"github.com/urfave/cli/v3"
)

func Create() *cli.Command {
	return &cli.Command{
		Name:  "create",
		Usage: "Create a new sandbox",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "name",
				Usage: "Name for the sandbox (RFC 1123 compliant, auto-generated if not provided)",
			},
			&cli.StringFlag{
				Name:   "source-dir",
				Usage:  "Path to reach source directory (dev mode only)",
				Value:  ".",
				Hidden: true,
			},
			&cli.StringFlag{
				Name:    "token",
				Usage:   "Vercel API token",
				Sources: cli.EnvVars("VERCEL_TOKEN"),
			},
			&cli.StringFlag{
				Name:    "team-id",
				Usage:   "Vercel team ID",
				Sources: cli.EnvVars("VERCEL_TEAM_ID"),
			},
			&cli.StringFlag{
				Name:    "project-id",
				Usage:   "Vercel project ID",
				Sources: cli.EnvVars("VERCEL_PROJECT_ID"),
			},
			&cli.StringFlag{
				Name:  "runtime",
				Usage: "Sandbox runtime (node18, node20, node22)",
				Value: "node22",
			},
			&cli.DurationFlag{
				Name:  "timeout",
				Usage: "Sandbox timeout",
				Value: time.Hour,
			},
			&cli.IntFlag{
				Name:  "proxy-port",
				Usage: "Proxy port inside sandbox",
				Value: 3000,
			},
		},
		Action: runCreate,
	}
}

func runCreate(ctx context.Context, c *cli.Command) error {
	store, err := sessions.NewStore()
	if err != nil {
		return fmt.Errorf("failed to initialize session store: %w", err)
	}

	name := c.String("name")
	if name != "" {
		if err := sessions.ValidateName(name); err != nil {
			return err
		}
		if store.Exists(name) {
			return fmt.Errorf("session %q already exists", name)
		}
	} else {
		name = store.GenerateName()
	}
	slog.Info("using session name", "name", name)

	token := c.String("token")
	if token == "" {
		token, err = vercel.LoadToken()
		if err != nil {
			return err
		}
	}

	teamID := c.String("team-id")
	projectID := c.String("project-id")
	if teamID == "" || projectID == "" {
		projCfg, err := vercel.LoadProjectConfig()
		if err != nil {
			return err
		}
		if teamID == "" {
			teamID = projCfg.TeamID
		}
		if projectID == "" {
			projectID = projCfg.ProjectID
		}
	}

	client := sandbox.NewClient(token, teamID, projectID)
	proxyPort := c.Int("proxy-port")

	slog.Info("creating sandbox")
	sb, routes, err := client.Create(ctx, sandbox.CreateOptions{
		Runtime: c.String("runtime"),
		Timeout: c.Duration("timeout"),
		Ports:   []int{int(proxyPort)},
	})
	if err != nil {
		return err
	}
	slog.Info("sandbox created", "id", sb.ID)

	slog.Info("waiting for sandbox to start")
	sb, routes, err = client.WaitForRunning(ctx, sb.ID)
	if err != nil {
		return err
	}
	slog.Info("sandbox running", "id", sb.ID)

	if Version == "dev" {
		sourceDir := c.String("source-dir")
		if err := installDev(ctx, client, sb.ID, sourceDir); err != nil {
			return err
		}
	} else {
		downloadURL := fmt.Sprintf("https://github.com/reach/releases/download/%s/reach-linux-amd64", Version)
		slog.Info("installing reach binary")
		installCmd := fmt.Sprintf("curl -fsSL %s -o /usr/local/bin/reach && chmod +x /usr/local/bin/reach", downloadURL)
		_, err = client.RunCommand(ctx, sb.ID, "sh", []string{"-c", installCmd}, false)
		if err != nil {
			return fmt.Errorf("failed to install reach: %w", err)
		}
	}

	slog.Info("starting reach server")
	serverCmd := fmt.Sprintf("/vercel/sandbox/.reach/bin/reach server --host 0.0.0.0 --port 2222 --proxy-port %d --name %s", proxyPort, name)
	_, err = client.RunCommand(ctx, sb.ID, "sh", []string{"-c", serverCmd}, true)
	if err != nil {
		return fmt.Errorf("failed to start reach server: %w", err)
	}

	var sandboxURL string
	for _, route := range routes {
		if route.Port == int(proxyPort) {
			sandboxURL = route.URL
			break
		}
	}
	if sandboxURL == "" && len(routes) > 0 {
		sandboxURL = routes[0].URL
	}

	if err := store.Add(name, sessions.Session{URL: sandboxURL}); err != nil {
		return fmt.Errorf("failed to save session: %w", err)
	}

	fmt.Printf("Sandbox ready: %s\n", name)
	fmt.Printf("URL: %s\n", sandboxURL)
	fmt.Printf("SSH available on port 2222\n")
	return nil
}

func installDev(ctx context.Context, client sandbox.Client, sandboxID, sourceDir string) error {
	slog.Info("setting up directories")
	_, err := client.RunCommand(ctx, sandboxID, "mkdir", []string{"-p", "/vercel/sandbox/.reach/bin", "/vercel/sandbox/.reach/src"}, false)
	if err != nil {
		return fmt.Errorf("failed to create directories: %w", err)
	}

	slog.Info("transferring local files to sandbox", "source", sourceDir)

	files, err := collectLocalFiles(sourceDir)
	if err != nil {
		return fmt.Errorf("failed to collect local files: %w", err)
	}

	slog.Info("collected files", "count", len(files))

	if err := client.WriteFiles(ctx, sandboxID, files); err != nil {
		return fmt.Errorf("failed to write files: %w", err)
	}

	slog.Info("installing go")
	_, err = client.RunCommand(ctx, sandboxID, "sh", []string{"-c",
		"curl -fsSL https://go.dev/dl/go1.23.0.linux-amd64.tar.gz | tar -C /vercel/sandbox/.reach -xzf -",
	}, false)
	if err != nil {
		return fmt.Errorf("failed to install go: %w", err)
	}

	slog.Info("building reach binary")
	_, err = client.RunCommand(ctx, sandboxID, "sh", []string{"-c",
		"cd /vercel/sandbox/.reach/src && /vercel/sandbox/.reach/go/bin/go build -o /vercel/sandbox/.reach/bin/reach ./cmd/reach",
	}, false)
	if err != nil {
		return fmt.Errorf("failed to build reach: %w", err)
	}

	return nil
}

func collectLocalFiles(sourceDir string) ([]sandbox.FileEntry, error) {
	var files []sandbox.FileEntry

	err := filepath.WalkDir(sourceDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			name := filepath.Base(path)
			if name == ".git" || name == "node_modules" || name == ".idea" {
				return filepath.SkipDir
			}
			return nil
		}

		ext := filepath.Ext(path)
		name := filepath.Base(path)
		if ext != ".go" && name != "go.mod" && name != "go.sum" {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		remotePath := "/vercel/sandbox/.reach/src/" + relPath
		files = append(files, sandbox.FileEntry{
			Path:    remotePath,
			Content: content,
		})

		return nil
	})

	return files, err
}
