package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
)

var (
	buildOnce  sync.Once
	buildErr   error
	binaryPath string
)

// BuildBridge compiles the bridge CLI for Linux.
// The binary is cached and reused across tests.
func BuildBridge() (string, error) {
	buildOnce.Do(func() {
		projectRoot, err := findProjectRoot()
		if err != nil {
			buildErr = fmt.Errorf("failed to find project root: %w", err)
			return
		}

		// Create a temp directory for the binary
		tmpDir, err := os.MkdirTemp("", "bridge-e2e-*")
		if err != nil {
			buildErr = fmt.Errorf("failed to create temp dir: %w", err)
			return
		}

		binaryPath = filepath.Join(tmpDir, "bridge")

		// Cross-compile for Linux (same architecture as host)
		cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/bridge")
		cmd.Dir = projectRoot
		cmd.Env = append(os.Environ(),
			"GOOS=linux",
			"GOARCH="+runtime.GOARCH,
			"CGO_ENABLED=0",
		)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			buildErr = fmt.Errorf("failed to build bridge: %w", err)
			return
		}
	})

	return binaryPath, buildErr
}

// CleanupBuild removes the built binary.
// Call this after all tests are done.
func CleanupBuild() {
	if binaryPath != "" {
		os.RemoveAll(filepath.Dir(binaryPath))
	}
}
