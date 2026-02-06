package commands

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/urfave/cli/v3"
	"github.com/vercel-eddie/bridge/pkg/bidi"
	"github.com/vercel-eddie/bridge/pkg/mutagen"
	"github.com/vercel-eddie/bridge/pkg/sshproxy"
	"github.com/vercel-eddie/bridge/pkg/tunnel"
)

const (
	// CIDR block for proxy IP allocation (used with DNS interception)
	proxyCIDR = "10.128.0.0/16"
)

func Intercept() *cli.Command {
	return &cli.Command{
		Name:  "intercept",
		Usage: "Intercept and tunnel traffic (run inside Devcontainer)",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "sandbox-url",
				Usage:    "URL of the bridge server in the sandbox",
				Sources:  cli.EnvVars("SANDBOX_URL"),
				Required: true,
			},
			&cli.StringFlag{
				Name:     "function-url",
				Usage:    "URL of the dispatcher function",
				Sources:  cli.EnvVars("FUNCTION_URL"),
				Required: true,
			},
			&cli.StringFlag{
				Name:    "name",
				Usage:   "Name for the sandbox (used for SSH config alias)",
				Sources: cli.EnvVars("SANDBOX_NAME"),
			},
			&cli.IntFlag{
				Name:  "proxy-port",
				Usage: "Port for transparent proxy (0 = random)",
				Value: 0,
			},
			&cli.IntFlag{
				Name:  "ssh-proxy-port",
				Usage: "Local port for SSH proxy (0 = random)",
				Value: 0,
			},
			&cli.StringFlag{
				Name:    "sync-source",
				Usage:   "Local directory to sync from",
				Sources: cli.EnvVars("SYNC_SOURCE"),
				Value:   ".",
			},
			&cli.StringFlag{
				Name:    "sync-target",
				Usage:   "Remote directory on sandbox to sync to (default: root@<name>:/vercel/sandbox)",
				Sources: cli.EnvVars("SYNC_TARGET"),
			},
			&cli.BoolFlag{
				Name:  "no-sync",
				Usage: "Disable file sync",
			},
			&cli.BoolFlag{
				Name:  "no-ssh-proxy",
				Usage: "Disable SSH proxy",
			},
		},
		Action: runIntercept,
	}
}

type interceptor struct {
	sandboxURL    string
	functionURL   string
	name          string
	proxyPort     int
	sshProxyPort  int
	syncSource    string
	syncTarget    string
	noSync        bool
	noSSHProxy    bool
	tunnel        *tunnel.Client
	listener      net.Listener
	sshProxy      *sshproxy.SSHProxy
	syncName      string
	mutagenClient *mutagen.Client
}

