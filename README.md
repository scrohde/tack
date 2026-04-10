# tack

`tack` is a tiny local issue management tool. It installs a skill for agents to use.

`tack` makes it easy to break a large plan into a well-structured graph of issues with clear scope, dependencies, and labels. It is designed to be used by agents, but it is also a nice way for humans to track work without leaving the terminal.

Instead of implementing a whole plan all at once, break it down into tasks and have one or more agents pick up ready tasks to work on individually with a fresh context window.

## Typical Human Workflow

Install `tack`, go to the repository you care about, and initialize it once:

```bash
tack init
```

This creates a `.tack` directory to hold the issue database and config, and it creates an `.agents/skills/tack` directory with the skill definition.

Then use an agent in planning mode to build a plan (or however you like to build a plan). Once the plan is solid, ask your agent to break the plan down into issues with:

> Use tack to create tasks from this plan.

After that, you can start a fresh implementation-focused session and say something like:

> Implement the next ready task and commit the changes.

You can get creative here - make use of sub-agents or whatever.

That keeps the execution agent narrow, focused, and much easier to review.

## Features That Matter For Agent Work

- `tack import --file manifest.json` can create a whole plan worth of issues in one pass
- `tack ready` hides parent issues that still have open children
- `tack list --json --summary` and `tack ready --json --summary` give compact automation-friendly output
- comments, labels, and dependency links make handoffs much cleaner
- read-heavy tack commands can safely overlap across separate processes

Issue IDs look like `tk-1`, `tk-2`, and so on.

## Getting Started

Requirements:

- Go 1.26 or newer
- a Git repository
- `$EDITOR` if you want interactive editing

Build it:

```bash
go build -o tack ./cmd/tack
```

Or install it:

```bash
go install ./cmd/tack
```

Run it anywhere inside the repo:

```bash
tack help
tack init
tack tui
```

Initialization creates repo-local state:

- `.tack/issues.db`
- `.tack/config.json`
- `.tack/.gitignore`
- `.agents/skills/tack/SKILL.md`
- `.agents/.gitignore` when `tack` creates `.agents/`

`tack init` keeps its ignore rules inside `.tack/`, and also adds `.agents/.gitignore` when it creates `.agents/`, so they don't automatically get added to your commits.

If you ever want to remove tack cleanly, delete `.tack` and `.agents/skills/tack`.

## Skill Install Locations

The install target is explicit:

- `tack skill install` installs to `<repo>/.agents/skills/tack`
- `tack skill install --home` installs to `$HOME/.agents/skills/tack`
- `tack skill install --path /tmp/skills` installs to `/tmp/skills/tack`

`tack init` performs the repo-local install automatically. Use `tack skill install` when you want to reinstall it explicitly or target `--home` or `--path`.

## Commands

```text
tack help
tack init
tack create
tack import --file <path> [--json]
tack show <id>
tack list
tack ready
tack update <id>
tack edit <id>
tack close <id>
tack reopen <id>
tack comment add|list
tack dep add|remove|list
tack skill install [--home|--path <dir>]
tack labels add|remove|list
tack export [--json] [--jira <epic-id>]
```

Useful examples:

```bash
tack list --json --summary
tack ready --json --summary
tack list --status open --type bug --label backend --limit 20
tack ready --assignee alice --json
tack export --jira tk-123
tack help ready
tack ready --help
```

`--summary` requires `--json`.

## Manifest Import

If the agent already has a finished plan, importing a manifest is the fastest way to create a real dependency graph:

```json
{
  "issues": [
    {
      "id": "epic",
      "title": "Ship the feature",
      "type": "epic",
      "priority": "high"
    },
    {
      "id": "backend",
      "title": "Build the API work",
      "parent": "epic",
      "depends_on": ["tests"],
      "labels": ["backend"]
    },
    {
      "id": "tests",
      "title": "Add regression coverage",
      "type": "task"
    }
  ]
}
```

Run it with:

```bash
tack import --file manifest.json
tack import --file manifest.json --json
```

The manifest-local `id`, `parent`, and `depends_on` values are aliases used to wire the graph together. Omitted `type` values default to `task`, and omitted `priority` values default to `medium`.

## Actor Resolution

Write operations need an actor name. `tack` resolves it in this order:

1. `--actor`
2. `TACK_ACTOR`
3. `.tack/config.json`
4. `git config user.name`
5. the current OS username

## Development

Run the tests with:

```bash
go test ./...
```
