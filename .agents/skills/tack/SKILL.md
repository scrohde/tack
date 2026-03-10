---
name: tack
description: Use when working with the local `tack` issue database in a git repository, including initializing `.tack/issues.db`, selecting ready work, claiming tasks, creating follow-up tasks, managing dependencies, adding implementation notes, and closing completed work.
---

# tack agent workflow

Use this skill when you need to work against the local `tack` database inside a git repository.

## Workflow
1. Pull the current actionable queue in compact form first:
   `tack ready --json --summary`
2. Claim work before changing it:
   `tack update <id> --claim --json`
3. If you need the full body for a specific issue, fetch it directly:
   `tack show <id> --json`
4. When you need to turn a plan into multiple linked issues, prefer a single manifest import:
   `tack import --file plan.json --json`
5. When you uncover follow-up work during implementation, create a linked task and attach dependencies or a parent:
   `tack create --title "..." --type task --priority medium --parent <id> --depends-on <id> --json`
6. Close completed work with an explicit reason:
   `tack close <id> --reason "..." --json`

## Notes
- Prefer `--json` for read and mutation commands when another tool will consume the output.
- Prefer `tack ready --json --summary` and `tack list --json --summary` for automation. Summary output omits large bodies and includes the fields agents usually need to decide what to do next: `id`, `title`, `status`, `type`, `priority`, `assignee`, `parent_id`, `labels`, `blocked_by`, and `open_children`.
- Parent issues are non-actionable by default while they still have open children. They stay visible in `tack list`, `tack show`, and `tack export --json`, but they do not appear in `tack ready` until all children are closed.
- Use `tack import --file <path>` when converting a dev plan or checklist into issues. Import manifests support per-issue alias `id`, `title`, `description`, `type`, `priority`, `labels`, `parent`, and `depends_on`, so you can create the graph in one pass.
- Minimal import manifest example:
  ```json
  {
    "issues": [
      {
        "id": "epic",
        "title": "Imported epic",
        "type": "epic",
        "priority": "high",
        "description": "parent"
      },
      {
        "id": "task",
        "title": "Imported task",
        "parent": "epic",
        "depends_on": ["bug"],
        "labels": ["backend"]
      },
      {
        "id": "bug",
        "title": "Imported bug",
        "type": "bug",
        "priority": "urgent"
      }
    ]
  }
  ```
- Use `tack comment add <id> --body-file ... --json` for substantial implementation notes.
- Use `tack dep add <blocked-id> <blocker-id>` to keep ready-work filtering accurate.
- Subcommand help works from either form: `tack <command> --help` or `tack help <command>`.
- Tack configures SQLite with WAL mode and a busy timeout, so separate read-oriented tack commands can safely overlap when automation opens multiple processes.
