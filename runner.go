package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type RunSpec struct {
	Args      []string // claude arguments
	Stdin     string
	Cwd       string // workspace folder (mounted at /work in docker)
	Name      string // container name suffix
	Workspace bool   // docker: mount Cwd, claude-home and config (false for light probes)
	Timeout   time.Duration
}

// runClaude runs the claude CLI locally or inside a docker container and returns stdout lines.
func (c *Config) runClaude(spec RunSpec) ([]string, error) {
	if spec.Timeout == 0 {
		spec.Timeout = time.Duration(c.Docker.TaskTimeoutMinutes) * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), spec.Timeout)
	defer cancel()

	var cmd *exec.Cmd
	name := ""
	if c.Docker.Enabled {
		name = fmt.Sprintf("nightshift-%s-%d", spec.Name, time.Now().Unix())
		args, env := c.dockerArgs(spec, name)
		cmd = exec.CommandContext(ctx, "docker", args...)
		cmd.Env = append(os.Environ(), env...)
	} else {
		cmd = exec.CommandContext(ctx, "claude", spec.Args...)
		cmd.Dir = spec.Cwd
	}
	cmd.Stdin = strings.NewReader(spec.Stdin)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	var lines []string
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	werr := cmd.Wait()
	if ctx.Err() != nil && name != "" {
		exec.Command("docker", "kill", name).Run()
	}
	if ctx.Err() != nil {
		return lines, fmt.Errorf("timeout after %s", spec.Timeout)
	}
	_ = werr // a non-zero exit is reported through the result event; callers inspect the events
	return lines, nil
}

func (c *Config) dockerArgs(spec RunSpec, name string) (args, env []string) {
	args = []string{"run", "--rm", "-i", "--name", name,
		"-e", "CLAUDE_CODE_OAUTH_TOKEN", "-e", "GH_TOKEN", "-e", "AZDO_PAT"}
	if t, err := c.target(); err == nil {
		env = append(env, "AZDO_PAT="+t.pat())
	}
	if c.Docker.Memory != "" {
		args = append(args, "--memory", c.Docker.Memory)
	}
	if c.Docker.CPUs != "" {
		args = append(args, "--cpus", c.Docker.CPUs)
	}
	if c.Docker.GitName != "" {
		args = append(args, "-e", "NIGHTSHIFT_GIT_NAME="+c.Docker.GitName, "-e", "NIGHTSHIFT_GIT_EMAIL="+c.Docker.GitEmail)
	}
	if spec.Workspace {
		args = append(args,
			"-v", spec.Cwd+":/work", "-w", "/work",
			"-v", "nightshift-claude-home:/home/agent/.claude",
			"-v", c.containerConfig()+":/etc/nightshift/config.json:ro",
			"-e", "NIGHTSHIFT_CONFIG=/etc/nightshift/config.json")
	}
	args = append(args, c.Docker.Image, "claude")
	return append(args, spec.Args...), env
}

// containerConfig writes a trimmed config (no PAT, no repo list) for `nightshift close` inside the container.
func (c *Config) containerConfig() string {
	slim := Config{Orgs: make([]Org, len(c.Orgs))}
	for i, o := range c.Orgs {
		o.PAT = ""
		slim.Orgs[i] = o
	}
	b, _ := json.MarshalIndent(slim, "", "  ")
	p := c.dataPath("container-config.json")
	os.WriteFile(p, b, 0o600)
	return p
}

// Event is the subset of claude's stream-json events we care about.
type Event struct {
	Type           string          `json:"type"`
	IsError        bool            `json:"is_error"`
	APIError       any             `json:"api_error"`
	APIErrorStatus any             `json:"api_error_status"`
	Result         json.RawMessage `json:"result"`
	RateLimitInfo  struct {
		ResetsAt       float64 `json:"resetsAt"`
		UnifiedWindows map[string]struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    float64 `json:"resetsAt"`
		} `json:"unifiedWindows"`
	} `json:"rate_limit_info"`
}

func (e Event) resultText() string {
	var s string
	if json.Unmarshal(e.Result, &s) == nil {
		return s
	}
	return ""
}

func parseEvents(lines []string) []Event {
	var evs []Event
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "{") {
			continue
		}
		var e Event
		if json.Unmarshal([]byte(l), &e) == nil {
			evs = append(evs, e)
		}
	}
	return evs
}

func lastEvent(evs []Event, typ string) *Event {
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Type == typ {
			return &evs[i]
		}
	}
	return nil
}

func writeLog(c *Config, name string, lines []string) {
	dir := c.dataPath("logs")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(dir+string(os.PathSeparator)+name, []byte(strings.Join(lines, "\n")), 0o644)
}
