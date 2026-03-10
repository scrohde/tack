# tack

`tack` is a local-first issue tracker for a single Git repository. It stores issues, comments,
dependencies, labels, and audit events in a repo-local SQLite database at `.tack/issues.db` and
exposes a scriptable CLI with JSON output for automation.

## Status

The current implementation supports:

- repo initialization with `tack init`
- issue creation, listing, show, update, edit, close, and reopen
- manifest-driven bulk issue import with `tack import --file manifest.json`
- ready-work filtering with `tack ready`
- comments, labels, and dependency management
- concurrent read-heavy automation across separate tack processes for commands like `tack ready` and `tack export --json`
- JSON export with `tack export --json`

Issue IDs use the form `tk-1`, `tk-2`, and so on.

## Requirements

- Go 1.26 or newer
- a Git repository; `tack` discovers the repo root by walking up to `.git`
- `$EDITOR` set if you want interactive editing for long descriptions or `tack edit`

## Build And Run

Build the CLI:

```bash
go build -o tack ./cmd/tack
```

Or install it onto your `PATH`:

```bash
go install ./cmd/tack
```

Run it from anywhere inside a Git worktree:

```bash
tack help
tack init
```

Initialization creates only repo-local tack state:

- `.tack/issues.db`
- `.tack/config.json`
- `.tack/.gitignore`

`tack init` keeps ignore rules inside `.tack/`, so you do not need to edit the repo root
`.gitignore`. It does not install any agent skill content.

Repo-local skill installs also create `.agents/.gitignore`, so both `.tack/` and `.agents/`
stay fully ignored by Git.

If you ever want to cleanly remove tack, remove `.tack` and `.agents/skills/tack`.

## Actor Resolution

Write operations need an actor. `tack` resolves it in this order:

1. `--actor`
2. `TACK_ACTOR`
3. `.tack/config.json`
4. `git config user.name`
5. the current OS username

## Typical Workflow

Install `tack` somewhere on your `PATH`, then work from inside the target Git repository.

Initialize the repo-local database once per repo:

```bash
tack init
```

Install the tack agent skill into that repo:

```bash
tack skill install
```

The install target is explicit:

- `tack skill install` installs to `<repo>/.agents/skills/tack`
- `tack skill install --home` installs to `$HOME/.agents/skills/tack`
- `tack skill install --path /tmp/skills` installs to `/tmp/skills/tack`

The repo-local mode is the default and is separate from `tack init`.

Create a plan for the code changes you want to make. In practice, this often means using a coding
agent to help draft the implementation plan before any tack items are created.

Convert that plan into detailed tacks so the work can be handed off cleanly across different agent
sessions or different agents. The coding agent will do this for you with a prompt like:

    Create tacks from the plan, be careful to not miss any details. Be explicit about scope, dependencies, and acceptance details so the resulting tasks can be handed off to another agent and be implementation-ready.

Once the plan has been fully decomposed into tacks, start a fresh agent session and ask it to:

    Implement 1 or 2 ready issues from tack.

For best results, have the agent implement a small set of issues per session.  This keeps context to a minimum and helps you verify the work being done.

## Command Reference (each subcommand has its own --help output)

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

Useful list filters:

```bash
tack list --status open --type bug --label backend --limit 20
tack ready --assignee alice --json
tack list --json --summary
tack ready --json --summary
```

`tack ready` only returns unassigned open issues whose blockers are closed, whose defer time has
passed, and which do not have open children. Parent issues stay visible in `tack list`, `tack show`,
and `tack export --json`; they simply leave the ready queue until all children close.

Both `tack list` and `tack ready` support `--json --summary` for a compact issue payload with
labels, blockers, and open-child IDs. `--summary` always requires `--json`.

Subcommand help is available both ways:

```bash
tack ready --help
tack help ready
```

Manifest import is JSON-only in v1:

```json
{
  "issues": [
    {
      "id": "epic",
      "title": "Agent workflow follow-up",
      "type": "epic",
      "priority": "high"
    },
    {
      "id": "task",
      "title": "Implement the importer",
      "parent": "epic",
      "depends_on": ["tests"],
      "labels": ["automation"]
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

The manifest `id`, `parent`, and `depends_on` fields are manifest-local aliases. Import creates
all issues and links atomically, defaults omitted `type` values to `task`, defaults omitted
`priority` values to `medium`, and returns `created_ids` plus an `alias_map` in JSON mode.

## Development

Run the test suite:

```bash
go test ./...
```

The existing tests cover repo initialization, CLI flows, actor resolution, ready-work filtering,
parent readiness transitions, concurrent read access, summary JSON output, dependency cycle
rejection, export, comments, labels, and event generation.
