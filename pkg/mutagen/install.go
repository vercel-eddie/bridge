package mutagen

import (
	"archive/tar"
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
)

var baseURL = "https://github.com/mutagen-io/mutagen/releases/download"

// InstallDir returns the directory where mutagen is installed.
func InstallDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/tmp"
	}
	return filepath.Join(home, ".reach", "bin")
}

// BinaryPath returns the full path to the mutagen binary.
func BinaryPath() string {
	name := BinaryName
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(InstallDir(), name)
}

// IsInstalled checks if mutagen is already installed.
func IsInstalled() bool {
	_, err := os.Stat(BinaryPath())
	return err == nil
}

// Install downloads and installs the mutagen binary for the current OS.
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

	if err := extractBinary(resp.Body); err != nil {
		return fmt.Errorf("failed to extract mutagen: %w", err)
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

func extractBinary(r io.Reader) error {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	binaryName := BinaryName
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
			outPath := BinaryPath()
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

	return fmt.Errorf("mutagen binary not found in archive")
}
