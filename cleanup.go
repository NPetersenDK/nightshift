package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// cleanup removes old logs and workspaces of closed User Stories (only if clean and fully pushed).
// Runs at most once per 24h unless force.
func (c *Config) cleanup(d *DevOps, force bool) {
	stamp := c.dataPath("cleanup.stamp")
	if st, err := os.Stat(stamp); !force && err == nil && time.Since(st.ModTime()) < 24*time.Hour {
		return
	}
	os.WriteFile(stamp, []byte(time.Now().Format(time.RFC3339)), 0o644)

	logDir := c.dataPath("logs")
	if entries, err := os.ReadDir(logDir); err == nil {
		cutoff := time.Now().AddDate(0, 0, -c.LogRetentionDays)
		for _, e := range entries {
			if info, err := e.Info(); err == nil && !e.IsDir() && info.ModTime().Before(cutoff) {
				os.Remove(filepath.Join(logDir, e.Name()))
			}
		}
	}

	st := c.loadState()
	root := c.worktreesRoot()
	dirs, _ := filepath.Glob(filepath.Join(root, "us*"))
	for _, dir := range dirs {
		if st != nil && filepath.Clean(st.WorktreeRoot) == filepath.Clean(dir) {
			continue
		}
		var id int
		if _, err := fmt.Sscanf(filepath.Base(dir), "us%d", &id); err != nil {
			continue
		}
		item, err := d.itemState(id)
		if err != nil || (item.Category != "Completed" && item.Category != "Removed") {
			continue
		}
		repos, _ := os.ReadDir(dir)
		clean := true
		for _, r := range repos {
			if r.IsDir() && !repoClean(filepath.Join(dir, r.Name())) {
				clean = false
				break
			}
		}
		if !clean {
			continue
		}
		for _, r := range repos {
			p := filepath.Join(dir, r.Name())
			if fi, err := os.Stat(filepath.Join(p, ".git")); err == nil && !fi.IsDir() {
				git(p, "worktree", "remove", "--force", p) // linked worktree; clones are just deleted below
			}
		}
		os.RemoveAll(dir)
	}
	for _, r := range c.Repos {
		if _, err := os.Stat(r.Path); err == nil {
			git(r.Path, "worktree", "prune")
		}
	}
}

func repoClean(dir string) bool {
	if out, err := git(dir, "status", "--porcelain"); err != nil || out != "" {
		return false
	}
	out, err := git(dir, "rev-list", "--count", "@{u}..HEAD")
	return err == nil && strings.TrimSpace(out) == "0"
}
