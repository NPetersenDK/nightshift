package main

import (
	"crypto/rand"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

//go:embed prompts/*.md
var promptFS embed.FS

type State struct {
	TaskID       int        `json:"taskId"`
	TaskTitle    string     `json:"taskTitle"`
	Branch       string     `json:"branch"`
	WorktreeRoot string     `json:"worktreeRoot"`
	Aliases      []string   `json:"aliases"`
	Repos        []RepoInfo `json:"repos"`
	SessionID    string     `json:"sessionId"`
	Status       string     `json:"status"`
	ResumeAfter  int64      `json:"resumeAfter"`
	Attempts     int        `json:"attempts"`
}

func (c *Config) loadState() *State {
	b, err := os.ReadFile(c.dataPath("state.json"))
	if err != nil {
		return nil
	}
	var s State
	if json.Unmarshal(b, &s) != nil {
		return nil
	}
	return &s
}

func (c *Config) saveState(s *State) {
	b, _ := json.MarshalIndent(s, "", "  ")
	os.WriteFile(c.dataPath("state.json"), b, 0o644)
}

func (c *Config) clearState() { os.Remove(c.dataPath("state.json")) }

func newUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// acquireLock returns a release func, or nil when another live worker holds a fresh lock.
// A lock whose PID no longer runs (e.g. after Ctrl+C) is stale and taken over.
func (c *Config) acquireLock() func() {
	p := c.dataPath(".worker.lock")
	stale := time.Duration(c.Docker.TaskTimeoutMinutes+60) * time.Minute
	for range 2 {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprint(f, os.Getpid())
			f.Close()
			return func() { os.Remove(p) }
		}
		b, _ := os.ReadFile(p)
		pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		if st, serr := os.Stat(p); serr == nil && (time.Since(st.ModTime()) > stale || (pid > 0 && !processAlive(pid))) {
			os.Remove(p)
			continue
		}
		fmt.Printf("locked by pid %d (%s)\n", pid, p)
		return nil
	}
	return nil
}

func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid) // fails on Windows when the process is gone
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

var reRepoTag = regexp.MustCompile(`repo:([\w.-]+)`)

