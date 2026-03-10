# tack

`tack` is a tiny local issue tracker for people who like working with coding agents without losing the plot.

Instead of keeping the whole plan in one giant chat, you turn the plan into durable repo-local tasks, complete with parent/child structure, dependencies, notes, and history. The human decides what should happen. The agent helps plan it, break it apart, and execute it one ready task at a time.

## What It Feels Like

The usual human workflow looks like this:

1. Ask an agent to help plan a project or feature.
2. Review the plan until it actually makes sense.
3. Ask the agent to convert that plan into tacks without dropping details.
4. Let the agent work from the ready queue in small, reviewable chunks.

That middle step is the important one. You do not want a vague todo list. You want implementation-ready tasks with:

- clear scope
- parent-child relationships
- blockers and dependencies
- labels and notes
- enough detail that a fresh agent session can pick up the work cleanly

In other words: plan once, decompose carefully, execute calmly.

## Why Use It

`tack` is useful when you want:

- repo-local task tracking instead of another hosted tool
- a clean handoff point between planning mode and execution mode
- agents to work in the right order instead of freestyle improvisation
- an audit trail of what changed, what got blocked, and what got closed

Everything lives in the repository at `.tack/issues.db`, so the work stays close to the code.

## Typical Human Workflow

Install `tack`, go to the repository you care about, and initialize it once:

```bash
tack init
```

If you want the repo-local agent instructions installed too:

```bash
tack skill install
```

Then use an agent in planning mode to build the actual project plan. Once the plan is solid, ask for the conversion step explicitly. A good prompt is:

> Create tack issues from this plan. Do not lose details. Preserve scope, acceptance criteria, parent-child relationships, and dependencies so the work can be executed in the right order by a later agent session.

After that, you can start a fresh implementation-focused session and say:

> Implement 1 or 2 ready issues from tack.

That keeps the execution agent narrow, focused, and much easier to review.

## A Simple Loop

```bash
tack ready --json --summary
tack show <id> --json
tack update <id> --claim --json
```

That is the core loop:

- ask tack what is ready
- inspect the exact issue to work on
- claim it before changing things

When the agent discovers new work, it can create follow-up tacks with the right parent and dependency links instead of burying that context in chat.

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
```

Initialization creates only repo-local state:

- `.tack/issues.db`
- `.tack/config.json`
- `.tack/.gitignore`

`tack init` keeps its ignore rules inside `.tack/`, so you do not need to edit the repo root `.gitignore`.

If you install the tack skill into the repo, tack also creates `.agents/.gitignore` so local agent instructions stay out of version control too.

If you ever want to remove tack cleanly, delete `.tack` and `.agents/skills/tack`.

## Skill Install Locations

The install target is explicit:

- `tack skill install` installs to `<repo>/.agents/skills/tack`
- `tack skill install --home` installs to `$HOME/.agents/skills/tack`
- `tack skill install --path /tmp/skills` installs to `/tmp/skills/tack`

This is separate from `tack init` on purpose.

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
tack export --json
```

Useful examples:

```bash
tack list --json --summary
tack ready --json --summary
tack list --status open --type bug --label backend --limit 20
tack ready --assignee alice --json
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
