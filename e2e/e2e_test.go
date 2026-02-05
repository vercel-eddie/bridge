package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// E2ESuite is the base test suite for e2e tests
type E2ESuite struct {
	suite.Suite
	ctx     context.Context
	cancel  context.CancelFunc
	sandbox *SandboxContainer
}

// SetupSuite runs once before all tests in the suite
func (s *E2ESuite) SetupSuite() {
	if testing.Short() {
		s.T().Skip("skipping e2e test in short mode")
	}

	s.ctx, s.cancel = context.WithTimeout(context.Background(), 5*time.Minute)

	var err error
	s.sandbox, err = NewSandbox(s.ctx, SandboxConfig{
		Command: []string{"bridge", "server", "--name", "test-sandbox"},
	})
	require.NoError(s.T(), err, "failed to create sandbox")
}

// TearDownSuite runs once after all tests in the suite
func (s *E2ESuite) TearDownSuite() {
	if s.sandbox != nil {
		if s.T().Failed() {
			// Print logs on failure for debugging
			logs, err := s.sandbox.Logs(s.ctx)
			if err == nil {
				s.T().Logf("Sandbox logs:\n%s", logs)
			}
		}
		s.sandbox.Terminate(s.ctx)
	}
	if s.cancel != nil {
		s.cancel()
	}
	// Clean up the built binary
	CleanupBuild()
}

// TestHealth verifies the sandbox responds to health checks
func (s *E2ESuite) TestHealth() {
	resp, err := http.Get(s.sandbox.URL() + "/health")
	s.Require().NoError(err, "health check request failed")
	defer resp.Body.Close()

	s.Equal(http.StatusOK, resp.StatusCode)
	s.Equal("test-sandbox", resp.Header.Get("X-Bridge-Name"))
}

// TestDevcontainerVersion verifies the devcontainer can run bridge commands
func (s *E2ESuite) TestDevcontainerVersion() {
	devcontainer, err := NewDevcontainer(s.ctx, DevcontainerConfig{
		Command: []string{"bridge", "--version"},
	})
	s.Require().NoError(err, "failed to create devcontainer")
	defer devcontainer.Terminate(s.ctx)

	// Wait for the container to exit
	exitCode, err := devcontainer.WaitForExit(s.ctx)
	s.Require().NoError(err)
	s.Equal(int64(0), exitCode, "expected exit code 0")

	logs, err := devcontainer.Logs(s.ctx)
	s.Require().NoError(err)
	s.Contains(logs, "bridge version")
}

// TestSSHConnection verifies that SSH works through the tunnel
func (s *E2ESuite) TestSSHConnection() {
	// Create a network for container-to-container communication
	network, err := NewTestNetwork(s.ctx, "ssh-test")
	s.Require().NoError(err, "failed to create network")
	defer network.Terminate(s.ctx)

	// Start sandbox
	sandbox, err := NewSandbox(s.ctx, SandboxConfig{
		Command: []string{"bridge", "server", "--name", "test-sandbox"},
		Network: network.Name,
	})
	s.Require().NoError(err, "failed to create sandbox")
	defer func() {
		logs, _ := sandbox.Logs(s.ctx)
		s.T().Logf("Sandbox logs:\n%s", logs)
		sandbox.Terminate(s.ctx)
	}()

	// Get sandbox IP on the network
	sandboxIP, err := sandbox.ContainerIP(s.ctx, network.Name)
	s.Require().NoError(err, "failed to get sandbox IP")

	// Start devcontainer
	devcontainer, err := NewDevcontainer(s.ctx, DevcontainerConfig{
		Network: network.Name,
	})
	s.Require().NoError(err, "failed to create devcontainer")
	defer func() {
		logs, _ := devcontainer.Logs(s.ctx)
		s.T().Logf("Devcontainer logs:\n%s", logs)
		devcontainer.Terminate(s.ctx)
	}()

	// Start bridge intercept in background to set up SSH proxy
	sandboxURL := "http://" + sandboxIP + ":3000"
	interceptCmd := []string{
		"sh", "-c",
		"bridge intercept " +
			"--sandbox-url " + sandboxURL + " " +
			"--function-url http://localhost:9999 " +
			"--name test-sandbox " +
			"--no-sync " +
			"> /tmp/intercept.log 2>&1 &",
	}
	exitCode, _, err := devcontainer.Exec(s.ctx, interceptCmd)
	s.Require().NoError(err, "failed to start bridge intercept")
	s.Require().Equal(0, exitCode, "failed to start bridge intercept")

	// Try to SSH through the tunnel and run echo
	sshCmd := []string{
		"sh", "-c",
		"ssh bridge.test-sandbox echo 'hello from sandbox'",
	}
	exitCode, output, err := devcontainer.Exec(s.ctx, sshCmd)

	// Print intercept logs for debugging
	_, interceptLogs, _ := devcontainer.Exec(s.ctx, []string{"cat", "/tmp/intercept.log"})
	s.T().Logf("Intercept logs:\n%s", interceptLogs)
	s.T().Logf("SSH output:\n%s", output)
	s.T().Logf("SSH exit code: %d", exitCode)

	s.Require().NoError(err, "SSH command failed")
	s.Require().Equal(0, exitCode, "SSH command returned non-zero exit code")
	s.Contains(output, "hello from sandbox", "unexpected SSH output")
}

