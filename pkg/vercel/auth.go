package vercel

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type authJSON struct {
	Token string `json:"token"`
}

type projectJSON struct {
	OrgID     string `json:"orgId"`
	ProjectID string `json:"projectId"`
}

type ProjectConfig struct {
	TeamID    string
	ProjectID string
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return "/tmp"
}

// LoadToken loads the Vercel authentication token from CLI config files or environment.
// It checks in order:
//  1. VERCEL_OIDC_TOKEN environment variable (preferred for sandbox)
//  2. VERCEL_TOKEN environment variable
//  3. ~/Library/Application Support/com.vercel.cli/auth.json (macOS)
//  4. ~/.config/vercel/auth.json (Linux)
func LoadToken() (string, error) {
	if token := os.Getenv("VERCEL_OIDC_TOKEN"); token != "" {
		return token, nil
	}
	if token := os.Getenv("VERCEL_TOKEN"); token != "" {
		return token, nil
	}

	home := homeDir()
	paths := []string{
		filepath.Join(home, "Library/Application Support/com.vercel.cli/auth.json"),
		filepath.Join(home, ".config/vercel/auth.json"),
	}

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}

		var auth authJSON
		if err := json.Unmarshal(data, &auth); err != nil {
			continue
		}

		if auth.Token != "" {
			return auth.Token, nil
		}
	}

	return "", errors.New("vercel token not found. Run 'vercel login' or set VERCEL_TOKEN")
}

// LoadProjectConfig loads the Vercel project configuration from .vercel/project.json.
// It checks in order:
//  1. VERCEL_TEAM_ID and VERCEL_PROJECT_ID environment variables
//  2. .vercel/project.json in the current directory
func LoadProjectConfig() (*ProjectConfig, error) {
	cfg := &ProjectConfig{
		TeamID:    os.Getenv("VERCEL_TEAM_ID"),
		ProjectID: os.Getenv("VERCEL_PROJECT_ID"),
	}

	if cfg.TeamID != "" && cfg.ProjectID != "" {
		return cfg, nil
	}

	data, err := os.ReadFile(".vercel/project.json")
	if err != nil {
		if cfg.TeamID == "" && cfg.ProjectID == "" {
			return nil, errors.New("vercel project not found. Run 'vercel link' or set VERCEL_TEAM_ID and VERCEL_PROJECT_ID")
		}
		if cfg.TeamID == "" {
			return nil, errors.New("VERCEL_TEAM_ID not set")
		}
		return nil, errors.New("VERCEL_PROJECT_ID not set")
	}

	var proj projectJSON
	if err := json.Unmarshal(data, &proj); err != nil {
		return nil, err
	}

	if cfg.TeamID == "" {
		cfg.TeamID = proj.OrgID
	}
	if cfg.ProjectID == "" {
		cfg.ProjectID = proj.ProjectID
	}

	if cfg.TeamID == "" {
		return nil, errors.New("team ID not found in .vercel/project.json")
	}
	if cfg.ProjectID == "" {
		return nil, errors.New("project ID not found in .vercel/project.json")
	}

	return cfg, nil
}
