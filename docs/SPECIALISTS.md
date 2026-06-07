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