// TestMutagenSync verifies that mutagen syncs files between devcontainer and sandbox
func (s *E2ESuite) TestMutagenSync() {
	// Create a network for container-to-container communication
	network, err := NewTestNetwork(s.ctx, "mutagen-test")
	s.Require().NoError(err, "failed to create network")
	defer network.Terminate(s.ctx)

	// Start sandbox
	sandbox, err := NewSandbox(s.ctx, SandboxConfig{
		Command: []string{"bridge", "server", "--name", "test-sandbox"},
		Network: network.Name,
	})
	s.Require().NoError(err, "failed to create sandbox")
	defer func() {
		logs, _ := sandbox.Logs(s.ctx)
		s.T().Logf("Sandbox logs:\n%s", logs)
		sandbox.Terminate(s.ctx)
	}()

	// Get sandbox IP on the network
	sandboxIP, err := sandbox.ContainerIP(s.ctx, network.Name)
	s.Require().NoError(err, "failed to get sandbox IP")

	// Start devcontainer
	devcontainer, err := NewDevcontainer(s.ctx, DevcontainerConfig{
		Network:    network.Name,
		Privileged: true,
	})
	s.Require().NoError(err, "failed to create devcontainer")
	defer func() {
		logs, _ := devcontainer.Logs(s.ctx)
		s.T().Logf("Devcontainer logs:\n%s", logs)
		devcontainer.Terminate(s.ctx)
	}()

	// Run bridge intercept
	sandboxURL := "http://" + sandboxIP + ":3000"
	interceptCmd := []string{
		"sh", "-c",
		"bridge intercept " +
			"--sandbox-url " + sandboxURL + " " +
			"--function-url http://localhost:9999 " +
			"--name test-sandbox " +
			"> /tmp/intercept.log 2>&1 &",
	}
	exitCode, _, err := devcontainer.Exec(s.ctx, interceptCmd)
	s.Require().NoError(err, "failed to start bridge intercept")
	s.Require().Equal(0, exitCode, "failed to start bridge intercept")

	// Create a test file
	exitCode, _, err = devcontainer.Exec(s.ctx, []string{"sh", "-c", "echo 'hello from devcontainer' > test.txt"})
	s.Require().NoError(err, "failed to create test file")
	s.Require().Equal(0, exitCode, "failed to create test file")

	// Wait for sync
	time.Sleep(1 * time.Second)

	// Print intercept logs
	_, interceptLogs, _ := devcontainer.Exec(s.ctx, []string{"cat", "/tmp/intercept.log"})
	s.T().Logf("Intercept logs:\n%s", interceptLogs)

	// Debug: list sandbox directory contents and permissions
	_, lsOutput, _ := sandbox.Exec(s.ctx, []string{"ls", "-la", "/vercel/sandbox"})
	s.T().Logf("Sandbox /vercel/sandbox contents:\n%s", lsOutput)

	// Check that the file exists in the sandbox
	exitCode, fileContent, err := sandbox.Exec(s.ctx, []string{"cat", "/vercel/sandbox/test.txt"})
	s.Require().NoError(err, "failed to read file from sandbox")
	s.Require().Equal(0, exitCode, "file not found in sandbox")
	s.Contains(fileContent, "hello from devcontainer", "file content mismatch")
}

// TestE2ESuite runs the e2e test suite
func TestE2ESuite(t *testing.T) {
	suite.Run(t, new(E2ESuite))
}
