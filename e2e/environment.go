package e2e

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"
	"time"
)

// Environment holds all containers for a full bridge E2E test.
// It wires up a sandbox (bridge server), dispatcher, and devcontainer (bridge intercept)
// so that requests to the dispatcher flow through the sandbox tunnel to the devcontainer app.
type Environment struct {
	Network      *TestNetwork
	Sandbox      *SandboxContainer
	Dispatcher   *DispatcherContainer
	Devcontainer *DevcontainerContainer
}

// EnvironmentConfig configures the E2E environment.
type EnvironmentConfig struct {
	// SandboxName is the name for the sandbox (default: "test-sandbox")
	SandboxName string
	// AppPort is the local app port in the devcontainer that receives inbound requests (default: 3000)
	AppPort int
	// DevcontainerPrivileged runs the devcontainer in privileged mode for iptables (default: false)
	DevcontainerPrivileged bool
}

func (cfg *EnvironmentConfig) setDefaults() {
	if cfg.SandboxName == "" {
		cfg.SandboxName = "test-sandbox"
	}
	if cfg.AppPort == 0 {
		cfg.AppPort = 3000
	}
}

const (
	// Network aliases used for container-to-container communication.
	// These allow all three containers to start in parallel since they
	// don't need to wait for each other's IPs.
	sandboxAlias    = "sandbox"
	dispatcherAlias = "dispatcher"
)

// SetupEnvironment creates the full bridge environment:
//  1. A Docker network for container-to-container communication
//  2. In parallel: a sandbox, dispatcher, and devcontainer
//  3. Bridge intercept running inside the devcontainer
//
// The caller must call TearDown when done.
func SetupEnvironment(ctx context.Context, cfg EnvironmentConfig) (*Environment, error) {
	cfg.setDefaults()

	env := &Environment{}

	// 1. Create a Docker network with a unique name so multiple environments
	// can run in parallel without colliding.
	id := make([]byte, 4)
	_, _ = rand.Read(id)
	networkName := fmt.Sprintf("bridge-e2e-%s", hex.EncodeToString(id))

	network, err := NewTestNetwork(ctx, networkName)
	if err != nil {
		return nil, fmt.Errorf("failed to create network: %w", err)
	}
	env.Network = network

	// 2. Start sandbox and dispatcher in parallel.
	// Network aliases let the dispatcher reference the sandbox by hostname.
	sandboxURL := fmt.Sprintf("http://%s:3000", sandboxAlias)
	functionURL := fmt.Sprintf("http://%s:8080", dispatcherAlias)

	var (
		wg          sync.WaitGroup
		sandboxErr  error
		dispatchErr error
	)

	wg.Add(2)

	go func() {
		defer wg.Done()
		sandbox, err := NewSandbox(ctx, SandboxConfig{
			Command:        []string{"bridge", "server", "--name", cfg.SandboxName},
			Network:        network.Name,
			NetworkAliases: []string{sandboxAlias},
		})
		if err != nil {
			sandboxErr = fmt.Errorf("failed to start sandbox: %w", err)
			return
		}
		env.Sandbox = sandbox
	}()

	go func() {
		defer wg.Done()
		dispatcher, err := NewDispatcher(ctx, DispatcherConfig{
			Env: map[string]string{
				"BRIDGE_SERVER_ADDR": sandboxURL,
			},
			Network:        network.Name,
			NetworkAliases: []string{dispatcherAlias},
		})
		if err != nil {
			dispatchErr = fmt.Errorf("failed to start dispatcher: %w", err)
			return
		}
		env.Dispatcher = dispatcher
	}()

	wg.Wait()

	if sandboxErr != nil || dispatchErr != nil {
		env.TearDown(ctx, nil)
		return nil, fmt.Errorf("container startup failed: sandbox=%v, dispatcher=%v",
			sandboxErr, dispatchErr)
	}

	// 3. Start devcontainer now that sandbox and dispatcher are healthy.
	// Bridge intercept needs the sandbox up for the SSH proxy and mutagen sync.
	devcontainer, err := NewDevcontainer(ctx, DevcontainerConfig{
		Network:    network.Name,
		Privileged: cfg.DevcontainerPrivileged,
	})
	if err != nil {
		env.TearDown(ctx, nil)
		return nil, fmt.Errorf("failed to start devcontainer: %w", err)
	}
	env.Devcontainer = devcontainer

	// 4. Start bridge intercept in the background.
	interceptCmd := []string{
		"sh", "-c",
		fmt.Sprintf(
			"bridge intercept "+
				"--sandbox-url %s "+
				"--function-url %s "+
				"--name %s "+
				"--app-port %d "+
				"> /tmp/intercept.log 2>&1 &",
			sandboxURL,
			functionURL,
			cfg.SandboxName,
			cfg.AppPort,
		),
	}

	exitCode, _, err := devcontainer.Exec(ctx, interceptCmd)
	if err != nil {
		env.TearDown(ctx, nil)
		return nil, fmt.Errorf("failed to exec bridge intercept: %w", err)
	}
	if exitCode != 0 {
		env.TearDown(ctx, nil)
		return nil, fmt.Errorf("bridge intercept exited with code %d", exitCode)
	}

	// 5. Wait for bridge intercept to connect to the sandbox.
	if err := waitForInterceptReady(ctx, env.Devcontainer); err != nil {
		env.TearDown(ctx, nil)
		return nil, fmt.Errorf("bridge intercept not ready: %w", err)
	}

	return env, nil
}

