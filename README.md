<p align="center">
  <img src="docs/assets/zero-logo.png" alt="Zero" width="385">
</p>

<p align="center"><strong>A terminal coding agent you own.</strong></p>

<p align="center">
  <a href="LICENSE"><img alt="license" src="https://img.shields.io/badge/license-MIT-blue"></a>
  <img alt="Go 1.25+" src="https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white">
  <img alt="25+ providers" src="https://img.shields.io/badge/providers-25+-34E2EA">
  <br>
  <strong>English</strong> | <a href="README_ZH.md">中文</a>
</p>

Zero is an AI coding agent for your local terminal. It can inspect a repository,
edit files, run commands, use browser/terminal helpers, and keep durable local
sessions while you choose the model and the permission level.

```bash
zero
zero exec "fix the failing test in ./pkg"
zero exec --output-format stream-json < turns.jsonl
```

## Why Zero

- **Use the model you want.** Bring OpenAI, Anthropic, Gemini, Groq, OpenRouter,
  DeepSeek, Mistral, xAI, Qwen, Kimi, GitHub Models, Ollama, LM Studio, or any
  OpenAI-/Anthropic-compatible endpoint.
- **Stay in control.** File writes, shell commands, network access, and
  out-of-workspace writes go through Zero's permission and sandbox policy.
- **Works in the terminal.** The TUI has model/provider pickers, image input,
  slash commands, live plan/tool rendering, scrollback, themes, and resume/fork
  support.
- **Works without the TUI.** `zero exec` is scriptable, supports text/JSON/
  stream-JSON I/O, isolated worktrees, spec-first runs, and meaningful exit
  codes for CI.
- **Keeps context local.** Sessions are stored on disk, searchable, resumable,
  and never uploaded as telemetry by Zero.
- **Extensible when you need it.** Use MCP servers, skills, plugins, hooks, and
  specialist subagents from the same CLI.

## Install

### npm

```bash
npm install -g @gitlawb/zero
zero
```

The npm package installs a small wrapper plus the matching Zero binary for your
platform from GitHub Releases. It supports Linux, macOS, and Windows on x64 and
arm64.

### Bun

Bun does not run dependency lifecycle scripts by default, so the `postinstall`
that fetches the Zero binary is skipped and the first run fails with
`No native binary found next to the npm wrapper`.

Install with Bun, then fetch the binary in one of two ways:

```bash
# Option A: run the installer manually
bun add @gitlawb/zero
node node_modules/@gitlawb/zero/scripts/postinstall.mjs

# Option B: allow the postinstall to run on install
# add to your package.json:  "trustedDependencies": ["@gitlawb/zero"]
bun add @gitlawb/zero
```

For global installs (`bun add -g @gitlawb/zero`), use Option A against the
global install path, or prefer the install scripts below.

### Install scripts

Linux/macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/Gitlawb/zero/main/scripts/install.sh | bash
```

Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/Gitlawb/zero/main/scripts/install.ps1 | iex
```

### From source

Source builds require Go 1.25+.

```bash
git clone https://github.com/Gitlawb/zero.git
cd zero
go run ./cmd/zero
```

Release installers and the npm wrapper require published GitHub Release assets.
If you are testing before the first public release, build from source:

```bash
go build -o zero ./cmd/zero
```

On Linux, build the sandbox helper too if you want native sandboxing:

```bash
go build -o zero-linux-sandbox ./cmd/zero-linux-sandbox
go build -o zero-seccomp ./cmd/zero-seccomp   # optional compatibility wrapper
```

Put `zero` and `zero-linux-sandbox` in the same directory on `PATH`
(`~/.local/bin` is a good default). macOS does not need an extra helper binary.
Windows source builds can use the main `zero.exe` as their sandbox helper; release
archives still ship standalone Windows helper executables.

More install details: [docs/INSTALL.md](docs/INSTALL.md).

## First Run

Start the TUI:

```bash
zero
```

The setup wizard helps you pick a provider and model. You can also configure
providers from the command line:

```bash
zero setup
zero providers list
zero models list
zero doctor
```

For API providers, set the matching environment variable before setup or enter
the key in the wizard:

```bash
export OPENAI_API_KEY=sk-...
export ANTHROPIC_API_KEY=...
export GEMINI_API_KEY=...
export LONGCAT_API_KEY=...
```

To configure Meituan LongCat (LongCat-2.0) directly, run:

```bash
zero providers setup longcat --set-active
```

For local models, run Ollama or LM Studio and then use `zero setup` or
`zero providers detect`.

## Daily Use

### Interactive TUI

```bash
zero
```

Useful controls:

| Control | Action |
|---|---|
| `Enter` | send the prompt |
| `/` | open slash-command suggestions |
| `Shift+Tab` | cycle permission mode |
| `Ctrl+B` | show/hide the sidebar |
| `Ctrl+C` | cancel or exit |

Common slash commands:

