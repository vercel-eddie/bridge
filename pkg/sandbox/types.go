package sandbox

import "time"

type Status string

const (
	StatusPending      Status = "pending"
	StatusRunning      Status = "running"
	StatusStopping     Status = "stopping"
	StatusStopped      Status = "stopped"
	StatusFailed       Status = "failed"
	StatusError        Status = "error"
	StatusSnapshotting Status = "snapshotting"
)

type Sandbox struct {
	ID        string `json:"id"`
	Status    Status `json:"status"`
	Memory    int    `json:"memory"`
	VCPUs     int    `json:"vcpus"`
	Region    string `json:"region"`
	Runtime   string `json:"runtime"`
	Timeout   int    `json:"timeout"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

type SandboxRoute struct {
	URL       string `json:"url"`
	Subdomain string `json:"subdomain"`
	Port      int    `json:"port"`
}

type SandboxResponse struct {
	Sandbox Sandbox        `json:"sandbox"`
	Routes  []SandboxRoute `json:"routes"`
}

type CreateOptions struct {
	Runtime string
	Timeout time.Duration
	Ports   []int
}

type Command struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Args      []string `json:"args"`
	Cwd       string   `json:"cwd"`
	SandboxID string   `json:"sandboxId"`
	ExitCode  *int     `json:"exitCode"` // nullable
	StartedAt int64    `json:"startedAt"`
}

type CommandResponse struct {
	Command Command `json:"command"`
}

// CommandResult is kept for backward compatibility
type CommandResult = Command

type FileEntry struct {
	Path    string
	Content []byte
}

type Snapshot struct {
	ID        string    `json:"id"`
	SandboxID string    `json:"sandboxId"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}
