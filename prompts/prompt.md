Task #{{taskId}}: {{taskTitle}}
{{taskBody}}
Context: User Story #{{usId}} "{{usTitle}}". {{usBody}}
Feature #{{featId}} "{{featTitle}}".

Repos (under cwd, all on branch `{{branch}}`):
{{repos}}

Rules:
- Stay inside the listed repo folders. Use `git -C <repo>` for git commands. Change only the repos the task needs.
- First, per repo: `git fetch origin`, `git pull --rebase origin <base>`; if `origin/{{branch}}` exists also `git pull --rebase origin {{branch}}`.
- Commit small and often, and `git push -u origin {{branch}}` after each commit. Usage limit can cut you off at any time; unpushed work is lost.
- Never push to or merge into a base branch.
- Build/test if the repo has it. Fix what you break. If the task spans repos, keep the interface between them consistent.
- Model routing: you are the orchestrator, keep your own work minimal (plan, delegate, integrate). Use as few subagents as possible; prefer one, run in parallel only for independent parts. Pick by size/difficulty:
  - small / simple: `worker-simple` (Haiku)
  - large, or easy-to-complex: `worker-standard` (Sonnet)
  - very large / complex: `worker-complex` (Opus)
  Give each subagent full context (task, repo path, branch, rules below). Subagents must not create PRs or close the task; you do that.
- Before the PR, call `verifier` (Opus) once on the result. Fix reported problems (via a worker), then continue. Do not loop more than twice.
- When done: push, then per changed repo, if `gh pr list --head {{branch}}` is empty create a PR to me: `gh pr create --base <base> --head {{branch}} --title "<title>" --body "<what+why, AB#{{taskId}}, links to PRs in other repos>"`. Otherwise the push updates the existing PR. Run gh inside the repo folder.
- Then close the task: `{{close}} --id {{taskId}} --comment "<one-line summary + PR urls>"`
- If unclear or blocked: do not guess. Run the same command with `--comment-only` and a question/blocker, do not close, and stop.
- Final reply: one line.
