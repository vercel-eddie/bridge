package sandbox

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"time"
)

const baseURL = "https://vercel.com/api"

type Client interface {
	Create(ctx context.Context, opts CreateOptions) (*Sandbox, []SandboxRoute, error)
	Get(ctx context.Context, sandboxID string) (*Sandbox, []SandboxRoute, error)
	WaitForRunning(ctx context.Context, sandboxID string) (*Sandbox, []SandboxRoute, error)
	RunCommand(ctx context.Context, sandboxID, cmd string, args []string, detached bool) (*Command, error)
	WriteFiles(ctx context.Context, sandboxID string, files []FileEntry) error
	Stop(ctx context.Context, sandboxID string) error
	Delete(ctx context.Context, sandboxID string) error
	CreateSnapshot(ctx context.Context, sandboxID, name string) (*Snapshot, error)
}

type client struct {
	token     string
	teamID    string
	projectID string
	http      *http.Client
}

func NewClient(token, teamID, projectID string) Client {
	return &client{
		token:     token,
		teamID:    teamID,
		projectID: projectID,
		http:      &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	return c.doWithQuery(ctx, method, path, body, nil)
}

func (c *client) doWithQuery(ctx context.Context, method, path string, body any, query map[string]string) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(data)
	}

	url := fmt.Sprintf("%s%s?teamId=%s&project=%s", baseURL, path, c.teamID, c.projectID)
	for k, v := range query {
		url += "&" + k + "=" + v
	}

	req, err := http.NewRequestWithContext(ctx, method, url, r)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	return c.http.Do(req)
}

func (c *client) parse(resp *http.Response, v any) error {
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}
	if v != nil {
		return json.NewDecoder(resp.Body).Decode(v)
	}
	return nil
}

func (c *client) Create(ctx context.Context, opts CreateOptions) (*Sandbox, []SandboxRoute, error) {
	body := map[string]any{
		"projectId": c.projectID,
		"runtime":   opts.Runtime,
		"timeout":   int(opts.Timeout.Milliseconds()),
		"ports":     opts.Ports,
	}

	resp, err := c.do(ctx, http.MethodPost, "/v1/sandboxes", body)
	if err != nil {
		return nil, nil, err
	}

	var result SandboxResponse
	if err := c.parse(resp, &result); err != nil {
		return nil, nil, err
	}
	return &result.Sandbox, result.Routes, nil
}

func (c *client) Get(ctx context.Context, sandboxID string) (*Sandbox, []SandboxRoute, error) {
	resp, err := c.do(ctx, http.MethodGet, "/v1/sandboxes/"+sandboxID, nil)
	if err != nil {
		return nil, nil, err
	}

	var result SandboxResponse
	if err := c.parse(resp, &result); err != nil {
		return nil, nil, err
	}
	return &result.Sandbox, result.Routes, nil
}

func (c *client) WaitForRunning(ctx context.Context, sandboxID string) (*Sandbox, []SandboxRoute, error) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-ticker.C:
			sb, routes, err := c.Get(ctx, sandboxID)
			if err != nil {
				return nil, nil, err
			}
			switch sb.Status {
			case StatusRunning:
				return sb, routes, nil
			case StatusFailed, StatusError, StatusStopped:
				return nil, nil, fmt.Errorf("sandbox entered %s state", sb.Status)
			}
		}
	}
}

func (c *client) RunCommand(ctx context.Context, sandboxID, cmd string, args []string, detached bool) (*Command, error) {
	body := map[string]any{
		"command": cmd,
		"args":    args,
	}

	resp, err := c.do(ctx, http.MethodPost, "/v1/sandboxes/"+sandboxID+"/cmd", body)
	if err != nil {
		return nil, err
	}

	var result CommandResponse
	if err := c.parse(resp, &result); err != nil {
		return nil, err
	}

	// If not detached, wait for command to finish using the wait query param
	if !detached {
		cmdID := result.Command.ID
		resp, err := c.doWithQuery(ctx, http.MethodGet, "/v1/sandboxes/"+sandboxID+"/cmd/"+cmdID, nil, map[string]string{"wait": "true"})
		if err != nil {
			return nil, err
		}
		if err := c.parse(resp, &result); err != nil {
			return nil, err
		}
	}

	return &result.Command, nil
}

func (c *client) WriteFiles(ctx context.Context, sandboxID string, files []FileEntry) error {
	// Write files using base64 + shell commands since the API uses gzip streams
	for _, f := range files {
		// Create parent directory
		dir := filepath.Dir(f.Path)
		if dir != "." && dir != "/" {
			_, err := c.RunCommand(ctx, sandboxID, "mkdir", []string{"-p", dir}, false)
			if err != nil {
				return fmt.Errorf("failed to create directory %s: %w", dir, err)
			}
		}

		// Write file using base64 decoding
		encoded := base64.StdEncoding.EncodeToString(f.Content)
		cmd := fmt.Sprintf("echo '%s' | base64 -d > %s", encoded, f.Path)
		_, err := c.RunCommand(ctx, sandboxID, "sh", []string{"-c", cmd}, false)
		if err != nil {
			return fmt.Errorf("failed to write file %s: %w", f.Path, err)
		}
	}
	return nil
}

func (c *client) Stop(ctx context.Context, sandboxID string) error {
	resp, err := c.do(ctx, http.MethodPost, "/v1/sandboxes/"+sandboxID+"/stop", nil)
	if err != nil {
		return err
	}
	return c.parse(resp, nil)
}

func (c *client) Delete(ctx context.Context, sandboxID string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/v1/sandboxes/"+sandboxID, nil)
	if err != nil {
		return err
	}
	return c.parse(resp, nil)
}

func (c *client) CreateSnapshot(ctx context.Context, sandboxID, name string) (*Snapshot, error) {
	body := map[string]string{"name": name}

	resp, err := c.do(ctx, http.MethodPost, "/v1/sandboxes/"+sandboxID+"/snapshot", body)
	if err != nil {
		return nil, err
	}

	var snap Snapshot
	return &snap, c.parse(resp, &snap)
}