func runIntercept(ctx context.Context, c *cli.Command) error {
	sandboxURL := c.String("sandbox-url")
	functionURL := c.String("function-url")
	name := c.String("name")
	proxyPort := c.Int("proxy-port")
	sshProxyPort := c.Int("ssh-proxy-port")
	syncSource := c.String("sync-source")
	syncTarget := c.String("sync-target")
	noSync := c.Bool("no-sync")
	noSSHProxy := c.Bool("no-ssh-proxy")

	// Derive name from sandbox URL if not provided
	if name == "" {
		u, err := url.Parse(sandboxURL)
		if err == nil {
			name = strings.Split(u.Host, ".")[0]
		} else {
			name = "sandbox"
		}
	}

	i := &interceptor{
		sandboxURL:   sandboxURL,
		functionURL:  functionURL,
		name:         name,
		proxyPort:    proxyPort,
		sshProxyPort: sshProxyPort,
		syncSource:   syncSource,
		syncTarget:   syncTarget,
		noSync:       noSync,
		noSSHProxy:   noSSHProxy,
		syncName:     "bridge-sync",
	}

	// Start transparent proxy listener
	if err := i.startProxyListener(); err != nil {
		return fmt.Errorf("failed to start proxy listener: %w", err)
	}

	slog.Info("Bridge intercept starting",
		"name", name,
		"sandbox_url", sandboxURL,
		"function_url", functionURL,
		"proxy_port", i.proxyPort,
	)

	// Warn if OIDC token is not set (needed for deployment protection bypass)
	if os.Getenv("VERCEL_OIDC_TOKEN") == "" {
		slog.Warn("VERCEL_OIDC_TOKEN not set - requests to protected deployments will fail with 401")
	}

	// Initialize tunnel client
	i.tunnel = tunnel.NewClient(sandboxURL, functionURL)

	// Set up iptables for traffic interception
	if err := i.setupIptables(); err != nil {
		slog.Warn("Failed to setup iptables",
			"error", err,
			"hint", "Traffic interception requires NET_ADMIN capability",
		)
	}

	// Start SSH proxy if enabled
	if !noSSHProxy {
		proxy, err := sshproxy.New(ctx, sshproxy.Config{
			Name:      name,
			TunnelURL: sandboxURL,
			LocalPort: sshProxyPort,
		})
		if err != nil {
			slog.Warn("Failed to start SSH proxy", "error", err)
		} else {
			i.sshProxy = proxy
			fmt.Printf("SSH: ssh %s\n", proxy.Host())
		}
	}

	// Create cancellable context
	ctx, cancel := context.WithCancel(ctx)

	// Handle cleanup on shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		slog.Info("Shutting down...")
		cancel()
		i.cleanup()
		os.Exit(0)
	}()

	// Start SSH proxy accept loop in background BEFORE starting sync
	// This ensures the proxy is accepting connections when mutagen tries to connect
	if i.sshProxy != nil {
		sshProxyReady := make(chan struct{})
		go func() {
			close(sshProxyReady) // Signal that we're about to start accepting
			if err := i.sshProxy.Serve(ctx); err != nil && ctx.Err() == nil {
				slog.Error("SSH proxy error", "error", err)
			}
		}()
		<-sshProxyReady // Wait for goroutine to start
	}

	// Derive sync target from SSH proxy if not explicitly provided
	// Use SSH host alias which is configured in ~/.bridge/ssh_config
	if i.syncTarget == "" && i.sshProxy != nil {
		i.syncTarget = fmt.Sprintf("vercel-sandbox@%s:/vercel/sandbox", i.sshProxy.Host())
	}

	// Start file sync if enabled
	if !noSync && i.syncTarget != "" {
		if err := i.startSync(); err != nil {
			slog.Error("Failed to start file sync", "error", err)
		}
	}

	// Start accepting connections on the transparent proxy
	go i.runTransparentProxy()

	// Connect to bridge server and serve
	i.tunnel.ConnectWithReconnect(ctx)

	return nil
}

func (i *interceptor) cleanup() {
	i.stopSync()
	i.cleanupIptables()
	if i.listener != nil {
		_ = i.listener.Close()
	}
	if i.tunnel != nil {
		_ = i.tunnel.Close()
	}
	if i.sshProxy != nil {
		_ = i.sshProxy.Close()
	}
}

func (i *interceptor) startProxyListener() error {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", i.proxyPort))
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	addr := listener.Addr().(*net.TCPAddr)
	i.proxyPort = addr.Port
	i.listener = listener

	return nil
}

func (i *interceptor) runTransparentProxy() {
	slog.Info("Transparent proxy listening", "port", i.proxyPort)

	for {
		conn, err := i.listener.Accept()
		if err != nil {
			slog.Error("Accept error", "error", err)
			return
		}

		go i.handleOutbound(conn)
	}
}

func (i *interceptor) handleOutbound(clientConn net.Conn) {
	defer clientConn.Close()

	// Get source address
	sourceAddr := clientConn.RemoteAddr().String()

	// Get original destination using SO_ORIGINAL_DST
	origDst, err := getOriginalDst(clientConn)
	if err != nil {
		slog.Error("Failed to get original destination", "error", err)
		return
	}

	slog.Debug("Intercepted outbound connection", "source", sourceAddr, "destination", origDst)

	// Dial through the tunnel
	targetConn, err := i.tunnel.DialThroughTunnel(sourceAddr, origDst)
	if err != nil {
		slog.Error("Failed to dial through tunnel", "source", sourceAddr, "destination", origDst, "error", err)
		return
	}
	defer targetConn.Close()

	slog.Info("Proxying connection", "source", sourceAddr, "destination", origDst)

	// Bidirectional copy
	bidi.New(clientConn, targetConn).Wait(context.Background())
}

