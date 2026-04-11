---
name: tack
description: tack is a local issue/task manager. Use when working with the local `tack` issue database in a git repository, selecting ready work, claiming tasks, turning plans into epics and linked issues, creating follow-up tasks, managing dependencies, adding implementation notes, and closing completed work.
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
4. When you need to turn a referenced plan into multiple linked issues, prefer a single manifest import:
   `tack import --file plan.json --json`
5. When you uncover follow-up work during implementation, create a linked task and attach dependencies or a parent:
   `tack create --title "..." --type task --priority medium --parent <id> --depends-on <id> --json`
6. Close completed work with an explicit reason:
   `tack close <id> --reason "..." --json`

## Notes
- Prefer `--json` for read and mutation commands when another tool will consume the output.
- Prefer `tack ready --json --summary` and `tack list --json --summary` for automation. Summary output omits large bodies and includes the fields agents usually need to decide what to do next: `id`, `title`, `status`, `type`, `priority`, `assignee`, `parent_id`, `labels`, `blocked_by`, and `open_children`.
- Parent issues are non-actionable by default while they still have open children. They stay visible in `tack list`, `tack show`, and `tack export --json`, but they do not appear in `tack ready` until all children are closed.
- Use `tack export --jira <epic-id>` when you need a Jira creation plan for one explicit epic subtree rather than the raw full-project export.
- Write issue `description` fields as Markdown when the body is more than a sentence or two. Prefer short sections, bullets, and code fences over dense prose so the CLI/TUI can render the description cleanly.
- When asked to create issues from a referenced plan, spec, or checklist, model the whole effort as one epic plus linked child issues instead of a flat list of standalone tasks.
- Start that import plan with a single `epic` issue representing the full initiative. Use the epic title for the overall outcome and put the plan summary, scope, and success criteria in the epic `description`.
- Do not let important plan details stay only in the source plan. Agents working the resulting issues may never see that original document, so the issue graph must preserve the requirements, constraints, sequencing notes, and acceptance criteria they need.
- Prefer `tack import --file <path>` over a series of `tack create` calls when translating a plan. Import lets you create the epic, all children, their labels, and `depends_on` edges in one pass.
- Give every issue a shared plan label that identifies the overall initiative, such as `plan:offline-sync` or `initiative:search-revamp`. Add 1-3 extra labels per issue for the specific workstream or concern, such as `cli`, `store`, `migration`, `docs`, or `qa`.
- Make labels concise and reusable. Prefer lowercase slug-like labels, and make sure each child issue has labels that explain both what part of the plan it belongs to and what kind of work it is.
- Split the plan into concrete child issues under the epic. Use `parent` for hierarchy and `depends_on` for sequencing or blockers, so `tack ready` can surface the next actionable work correctly.
- Write the epic and every child issue so they are self-contained. Copy forward the relevant details from the plan instead of replacing them with vague summaries like "implement backend work" or "finish docs".
- For the epic `description`, preserve the high-level problem statement, scope boundaries, assumptions, risks, cross-cutting constraints, and overall success criteria.
- For each child issue `description`, include the specific slice of the plan that issue owns: the objective, concrete requirements, implementation notes, edge cases, dependencies, and clear acceptance criteria. If a child issue depends on decisions or constraints from another part of the plan, restate the relevant context in that child issue too.
- When the source plan has checklists, phases, or subsections, translate them into issue descriptions intentionally. Preserve the substance, not just the headings, so future agents can act from the task alone without reopening the original plan.
- Use `tack import --file <path>` when converting a dev plan or checklist into issues. Import manifests support per-issue alias `id`, `title`, `description`, `type`, `priority`, `labels`, `parent`, and `depends_on`, so you can create the graph in one pass.
- Plan-oriented import manifest example:
  ```json
  {
    "issues": [
      {
        "id": "epic",
        "title": "Ship offline sync",
        "type": "epic",
        "priority": "high",
        "labels": ["plan:offline-sync", "sync"],
        "description": "Deliver offline sync across the CLI, store, and migration flow.\n\n## Success criteria\n- local changes queue safely offline\n- sync resumes cleanly after reconnect\n- rollout notes and operator docs are ready"
      },
      {
        "id": "store",
        "title": "Persist outbound sync queue in store",
        "parent": "epic",
        "priority": "high",
        "labels": ["plan:offline-sync", "store", "backend"],
        "description": "Create durable storage for outbound sync operations.\n\n## Requirements\n- queue local mutations while offline\n- preserve enqueue order across restarts\n- mark entries for retry after transient failures\n\n## Constraints\n- reuse the existing migration flow\n- do not block read-only commands while replay state loads\n\n## Acceptance criteria\n- queued operations survive restart\n- replay resumes in original order\n- permanent failures are surfaced distinctly from retryable failures"
      },
      {
        "id": "cli",
        "title": "Add CLI status and retry flows for sync queue",
        "parent": "epic",
        "depends_on": ["store"],
        "labels": ["plan:offline-sync", "cli", "ux"],
        "description": "Expose queue state and recovery controls in the CLI.\n\n## Requirements\n- show whether sync is healthy, paused, or retrying\n- provide a retry path for failed queued work\n- explain when the CLI is waiting on persisted queue state from the store layer\n\n## Dependencies\n- depends on the persisted queue behavior from the store task\n\n## Acceptance criteria\n- operators can inspect queue state from the CLI\n- retry behavior does not hide permanent failures\n- output is clear enough for troubleshooting without reading the original plan"
      },
      {
        "id": "docs",
        "title": "Document sync recovery and rollout steps",
        "parent": "epic",
        "depends_on": ["cli"],
        "labels": ["plan:offline-sync", "docs", "ops"],
        "description": "Capture the rollout and support guidance needed for offline sync.\n\n## Include\n- recovery steps for stuck or failed queued work\n- rollout notes for enabling the feature safely\n- operator guidance that matches the final CLI language and states\n\n## Acceptance criteria\n- docs cover recovery, rollout, and troubleshooting\n- instructions match the implemented CLI and store behavior"
      }
    ]
  }
  ```
- Use `tack comment add <id> --body-file ... --json` for substantial implementation notes.
- Use `tack dep add <blocked-id> <blocker-id>` to keep ready-work filtering accurate.
- Subcommand help works from either form: `tack <command> --help` or `tack help <command>`.
- Tack configures SQLite with WAL mode and a busy timeout, so separate read-oriented tack commands can safely overlap when automation opens multiple processes.
