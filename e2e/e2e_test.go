package e2e

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// E2ESuite is the base test suite for e2e tests
type E2ESuite struct {
	suite.Suite
	ctx    context.Context
	cancel context.CancelFunc
	env    *Environment
}

// SetupSuite runs once before all tests in the suite
func (s *E2ESuite) SetupSuite() {
	if testing.Short() {
		s.T().Skip("skipping e2e test in short mode")
	}

	s.ctx, s.cancel = context.WithTimeout(context.Background(), 5*time.Minute)

	var err error
	s.env, err = SetupEnvironment(s.ctx, EnvironmentConfig{})
	require.NoError(s.T(), err, "failed to setup environment")
}

// TearDownSuite runs once after all tests in the suite
func (s *E2ESuite) TearDownSuite() {
	if s.env != nil {
		s.env.TearDown(s.ctx, s.T())
	}
	if s.cancel != nil {
		s.cancel()
	}
	CleanupBuild()
}

// TestHealth verifies the sandbox responds to health checks
func (s *E2ESuite) TestHealth() {
	resp, err := http.Get(s.env.Sandbox.URL() + "/health")
	s.Require().NoError(err, "health check request failed")
	defer resp.Body.Close()

	s.Equal(http.StatusOK, resp.StatusCode)
	s.Equal("test-sandbox", resp.Header.Get("X-Bridge-Name"))
}

// TestDevcontainerVersion verifies the devcontainer can run bridge commands
func (s *E2ESuite) TestDevcontainerVersion() {
	exitCode, output, err := s.env.Devcontainer.Exec(s.ctx, []string{"bridge", "--version"})
	s.Require().NoError(err)
	s.Equal(0, exitCode, "expected exit code 0")
	s.Contains(output, "bridge version")
}

// TestSSHConnection verifies that SSH works through the tunnel
func (s *E2ESuite) TestSSHConnection() {
	sshCmd := []string{
		"sh", "-c",
		"ssh bridge.test-sandbox echo 'hello from sandbox'",
	}
	exitCode, output, err := s.env.Devcontainer.Exec(s.ctx, sshCmd)

	s.T().Logf("SSH output:\n%s", output)
	s.T().Logf("SSH exit code: %d", exitCode)

	s.Require().NoError(err, "SSH command failed")
	s.Require().Equal(0, exitCode, "SSH command returned non-zero exit code")
	s.Contains(output, "hello from sandbox", "unexpected SSH output")
}

// TestMutagenSync verifies that mutagen syncs files between devcontainer and sandbox
func (s *E2ESuite) TestMutagenSync() {
	// Create a test file in the devcontainer
	exitCode, _, err := s.env.Devcontainer.Exec(s.ctx, []string{"sh", "-c", "echo 'hello from devcontainer' > test.txt"})
	s.Require().NoError(err, "failed to create test file")
	s.Require().Equal(0, exitCode, "failed to create test file")

	// Wait for sync
	time.Sleep(2 * time.Second)

	// Debug: list sandbox directory contents
	_, lsOutput, _ := s.env.Sandbox.Exec(s.ctx, []string{"ls", "-la", "/vercel/sandbox"})
	s.T().Logf("Sandbox /vercel/sandbox contents:\n%s", lsOutput)

	// Check that the file exists in the sandbox
	exitCode, fileContent, err := s.env.Sandbox.Exec(s.ctx, []string{"cat", "/vercel/sandbox/test.txt"})
	s.Require().NoError(err, "failed to read file from sandbox")
	s.Require().Equal(0, exitCode, "file not found in sandbox")
	s.Contains(fileContent, "hello from devcontainer", "file content mismatch")
}

// TestDispatcherForward verifies that a request to the dispatcher is forwarded
// through the tunnel to the devcontainer app.
func (s *E2ESuite) TestDispatcherForward() {
	// Start a simple HTTP server on port 3000 inside the devcontainer.
	// Use busybox httpd without -f so it daemonizes and survives the exec session.
	startServer := []string{
		"sh", "-c",
		"mkdir -p /tmp/www && echo 'hello from devcontainer app' > /tmp/www/index.html && httpd -p 3000 -h /tmp/www",
	}
	exitCode, output, err := s.env.Devcontainer.Exec(s.ctx, startServer)
	s.Require().NoError(err, "failed to start HTTP server")
	s.Require().Equal(0, exitCode, "failed to start HTTP server: %s", output)

	// Verify the server is listening before sending traffic through the tunnel.
	exitCode, output, err = s.env.Devcontainer.Exec(s.ctx, []string{"wget", "-q", "-O", "-", "http://127.0.0.1:3000/index.html"})
	s.Require().NoError(err, "httpd not reachable")
	s.Require().Equal(0, exitCode, "httpd not reachable: %s", output)

	// Send a request to the dispatcher — this triggers the tunnel pairing
	// (dispatcher connects to sandbox on first request) and forwards
	// through: dispatcher → sandbox → devcontainer intercept → local app.
	// Retry a few times since the first request triggers tunnel setup.
	var resp *http.Response
	for attempt := 1; attempt <= 5; attempt++ {
		resp, err = http.Get(s.env.Dispatcher.URL() + "/index.html")
		if err == nil && resp.StatusCode == http.StatusOK {
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		s.T().Logf("Attempt %d: status=%d err=%v", attempt, resp.StatusCode, err)
		time.Sleep(2 * time.Second)
	}

	s.Require().NoError(err, "request to dispatcher failed")
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	s.Require().NoError(err, "failed to read response body")

	s.T().Logf("Dispatcher response status: %d", resp.StatusCode)
	s.T().Logf("Dispatcher response body: %s", string(body))

	s.Equal(http.StatusOK, resp.StatusCode)
	s.Contains(string(body), "hello from devcontainer app")
}

// TestE2ESuite runs the e2e test suite
func TestE2ESuite(t *testing.T) {
	suite.Run(t, new(E2ESuite))
}
