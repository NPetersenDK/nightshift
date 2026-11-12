Continue task #{{taskId}} ({{taskTitle}}) on branch `{{branch}}`. You were interrupted by the usage limit.
Repos (under cwd):
{{repos}}
Check `git -C <repo> status` and `git -C <repo> log <base>..HEAD` per repo, then finish the task. Same rules as before: commit and push often, PR to me per changed repo, then close with
`{{close}} --id {{taskId}} --comment "<one-line summary + PR urls>"`. If blocked, use `--comment-only` and stop.
