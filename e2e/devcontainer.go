package e2e

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/wait"
)

// DevcontainerContainer represents a running devcontainer with the bridge CLI
type DevcontainerContainer struct {
	Container testcontainers.Container
	Host      string
}

// DevcontainerConfig configures the devcontainer
type DevcontainerConfig struct {
	// Command is the bridge command to run (e.g., "intercept", "--sandbox-url", "...")
	Command []string
	// Env is additional environment variables
	Env map[string]string
	// Privileged runs the container in privileged mode (needed for iptables)
	Privileged bool
	// Network is the Docker network to join (for container-to-container communication)
	Network string
	// WaitFor is an optional wait strategy. If nil, the container starts without waiting.
	WaitFor wait.Strategy
}

// NewDevcontainer creates and starts a new devcontainer running the bridge CLI.
// The caller is responsible for calling Terminate() when done.
func NewDevcontainer(ctx context.Context, cfg DevcontainerConfig) (*DevcontainerContainer, error) {
	// Build the bridge binary for Linux
	binaryPath, err := BuildBridge()
	if err != nil {
		return nil, fmt.Errorf("failed to build bridge: %w", err)
	}

	// Create a temp directory for the Docker build context
	buildCtx, err := createBuildContext(binaryPath, "Dockerfile.devcontainer")
	if err != nil {
		return nil, fmt.Errorf("failed to create build context: %w", err)
	}

	req := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    buildCtx,
			Dockerfile: "Dockerfile",
		},
		Cmd:        cfg.Command,
		Env:        cfg.Env,
		Privileged: cfg.Privileged,
		WaitingFor: cfg.WaitFor,
	}

	if cfg.Network != "" {
		req.Networks = []string{cfg.Network}
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		os.RemoveAll(buildCtx)
		return nil, fmt.Errorf("failed to start devcontainer: %w", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		container.Terminate(ctx)
		os.RemoveAll(buildCtx)
		return nil, fmt.Errorf("failed to get container host: %w", err)
	}

	return &DevcontainerContainer{
		Container: container,
		Host:      host,
	}, nil
}

// Terminate stops and removes the container
func (d *DevcontainerContainer) Terminate(ctx context.Context) error {
	if d.Container != nil {
		return d.Container.Terminate(ctx)
	}
	return nil
}

// Logs returns the container logs
func (d *DevcontainerContainer) Logs(ctx context.Context) (string, error) {
	reader, err := d.Container.Logs(ctx)
	if err != nil {
		return "", err
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WaitForExit waits for the container to exit and returns the exit code
func (d *DevcontainerContainer) WaitForExit(ctx context.Context) (int64, error) {
	state, err := d.Container.State(ctx)
	if err != nil {
		return -1, err
	}

	// If already exited, return immediately
	if !state.Running {
		return int64(state.ExitCode), nil
	}

	// Poll until the container exits
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return -1, ctx.Err()
		case <-ticker.C:
			state, err := d.Container.State(ctx)
			if err != nil {
				return -1, err
			}
			if !state.Running {
				return int64(state.ExitCode), nil
			}
		}
	}
}

// Exec executes a command in the container and returns the exit code and output
func (d *DevcontainerContainer) Exec(ctx context.Context, cmd []string) (int, string, error) {
	// Explicitly set working directory to match WORKDIR from Dockerfile
	exitCode, reader, err := d.Container.Exec(ctx, cmd, tcexec.WithWorkingDir("/workspaces/testproject"))
	if err != nil {
		return exitCode, "", err
	}

	output, _ := io.ReadAll(reader)
	return exitCode, string(output), nil
}

// ContainerIP returns the container's IP address on the given network
func (d *DevcontainerContainer) ContainerIP(ctx context.Context, network string) (string, error) {
	inspect, err := d.Container.Inspect(ctx)
	if err != nil {
		return "", err
	}

	if network == "" {
		// Return the first available IP
		for _, net := range inspect.NetworkSettings.Networks {
			return net.IPAddress, nil
		}
		return "", fmt.Errorf("no network found")
	}

	net, ok := inspect.NetworkSettings.Networks[network]
	if !ok {
		return "", fmt.Errorf("network %s not found", network)
	}
	return net.IPAddress, nil
}
