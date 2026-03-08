# AGENTS

## Purpose

`tack` is a local-first issue tracker for a single Git repository. The CLI stores issue data in
`.tack/issues.db` and is implemented in Go.

## Stack

- Go 1.26
- SQLite via `modernc.org/sqlite`

## Repo Layout

- `cmd/tack`: CLI entrypoint
- `internal/cli`: command parsing and user-facing output
- `internal/store`: repo discovery, SQLite schema, persistence, and query logic
- `internal/issues`: domain models, actor resolution, and editor helpers
- `internal/skill`: embedded skill content and install logic
- `.agents/skills/tack/SKILL.md`: source for the tack skill
- `scripts/generate_skill.go`: regenerates the embedded skill file

## Core Commands

- `go build -o tack ./cmd/tack`
- `go test ./...`
- `golangci-lint run --fix`
- `go run ./cmd/tack --help`
- `go generate ./internal/skill`


## Editing Rules

- If you change `.agents/skills/tack/SKILL.md`, regenerate
  `internal/skill/tack_generated.go` with `go generate ./internal/skill`.
- Do not edit `internal/skill/tack_generated.go` by hand.
- Preserve both human-readable CLI output and `--json` behavior when changing commands.
- Add or update tests alongside behavior changes. Existing CLI and store tests are the main safety
  net.
- Make sure test and lint pass before committing.

## Testing Notes

- `internal/testutil` contains temp-repo helpers used by CLI and store tests.
- Prefer focused test coverage near the package you changed, then run `go test ./...`.
