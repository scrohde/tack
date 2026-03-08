# tack

`tack` is a local-first issue tracker for a single Git repository. It stores issues, comments,
dependencies, labels, and audit events in a repo-local SQLite database at `.tack/issues.db` and
exposes a scriptable CLI with JSON output for automation.

## Status

The current implementation supports:

- repo initialization with `tack init`
- issue creation, listing, show, update, edit, close, and reopen
- ready-work filtering with `tack ready`
- comments, labels, and dependency management
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

Initialization creates:

- `.tack/issues.db`
- `.tack/config.json`
- `.tack/.gitignore`

`tack init` keeps ignore rules inside `.tack/`, so you do not need to edit the repo root
`.gitignore`.

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

Create a plan for the code changes you want to make. In practice, this often means using a coding
agent to help draft the implementation plan before any tack items are created.

Convert that plan into detailed tacks so the work can be handed off cleanly across different agent
sessions or different agents. The coding agent will do this for you with a prompt like:

    Create tacks from the plan being careful to not miss any details. Be explicit about scope, dependencies, and acceptance details so the resulting tasks are implementation-ready.

Once the plan has been fully decomposed into tacks, start a fresh agent session and ask it to:

    Implement 1 or 2 ready issues from tack.

For best results, have the agent implement a small set of issues per session.  This keeps context to a minimum and helps you verify the work being done.

## Command Reference (each subcommand has its own --help output)

```text
tack help
tack init
tack create
tack show <id>
tack list
tack ready
tack update <id>
tack edit <id>
tack close <id>
tack reopen <id>
tack comment add|list
tack dep add|remove|list
tack skill install
tack labels add|remove|list
tack export --json
```

Useful list filters:

```bash
tack list --status open --type bug --label backend --limit 20
tack ready --assignee alice --json
```

## Development

Run the test suite:

```bash
go test ./...
```

The existing tests cover repo initialization, CLI flows, actor resolution, ready-work filtering,
dependency cycle rejection, export, comments, labels, and event generation.
