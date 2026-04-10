# tack

A local issue tracker for Git repositories. Designed for agents, friendly for humans.

`tack` makes it easy to break a large plan into a well-structured graph of issues with clear scope, dependencies, and labels. Instead of implementing a whole plan all at once, break it down into tasks and have one or more agents pick up ready tasks to work on individually with a fresh context window.

## Getting Started

Requirements:

- Go 1.26 or newer
- a Git repository
- `$EDITOR` if you want interactive editing

Build and install:

```bash
go build -o tack ./cmd/tack
# or install into $GOPATH/bin:
go install ./cmd/tack
```

Initialize inside any Git repo:

```bash
tack init
```

This creates:

- `.tack/issues.db` — the issue database
- `.tack/config.json` — local config
- `.tack/.gitignore` — keeps database out of commits
- `.agents/skills/tack/SKILL.md` — skill definition for agents
- `.agents/.gitignore` — when `tack` creates `.agents/`

To remove tack cleanly, delete `.tack` and `.agents/skills/tack`.

## Typical Workflow

Use an agent in planning mode to build a plan (or however you like to build a plan). Once the plan is solid, ask your agent to break the plan down into issues:

> Use tack to create tasks from this plan.

Then start a fresh implementation-focused session:

> Implement the next ready task and commit the changes.

That keeps the execution agent narrow, focused, and much easier to review.

## Features That Matter For Agent Work

- `tack import --file manifest.json` creates a whole plan worth of issues in one pass
- `tack ready` surfaces only actionable work — parent issues stay hidden until all children are closed
- `tack list --json --summary` and `tack ready --json --summary` give compact automation-friendly output
- comments, labels, and dependency links make handoffs between agents cleaner
- read-heavy commands can safely overlap across separate processes
- every mutation command supports `--json` for structured output

Issue IDs look like `tk-1`, `tk-2`, and so on.

## TUI

`tack tui` opens an interactive terminal UI for browsing, filtering, and inspecting issues.

```bash
tack tui
tack tui --ready              # start in the ready view
tack tui --label backend      # pre-filter by label
```

Key bindings: `j`/`k` or arrows to navigate, `enter` to inspect, `r` to toggle list/ready views, `tab` to switch panes, `/` to filter, `g` for the issue graph, `G` for the project graph, `?` for help, `q` to quit.

## Commands

```text
tack init [--json]
tack create [flags]
tack import --file <path> [--json]
tack show <id> [--json]
tack tui [flags]
tack list [flags]
tack ready [flags]
tack update <id> [flags]
tack edit <id>
tack close <id> [--reason ...]
tack reopen <id> [--reason ...]
tack comment add|list
tack dep add|remove|list
tack labels add|remove|list
tack skill install [--home|--path <dir>] [--json]
tack export [--json] [--jira <epic-id>]
```

Useful examples:

```bash
tack list --json --summary
tack ready --json --summary
tack list --status open --type bug --label backend --limit 20
tack list --assignee alice --json
tack export --jira tk-123
tack help ready
```

`--summary` requires `--json`.

## Skill Install Locations

`tack init` installs the agent skill automatically. Use `tack skill install` to reinstall or target a different location:

- `tack skill install` → `<repo>/.agents/skills/tack`
- `tack skill install --home` → `$HOME/.agents/skills/tack`
- `tack skill install --path /tmp/skills` → `/tmp/skills/tack`

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

Run the tests:

```bash
go test ./...
```

Lint and format:

```bash
mkdir -p /tmp/go-build-cache /tmp/golangci-lint-cache
GOCACHE=/tmp/go-build-cache GOLANGCI_LINT_CACHE=/tmp/golangci-lint-cache golangci-lint fmt
GOCACHE=/tmp/go-build-cache GOLANGCI_LINT_CACHE=/tmp/golangci-lint-cache golangci-lint run --fix
```

If you change `.agents/skills/tack/SKILL.md`, regenerate the embedded skill file:

```bash
go generate ./internal/skill
```
