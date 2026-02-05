package mutagen

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
)

const (
	Version    = "0.18.1"
	BinaryName = "mutagen"
	AgentName  = "mutagen-agent"
)

var baseURL = "https://github.com/mutagen-io/mutagen/releases/download"

// InstallDir returns the directory where mutagen binaries are installed.
func InstallDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/tmp"
	}
	return filepath.Join(home, ".bridge", "bin")
}

// BinaryPath returns the full path to the mutagen binary.
func BinaryPath() string {
	name := BinaryName
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(InstallDir(), name)
}

// AgentPath returns the full path to the mutagen agent binary.
func AgentPath() string {
	return filepath.Join(InstallDir(), AgentName)
}

// IsInstalled checks if mutagen is already installed.
func IsInstalled() bool {
	_, err := os.Stat(BinaryPath())
	return err == nil
}

// IsAgentInstalled checks if the mutagen agent is already installed.
func IsAgentInstalled() bool {
	_, err := os.Stat(AgentPath())
	return err == nil
}

// Install downloads and installs the mutagen binary for the current OS.
// This is used on the client side to install the mutagen CLI.
func Install() error {
	if IsInstalled() {
		return nil
	}

	osName, archName, err := getPlatform()
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/v%s/mutagen_%s_%s_v%s.tar.gz", baseURL, Version, osName, archName, Version)

	if err := os.MkdirAll(InstallDir(), 0755); err != nil {
		return fmt.Errorf("failed to create install directory: %w", err)
	}

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("failed to download mutagen: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download mutagen: %s", resp.Status)
	}

	if err := extractBinary(resp.Body, BinaryName, BinaryPath()); err != nil {
		return fmt.Errorf("failed to extract mutagen: %w", err)
	}

	return nil
}

// InstallAgent downloads and installs the mutagen agent binary for the current OS.
// This is used on the server side (sandbox) to install the agent that mutagen will invoke.
// It also creates the symlink that mutagen expects at ~/.mutagen/agents/<version>/mutagen-agent.
func InstallAgent() error {
	if IsAgentInstalled() {
		// Still need to ensure the symlink exists
		return ensureAgentSymlink()
	}

	osName, archName, err := getPlatform()
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/v%s/mutagen_%s_%s_v%s.tar.gz", baseURL, Version, osName, archName, Version)

	if err := os.MkdirAll(InstallDir(), 0755); err != nil {
		return fmt.Errorf("failed to create install directory: %w", err)
	}

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("failed to download mutagen: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download mutagen: %s", resp.Status)
	}

	if err := extractAgentFromTarball(resp.Body, osName, archName); err != nil {
		return fmt.Errorf("failed to extract mutagen agent: %w", err)
	}

	// Create symlink so mutagen can find the agent at the expected location
	return ensureAgentSymlink()
}

// ensureAgentSymlink creates the symlink that mutagen expects to find the agent.
// Mutagen looks for the agent at ~/.mutagen/agents/<version>/mutagen-agent
func ensureAgentSymlink() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	// Create the directory mutagen expects
	mutagenAgentDir := filepath.Join(home, ".mutagen", "agents", Version)
	if err := os.MkdirAll(mutagenAgentDir, 0755); err != nil {
		return fmt.Errorf("failed to create mutagen agent directory: %w", err)
	}

	symlinkPath := filepath.Join(mutagenAgentDir, AgentName)
	targetPath := AgentPath()

	// Check if symlink already exists and points to the right place
	if existingTarget, err := os.Readlink(symlinkPath); err == nil {
		if existingTarget == targetPath {
			return nil // Symlink already correct
		}
		// Wrong target, remove it
		os.Remove(symlinkPath)
	} else if !os.IsNotExist(err) {
		// File exists but is not a symlink, remove it
		os.Remove(symlinkPath)
	}

	// Create the symlink
	if err := os.Symlink(targetPath, symlinkPath); err != nil {
		return fmt.Errorf("failed to create agent symlink: %w", err)
	}

	return nil
}

func getPlatform() (string, string, error) {
	var osName string
	switch runtime.GOOS {
	case "darwin":
		osName = "darwin"
	case "linux":
		osName = "linux"
	case "windows":
		osName = "windows"
	default:
		return "", "", fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}

	var archName string
	switch runtime.GOARCH {
	case "amd64":
		archName = "amd64"
	case "arm64":
		archName = "arm64"
	default:
		return "", "", fmt.Errorf("unsupported architecture: %s", runtime.GOARCH)
	}

	return osName, archName, nil
}

func extractBinary(r io.Reader, binaryName, outPath string) error {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		if header.Name == binaryName || header.Name == "./"+binaryName {
			outFile, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				return err
			}
			defer outFile.Close()

			if _, err := io.Copy(outFile, tr); err != nil {
				return err
			}

			return nil
		}
	}

	return fmt.Errorf("%s not found in archive", binaryName)
}

// extractAgentFromTarball extracts the mutagen agent from the release tarball.
// The agent is inside a nested mutagen-agents.tar.gz file.
func extractAgentFromTarball(r io.Reader, osName, archName string) error {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	// First, find and extract the mutagen-agents.tar.gz
	var agentsTarball []byte
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		if header.Name == "mutagen-agents.tar.gz" || header.Name == "./mutagen-agents.tar.gz" {
			agentsTarball, err = io.ReadAll(tr)
			if err != nil {
				return err
			}
			break
		}
	}

	if agentsTarball == nil {
		return fmt.Errorf("mutagen-agents.tar.gz not found in archive")
	}

	// Now extract the specific agent from the agents tarball
	agentsGr, err := gzip.NewReader(bytes.NewReader(agentsTarball))
	if err != nil {
		return err
	}
	defer agentsGr.Close()

	agentsTr := tar.NewReader(agentsGr)
	targetName := fmt.Sprintf("%s_%s", osName, archName)

	for {
		header, err := agentsTr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		if header.Name == targetName || header.Name == "./"+targetName {
			outFile, err := os.OpenFile(AgentPath(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				return err
			}
			defer outFile.Close()

			if _, err := io.Copy(outFile, agentsTr); err != nil {
				return err
			}

			return nil
		}
	}

	return fmt.Errorf("agent binary %s not found in agents archive", targetName)
}
