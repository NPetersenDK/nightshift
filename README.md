# nightshift

Runs [Claude Code](https://claude.com/claude-code) on your Azure DevOps tasks while you sleep. A scheduler calls `nightshift run` every few minutes. It checks your Claude subscription usage (5-hour session and weekly limits). If there is headroom, it picks the next `New` Task from your configured Features, lets Claude solve it on a feature branch, opens a PR and closes the task with a comment. If the limit is hit mid-task, it sleeps until the reset and resumes the same session.

## Quick start

1. **Install** (Linux/macOS: amd64+arm64, Windows: amd64+arm64)
   ```sh
   curl -fsSL https://raw.githubusercontent.com/NPetersenDK/nightshift/main/install.sh | sh
   ```
   ```powershell
   irm https://raw.githubusercontent.com/NPetersenDK/nightshift/main/install.ps1 | iex
   ```
2. **Create a config** in an empty folder: `nightshift init`, then edit `env.json` (DevOps org/project/team, repos, Features).
3. **Log in Claude**: run `claude setup-token`, put the token in `claude-token.txt` next to `env.json`.
4. **GitHub** (for PRs): put a fine-grained token in `gh-token.txt` (or set `GH_TOKEN`).
5. **Check**: `nightshift usage`, `nightshift work`, `nightshift run --dry-run`.
6. **Schedule**: `nightshift install` (Task Scheduler on Windows, prints a cron line elsewhere).

With `docker.enabled: true` (default in the starter config) the only requirement on the host is Docker. Claude runs inside the `ghcr.io/npetersendk/nightshift-agent` image, which contains Claude Code, git, gh, Node, .NET and Go. Set it to `false` to run `claude` directly on the host.

## How work is found

- `projects[].features` lists the Features nightshift works on. `nightshift sync` creates them (tagged `keep-open; repo:<alias>`, in the team's area path) and keeps them open. Other Features are ignored.
- Put User Stories and Tasks under those Features. Repos come from `repo:<alias>` tags on the Task, Story or Feature (nearest wins) and are mapped in `repos`.
- One branch `feature/us<id>-<slug>` per User Story, same name in every repo. Tasks in a story share it.
- When all Tasks in a Story are closed, the Story closes. Features tagged `keep-open` never close.
- `projects[].team` limits work to that DevOps team's area paths.

## Models

The orchestrator runs on `model`. It delegates to subagents: Haiku for small tasks, Sonnet for large/easy-to-complex, Opus for very large/complex. Opus also reviews the diff once before the PR. Override with `models` in `env.json`.

## Commands

```
nightshift [run] [--dry-run]   sync features, then work tasks
nightshift sync | usage | work | cleanup
nightshift close --id N --comment "..." [--comment-only] [--no-cascade]
nightshift install [--every 10]
nightshift init | version
```

Config is read from `$NIGHTSHIFT_CONFIG`, `./env.json`, or next to the binary. State and logs live in `data/` beside it.

## Security

- `env.json`, `claude-token.txt` and `gh-token.txt` are secrets: keep them out of git (already in `.gitignore`) and readable only by the scheduler user. Prefer env vars (`AZDO_PAT`, `GH_TOKEN`, `CLAUDE_CODE_OAUTH_TOKEN`) on servers.
- Work item text and repo contents are prompt-injection surface. In docker mode Claude runs with `--dangerously-skip-permissions` inside the container: use narrowly scoped tokens and protect your base branches.
- In docker mode the DevOps PAT is passed to the container for `nightshift close`. Use a PAT limited to Work Items.

## Build / release

`go build .` for a local binary. Pushing a `v*` tag triggers GitHub Actions: binaries via GoReleaser and a multi-arch image on ghcr.io.
