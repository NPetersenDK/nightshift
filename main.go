package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

const usageText = `nightshift - runs Claude Code on Azure DevOps tasks when subscription usage allows

  nightshift [run] [--dry-run]   sync features, then work tasks (default; run from cron/Task Scheduler)
  nightshift sync                create/update the Features from config
  nightshift usage [--json]      show Claude usage windows
  nightshift work [--json]       list open work (configured Features only)
  nightshift cleanup             remove old logs and finished workspaces
  nightshift close --id N --comment "..." [--state S] [--comment-only] [--no-cascade]
  nightshift install [--every 10]  schedule "run" (Task Scheduler on Windows, prints cron line elsewhere)
  nightshift init                create a starter env.json in the current folder
  nightshift version

Config: $NIGHTSHIFT_CONFIG, ./env.json, or env.json next to the binary.`

func main() {
	cmd := "run"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	if cmd == "help" || (len(args) > 0 && (args[0] == "-h" || args[0] == "-help" || args[0] == "--help")) {
		fmt.Println(usageText)
		return
	}
	if err := execute(cmd, args); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func execute(cmd string, args []string) error {
	switch cmd {
	case "version":
		fmt.Println(version)
		return nil
	case "init":
		return initConfig()
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "")
	asJSON := fs.Bool("json", false, "")
	id := fs.Int("id", 0, "")
	comment := fs.String("comment", "", "")
	state := fs.String("state", "", "")
	commentOnly := fs.Bool("comment-only", false, "")
	noCascade := fs.Bool("no-cascade", false, "")
	every := fs.Int("every", 10, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	t, err := cfg.target()
	if err != nil {
		return err
	}
	d := newDevOps(t)

	switch cmd {
	case "run":
		lines, err := syncFeatures(d)
		for _, l := range lines {
			fmt.Println(l)
		}
		if err != nil {
			return err
		}
		return cfg.runWorker(d, *dry)
	case "sync":
		lines, err := syncFeatures(d)
		for _, l := range lines {
			fmt.Println(l)
		}
		return err
	case "usage":
		u, err := getUsage(cfg)
		if err != nil {
			return err
		}
		if *asJSON {
			fmt.Println(mustJSON(u))
		} else {
			fmt.Println(u.text())
		}
	case "work":
		roots, err := openWork(d)
		if err != nil {
			return err
		}
		if *asJSON {
			if roots == nil {
				roots = []*Node{}
			}
			fmt.Println(workJSON(roots))
		} else if txt := workText(roots); txt != "" {
			fmt.Println(txt)
		}
	case "cleanup":
		cfg.cleanup(d, true)
	case "close":
		if *id == 0 || *comment == "" {
			return fmt.Errorf("close needs --id and --comment")
		}
		lines, err := closeItem(d, CloseOpts{ID: *id, Comment: *comment, State: *state, CommentOnly: *commentOnly, NoCascade: *noCascade})
		for _, l := range lines {
			fmt.Println(l)
		}
		return err
	case "install":
		return install(*every)
	default:
		return fmt.Errorf("unknown command %q (try: nightshift help)", cmd)
	}
	return nil
}

var version = "dev" // set by release build via -ldflags

//go:embed env.example.json
var exampleConfig []byte

func initConfig() error {
	const name = "env.json"
	if _, err := os.Stat(name); err == nil {
		return fmt.Errorf("%s already exists", name)
	}
	if err := os.WriteFile(name, exampleConfig, 0o600); err != nil {
		return err
	}
	fmt.Println(`created env.json. Next:
  1. edit env.json: your DevOps org/project/team, repos and the Features to work on
  2. run "claude setup-token" and save the token in claude-token.txt (next to env.json)
  3. optional: gh-token.txt with a GitHub token (needed for PRs in docker mode)
  4. nightshift usage   (check the setup), then nightshift run --dry-run
  5. nightshift install (schedule it)`)
	return nil
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func install(every int) error {
	exe, _ := os.Executable()
	if runtime.GOOS == "windows" {
		out, err := exec.Command("schtasks", "/Create", "/F", "/TN", "Nightshift", "/SC", "MINUTE", "/MO", fmt.Sprint(every),
			"/TR", fmt.Sprintf(`"%s" run`, exe)).CombinedOutput()
		if err != nil {
			return fmt.Errorf("schtasks: %s", strings.TrimSpace(string(out)))
		}
		fmt.Printf("installed every %dm\n", every)
		return nil
	}
	fmt.Printf("*/%d * * * * flock -n /tmp/nightshift.lock %s run >> /var/log/nightshift.log 2>&1\n", every, exe)
	return nil
}
