package mutagen

import (
	"context"
	"os"
	"os/exec"
)

// Client is an interface for interacting with the mutagen binary.
type Client interface {
	// Sync creates a new sync session.
	Sync(ctx context.Context, alpha, beta string, opts ...string) error

	// List lists all sync sessions.
	List(ctx context.Context) ([]byte, error)

	// Terminate terminates a sync session.
	Terminate(ctx context.Context, session string) error

	// Pause pauses a sync session.
	Pause(ctx context.Context, session string) error

	// Resume resumes a sync session.
	Resume(ctx context.Context, session string) error

	// Flush flushes a sync session.
	Flush(ctx context.Context, session string) error

	// Run executes an arbitrary mutagen command.
	Run(ctx context.Context, args ...string) ([]byte, error)

	// RunInteractive executes a mutagen command with stdout/stderr attached.
	RunInteractive(ctx context.Context, args ...string) error
}

type client struct {
	binaryPath string
}

// NewClient creates a new mutagen client.
// It ensures mutagen is installed before returning.
func NewClient() (Client, error) {
	if err := Install(); err != nil {
		return nil, err
	}
	return &client{binaryPath: BinaryPath()}, nil
}

func (c *client) Sync(ctx context.Context, alpha, beta string, opts ...string) error {
	args := append([]string{"sync", "create", alpha, beta}, opts...)
	return c.RunInteractive(ctx, args...)
}

func (c *client) List(ctx context.Context) ([]byte, error) {
	return c.Run(ctx, "sync", "list")
}

func (c *client) Terminate(ctx context.Context, session string) error {
	_, err := c.Run(ctx, "sync", "terminate", session)
	return err
}

func (c *client) Pause(ctx context.Context, session string) error {
	_, err := c.Run(ctx, "sync", "pause", session)
	return err
}

func (c *client) Resume(ctx context.Context, session string) error {
	_, err := c.Run(ctx, "sync", "resume", session)
	return err
}

func (c *client) Flush(ctx context.Context, session string) error {
	_, err := c.Run(ctx, "sync", "flush", session)
	return err
}

func (c *client) Run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.binaryPath, args...)
	return cmd.CombinedOutput()
}

func (c *client) RunInteractive(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, c.binaryPath, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