func (i *interceptor) setupIptables() error {
	// Check if iptables exists
	if _, err := exec.LookPath("iptables"); err != nil {
		return fmt.Errorf("iptables not found: %w", err)
	}

	// Get our own UID to exclude our traffic from interception
	uid := fmt.Sprintf("%d", os.Getuid())

	cmds := [][]string{
		// Create a new chain for our rules
		{"iptables", "-t", "nat", "-N", "BRIDGE_INTERCEPT"},

		// TCP interception: redirect traffic to our proxy CIDR
		{"iptables", "-t", "nat", "-A", "BRIDGE_INTERCEPT", "-d", proxyCIDR, "-p", "tcp", "-m", "owner", "!", "--uid-owner", uid, "-j", "REDIRECT", "--to-ports", fmt.Sprintf("%d", i.proxyPort)},

		// Jump to our chain from OUTPUT
		{"iptables", "-t", "nat", "-A", "OUTPUT", "-d", proxyCIDR, "-p", "tcp", "-j", "BRIDGE_INTERCEPT"},
	}

	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		if output, err := cmd.CombinedOutput(); err != nil {
			slog.Debug("iptables command failed",
				"command", args,
				"error", err,
				"output", string(output),
			)
		}
	}

	slog.Info("iptables rules configured", "proxy_port", i.proxyPort, "proxy_cidr", proxyCIDR)
	return nil
}

func (i *interceptor) cleanupIptables() {
	cmds := [][]string{
		{"iptables", "-t", "nat", "-D", "OUTPUT", "-d", proxyCIDR, "-p", "tcp", "-j", "BRIDGE_INTERCEPT"},
		{"iptables", "-t", "nat", "-F", "BRIDGE_INTERCEPT"},
		{"iptables", "-t", "nat", "-X", "BRIDGE_INTERCEPT"},
	}

	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		_ = cmd.Run()
	}
	slog.Info("iptables rules cleaned up")
}

func (i *interceptor) startSync() error {
	// Install mutagen if not already installed
	slog.Info("Checking mutagen installation...")
	if err := mutagen.Install(); err != nil {
		return fmt.Errorf("failed to install mutagen: %w", err)
	}
	slog.Info("Mutagen installed", "path", mutagen.BinaryPath())

	// Create mutagen client
	client, err := mutagen.NewClient()
	if err != nil {
		return fmt.Errorf("failed to create mutagen client: %w", err)
	}
	i.mutagenClient = client

	// Resolve absolute path for sync source
	absSource, err := filepath.Abs(i.syncSource)
	if err != nil {
		return fmt.Errorf("failed to resolve sync source: %w", err)
	}
	slog.Info("Starting mutagen sync", "source", i.syncSource, "abs_source", absSource, "target", i.syncTarget)

	// Create sync session
	if err := client.CreateSyncSession(mutagen.SyncConfig{
		Name:      i.syncName,
		Source:    absSource,
		Target:    i.syncTarget,
		IgnoreVCS: true,
		SyncMode:  "two-way-resolved",
	}); err != nil {
		return fmt.Errorf("failed to create mutagen sync: %w", err)
	}

	slog.Info("Mutagen sync started", "name", i.syncName)

	return nil
}

func (i *interceptor) stopSync() {
	if i.mutagenClient == nil || i.syncName == "" {
		return
	}

	if err := i.mutagenClient.TerminateSyncSession(i.syncName); err != nil {
		slog.Error("Failed to terminate mutagen sync", "error", err)
	} else {
		slog.Info("Mutagen sync terminated", "name", i.syncName)
	}
}