func repoAliases(tags string) []string {
	var out []string
	for _, m := range reRepoTag.FindAllStringSubmatch(tags, -1) {
		if !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	return out
}

type Next struct {
	Feature, Story, Task *Node
	Aliases              []string
}

// nextTask picks the first New task. Repos come from repo:<alias> tags on Task, then Story, then Feature.
func (c *Config) nextTask(d *DevOps) (*Next, error) {
	feats, err := openWork(d)
	if err != nil {
		return nil, err
	}
	for _, feat := range feats {
		for _, child := range feat.Children {
			type pair struct{ story, task *Node }
			var pairs []pair
			if child.Type == "Task" {
				pairs = append(pairs, pair{feat, child})
			} else {
				for _, t := range child.Children {
					if t.Type == "Task" {
						pairs = append(pairs, pair{child, t})
					}
				}
			}
			for _, p := range pairs {
				if p.task.State != "New" {
					continue
				}
				var aliases []string
				for _, n := range []*Node{p.task, p.story, feat} {
					if aliases = repoAliases(n.Tags); len(aliases) > 0 {
						break
					}
				}
				var unknown []string
				for _, a := range aliases {
					if _, ok := c.Repos[a]; !ok {
						unknown = append(unknown, a)
					}
				}
				if len(aliases) == 0 || len(unknown) > 0 {
					why := "mangler"
					if len(unknown) > 0 {
						why = "ukendt: " + strings.Join(unknown, ",")
					}
					fmt.Printf("skip #%d repo-tag %s\n", p.task.ID, why)
					continue
				}
				return &Next{feat, p.story, p.task, aliases}, nil
			}
		}
	}
	return nil, nil
}

func (c *Config) expandPrompt(file string, m map[string]string) string {
	b, _ := promptFS.ReadFile("prompts/" + file)
	text := string(b)
	for k, v := range m {
		text = strings.ReplaceAll(text, "{{"+k+"}}", v)
	}
	return text
}

func formatRepos(repos []RepoInfo) string {
	lines := make([]string, len(repos))
	for i, r := range repos {
		lines[i] = fmt.Sprintf("- %s: ./%s (base %s)", r.Alias, r.Alias, r.Base)
	}
	return strings.Join(lines, "\n")
}

func (c *Config) closeCommand() (cmd, toolPattern string) {
	if c.Docker.Enabled {
		return "nightshift close", "Bash(nightshift close*)"
	}
	exe, _ := os.Executable()
	base := strings.TrimSuffix(filepath.Base(exe), filepath.Ext(exe))
	return fmt.Sprintf("%q close", exe), fmt.Sprintf("Bash(*%s* close*)", base)
}

func (c *Config) agentsJSON() string {
	m := map[string]string{"simple": "haiku", "standard": "sonnet", "complex": "opus"}
	for k, v := range c.Models {
		if v != "" {
			m[k] = v
		}
	}
	type agent struct {
		Model       string `json:"model"`
		Description string `json:"description"`
		Prompt      string `json:"prompt"`
	}
	reply := " Reply with a few lines: files changed, anything unfinished."
	b, _ := json.Marshal(map[string]agent{
		"worker-simple":   {m["simple"], "Small/simple task: single-file edit, rename, config, docs, trivial fix.", "You implement one small, well-scoped change. Follow the instructions given." + reply},
		"worker-standard": {m["standard"], "Medium/large task of easy-to-complex difficulty: features, bugfixes, refactors across a few files.", "You implement one scoped change completely, incl. build/tests where present." + reply},
		"worker-complex":  {m["complex"], "Very large or complex task: architecture, cross-repo design, hard debugging, security-sensitive changes.", "You implement one hard change carefully, incl. build/tests where present." + reply},
		"verifier":        {m["complex"], "Reviews the finished diff against the task. Use once before the PR.", `Review the diff (git diff against base) versus the task. Check correctness, missed requirements, broken builds/tests, interface mismatches between repos. Do not edit files. Reply "OK" or a short list of concrete problems.`},
	})
	return string(b)
}

type RunResult struct {
	Result *Event
	Limit  *Event
}

func (c *Config) invokeClaude(prompt, cwd string, aliases []string, sessionArgs []string, logName string, taskID int) (*RunResult, error) {
	args := []string{"-p", "--output-format", "stream-json", "--verbose"}
	if c.Docker.Enabled && c.Docker.allowAll() {
		args = append(args, "--dangerously-skip-permissions", "--agents", c.agentsJSON())
	} else {
		_, closeTool := c.closeCommand()
		tools := []string{"Agent", "Read", "Edit", "Write", "Glob", "Grep", "Bash(git *)", "Bash(gh *)", closeTool}
		for _, a := range aliases {
			tools = append(tools, c.Repos[a].AllowedTools...)
		}
		agents := c.agentsJSON() // inline: a host file path would not exist inside the container
		if !c.Docker.Enabled {
			agents = c.dataPath("agents.json")
			os.WriteFile(agents, []byte(c.agentsJSON()), 0o644)
		}
		args = append(args, "--permission-mode", "acceptEdits", "--permission-prompts", "none",
			"--allowedTools", strings.Join(tools, ","), "--agents", agents)
	}
	args = append(args, sessionArgs...)
	model := c.Model
	for _, a := range aliases {
		if m := c.Repos[a].Model; m != "" {
			model = m
			break
		}
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	lines, err := c.runClaude(RunSpec{Args: args, Stdin: prompt, Cwd: cwd, Name: fmt.Sprintf("task%d", taskID), Workspace: true})
	writeLog(c, logName, lines)
	evs := parseEvents(lines)
	return &RunResult{Result: lastEvent(evs, "result"), Limit: lastEvent(evs, "rate_limit_event")}, err
}

func isLimitHit(r *Event) bool {
	if r == nil || !r.IsError {
		return false
	}
	return fmt.Sprint(r.APIError) == "usage_limit_reached" || fmt.Sprint(r.APIErrorStatus) == "429"
}

type runOutcome struct {
	Continue bool
	Message  string
}

func (c *Config) completeRun(d *DevOps, st *State, run *RunResult, runErr error) runOutcome {
	now := time.Now().Unix()
	if isLimitHit(run.Result) {
		reset := now + 900
		if run.Limit != nil && run.Limit.RateLimitInfo.ResetsAt > 0 {
			reset = int64(run.Limit.RateLimitInfo.ResetsAt)
		}
		st.Status, st.ResumeAfter = "paused", reset+60
		c.saveState(st)
		return runOutcome{false, fmt.Sprintf("paused #%d until %s", st.TaskID, time.Unix(st.ResumeAfter, 0).Format("15:04"))}
	}
	if run.Result == nil || run.Result.IsError {
		st.Attempts++
		if st.Attempts >= 2 {
			why := ""
			if run.Result != nil {
				why = run.Result.resultText()
			} else if runErr != nil {
				why = runErr.Error()
			}
			d.comment(st.TaskID, fmt.Sprintf("Worker fejlede (%s). Se logs.", why))
			c.clearState()
			return runOutcome{true, fmt.Sprintf("failed #%d", st.TaskID)}
		}
		st.Status, st.ResumeAfter = "paused", now+900
		c.saveState(st)
		return runOutcome{false, fmt.Sprintf("retry #%d", st.TaskID)}
	}
	s, _ := d.itemState(st.TaskID)
	c.clearState()
	if s.Category == "Completed" {
		return runOutcome{true, fmt.Sprintf("done #%d", st.TaskID)}
	}
	return runOutcome{true, fmt.Sprintf("needs-attention #%d", st.TaskID)}
}

func (c *Config) usageGate() (string, error) {
	u, err := getUsage(c)
	if err != nil {
		return "", err
	}
	s, ok1 := u.get("Current session")
	w, ok2 := u.get("Current week (all models)")
	switch {
	case !ok1 || !ok2:
		return "wait usage unreadable", nil
	case s >= c.Limits.Session:
		return fmt.Sprintf("wait session %d%%", int(s*100)), nil
	case w >= c.Limits.Week:
		return fmt.Sprintf("wait week %d%%", int(w*100)), nil
	}
	return "", nil
}

// runWorker is one scheduled run: resumes a paused task, otherwise takes the next New task, until idle or limited.
func (c *Config) runWorker(d *DevOps, dryRun bool) error {
	release := c.acquireLock()
	if release == nil {
		return nil
	}
	defer release()

	st := c.loadState()
	for started := 0; started < c.Limits.MaxTasksPerRun; started++ {
		resume := st != nil
		var next *Next
		if resume && st.Status == "paused" && time.Now().Unix() < st.ResumeAfter {
			fmt.Printf("paused #%d\n", st.TaskID)
			return nil
		}
		if !resume {
			var err error
			if next, err = c.nextTask(d); err != nil {
				return err
			}
			if next == nil {
				fmt.Println("idle")
				c.cleanup(d, false)
				return nil
			}
		}
		msg, err := c.usageGate()
		if err != nil {
			return err
		}
		if msg != "" {
			fmt.Println(msg)
			return nil
		}
		if dryRun {
			if resume {
				fmt.Printf("would resume #%d\n", st.TaskID)
			} else {
				fmt.Printf("would start #%d repos: %s\n", next.Task.ID, strings.Join(next.Aliases, ","))
			}
			return nil
		}

		closeCmd, _ := c.closeCommand()
		var run *RunResult
		var runErr error
		logName := func() string { return fmt.Sprintf("task%d-%d.jsonl", st.TaskID, time.Now().Unix()) }
		if !resume {
			ws, err := c.initWorkspace(next.Story, next.Aliases)
			if err != nil {
				return err
			}
			inprog, _ := d.stateName("Task", "InProgress")
			if inprog != "" {
				if err := d.setState(next.Task.ID, inprog); err != nil {
					return err
				}
			}
			st = &State{TaskID: next.Task.ID, TaskTitle: next.Task.Title, Branch: ws.Branch, WorktreeRoot: ws.Root,
				Aliases: next.Aliases, Repos: ws.Repos, SessionID: newUUID(), Status: "running"}
			c.saveState(st)
			prompt := c.expandPrompt("prompt.md", map[string]string{
				"taskId": fmt.Sprint(next.Task.ID), "taskTitle": next.Task.Title,
				"taskBody": strings.TrimSpace(next.Task.Description + "\n" + next.Task.AcceptanceCriteria),
				"usId":     fmt.Sprint(next.Story.ID), "usTitle": next.Story.Title, "usBody": next.Story.Description,
				"featId": fmt.Sprint(next.Feature.ID), "featTitle": next.Feature.Title,
				"branch": ws.Branch, "repos": formatRepos(ws.Repos), "close": closeCmd,
			})
			fmt.Printf("start #%d\n", st.TaskID)
			run, runErr = c.invokeClaude(prompt, ws.Root, st.Aliases, []string{"--session-id", st.SessionID}, logName(), st.TaskID)
		} else {
			st.Status = "running"
			c.saveState(st)
			prompt := c.expandPrompt("prompt-resume.md", map[string]string{
				"taskId": fmt.Sprint(st.TaskID), "taskTitle": st.TaskTitle, "branch": st.Branch,
				"repos": formatRepos(st.Repos), "close": closeCmd,
			})
			fmt.Printf("resume #%d\n", st.TaskID)
			run, runErr = c.invokeClaude(prompt, st.WorktreeRoot, st.Aliases, []string{"--resume", st.SessionID}, logName(), st.TaskID)
		}
		out := c.completeRun(d, st, run, runErr)
		fmt.Println(out.Message)
		if !out.Continue {
			return nil
		}
		st = nil
	}
	return nil
}