| Command | Purpose |
|---|---|
| `/model`, `/provider` | switch the active model/provider |
| `/spec`, `/plan` | draft and review a plan before building |
| `/image` | attach an image for vision-capable models |
| `/resume`, `/rewind` | continue or roll back local sessions |
| `/compact`, `/context` | manage context usage |
| `/permissions`, `/tools` | inspect available tools and policy |
| `/add-dir` | allow an extra write directory for this session |
| `/theme`, `/doctor`, `/config` | adjust appearance and inspect setup |

### Headless `exec`

```bash
zero exec "explain internal/agent/loop.go"
zero exec --model claude-sonnet-4.5 "refactor the config loader"
zero exec --use-spec "add rate limiting to the API client"
zero exec --worktree "try the migration in an isolated worktree"
zero exec --resume
zero exec --fork <session-id> "try the other approach"
```

Programmatic use:

```bash
zero exec --input-format stream-json --output-format stream-json < turns.jsonl
```

The stream-JSON contract is documented in
[docs/STREAM_JSON_PROTOCOL.md](docs/STREAM_JSON_PROTOCOL.md).

## Safety Model

Zero is designed to make side effects visible.

- Workspace reads are allowed by default.
- File writes are limited to the workspace unless you grant another directory.
- Shell commands, network access, destructive commands, and elevated actions are
  permission-gated.
- `--add-dir <path>` and `/add-dir <path>` grant additional write roots without
  giving the agent the whole filesystem.
- Unsafe/autonomous modes are explicit opt-ins.
- Secrets are redacted from tool output and logs where Zero controls the surface.

Example:

```bash
zero --add-dir ../docs-site
zero exec --add-dir ../shared "update both repos"
```

Sandbox behavior can be inspected with:

```bash
zero sandbox policy
zero sandbox grants list
```

## Web And Local Control

Zero includes local file/search/edit/shell tools, `web_fetch` for public URLs,
and MCP support for additional tools.

For local dev servers, use shell commands such as `curl` through `exec_command`
so the normal sandbox and permission policy applies. Long-running commands stay
attached to a background terminal session and can be listed or stopped from the
TUI.

The npm package also includes browser and terminal helper packages used by local
browser/terminal tools. Source builds can use the same helpers when they are on
`PATH` or configured in Zero's local-control settings.

## Common Commands

```text
zero                  interactive TUI
zero exec             one-shot or scripted agent run
zero setup            first-run provider setup
zero auth             OAuth/login helpers for supported providers
zero models           model registry and capabilities
zero providers        provider profiles and detection
zero doctor           setup, key, and connectivity checks
zero context          context-budget report
zero repo-map         deterministic repository map
zero repo-info        local repository summary
zero search | find    search local session history
zero sessions         inspect, resume, fork, and rewind sessions
zero spec             manage spec-mode drafts
zero specialist       manage specialist subagents
zero skills           manage markdown instruction skills
zero plugins          manage plugins
zero hooks            manage lifecycle hooks
zero mcp              manage MCP servers and tools
zero serve --mcp      expose Zero tools over MCP stdio
zero sandbox          inspect sandbox policy and grants
zero worktrees        prepare isolated git worktrees
zero verify           detect and run local verification checks
zero changes          inspect and commit local git changes
zero usage            token usage and estimated cost
zero cron             scheduled agent jobs
zero update           check for newer releases
```

## Appearance And Accessibility

| Control | Effect |
|---|---|
| `NO_COLOR=<anything>` | disables color output |
| `ZERO_THEME=<name>` | selects the startup theme (`auto`, `dark`, `light`, or a color theme like `dracula`, `nord`, `gruvbox`, `tokyo-night`, `catppuccin`, `one-dark`, `solarized-dark`, `rose-pine`, `everforest`, `solarized-light`) |
| `--theme <name>` | selects the TUI theme from the CLI (same names) |
| `/theme` | opens the theme picker inside the TUI (live preview; `/theme <name>` switches directly) |
| `ZERO_NO_FADE=1` | disables streaming fade animation |

Meaning does not rely on color alone; diffs, permissions, and statuses also use
text or glyph markers.

## Development

```bash
go test ./...
go run ./cmd/zero-release build
go run ./cmd/zero-release smoke
go run ./cmd/zero-perf-bench
```

Cross-compile examples:

```bash
go run ./cmd/zero-release build --goos linux --goarch amd64
go run ./cmd/zero-release build --goos windows --goarch amd64 --output dist/zero.exe
```

## Documentation

- [Install](docs/INSTALL.md)
- [Update flow](docs/UPDATE.md)
- [Stream-JSON protocol](docs/STREAM_JSON_PROTOCOL.md)
- [Specialists](docs/SPECIALISTS.md)
- [GitHub Action](docs/GITHUB_ACTION.md)
- [Benchmarks](docs/BENCHMARK.md)
- [Performance](docs/PERFORMANCE.md)
- [Agent evals](docs/AGENT_EVALS.md)

## Contributing

Contributions are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md), run the
relevant tests, and open a focused pull request.

Security reports should follow [SECURITY.md](SECURITY.md).

## License

Zero is released under the [MIT License](LICENSE).
