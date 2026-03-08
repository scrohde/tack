---
name: tack
description: Use when working with the local `tack` issue database in a git repository, including initializing `.tack/issues.db`, selecting ready work, claiming tasks, creating follow-up tasks, managing dependencies, adding implementation notes, and closing completed work.
---

# tack agent workflow

Use this skill when you need to work against the local `tack` database inside a git repository.

## Workflow
1. Discover the repo root and initialize tack if `.tack/issues.db` does not exist:
   `tack init`
2. Pull the current ready queue for automation-friendly selection:
   `tack ready --json`
3. Claim work before changing it:
   `tack update <id> --claim --json`
4. When you uncover follow-up work, create a linked task and attach dependencies or a parent:
   `tack create --title "..." --type task --priority medium --parent <id> --depends-on <id> --json`
5. Close completed work with an explicit reason:
   `tack close <id> --reason "..." --json`

## Notes
- Prefer `--json` for read and mutation commands when another tool will consume the output.
- Use `tack comment add <id> --body-file ... --json` for substantial implementation notes.
- Use `tack dep add <blocked-id> <blocker-id>` to keep ready-work filtering accurate.
