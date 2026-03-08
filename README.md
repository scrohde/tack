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

Run it from anywhere inside a Git worktree:

```bash
./tack help
./tack init
```

You can also run it without building a binary:

```bash
go run ./cmd/tack --help
```

Initialization creates:

- `.tack/issues.db`
- `.tack/config.json`

The repo’s `.gitignore` already excludes `.tack/` and the local `tack` binary.

## Actor Resolution

Write operations need an actor. `tack` resolves it in this order:

1. `--actor`
2. `TACK_ACTOR`
3. `.tack/config.json`
4. `git config user.name`
5. the current OS username

## Typical Workflow

Initialize the repo-local database:

```bash
./tack init
```

Create work:

```bash
./tack create \
  --title "Add README.md" \
  --type task \
  --priority medium \
  --description "Document the project" \
  --json
```

See what is ready to work:

```bash
./tack ready --json
```

Claim an issue and move it to `in_progress`:

```bash
./tack update tk-1 --claim --json
```

Inspect or edit it:

```bash
./tack show tk-1
./tack edit tk-1
./tack update tk-1 --priority high --assignee "" --json
```

Track dependencies, labels, and comments:

```bash
./tack dep add tk-2 tk-1
./tack labels add tk-1 docs cli
./tack comment add tk-1 --body "Implementation started"
```

Close completed work:

```bash
./tack close tk-1 --reason "README added" --json
```

## Command Reference

```text
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
tack labels add|remove|list
tack export --json
```

Useful list filters:

```bash
./tack list --status open --type bug --label backend --limit 20
./tack ready --assignee alice --json
```

## Development

Run the test suite:

```bash
go test ./...
```

The existing tests cover repo initialization, CLI flows, actor resolution, ready-work filtering,
dependency cycle rejection, export, comments, labels, and event generation.
