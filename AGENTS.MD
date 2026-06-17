---
description: Extending Zero — how to write AGENTS.md files, custom specialists, skills, hooks, MCP servers, and plugins for the open-source CLI coding agent.
globs: "*.go, *.js, *.md"
alwaysApply: false
---

# Extending Zero

Zero is an open-source terminal coding agent in the same family as Claude Code, Codex, and Droid. Out of the box it does the obvious things — read, edit, run, search — but the design point of the project is that **every surface is configurable**. This document is the user-facing guide for that configuration.

If you only want to *use* Zero, the [README](README.md) is enough. This page is for the other three jobs:

1. Tell the agent about *your* project (drop an `AGENTS.md` in your repo).
2. Add new specialist sub-agents (custom droids).
3. Wire Zero into the rest of your toolchain (MCP, skills, hooks, plugins).

## 1. Drop a project `AGENTS.md`

When Zero starts in a directory, it looks for project-level instructions in two places and applies both:

| Path | Scope | Notes |
| --- | --- | --- |
| `./AGENTS.md` | repo-root | The classic spot — committed to your repo, shared with the team. |
| `./.zero/AGENTS.md` | project-local | Hidden, gitignored. Personal notes that stay out of git. |

Both files use the same format. YAML frontmatter is optional; the markdown body is loaded as instructions for the agent.

```markdown
---
description: One-line summary the agent sees in its prompt.
globs: "*.go, *.py"
alwaysApply: false
---

# Project conventions for <your project>

- Build with `make`, not `go build` directly.
- Tests live next to the source file (`foo_test.go` next to `foo.go`).
- Run `make lint` before opening a PR.
- Never edit files under `third_party/` — those are vendored.
```

Tips:

- Keep it under ~80 lines. Long files dilute the agent's attention.
- Use `globs:` to scope which files the rules apply to (mirrors Claude Code's `settings.json` scoping).
- Re-state rules in the imperative voice: "Run `make lint`", not "you should consider running the linter".
- Don't put secrets, model IDs, or environment-specific paths in `AGENTS.md`. Use `config.json` for those.

## 2. Custom specialists (droids)

Specialists are Zero's sub-agents. Three scopes, in priority order:

| Scope | Path | Shared? |
| --- | --- | --- |
| Built-in | compiled into Zero | yes — `worker`, `explorer`, `code-review` |
| User | `~/.config/zero/specialists/*.md` | no — your machine only |
| Project | `./.zero/specialists/*.md` | yes — the repo team |

Project overrides user overrides built-in when names collide.

A specialist is a markdown manifest with frontmatter and a system prompt:

```markdown
---
description: Reviews API changes for breaking-change risk and missing tests.
tools: read-only,plan
---

You review API changes. For every changed hunk in `internal/api/` or any file
that ends in `_api.go`:

1. Confirm the public signature is backward-compatible, or note the breaking
   change explicitly with the migration path.
2. Confirm a corresponding test exists in `internal/api/*_test.go` and that
   the new behaviour is exercised.
3. Flag any new exported symbol without a doc comment.

Reply with one JSON object per finding: `{"file", "line", "severity", "message", "fix"}`.
```

CLI management:

```bash
zero specialist list
zero specialist show api-reviewer
zero specialist create api-reviewer \
    --project \
    --description "Reviews API changes" \
    --tools read-only,plan \
    --prompt-file api-reviewer.md
zero specialist edit api-reviewer --project
zero specialist delete api-reviewer --project
```

The full format spec (frontmatter fields, tool scopes, prompt conventions) is in [`docs/SPECIALISTS.md`](docs/SPECIALISTS.md).

## 3. Skills

Skills are markdown instruction packs the agent can pull in on demand. Each skill is a directory containing a `SKILL.md`:

```
~/.local/share/zero/skills/
  run-benchmarks/
    SKILL.md
  write-changelog/
    SKILL.md
```

`SKILL.md` format:

```markdown
---
description: Run the project's benchmark suite and summarize the deltas.
---

# Run benchmarks

1. `make bench` — captures the wall-clock and RSS before and after.
2. `benchstat before.txt after.txt` — diffs the two.
3. Report any regression > 5% with the function name and the previous value.
```

Override the directory with `ZERO_SKILLS_DIR=...`. A missing directory is fine — Zero just reports "no skills".

The `skill` core tool lets the agent load any discovered skill by name.

## 4. Hooks

Hooks fire shell commands on lifecycle events. Configure them in JSON:

- User: `~/.config/zero/hooks.json`
- Project: `./.zero/hooks.json`

```json
{
  "enabled": true,
  "hooks": [
    {
      "id": "block-rm-rf",
      "event": "beforeTool",
      "matcher": "bash",
      "command": "/usr/local/bin/zero-hook-block-rmrf.sh",
      "args": ["${tool}", "${input}"],
      "enabled": true
    },
    {
      "id": "log-session",
      "event": "sessionStart",
      "command": "/usr/local/bin/zero-hook-log.sh",
      "args": ["start"],
      "enabled": true
    }
  ]
}
```

Events the agent emits (in dispatch order):

| Event | Fires when | Matcher allowed? |
| --- | --- | --- |
| `beforeTool` | A tool is about to run | yes (tool name) |
| `afterTool` | A tool just returned | yes (tool name) |
| `sessionStart` | A session begins | no |
| `sessionEnd` | A session ends | no |
| `specialistStart` | A sub-agent is spawned | yes (specialist name) |
| `specialistStop` | A sub-agent ends | yes (specialist name) |

A hook's exit code decides what happens next: `0` continues, non-zero blocks the tool call (`beforeTool`) or surfaces an error (`afterTool`). Hooks that misbehave are recorded in the audit log; use `zero doctor` to surface them.

## 5. MCP — Model Context Protocol

Zero is both an **MCP client** (it can call external MCP servers) and an **MCP server** (other agents can call its tools).

### As a client — configure MCP servers in `config.json`

```json
{
  "mcp": {
    "servers": {
      "docs": {
        "type": "stdio",
        "command": "docs-mcp",
        "args": ["--port", "7777"]
      },
      "github": {
        "type": "http",
        "url": "https://api.example.com/mcp",
        "headers": { "Authorization": "Bearer ${env:GITHUB_TOKEN}" }
      }
    }
  }
}
```

Manage via CLI:

```bash
zero mcp add docs --type stdio -- docs-mcp --port 7777
zero mcp add github --type http --url https://api.example.com/mcp
zero mcp list
zero mcp check docs
zero mcp remove github
zero mcp oauth login github
```

Servers are merged from user and project configs (project wins on conflicts). Token-bearing values come from environment variables — never inline them.

### As a server — expose Zero's tools to another agent

```bash
zero serve --mcp
```

The server speaks MCP over stdio. Configure it from the receiving side as a `stdio` server whose command is `zero serve --mcp`.

## 6. Plugins

A plugin is a self-contained directory that bundles tools, hooks, and skills for one capability. Plugins live at:

- User: `~/.config/zero/plugins/<id>/`
- Project: `./.zero/plugins/<id>/`

Each plugin has a `plugin.json` manifest:

```json
{
  "id": "github-pr-review",
  "name": "GitHub PR Review",
  "description": "Adds review tools for GitHub PRs.",
  "version": "1.0.0",
  "tools": [
    { "name": "list_prs", "command": "./tools/list_prs.sh" }
  ],
  "hooks": [
    { "name": "pre-merge-check", "event": "beforeTool", "command": "./hooks/pre-merge.sh" }
  ],
  "skills": [
    { "path": "./skills/review-checklist/SKILL.md" }
  ]
}
```

Install and manage:

```bash
zero plugins install ./github-pr-review
zero plugins list
zero plugins enable github-pr-review
zero plugins disable github-pr-review
zero plugins remove github-pr-review
```

Plugin commands run with the plugin directory as their working directory. Use relative paths; the loader resolves them at activation time.

## 7. Configuration locations

Three layers, applied in order (later layers override earlier ones):

| Layer | Path | Notes |
| --- | --- | --- |
| Built-in defaults | compiled in | Lowest priority. |
| User config | `~/.config/zero/config.json` | Your machine. Never committed. |
| Project config | `./.zero/config.json` | The repo. Committed (or not, your call). |
| CLI flags | `--model`, `--mode`, ... | Highest priority, per-invocation. |
| Environment | `ZERO_*` | Provider commands, secrets, skills dir override. |

The user config holds things that should follow the user across projects (default provider, default model, theme). The project config holds things the team agreed on (provider catalog, sandbox policies, model restrictions).

The sandbox `additionalWriteRoots` key is **ignored in project config** by design — a checked-out repo cannot widen its own sandbox. Set it in the user config or pass `--add-dir` per-invocation.

## 8. End-to-end example

A team that wants every contributor's Zero to behave the same way commits:

- `AGENTS.md` — project conventions, build commands, do-not-edit lists.
- `.zero/config.json` — provider catalog, default model, allowed tools.
- `.zero/specialists/api-reviewer.md` — the team's PR-review droid.
- `.zero/hooks.json` — block `rm -rf` and `git push --force` on `beforeTool`.
- `.zero/plugins/internal-tooling/` — a plugin that adds the team's internal CLI tools to the agent's toolset.

Each contributor adds only:

- `~/.config/zero/config.json` — their personal API keys, theme, default mode.
- `~/.local/share/zero/skills/` — personal skills they keep across projects.

That's it. Run `zero` from the repo root and the agent has the team's full instruction set, every contributor's personal setup, and nothing else.

## 9. Reference

- [README](README.md) — install, quickstart, command reference.
- [docs/SPECIALISTS.md](docs/SPECIALISTS.md) — full specialist manifest spec.
- [docs/PRD.md](docs/PRD.md) — product requirements, full feature surface.
- [docs/STREAM_JSON_PROTOCOL.md](docs/STREAM_JSON_PROTOCOL.md) — `zero exec` I/O contract.
- [docs/INSTALL.md](docs/INSTALL.md) — install from source or release.