// waitForInterceptReady polls the bridge intercept log until it shows the tunnel is connected.
func waitForInterceptReady(ctx context.Context, dc *DevcontainerContainer) error {
	deadline := time.After(30 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			// Dump logs for debugging
			_, logs, _ := dc.Exec(ctx, []string{"cat", "/tmp/intercept.log"})
			return fmt.Errorf("timed out waiting for bridge intercept to connect.\nIntercept logs:\n%s", logs)
		case <-ticker.C:
			exitCode, output, err := dc.Exec(ctx, []string{"grep", "-q", "Registration sent", "/tmp/intercept.log"})
			if err != nil {
				continue
			}
			if exitCode == 0 {
				_ = output
				return nil
			}
		}
	}
}

// TearDown stops all containers in parallel and cleans up the network.
// If t is non-nil and the test failed, container logs are printed for debugging.
func (e *Environment) TearDown(ctx context.Context, t *testing.T) {
	var wg sync.WaitGroup

	if e.Devcontainer != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if t != nil && t.Failed() {
				logs, err := e.Devcontainer.Logs(ctx)
				if err == nil {
					t.Logf("Devcontainer logs:\n%s", logs)
				}
				_, interceptLogs, _ := e.Devcontainer.Exec(ctx, []string{"cat", "/tmp/intercept.log"})
				t.Logf("Intercept logs:\n%s", interceptLogs)
			}
			e.Devcontainer.Terminate(ctx)
		}()
	}
	if e.Dispatcher != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if t != nil && t.Failed() {
				logs, err := e.Dispatcher.Logs(ctx)
				if err == nil {
					t.Logf("Dispatcher logs:\n%s", logs)
				}
			}
			e.Dispatcher.Terminate(ctx)
		}()
	}
	if e.Sandbox != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if t != nil && t.Failed() {
				logs, err := e.Sandbox.Logs(ctx)
				if err == nil {
					t.Logf("Sandbox logs:\n%s", logs)
				}
			}
			e.Sandbox.Terminate(ctx)
		}()
	}

	wg.Wait()

	if e.Network != nil {
		e.Network.Terminate(ctx)
	}
}

// InterceptLogs returns the bridge intercept log output from the devcontainer.
func (e *Environment) InterceptLogs(ctx context.Context) (string, error) {
	_, output, err := e.Devcontainer.Exec(ctx, []string{"cat", "/tmp/intercept.log"})
	if err != nil {
		return "", err
	}
	return output, nil
}
