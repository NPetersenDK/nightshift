package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Limits struct {
		Session        float64 `json:"session"`
		Week           float64 `json:"week"`
		MaxTasksPerRun int     `json:"maxTasksPerRun"`
	} `json:"limits"`
	LogRetentionDays int               `json:"logRetentionDays"`
	Model            string            `json:"model"`
	Models           map[string]string `json:"models"`
	WorktreesRoot    string            `json:"worktreesRoot"`
	CloseAs          string            `json:"closeAs"` // state category for finished items: Resolved (default) or Completed
	Webhook          Webhook           `json:"webhook"`
	Docker           DockerCfg         `json:"docker"`
	Repos            map[string]Repo   `json:"repos"`
	Orgs             []Org             `json:"orgs"`

	dir string // folder holding the config file; data/ and token files live here
}

type DockerCfg struct {
	Enabled            bool   `json:"enabled"`
	Image              string `json:"image"`
	TaskTimeoutMinutes int    `json:"taskTimeoutMinutes"`
	Memory             string `json:"memory"`
	CPUs               string `json:"cpus"`
	GitName            string `json:"gitName"`
	GitEmail           string `json:"gitEmail"`
	AllowAllTools      *bool  `json:"allowAllTools"` // default true: skip all permission checks inside the container
}

func (d DockerCfg) allowAll() bool { return d.AllowAllTools == nil || *d.AllowAllTools }

type Repo struct {
	URL          string   `json:"url"`
	Path         string   `json:"path"`
	AllowedTools []string `json:"allowedTools"`
	Model        string   `json:"model"`
}

type Org struct {
	Name     string    `json:"name"`
	PAT      string    `json:"pat"`
	Projects []Project `json:"projects"`
}

type Project struct {
	Name     string       `json:"name"`
	Team     string       `json:"team"`
	Features []FeatureDef `json:"features"`
}

type FeatureDef struct {
	Name  string   `json:"name"`
	Repos []string `json:"repos"`
}

func configPath() string {
	if p := os.Getenv("NIGHTSHIFT_CONFIG"); p != "" {
		return p
	}
	if _, err := os.Stat("env.json"); err == nil {
		p, _ := filepath.Abs("env.json")
		return p
	}
	exe, _ := os.Executable()
	return filepath.Join(filepath.Dir(exe), "env.json")
}

func loadConfig() (*Config, error) {
	path := configPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	cfg.Limits.Session, cfg.Limits.Week, cfg.Limits.MaxTasksPerRun = 0.9, 0.95, 10
	cfg.LogRetentionDays = 14
	cfg.Docker.Image, cfg.Docker.TaskTimeoutMinutes = "ghcr.io/npetersendk/nightshift-agent:latest", 360
	if err := json.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg.dir = filepath.Dir(path)
	switch strings.ToLower(cfg.CloseAs) {
	case "", "resolved":
		cfg.CloseAs = "Resolved"
	case "completed", "closed", "done":
		cfg.CloseAs = "Completed"
	default:
		return nil, fmt.Errorf("%s: closeAs must be Resolved or Completed, got %q", path, cfg.CloseAs)
	}
	for alias, r := range cfg.Repos {
		if r.Path == "" {
			r.Path = cfg.dataPath(filepath.Join("repos", alias))
			cfg.Repos[alias] = r
		}
	}
	loadSecretFile("CLAUDE_CODE_OAUTH_TOKEN", filepath.Join(cfg.dir, "claude-token.txt"))
	loadSecretFile("GH_TOKEN", filepath.Join(cfg.dir, "gh-token.txt"))
	return cfg, nil
}

// loadSecretFile sets env var from a file unless the env var is already set.
func loadSecretFile(env, file string) {
	if os.Getenv(env) != "" {
		return
	}
	if b, err := os.ReadFile(file); err == nil {
		os.Setenv(env, strings.TrimSpace(string(b)))
	}
}

func (c *Config) dataPath(name string) string {
	dir := os.Getenv("NIGHTSHIFT_DATA")
	if dir == "" {
		dir = filepath.Join(c.dir, "data")
	}
	os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, name)
}

func (c *Config) worktreesRoot() string {
	if c.WorktreesRoot != "" {
		return c.WorktreesRoot
	}
	return c.dataPath("worktrees")
}

type Target struct {
	Org     Org
	Project Project
}

func (c *Config) target() (Target, error) {
	if len(c.Orgs) == 0 || len(c.Orgs[0].Projects) == 0 {
		return Target{}, errors.New("no org/project in config")
	}
	return Target{c.Orgs[0], c.Orgs[0].Projects[0]}, nil
}

func (t Target) pat() string {
	if p := os.Getenv("AZDO_PAT"); p != "" {
		return p
	}
	return t.Org.PAT
}
