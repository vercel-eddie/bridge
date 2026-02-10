package e2e

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	// SandboxPort is the default port the bridge server listens on
	SandboxPort = "3000/tcp"
)

// SandboxContainer represents a running sandbox container with the bridge CLI
type SandboxContainer struct {
	Container testcontainers.Container
	Host      string
	Port      string
}

// SandboxConfig configures the sandbox container
type SandboxConfig struct {
	// Command is the bridge command to run (e.g., "server")
	Command []string
	// Env is additional environment variables
	Env map[string]string
	// ExposedPorts are additional ports to expose
	ExposedPorts []string
	// Network is the Docker network to join
	Network string
	// NetworkAliases are DNS aliases for this container on the network
	NetworkAliases []string
}

// NewSandbox creates and starts a new sandbox container running the bridge CLI.
// The caller is responsible for calling Terminate() when done.
func NewSandbox(ctx context.Context, cfg SandboxConfig) (*SandboxContainer, error) {
	// Build the bridge binary for Linux
	binaryPath, err := BuildBridge()
	if err != nil {
		return nil, fmt.Errorf("failed to build bridge: %w", err)
	}

	// Create a temp directory for the Docker build context
	buildCtx, err := createBuildContext(binaryPath, "Dockerfile.sandbox")
	if err != nil {
		return nil, fmt.Errorf("failed to create build context: %w", err)
	}

	exposedPorts := []string{SandboxPort}
	exposedPorts = append(exposedPorts, cfg.ExposedPorts...)

	req := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    buildCtx,
			Dockerfile: "Dockerfile",
		},
		ExposedPorts: exposedPorts,
		Cmd:          cfg.Command,
		Env:          cfg.Env,
		User:         "vercel-sandbox", // Explicitly run as vercel-sandbox user
		WaitingFor:   wait.ForHTTP("/health").WithPort(SandboxPort).WithStartupTimeout(60 * time.Second),
	}

	if cfg.Network != "" {
		req.Networks = []string{cfg.Network}
		if len(cfg.NetworkAliases) > 0 {
			req.NetworkAliases = map[string][]string{
				cfg.Network: cfg.NetworkAliases,
			}
		}
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		os.RemoveAll(buildCtx)
		return nil, fmt.Errorf("failed to start sandbox container: %w", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		container.Terminate(ctx)
		os.RemoveAll(buildCtx)
		return nil, fmt.Errorf("failed to get container host: %w", err)
	}

	mappedPort, err := container.MappedPort(ctx, SandboxPort)
	if err != nil {
		container.Terminate(ctx)
		os.RemoveAll(buildCtx)
		return nil, fmt.Errorf("failed to get mapped port: %w", err)
	}

	return &SandboxContainer{
		Container: container,
		Host:      host,
		Port:      mappedPort.Port(),
	}, nil
}

// URL returns the HTTP URL to reach the sandbox
func (s *SandboxContainer) URL() string {
	return fmt.Sprintf("http://%s:%s", s.Host, s.Port)
}

// WebSocketURL returns the WebSocket URL to reach the sandbox
func (s *SandboxContainer) WebSocketURL() string {
	return fmt.Sprintf("ws://%s:%s", s.Host, s.Port)
}

// Terminate stops and removes the container
func (s *SandboxContainer) Terminate(ctx context.Context) error {
	if s.Container != nil {
		return s.Container.Terminate(ctx)
	}
	return nil
}

// Logs returns the container logs
func (s *SandboxContainer) Logs(ctx context.Context) (string, error) {
	reader, err := s.Container.Logs(ctx)
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

// ContainerIP returns the container's IP address on the given network
func (s *SandboxContainer) ContainerIP(ctx context.Context, network string) (string, error) {
	inspect, err := s.Container.Inspect(ctx)
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

// Exec executes a command in the container as vercel-sandbox and returns the exit code
func (s *SandboxContainer) Exec(ctx context.Context, cmd []string) (int, string, error) {
	exitCode, reader, err := s.Container.Exec(ctx, cmd, exec.WithUser("vercel-sandbox"))
	if err != nil {
		return exitCode, "", err
	}

	output, _ := io.ReadAll(reader)
	return exitCode, string(output), nil
}

// ExecAsRoot executes a command in the container as root and returns the exit code
func (s *SandboxContainer) ExecAsRoot(ctx context.Context, cmd []string) (int, string, error) {
	exitCode, reader, err := s.Container.Exec(ctx, cmd)
	if err != nil {
		return exitCode, "", err
	}

	output, _ := io.ReadAll(reader)
	return exitCode, string(output), nil
}

// createBuildContext creates a temporary directory with the binary and Dockerfile
func createBuildContext(binaryPath, dockerfileName string) (string, error) {
	projectRoot, err := findProjectRoot()
	if err != nil {
		return "", err
	}

	tmpDir, err := os.MkdirTemp("", "bridge-docker-ctx-*")
	if err != nil {
		return "", err
	}

	// Copy the binary
	if err := copyFile(binaryPath, filepath.Join(tmpDir, "bridge")); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("failed to copy binary: %w", err)
	}

	// Copy the Dockerfile
	srcDockerfile := filepath.Join(projectRoot, "e2e", dockerfileName)
	if err := copyFile(srcDockerfile, filepath.Join(tmpDir, "Dockerfile")); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("failed to copy Dockerfile: %w", err)
	}

	return tmpDir, nil
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}

	// Preserve executable permission
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.Chmod(dst, srcInfo.Mode())
}

// findProjectRoot finds the project root by looking for go.mod
func findProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find project root (go.mod)")
		}
		dir = parent
	}
}
