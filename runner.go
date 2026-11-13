package main

import (
	"bufio"
	"bytes"
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
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		lastDiag = err.Error()
		return nil, err
	}
	// claude -p sometimes stays alive after its final result (seen after background subagents),
	// which would block us until the task timeout. Once a result arrives with nothing left running,
	// give it a short grace period to exit, then kill it. Any further output cancels the kill.
	kill := func() {
		if name != "" {
			exec.Command("docker", "kill", name).Run()
		} else if cmd.Process != nil {
			cmd.Process.Kill()
		}
	}
	var linger *time.Timer
	var lines []string
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		line := sc.Text()
		lines = append(lines, line)
		if linger != nil {
			linger.Stop()
			linger = nil
		}
		if isFinalResult(line) {
			linger = time.AfterFunc(resultGrace, kill)
		}
	}
	if linger != nil {
		linger.Stop()
	}
	werr := cmd.Wait()
	lastDiag = diagnose(werr, lines, stderr.String())
	if ctx.Err() != nil && name != "" {
		exec.Command("docker", "kill", name).Run()
	}
	if ctx.Err() != nil {
		return lines, fmt.Errorf("timeout after %s", spec.Timeout)
	}
	_ = werr // a non-zero exit is reported through the result event; callers inspect the events
	return lines, nil
}

// resultGrace is how long claude may linger after its final result before it is killed.
const resultGrace = 60 * time.Second

// isFinalResult reports whether a stream-json line is a result event after which no more work is pending:
// no queued turns and every spawned subagent completed, failed or was killed.
func isFinalResult(line string) bool {
	if !strings.Contains(line, `"type":"result"`) {
		return false
	}
	var e struct {
		Type        string `json:"type"`
		QueuedTurns int    `json:"queued_turn_count"`
		Subagents   *struct {
			Spawned   int            `json:"spawned"`
			Completed int            `json:"completed"`
			Failed    int            `json:"failed"`
			Killed    map[string]int `json:"killed"`
		} `json:"subagent_stats"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(line)), &e) != nil || e.Type != "result" || e.QueuedTurns > 0 {
		return false
	}
	if s := e.Subagents; s != nil {
		ended := s.Completed + s.Failed
		for _, n := range s.Killed {
			ended += n
		}
		if ended < s.Spawned {
			return false
		}
	}
	return true
}

// lastDiag describes the most recent claude run (exit error, stderr, stdout head) for error messages.
var lastDiag string

func diagnose(werr error, lines []string, stderr string) string {
	var b strings.Builder
	if werr != nil {
		b.WriteString(werr.Error() + "\n")
	}
	if s := strings.TrimSpace(stderr); s != "" {
		b.WriteString(clip(s, 800) + "\n")
	}
	if len(lines) > 0 {
		b.WriteString(clip(strings.Join(lines, "\n"), 800))
	}
	return strings.TrimSpace(b.String())
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
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
