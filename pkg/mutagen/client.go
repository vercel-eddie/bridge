package mutagen

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Client provides a Go API for interacting with mutagen sync sessions.
// The mutagen binary must be installed separately using Install() before
// creating a client.
type Client struct {
	binaryPath string
}

// SyncConfig configures a sync session.
type SyncConfig struct {
	// Name is the session name for identification
	Name string
	// Source is the local path to sync from
	Source string
	// Target is the remote path (e.g., "root@host:/path" or "root@host:port:/path")
	Target string
	// IgnoreVCS ignores version control directories
	IgnoreVCS bool
	// SyncMode is the sync mode (e.g., "two-way-resolved")
	SyncMode string
}

// NewClient creates a new mutagen client.
// Returns an error if mutagen is not installed.
// Call Install() first to ensure the binary is available.
func NewClient() (*Client, error) {
	if !IsInstalled() {
		return nil, fmt.Errorf("mutagen is not installed; call Install() first")
	}

	return &Client{
		binaryPath: BinaryPath(),
	}, nil
}

// CreateSyncSession creates a new sync session with the given configuration.
func (c *Client) CreateSyncSession(cfg SyncConfig) error {
	// First, terminate any existing session with this name
	termCmd := exec.Command(c.binaryPath, "sync", "terminate", cfg.Name)
	termCmd.Run() // Ignore errors - session might not exist

	args := []string{
		"sync", "create",
		"--name", cfg.Name,
		cfg.Source,
		cfg.Target,
	}

	if cfg.IgnoreVCS {
		args = append(args, "--ignore-vcs")
	}

	if cfg.SyncMode != "" {
		args = append(args, fmt.Sprintf("--sync-mode=%s", cfg.SyncMode))
	}

	cmd := exec.Command(c.binaryPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to create sync session: %w (output: %s)", err, string(output))
	}

	// Flush to ensure initial sync completes
	flushCmd := exec.Command(c.binaryPath, "sync", "flush", cfg.Name)
	if err := flushCmd.Run(); err != nil {
		return fmt.Errorf("failed to flush sync session: %w", err)
	}

	return nil
}

// TerminateSyncSession terminates a sync session by name.
func (c *Client) TerminateSyncSession(name string) error {
	cmd := exec.Command(c.binaryPath, "sync", "terminate", name)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to terminate sync session: %w", err)
	}
	return nil
}

// FlushSyncSession forces a sync cycle for the named session and waits for it to complete.
func (c *Client) FlushSyncSession(name string) error {
	cmd := exec.Command(c.binaryPath, "sync", "flush", name)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to flush sync session: %w", err)
	}
	return nil
}

// WaitForSyncReady waits for the sync session to be connected and ready.
func (c *Client) WaitForSyncReady(name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cmd := exec.Command(c.binaryPath, "sync", "list", name)
		output, err := cmd.CombinedOutput()
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		// Check if session is watching (ready)
		outputStr := string(output)
		if strings.Contains(outputStr, "Watching for changes") {
			return nil
		}
		// Also accept "Scanning files" as a reasonable state
		if strings.Contains(outputStr, "Scanning files") {
			return nil
		}

		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for sync session to be ready")
}

// PauseSyncSession pauses the named sync session.
func (c *Client) PauseSyncSession(name string) error {
	cmd := exec.Command(c.binaryPath, "sync", "pause", name)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to pause sync session: %w", err)
	}
	return nil
}

// ResumeSyncSession resumes the named sync session.
func (c *Client) ResumeSyncSession(name string) error {
	cmd := exec.Command(c.binaryPath, "sync", "resume", name)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to resume sync session: %w", err)
	}
	return nil
}

// GetSyncStatus returns a brief status string for the named sync session.
func (c *Client) GetSyncStatus(name string) (string, error) {
	cmd := exec.Command(c.binaryPath, "sync", "list", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get sync status: %w", err)
	}

	// Extract status and synchronizable contents
	outputStr := string(output)
	var status string
	var alphaContents, betaContents string
	inAlpha := false
	inBeta := false
	for _, line := range strings.Split(outputStr, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Status:") {
			status = trimmed
		}
		if strings.HasPrefix(trimmed, "Alpha:") {
			inAlpha = true
			inBeta = false
		}
		if strings.HasPrefix(trimmed, "Beta:") {
			inAlpha = false
			inBeta = true
		}
		if strings.Contains(line, "files") || strings.Contains(line, "symbolic links") {
			if inAlpha {
				alphaContents += trimmed + " "
			} else if inBeta {
				betaContents += trimmed + " "
			}
		}
	}
	return fmt.Sprintf("%s | Alpha: %s| Beta: %s", status, alphaContents, betaContents), nil
}
