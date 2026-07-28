# Zero Specialists

Specialists are named sub-agents that Zero can delegate focused work to through
the `Task` tool. A specialist is a markdown manifest with YAML-style
frontmatter plus a system prompt body.

Specialists can be built in, user-scoped, or project-scoped:

| Scope | Path | Notes |
| --- | --- | --- |
| Built-in | compiled into Zero | `worker`, `explorer`, and `code-review` ship with the binary. |
| User | `~/.config/zero/specialists/*.md` | Available across local workspaces. |
| Project | `.zero/specialists/*.md` | Shared with the current repository when committed. |

Project specialists override user and built-in specialists with the same name.
User specialists override built-ins.

## CLI Management

```bash
zero specialist list
zero specialist show worker
zero specialist path

zero specialist create api-review \
  --project \
  --description "Reviews API changes" \
  --tools read-only,plan \
  --prompt "Review API changes for compatibility and missing tests."

zero specialist edit api-review --project
zero specialist delete api-review --project
```

Use `--json` with `list`, `show`, `path`, `create`, or `delete` when scripting.
`create --force` replaces an existing manifest, but refuses symlink overwrites.
`edit` also refuses symlink manifests before opening `$VISUAL` or `$EDITOR`.

## Manifest Format

```markdown
---
name: api-review
description: Reviews API changes for compatibility and missing tests.
tools:
  - read-only
  - plan
---

Review API changes for behavior regressions, compatibility breaks, and missing
tests. Report concrete findings with file paths.
```

Supported frontmatter keys:

| Key | Purpose |
| --- | --- |
| `name` | Lowercase specialist id. Use letters, numbers, and dashes. |
| `description` | Short summary shown in listings and task metadata. |
| `extends` | Optional base specialist to inherit prompt/model/tools from. |
| `model` | Optional model override. Empty means inherit the parent model. |
| `reasoningEffort` | Optional reasoning effort override. |
| `tools` | Array of tool categories or tool ids. |

If the body is empty and `description` is set, Zero uses the description as the
system prompt and reports a warning in `zero specialist show`.

## Tool Selection

Known categories:

| Category | Tools |
| --- | --- |
| `read-only` | `read_file`, `list_directory`, `grep`, `glob` |
| `edit` | read-only tools plus `write_file`, `edit_file`, `apply_patch` |
| `execute` | read-only tools plus `bash` |
| `plan` | `update_plan` |

Specialist manifests cannot enable `Task`, `TaskOutput`, `TaskStop`, or
`GenerateSpecialist`, so child specialists cannot spawn more specialists or
author new ones.

## Agent Tools

Zero registers these tools for top-level agent runs:

| Tool | Purpose |
| --- | --- |
| `Task` | Launch a specialist sub-agent for a focused prompt. |
| `TaskOutput` | Read or block on a background specialist task's output. |
| `TaskStop` | Stop a running background specialist task. |
| `GenerateSpecialist` | Create a project-local specialist manifest from a description. |

`GenerateSpecialist` is project-scoped only. It writes to
`.zero/specialists`, not the user specialist directory.

Example LLM-facing `Task` payload:

```json
{
  "name": "explorer",
  "description": "Find session storage code",
  "prompt": "Find the files that create, load, and list sessions."
}
```

Background task payload:

```json
{
  "name": "worker",
  "description": "Audit release docs",
  "prompt": "Check the release docs for stale TypeScript references.",
  "run_in_background": true
}
```

The returned `task_id` is also the child session id. Use it with
`TaskOutput`, `TaskStop`, or `Task` resume.

## Background State

Background specialist output is stored under:

```text
${XDG_DATA_HOME:-~/.local/share}/zero/background/
```

Each task has:

- `<task_id>.ndjson` for the child process stream output
- `<task_id>.json` for task metadata such as status, PID, parent session, and
  timestamps

Persisted metadata lets a new background manager instance read completed task
output or stop a still-running task by id.

If Zero is restarted while a background task is still marked `running`, the new
manager marks that task `error` and clears its PID. This avoids sending
`TaskStop` to a stale PID that may now belong to an unrelated process.

## Recovering an Interrupted Overwrite

Overwriting a specialist (`--force`) publishes the new file atomically, so an
interrupted write leaves either the old manifest or the new one — never a
half-written file. Windows has one rare exception worth knowing about.

There, the swap goes through `ReplaceFileW`, which preserves the destination's
security descriptor instead of silently replacing it with the directory's
inherited one. If it fails with `ERROR_UNABLE_TO_MOVE_REPLACEMENT_2` (1177),
Zero has already moved your original aside and tries to move it back. That
rollback almost always succeeds, and the failed write changes nothing.

If the rollback itself fails — typically because another process is holding a
lock on the file — the original is not lost, but it is left under a name Zero
does not read:

```text
<specialist dir>/.zero-replace-<random>.backup
```

Only `*.md` files are loaded as specialists, so until that file is renamed the
specialist will not appear in `zero specialist list` or resolve by name. The
error Zero prints names both paths; recover by closing whatever holds the lock
and renaming the backup back:

```powershell
Move-Item .zero-replace-<random>.backup <name>.md
```

A `.zero-replace-*.backup` can also linger after a *successful* overwrite if the
backup could not be deleted afterward. That case is reported as a warning rather
than an error — the new manifest is already in place, and the leftover file is
safe to delete.
