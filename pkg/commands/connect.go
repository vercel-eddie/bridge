package commands

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/reach/pkg/proxy"
	"github.com/reach/pkg/sessions"
	"github.com/urfave/cli/v3"
)

func Connect() *cli.Command {
	return &cli.Command{
		Name:  "connect",
		Usage: "Connect to a sandbox",
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:  "local-port",
				Usage: "Local port for SSH proxy (random if not specified)",
			},
		},
		Arguments: []cli.Argument{
			&cli.StringArg{
				Name:      "target",
				UsageText: "The name or URL of a sandbox",
				Config: cli.StringConfig{
					TrimSpace: true,
				},
			},
		},
		Action: runConnect,
	}
}

func runConnect(ctx context.Context, c *cli.Command) error {
	arg := c.StringArg("target")
	localPort := c.Int("local-port")

	store, err := sessions.NewStore()
	if err != nil {
		return fmt.Errorf("failed to initialize session store: %w", err)
	}

	var sandboxURL string
	var name string

	// Check if it's a URL or an alias
	if strings.HasPrefix(arg, "http://") || strings.HasPrefix(arg, "https://") {
		sandboxURL = arg
		// Extract name from URL if possible
		u, err := url.Parse(arg)
		if err != nil {
			return fmt.Errorf("invalid URL: %w", err)
		}
		name = strings.Split(u.Host, ".")[0]
	} else {
		// Look up in sessions
		session, ok := store.Get(arg)
		if !ok {
			return fmt.Errorf("session %q not found", arg)
		}
		sandboxURL = session.URL
		name = arg
	}

	// The sandbox URL is already the proxy endpoint
	proxyURL := sandboxURL

	slog.Info("connecting to sandbox", "name", name, "proxy", proxyURL)

	// Create TCP proxy and listen to get the actual port
	tcpProxy := proxy.NewTCPProxy(proxy.TCPProxyConfig{
		Host:     "127.0.0.1",
		Port:     localPort,
		ProxyURL: proxyURL,
	})

	if err := tcpProxy.Listen(); err != nil {
		return fmt.Errorf("failed to start local proxy: %w", err)
	}

	actualPort := tcpProxy.Port()

	// Update SSH config with the actual port
	if err := updateSSHConfig(name, actualPort); err != nil {
		return fmt.Errorf("failed to update SSH config: %w", err)
	}
	slog.Info("ssh config updated", "host", fmt.Sprintf("reach.%s", name))

	// Clean up SSH config on exit
	defer func() {
		if err := removeSSHConfig(name); err != nil {
			slog.Error("failed to remove SSH config", "error", err)
		} else {
			slog.Info("ssh config removed", "host", fmt.Sprintf("reach.%s", name))
		}
	}()

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	fmt.Printf("Sandbox connected: %s\n", name)
	fmt.Printf("SSH: ssh reach.%s\n", name)
	fmt.Printf("Local proxy listening on %s\n", tcpProxy.Addr())

	// Serve with reconnection logic
	return serveWithReconnect(ctx, tcpProxy)
}

func serveWithReconnect(ctx context.Context, tcpProxy proxy.TCPProxy) error {
	backoff := 1 * time.Second
	maxBackoff := 10 * time.Second

	for {
		err := tcpProxy.Serve(ctx)
		if err == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return nil
		default:
		}

		slog.Warn("connection dropped, reconnecting", "error", err, "backoff", backoff)

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func updateSSHConfig(name string, port int) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	reachDir := filepath.Join(home, ".reach")
	if err := os.MkdirAll(reachDir, 0755); err != nil {
		return err
	}

	// Manage reach SSH configs in a separate file
	reachSSHConfig := filepath.Join(reachDir, "ssh_config")

	// Read existing config
	existingConfig := ""
	if data, err := os.ReadFile(reachSSHConfig); err == nil {
		existingConfig = string(data)
	}

	hostAlias := fmt.Sprintf("reach.%s", name)
	hostEntry := fmt.Sprintf(`Host %s
    HostName 127.0.0.1
    Port %d
    ForwardAgent yes
    StrictHostKeyChecking no
    UserKnownHostsFile /dev/null

`, hostAlias, port)

	// Check if entry already exists and update it, or append
	if strings.Contains(existingConfig, fmt.Sprintf("Host %s\n", hostAlias)) {
		// Remove existing entry and add new one
		lines := strings.Split(existingConfig, "\n")
		var newLines []string
		skip := false
		for _, line := range lines {
			if strings.HasPrefix(line, "Host "+hostAlias) {
				skip = true
				continue
			}
			if skip && strings.HasPrefix(line, "Host ") {
				skip = false
			}
			if !skip {
				newLines = append(newLines, line)
			}
		}
		existingConfig = strings.Join(newLines, "\n")
	}

	newConfig := existingConfig + hostEntry

	if err := os.WriteFile(reachSSHConfig, []byte(newConfig), 0644); err != nil {
		return err
	}

	// Ensure main SSH config includes our config
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return err
	}

	mainSSHConfig := filepath.Join(sshDir, "config")
	includeDirective := fmt.Sprintf("Include %s\n", reachSSHConfig)

	mainConfig := ""
	if data, err := os.ReadFile(mainSSHConfig); err == nil {
		mainConfig = string(data)
	}

	if !strings.Contains(mainConfig, reachSSHConfig) {
		// Add include at the beginning
		newMainConfig := includeDirective + mainConfig
		if err := os.WriteFile(mainSSHConfig, []byte(newMainConfig), 0644); err != nil {
			return err
		}
	}

	return nil
}

func removeSSHConfig(name string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	reachSSHConfig := filepath.Join(home, ".reach", "ssh_config")

	data, err := os.ReadFile(reachSSHConfig)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	hostAlias := fmt.Sprintf("reach.%s", name)
	existingConfig := string(data)

	if !strings.Contains(existingConfig, fmt.Sprintf("Host %s\n", hostAlias)) {
		return nil
	}

	// Remove the entry
	lines := strings.Split(existingConfig, "\n")
	var newLines []string
	skip := false
	for _, line := range lines {
		if strings.HasPrefix(line, "Host "+hostAlias) {
			skip = true
			continue
		}
		if skip && strings.HasPrefix(line, "Host ") {
			skip = false
		}
		if skip && strings.TrimSpace(line) == "" {
			continue
		}
		if !skip {
			newLines = append(newLines, line)
		}
	}

	newConfig := strings.Join(newLines, "\n")
	return os.WriteFile(reachSSHConfig, []byte(newConfig), 0644)
}
