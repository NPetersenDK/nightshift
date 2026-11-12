package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type RepoInfo struct {
	Alias string `json:"alias"`
	Base  string `json:"base"`
}

type Workspace struct {
	Branch string
	Root   string
	Repos  []RepoInfo
}

func git(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		return s, fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, s)
	}
	return s, nil
}

var reNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	s = strings.ToLower(s)
	s = strings.NewReplacer("æ", "ae", "ø", "oe", "å", "aa").Replace(s)
	s = strings.Trim(reNonAlnum.ReplaceAllString(s, "-"), "-")
	if len(s) > 40 {
		s = s[:40]
	}
	return strings.Trim(s, "-")
}

func baseBranch(dir string) string {
	if b, err := git(dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil && b != "" {
		return strings.TrimPrefix(b, "origin/")
	}
	return "main"
}

func remoteHasBranch(dir, branch string) bool {
	_, err := git(dir, "ls-remote", "--exit-code", "--heads", "origin", branch)
	return err == nil
}

// initWorkspace prepares one folder per repo for a User Story, all on the same feature branch.
// Local mode uses git worktrees off the configured repo path; docker mode uses self-contained
// clones (worktree metadata would point at host paths that don't exist in the container).
func (c *Config) initWorkspace(story *Node, aliases []string) (*Workspace, error) {
	ws := &Workspace{
		Branch: fmt.Sprintf("feature/us%d-%s", story.ID, slug(story.Title)),
		Root:   filepath.Join(c.worktreesRoot(), fmt.Sprintf("us%d", story.ID)),
	}
	if err := os.MkdirAll(ws.Root, 0o755); err != nil {
		return nil, err
	}
	for _, alias := range aliases {
		r := c.Repos[alias]
		dest := filepath.Join(ws.Root, alias)
		var base string
		if c.Docker.Enabled {
			fresh := false
			if _, err := os.Stat(dest); err != nil {
				if out, err := exec.Command("git", "clone", r.URL, dest).CombinedOutput(); err != nil {
					return nil, fmt.Errorf("git clone %s: %s", alias, strings.TrimSpace(string(out)))
				}
				fresh = true
			}
			if _, err := git(dest, "fetch", "origin", "--prune"); err != nil {
				return nil, err
			}
			base = baseBranch(dest)
			if fresh {
				var err error
				if remoteHasBranch(dest, ws.Branch) {
					_, err = git(dest, "checkout", "-B", ws.Branch, "origin/"+ws.Branch)
				} else {
					_, err = git(dest, "checkout", "-b", ws.Branch, "origin/"+base)
				}
				if err != nil {
					return nil, err
				}
			}
		} else {
			if _, err := os.Stat(r.Path); err != nil {
				if out, err := exec.Command("git", "clone", r.URL, r.Path).CombinedOutput(); err != nil {
					return nil, fmt.Errorf("git clone %s: %s", alias, strings.TrimSpace(string(out)))
				}
			}
			if _, err := git(r.Path, "fetch", "origin", "--prune"); err != nil {
				return nil, err
			}
			base = baseBranch(r.Path)
			if _, err := os.Stat(dest); err != nil {
				var err error
				if remoteHasBranch(r.Path, ws.Branch) {
					_, err = git(r.Path, "worktree", "add", "-B", ws.Branch, dest, "origin/"+ws.Branch)
				} else {
					_, err = git(r.Path, "worktree", "add", "-b", ws.Branch, dest, "origin/"+base)
				}
				if err != nil {
					return nil, err
				}
			}
		}
		ws.Repos = append(ws.Repos, RepoInfo{alias, base})
	}
	return ws, nil
}
