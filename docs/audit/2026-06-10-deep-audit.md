# Zero — Deep Code Audit Report

Date: 2026-06-10 · Scope: every module in the repository (16 audit areas, 175 findings) · Method: one specialist auditor per module group plus cross-module wiring, hygiene, and TUI-architecture deep dives; findings adversarially verified where capacity allowed (the verification fleet was interrupted by API session limits partway, so findings below are marked **verified** only where a verifier completed; all others are auditor-reported with quoted code evidence).

**175 unique findings** — 2 critical · 30 high · 71 medium · 72 low

## Fixed in this pass (2026-06-10)

The TUI was re-architected around a settled-row flush frontier (history now lives in native terminal scrollback) and the following audit findings were fixed and verified by the full test suite:

**TUI (internal/tui)**
- History unreachable: settled transcript rows now print once to native scrollback via `tea.Println` (ordered, ack-serialized); `View()` renders only the live tail. Scroll-up, text selection, and copy of full history now work. Title bar prints once at startup at the real terminal width.
- Edited code reviewable in history: flushed cards use a deep 400-line body cap (live region keeps 16), so full diffs land in scrollback.
- Clickable edited places: OSC 8 `file://` hyperlinks (percent-encoded) on diff paths, file-tool targets, and grep locations; `ansiSequenceEnd` now parses OSC, and truncation re-closes open hyperlinks.
- Streamed interim text preserved as non-final assistant rows (tool call, error, and cancel paths) instead of vanishing.
- Visible "Run cancelled." marker (plus partial answer) on Esc/Ctrl+C.
- Transcript dedup key is run-scoped + per-run ordinal suffix for repeated provider tool-call ids (Gemini) — live and on rehydration.
- Ctrl+C after an Esc-cancel and `/exit` mid-run no longer orphan checkpoint blobs (deferred quit until flush; `/spec` gated while exiting).
- Cancelled-run late flush goes to the session recorded at cancel time (no cross-session contamination); `/rewind` blocked while a flush is outstanding; cancelled-run usage recorded.
- `/resume` and `/rewind` flush rehydrated history to scrollback in one batch; ask_user questions AND answers persist and rehydrate.
- Composer: input width tracks the terminal (no more blind typing), shell-style ↑/↓ input history recall.
- `wrapPlainText` preserves indentation (code blocks survive); `looksLikeDiff` no longer hijacks output containing `---` separators; `shortenPath` home-boundary fix; rune-safe truncation of titles/outputs; O(n²) transcript append removed; completed runs release their CancelFunc; ask-user card echoes the typed answer; permission card labels `[esc] cancel run`; dead footer/actions/theme tokens removed.

**Cross-module**
- `zero search`: lowered-text byte offsets mapped back to the original (panic + invalid-UTF-8 context fixed); unknown `--session-id` errors instead of silently returning 0 hits; corrupt sessions skipped, not fatal.
- `zero usage report`: corrupt sessions skipped; works outside a git repository (net-LOC degrades to 0).
- Providers: HTTP 529 retried; SSE comment keep-alives reset the idle watchdog.
- Model registry: gpt-4.1 max output 16,384 → 32,768; claude-haiku-3.5 vision flag removed.
- `zero update --check`: works on dev/source builds (treats non-semver version as 0.0.0).
- MCP: server answers `ping`; `zero mcp list --json` structurally redacts env/header values.
- Sandbox: new `sandbox.network: allow|deny` config knob (the engine's NetworkDeny default was previously unreachable from any config surface).
- `zero doctor`: reports apiKey presence (`set`/`not set`) instead of an unconditional `[REDACTED]`.
- Config writer: atomic temp+rename write. notify: C1 control bytes stripped from OSC-9. imageinput: real open/read errors surfaced.
- Rune-safe truncation in exec session titles, stream-json output, status lines, cron excerpts, session replay previews, git subjects/output, and the agent system prompt.
- Hygiene: repo gofmt'ed; CI now gates on gofmt + go vet; dead glamour dependency dropped via `go mod tidy`.

Everything else below remains open and is reported for follow-up.

## Severity index

| Sev | Area | Location | Finding |
|---|---|---|---|
| critical | mcp-serve | `internal/mcp/protocol.go:69` | Unbounded Content-Length allocation lets a peer crash the whole process |
| critical | cli-commands | `internal/search/search.go:346` | zero search panics (slice out of range) and emits misaligned/invalid-UTF-8 context due to ToLower byte-offset mismatch |
| high | wiring-parity | `internal/tui/transcript.go:137` | Transcript dedupe key omits runID — repeated provider tool-call IDs silently drop later tool rows |
| high | wiring-parity | `internal/tui/model.go:853` | /exit during an in-flight run quits immediately, orphaning checkpoint blobs and dropping the run's session events |
| high | mcp-serve | `internal/mcp/protocol.go:42` | MCP stdio transport uses LSP Content-Length framing instead of MCP newline-delimited JSON |
| high | mcp-serve | `internal/streamjson/streamjson.go:310` | streamjson secret patterns mangle ordinary text in every stream-json output event |
| high | leaf-packages | `internal/providerhealth/providerhealth.go:285` | Health probe SSRF blocklist and credentials defeated by redirects and DNS rebinding |
| high | tests-hygiene | `internal/tui/model.go:599` | Transcript view ignores terminal height — rows beyond one screen are silently dropped (no viewport/scrollback), and tests only lock in width behavior |
| high | sessions-usage | `internal/sessions/store.go:547` | Torn append (crash mid-write) permanently bricks a session: ReadEvents hard-fails on any malformed JSONL line |
| high | sessions-usage | `internal/tui/model.go:512` | Cancelled run's deferred event flush is appended to whatever session is active at flush time (cross-session contamination) |
| high | sessions-usage | `internal/tui/session_controls.go:176` | /rewind is allowed while a cancelled run is still flushing: prune deletes its checkpoint blobs, then the late flush re-appends pre-rewind events after the rewind marker |
| high | tui-scroll-arch | `internal/tui/model.go:584` | Entire chat history lives inside View(); inline renderer clips it at terminal height, so scrollback/selection of history is impossible |
| high | tui-scroll-arch | `internal/tui/transcript.go:137` | Transcript dedupe key ignores run/turn, so Gemini's repeating synthetic tool-call IDs drop every tool card after the first turn |
| high | tui-scroll-arch | `internal/tui/rendering.go:525` | cardBodyMaxLines=16 permanently hides long diffs and tool output with no expansion or scroll path |
| high | tui-interactions | `internal/tui/transcript.go:135` | Transcript dedup key ignores runID, silently dropping tool rows for providers with per-turn synthesized ToolCallIDs |
| high | tui-interactions | `internal/tui/model.go:512` | Cancelled-run event flush appends to whichever session is active at flush time, contaminating other sessions and breaking their /rewind |
| high | ops-background | `internal/cli/cron_run.go:173` | Cron store has no inter-process locking: paused/edited jobs are silently clobbered by the scheduler's stale read-modify-write, and concurrent schedulers double-fire |
| high | tui-core | `internal/tui/run.go:26` | Inline renderer silently discards transcript taller than the terminal — history is unrecoverable |
| high | tui-core | `internal/tui/transcript.go:139` | Transcript dedup key ignores runID: repeated provider tool-call IDs (Gemini gemini_tool_N) silently drop later tool cards |
| high | tui-core | `internal/tui/model.go:311` | Ctrl+C after an Esc-cancel quits immediately and drops the pending checkpoint flush |
| high | tui-core | `internal/tui/model.go:853` | /exit quits immediately during an in-flight run or pending flush, orphaning checkpoint session events |
| high | tools-sandbox | `internal/tools/bash.go:104` | bash tool timeout does not kill the command when a child inherits the output pipes |
| high | tools-sandbox | `internal/sandbox/runner.go:262` | macOS sandbox-exec profile blocks /tmp and /dev/null, breaking most wrapped shell commands |
| high | tools-sandbox | `internal/tools/apply_patch.go:161` | apply_patch misparses unified-diff body lines starting with '--- '/'+++ ' as file paths |
| high | tools-sandbox | `internal/tools/grep.go:224` | grep glob filter is applied to root-relative paths, silently dropping matches under a subdirectory search |
| high | runtime-providers | `internal/providers/openai/types.go:3` | Reasoning effort is modeled, validated, and surfaced everywhere but never serialized into any provider request |
| high | specialist-verify | `internal/specialist/exec.go:166` | Specialist sessions launched without a description are unresumable (AgentName never recorded / recorded as garbage) |
| high | agent-loop | `internal/agent/loop.go:233` | Truncated/filtered responses (FinishReason) are produced by every provider but never consumed by the agent loop |
| high | cli-commands | `internal/cli/cron.go:112` | cron add silently ignores explicit cron expression when --recipe is also given |
| high | cli-commands | `internal/cli/usage.go:121` | zero usage hard-fails outside a git repository (entire token report aborted by the net-LOC helper) |
| high | config-plugins | `internal/hooks/hooks.go:372` | Hooks subsystem is loaded and listed but never executed (no event dispatch, no edit command) |
| high | config-plugins | `internal/cli/extensions.go:186` | `zero mcp list --json` leaks MCP server env/header secret values instead of using the redacting MCPServerSnapshot |
| medium | wiring-parity | `internal/cli/exec_sessions.go:114` | exec session recorder swallows persistence errors — first failed AppendEvent silently disables all session recording |
| medium | wiring-parity | `internal/tui/session.go:190` | ask_user events are persisted but dropped on /resume rehydration — questionnaire history lost |
| medium | wiring-parity | `internal/tui/session_controls.go:117` | /style stores a preference nothing consumes; /theme, /input-style and /compact are backend-less stubs listed in /help and autocomplete |
| medium | wiring-parity | `internal/tui/session_controls.go:42` | /effort (TUI) and --reasoning-effort (exec) never reach the provider request — effort is validated, stored, and ignored |
| medium | wiring-parity | `internal/tui/model.go:1067` | Cancelling a run (Esc/Ctrl+C) leaves no visible marker in the live transcript despite code comments claiming one is written |
| medium | wiring-parity | `internal/tui/transcript.go:113` | appendTranscriptRow copies the whole transcript and re-scans it for dedupe on every append — O(n²) in the streaming hot path |
| medium | cli-core | `internal/cli/app.go:149` | `zero --skip-permissions-unsafe` silently discards all trailing arguments |
| medium | cli-core | `internal/cli/exec.go:500` | Ctrl+C with `-o json` ends the JSON event stream with no error/done terminal event |
| medium | cli-core | `internal/cli/exec.go:182` | `zero exec --list-tools -o json` ignores the requested JSON format and prints plain text |
| medium | cli-core | `internal/tui/run.go:41` | TUI program errors are swallowed: exit code 1 with zero diagnostics on the default chat path |
| medium | mcp-serve | `internal/mcp/client.go:302` | Client readLoop misroutes server-to-client requests as responses (id collision) and never answers ping |
| medium | mcp-serve | `internal/mcp/client.go:246` | Stdio request write blocks indefinitely while holding client.mu and ignores ctx cancellation |
| medium | mcp-serve | `internal/mcp/client.go:98` | Child stderr captured into an unbounded bytes.Buffer for the entire server lifetime |
| medium | mcp-serve | `internal/mcp/network_client.go:621` | Streamable-HTTP SSE response decoding takes the first event instead of the matching response |
| medium | mcp-serve | `internal/mcp/network_client.go:637` | 1 MiB SSE scanner token cap permanently kills the SSE session on large messages |
| medium | leaf-packages | `internal/secrets/scanner.go:81` | secrets.Redact leaves a private key un-redacted when an inner pattern matches inside it |
| medium | leaf-packages | `internal/search/search.go:345` | Search match offsets computed on ToLower'd text are applied to the original text |
| medium | leaf-packages | `internal/search/search.go:108` | One corrupt session aborts the entire /search and `zero search` surface |
| medium | leaf-packages | `internal/redaction/redaction.go:362` | redactURLPasswords recompiles its regexp on every RedactString call (hot path) |
| medium | leaf-packages | `internal/zerogit/zerogit.go:212` | zerogit parseStatus keeps git's C-quoted paths and combined rename strings verbatim |
| medium | leaf-packages | `internal/worktrees/worktrees.go:247` | worktrees defaultRunGit mixes stderr into parsed stdout and never fills CommandResult.Stderr |
| medium | tests-hygiene | `.github/workflows/ci.yml:31` | 10 files fail gofmt and CI has no gofmt/go vet gate to catch them |
| medium | tests-hygiene | `internal/tui/transcript.go:303` | truncateTUIOutput byte-slices mid-rune, emitting invalid UTF-8 into transcript rows; the only covering test is ASCII-only |
| medium | sessions-usage | `internal/sessions/store.go:632` | No fsync before rename/append: metadata.json (rewritten on every event) can be empty after a crash, silently hiding the session |
| medium | sessions-usage | `internal/tui/session_controls.go:235` | TUI '/rewind latest' keeps the dangling tool_call event of the mutation it just undid |
| medium | sessions-usage | `internal/sessions/store.go:387` | Fork duplicates provider_usage events and rewrites their timestamps, double-counting and re-dating usage in `zero usage report` |
| medium | sessions-usage | `internal/cli/usage.go:46` | One corrupt session aborts the entire `zero usage report` |
| medium | sessions-usage | `internal/usage/report.go:17` | Per-event 'model' field persisted under --allow-escalation is never read by the usage report, mispricing escalated runs |
| medium | sessions-usage | `internal/cli/exec_sessions.go:115` | execSessionRecorder latches the first persist failure, silently drops all later events, and the error is never surfaced |
| medium | sessions-usage | `internal/tui/transcript.go:113` | appendTranscriptRow is O(n) copy + O(n) dedupe scan per row, making transcript growth and session rehydration O(n^2) |
| medium | tui-scroll-arch | `internal/tui/model.go:569` | Assistant text streamed before tool calls is discarded — vanishes from the transcript and the session log |
| medium | tui-scroll-arch | `internal/tui/model.go:1081` | Esc/Ctrl+C cancellation leaves no visible marker in the live transcript |
| medium | tui-scroll-arch | `internal/tui/model.go:227` | Composer never sets textinput.Width, so input longer than the terminal is truncated with the cursor hidden — blind typing |
| medium | tui-scroll-arch | `internal/tui/transcript.go:117` | Full-transcript re-render on every message plus O(n²) append path |
| medium | tui-scroll-arch | `internal/tui/session_controls.go:117` | /style sets responseStyle that nothing ever reads — the command silently has no effect |
| medium | tui-scroll-arch | `internal/tui/view.go:394` | looksLikeDiff false-positives on any output containing a line starting with '---', breaking bash/generic cards |
| medium | tui-scroll-arch | `internal/tui/rendering.go:261` | wrapPlainText collapses all internal whitespace, destroying code indentation in assistant answers and streamed text |
| medium | tui-scroll-arch | `internal/tui/model.go:456` | Resize is unhandled beyond storing width/height: shrink garbles the whole surface, and the first frame renders at a hardcoded 96 cols |
| medium | tui-interactions | `internal/tui/session_controls.go:176` | /rewind allowed while a cancelled run's flush is outstanding — prunes the cancelled run's checkpoint blobs and re-appends rewound-away events |
| medium | tui-interactions | `internal/tui/session.go:190` | ask_user exchanges vanish on rehydration and user answers are never persisted |
| medium | tui-interactions | `internal/tui/model.go:853` | /exit during an in-flight run quits without cancelling or flushing, orphaning checkpoints; /spec can start a run while exiting |
| medium | tui-interactions | `internal/tui/transcript.go:113` | appendTranscriptRow is O(N) copy + O(N) dedup scan per row — O(N²) on /resume rehydration and long runs |
| medium | ops-background | `internal/cron/schedule.go:171` | Cron Next double-fires wall-clock schedules on DST fall-back (repeated hour) |
| medium | ops-background | `internal/cli/cron.go:112` | `zero cron add <expr> --recipe R` silently discards the user's cron expression in favor of the recipe's |
| medium | ops-background | `internal/background/process_posix.go:50` | POSIX background-task kill terminates only the leader PID; the specialist's subprocess tree leaks on SIGKILL escalation (Windows kills the whole tree) |
| medium | ops-background | `internal/background/manager.go:462` | Starting a second zero process clobbers a live sibling's running background-task metadata to error/PID=0 on disk |
| medium | tui-core | `internal/tui/spec_mode.go:22` | /spec can start a new run while the app is exiting, which the deferred tea.Quit then kills mid-flight |
| medium | tui-core | `internal/tui/model.go:1081` | Esc-cancelling a run leaves zero feedback in the live transcript (the 'Run cancelled.' marker only goes to the session log) |
| medium | tui-core | `internal/tui/transcript.go:117` | appendTranscriptRow copies the whole transcript and rescans it for dedup on every append — O(n²) |
| medium | tui-core | `internal/tui/model.go:610` | Full transcript re-rendered from scratch every frame (every spinner tick and stream delta), including per-line regex parsing |
| medium | tui-core | `internal/tui/session.go:93` | UTF-8 strings byte-sliced at fixed limits: session titles and tool-result text can be cut mid-rune |
| medium | tui-core | `internal/tui/model.go:227` | Composer textinput has no Width set: typing beyond the terminal width is clipped invisibly instead of scrolling |
| medium | tui-core | `internal/tui/session_controls.go:117` | /style sets m.responseStyle but nothing ever reads it — the command is a silent no-op |
| medium | tui-core | `internal/tui/rendering.go:261` | wrapPlainText collapses all whitespace via strings.Fields — assistant answers lose code indentation and alignment |
| medium | tools-sandbox | `internal/tools/registry.go:224` | escalate_model tool is implemented and registry-aware but never included in any Core* tool set |
| medium | tools-sandbox | `internal/sandbox/safe_command.go:138` | Interactive-command detector flags pager/REPL names that appear inside quoted arguments |
| medium | runtime-providers | `internal/providers/providerio/providerio.go:184` | SSE comment keep-alives never reset the 90s idle watchdog, so heartbeating upstreams are aborted as idle |
| medium | runtime-providers | `internal/modelregistry/catalog.go:27` | Registry caps gpt-4.1 output at 16,384 tokens; the API max is 32,768 (the file itself uses 32,768 for mini/nano) |
| medium | runtime-providers | `internal/providers/gemini/types.go:88` | Gemini adapter never reads cachedContentTokenCount, so CachedInputTokens is always 0 and the catalog's Gemini cached pricing is unreachable |
| medium | runtime-providers | `internal/providers/openai/tool_state.go:95` | OpenAI adapter drops fully-formed tool calls that lack an id instead of synthesizing one; zeroruntime's empty-ID collector support is unreachable |
| medium | specialist-verify | `internal/specialist/exec.go:150` | Specialist children always run with --auto high (PermissionModeUnsafe): one Task approval silently grants unprompted shell/write access |
| medium | specialist-verify | `internal/specialist/accounting.go:72` | Race: duplicate specialist stop/usage accounting events — check-then-append dedupe is not atomic and runs concurrently from onExit and TaskOutput |
| medium | specialist-verify | `internal/specialist/streamer.go:43` | Foreground specialist run fails entirely when any child stream-json line exceeds 1 MiB (untruncated tool_call args) |
| medium | agent-loop | `internal/agent/loop.go:468` | "Always allow" permission decision is converted into a denial when grant persistence fails (always, when Options.Sandbox is nil) |
| medium | agent-loop | `internal/agent/compaction.go:58` | estimateTokens ignores image attachments (and the compaction trigger ignores tool definitions), so the context budget undercounts |
| medium | agent-loop | `internal/agent/compaction.go:376` | Compaction summarizer provider calls bypass OnUsage — their token spend is invisible to usage accounting |
| medium | cli-commands | `internal/update/update.go:135` | zero update --check always fails on source/dev builds: "invalid semantic version: dev" |
| medium | cli-commands | `internal/cli/cron_run.go:115` | cron run start-up reconcile silently cancels --run-now jobs |
| medium | cli-commands | `internal/sessions/lineage.go:140` | zero sessions tree is O(nodes x sessions) disk reads (full store re-list per tree node) |
| medium | config-plugins | `internal/config/resolver.go:95` | ResolvedConfig.MCP is computed but never read; provider-command MCP servers are silently dropped |
| medium | config-plugins | `internal/hooks/hooks.go:503` | AuditStore.append is O(n^2): it re-reads and re-parses the entire audit JSONL on every append |
| low | wiring-parity | `internal/tui/options.go:28` | tui.Options.UsageTracker, ReasoningEffort, and ResponseStyle are read by the TUI but never set by any production caller |
| low | wiring-parity | `internal/tui/transcript.go:303` | Byte-indexed truncation of UTF-8 strings can split multi-byte runes (tool output, session titles) |
| low | wiring-parity | `go.mod:10` | Dead direct dependencies: glamour (and x/ansi, termenv) declared in go.mod but imported nowhere |
| low | wiring-parity | `internal/tui/rendering.go:62` | Dead production code in the TUI: footer/help helpers, unused transcript actions, unused theme tokens, dead resumeText branch, and the never-wired OnContext callback |
| low | wiring-parity | `internal/tui/model.go:525` | Completed runs never invoke their context cancel function — CancelFunc leak per prompt |
| low | cli-core | `internal/cli/exec.go:353` | Exec path bypasses injected session store and clock (deps seam silently ignored) |
| low | cli-core | `internal/cli/exec_sessions.go:88` | Session title truncation slices UTF-8 mid-rune |
| low | cli-core | `internal/cli/exec_writer.go:412` | Stream-json and stderr tool-output truncation split runes mid-sequence |
| low | cli-core | `internal/zerocommands/contracts.go:166` | Providers list/current reports "api key: not set" for profiles authenticated via --auth-header-value |
| low | mcp-serve | `internal/cli/exec_writer.go:412` | Stream-json/status truncation slices strings mid-rune, emitting invalid UTF-8 |
| low | mcp-serve | `internal/streamjson/streamjson.go:30` | streamjson.EventRestore is declared but never emitted |
| low | mcp-serve | `internal/mcp/server.go:129` | MCP server answers "ping" with method-not-found instead of an empty result |
| low | leaf-packages | `internal/secrets/scanner.go:35` | Secret scanner misses modern OpenAI key formats (sk-proj-, sk-svcacct-, keys with -/_) |
| low | leaf-packages | `internal/redaction/redaction.go:222` | RedactValue reports shared (non-circular) pointers/maps as "[Circular]", dropping data |
| low | leaf-packages | `internal/redaction/redaction.go:174` | RedactError stack-trace support is dead code (interface matches nothing) |
| low | leaf-packages | `internal/zerogit/zerogit.go:206` | Commit subject length/truncation is byte-based: rejects valid non-ASCII subjects and can emit invalid UTF-8 |
| low | leaf-packages | `internal/zerogit/zerogit.go:417` | zerogit resolveRunners silently drops env for caller-supplied runners — temp-index isolation lost |
| low | leaf-packages | `internal/imageinput/imageinput.go:58` | imageinput reports every open/read failure as "image file not found" |
| low | tests-hygiene | `internal/tui/session.go:93` | Session titles byte-truncated at 80 can split a UTF-8 rune and persist invalid UTF-8 into session metadata (TUI, spec mode, and exec) |
| low | tests-hygiene | `internal/cli/exec_writer.go:412` | stream-json and status-line output truncation byte-slices mid-rune, corrupting tool output in the machine-readable protocol |
| low | tests-hygiene | `internal/cli/cron.go:275` | cron prompt/error truncation helpers byte-slice mid-rune |
| low | tests-hygiene | `internal/tui/commands_test.go:46` | Dead test helper: commandTestStringSliceContains is defined but never called |
| low | tests-hygiene | `internal/tui/session_test.go:215` | Dead test state: runtimeMessages slice is appended from the agent goroutine but never read |
| low | sessions-usage | `internal/sessions/replay.go:237` | Naive byte-slice truncation can split multi-byte UTF-8 runes in prompts and titles |
| low | sessions-usage | `internal/sessions/store.go:555` | Second-granularity RFC3339 timestamps make Latest()/list ordering pick the wrong session on same-second ties |
| low | tui-scroll-arch | `internal/tui/rendering.go:68` | Dead footer/help rendering code and dead message fields |
| low | tui-scroll-arch | `internal/tui/transcript.go:303` | Byte-index truncation can split UTF-8 runes in session titles and tool-result row text |
| low | tui-scroll-arch | `internal/tui/model.go:853` | /exit during the Ctrl+C checkpoint-flush wait quits immediately, orphaning the checkpoints the deferred quit exists to protect |
| low | tui-interactions | `internal/tui/command_center.go:44` | Dead code: unreachable resumeText branch, never-rendered footer helpers, and stale comments referencing a footer that no longer exists |
| low | tui-interactions | `internal/tui/session.go:93` | Byte-index truncation can split multibyte runes in session titles and tool-result text |
| low | tui-interactions | `internal/tui/session_controls.go:165` | /compact request state is write-only — nothing consumes it |
| low | tui-interactions | `internal/tui/model.go:837` | After Ctrl+C with a hung run the UI is silently inert: no exiting indicator, prompts refused without feedback |
| low | tui-interactions | `internal/tui/rendering.go:482` | Permission card advertises an unlabeled [esc] action whose actual effect is cancelling the entire run |
| low | ops-background | `internal/cli/update.go:37` | `zero update --check` always fails on non-release builds: version "dev" is rejected before the network call |
| low | ops-background | `internal/tui/command_center.go:17` | TUI /doctor omits config paths, so config.files and config.validation always warn in the TUI while the CLI surface passes them |
| low | ops-background | `internal/cli/cron.go:275` | Byte-indexed truncation in promptExcerpt/cronTruncate splits multi-byte UTF-8 runes |
| low | ops-background | `internal/notify/notify.go:134` | notify.sanitizeMessage passes C1 control characters (U+0080–U+009F) into the OSC-9 escape it is meant to protect |
| low | ops-background | `internal/background/process_posix.go:38` | terminateProcess grace-period polling probes a possibly-reaped PID and can SIGKILL a recycled PID |
| low | tui-core | `internal/tui/model.go:512` | Cancelled runs' usage events are flushed to the session log but never recorded in the usage tracker |
| low | tui-core | `internal/tui/rendering.go:490` | renderFocusedAskUserPrompt receives the live input value but never renders it |
| low | tui-core | `internal/tui/rendering.go:62` | Dead production code: legacy footer/help builders and four theme styles are never used outside tests |
| low | tui-core | `internal/tui/view.go:227` | shortenPath matches the home directory as a bare string prefix, mangling sibling paths |
| low | tools-sandbox | `internal/tools/registry.go:108` | OnSandboxDecision callback option is set up to fire but is never wired by any caller (dead code) |
| low | tools-sandbox | `internal/sandbox/risk.go:53` | Destructive/network/installer classification recompiles N regexes over the full command on every shell call |
| low | tools-sandbox | `internal/sandbox/grants.go:146` | Grant store prefix/substring safety relies on exact-key map but Lookup trims while writer key may not match normalized name |
| low | tools-sandbox | `internal/tools/read_file.go:89` | read_file rune-width line numbering can misalign and read_file returns Truncated but no truncation marker in output |
| low | runtime-providers | `internal/modelregistry/catalog.go:36` | claude-haiku-3.5 is flagged vision-capable, but claude-3-5-haiku-20241022 does not support image input |
| low | runtime-providers | `internal/providers/factory.go:118` | isImplicitOpenAI is provably unreachable at its only call site (dead condition in profile resolution) |
| low | runtime-providers | `internal/providercatalog/catalog.go:46` | Descriptor.Public is declared but never written or read anywhere |
| low | runtime-providers | `internal/providers/providerio/retry.go:107` | HTTP 529 (Anthropic overloaded) is classified as a rate-limit error but excluded from the retry policy that retries 429/503 |
| low | specialist-verify | `internal/verify/verify.go:215` | verify.RunLoop / LoopOptions / LoopReport are dead code — production retry loop is reimplemented in selfverify |
| low | specialist-verify | `internal/specialist/output_tool.go:244` | formatTaskOutput is dead code |
| low | specialist-verify | `internal/specialist/exec.go:346` | SetPID failure path deletes the prompt file out from under a running child and records no terminal accounting/status |
| low | specialist-verify | `internal/tui/spec_mode.go:301` | Spec/session titles are truncated by byte index, splitting multi-byte UTF-8 runes |
| low | agent-loop | `internal/agent/loop.go:188` | Mid-stream context cancellation is returned as a flattened errors.New string; the ctx.Err() identity check is unreachable for that path |
| low | agent-loop | `internal/agent/types.go:183` | OnContext / MeasureContext context-utilization pipeline has no consumer on any surface |
| low | agent-loop | `internal/agent/loop.go:1072` | Duplicate items-schema assignment in propertyToRuntimeMap |
| low | agent-loop | `internal/agent/loop.go:473` | Dead stores to requestEvent after always-allow, and unreachable fallbackPermissionEvent |
| low | agent-loop | `internal/agent/system_prompt.go:89` | Project-guidelines truncation can split a multibyte UTF-8 rune in the system prompt |
| low | agent-loop | `internal/agent/loop.go:183` | Reactive mid-stream retry never forwards the retried assistant text to OnText, so streaming surfaces show the aborted partial text instead |
| low | agent-loop | `internal/zeroruntime/helpers.go:104` | Streamed text accumulation is O(n^2): collected.Text += delta copies the whole buffer on every text event |
| low | cli-commands | `internal/cli/cron_run.go:150` | cron run records lose the failure reason: exec errors go to the discarded stdout stream |
| low | cli-commands | `internal/cli/cron.go:275` | promptExcerpt/cronTruncate byte-slice strings mid-rune, emitting invalid UTF-8 |
| low | cli-commands | `internal/cli/exec_sessions.go:55` | exec resume/fork bypasses the injected session store (deps.newSessionStore never used by exec) |
| low | cli-commands | `internal/search/search.go:309` | search --session-id with a nonexistent session silently succeeds with 0 results |
| low | cli-commands | `internal/doctor/doctor.go:134` | doctor always prints apiKey: [REDACTED] whether or not a key is configured |
| low | config-plugins | `internal/zerocommands/sandbox_snapshots.go:104` | zerocommands snapshot library is largely dead: sandbox plan/decision/policy + hook/plugin/MCP snapshots have no production consumer |
| low | config-plugins | `internal/config/contracts.go:12` | internal/config/contracts.go (ContractGap API) is unused dead code |
| low | config-plugins | `internal/skills/skills.go:91` | skills.Duplicates is never surfaced and skills.Get is unused; duplicate-name collisions are silently dropped |
| low | config-plugins | `internal/plugins/plugins.go:893` | Plugin and standalone hook validators disagree on the allowed hook event set |
| low | config-plugins | `internal/config/resolver.go:136` | config maxTurns <= 0 is silently ignored rather than validated |
| low | config-plugins | `internal/config/writer.go:53` | config writer performs a non-atomic in-place write of the user config |

## Findings by area

### Cross-module wiring parity

Cross-module wiring audit of the zero CLI/TUI. (1) tui.Options: all fields set in internal/cli/app.go runInteractiveTUI EXCEPT UsageTracker, ReasoningEffort, and ResponseStyle, which are read by newModel but set by no production caller (read-but-never-set; RuntimeMessageSink is legitimately wired inside tui.Run). (2) Slash commands: /theme and /input-style verified as stubs rendering "does not have a backend setting yet"; /compact only increments a counter ("Backend: state: pending integration"); /style stores a preference nothing consumes; /effort is stored and passed to agent.Options but never reaches the provider request (exec.go documents the same gap for --reasoning-effort). Help text/autocomplete are generated from the same commandDefinitions table so those surfaces are internally consistent. (3) Session events: ask_user records are persisted as role-"ask_user" EventMessage but dropped on /resume rehydration (no "content" key), and EventSessionRewind/EventCompaction have no rehydration arms (EventCompaction has no writers at all — agent-loop compaction is in-memory only). (4) agent.Options callbacks: TUI and exec wire equivalent OnText/OnToolCall/OnToolResult/OnPermission/OnUsage; exec intentionally omits OnAskUser/OnPermissionRequest; OnContext is wired by neither (dead callback); exec's session recorder swallows AppendEvent errors (set-but-never-read err field). (5) zeroline removal is clean: the only remaining references are in docs/design/ZERO_LIME_TUI_REBUILD_PROMPT.md (the rebuild instructions themselves); no help text, scripts, .github, README, or Go code references survive — though stale TUI comments still reference the removed "footer". (6) go.mod: glamour is confirmed dead (direct dep, zero imports); charmbracelet/x/ansi and muesli/termenv are also direct-but-unimported (tidy would demote them). Highest-impact concrete bugs found along the traces: the transcript dedupe key omits runID so Gemini's per-turn-reset synthetic tool IDs cause later tool cards to be silently dropped; /exit mid-run quits without the Ctrl+C checkpoint-flush machinery, orphaning checkpoint blobs and losing the run's session events; cancelled runs show no visible "Run cancelled." marker despite comments claiming one; appendTranscriptRow is O(n²) (full copy + full dedupe scan per streamed row); several byte-indexed truncations can split UTF-8 runes.

#### [high/bug] Transcript dedupe key omits runID — repeated provider tool-call IDs silently drop later tool rows
`internal/tui/transcript.go:137`

appendTranscriptRow dedupes rows via transcriptRowKey, which for rowToolCall/rowToolResult is keyed on kind+id only, with no run scoping. The render-side context (rowContext) was explicitly fixed to key on runID+id because providers synthesize ToolCallIDs that repeat (rendering.go:124-127: "some providers synthesize ToolCallIDs that repeat across turns (e.g. Gemini's gemini_tool_N)"), and the Gemini provider does exactly that: internal/providers/gemini/provider.go:147 creates a fresh streamState per request, so syntheticToolIndex resets to 1 every turn and emits gemini_tool_1, gemini_tool_2, ... again. Result: on Gemini (or any provider without native IDs), the SECOND turn's/run's tool-call and tool-result rows arriving via agentRowMsg are classified as duplicates of the first turn's rows and never appended — the user sees no card for those tool executions. Rehydrated rows (runID 0) collide with live rows the same way after /resume.

```
transcript.go:136-140: `case rowToolCall, rowToolResult:\n\tif row.id != \"\" {\n\t\treturn fmt.Sprintf(\"%d:%s\", row.kind, row.id)\n\t}`  — no runID, although transcriptRow carries one (transcript.go:34 `runID int`). gemini/provider.go:269-271: `if id == \"\" { id = fmt.Sprintf(\"gemini_tool_%d\", syntheticIndex) }` with `state := streamState{}` per request (line 147). rendering.go:135 already scopes rcKey: `return strconv.Itoa(runID) + \":\" + id`.
```

**Suggested fix:** Include the run scope in the dedupe key, mirroring rcKey: `return fmt.Sprintf("%d:%d:%s", row.kind, row.runID, row.id)` (and similarly add runID to the rowPermission/rowAskUser key arms). Within-run dedupe between live agentRowMsg rows and the final agentResponseMsg re-append still works because both carry the same runID.

#### [high/bug] /exit during an in-flight run quits immediately, orphaning checkpoint blobs and dropping the run's session events
`internal/tui/model.go:853`

The Ctrl+C handler (model.go:294-314) goes to great lengths to defer tea.Quit until a cancelled run's final agentResponseMsg has been flushed, because the run's tool_call/tool_result/EventSessionCheckpoint events are only persisted at run end while SnapshotForCheckpoint has ALREADY written the blobs to disk — quitting early "would drop that message, orphaning the checkpoints and breaking /rewind" (the code's own words). But the /exit (alias /quit) slash command is reachable while a run is pending (handleSubmit only blocks commandPrompt when m.pending) and returns tea.Quit immediately without calling cancelRun() or waiting on flushRunIDs. Typing /exit mid-run therefore loses every session event the run accumulated (tool calls, results, permission events, usage) and leaves the already-written checkpoint blobs unreferenced on disk — exactly the loss the flushRunIDs machinery exists to prevent.

```
model.go:853-855: `case commandExit:\n\tm.exiting = true\n\treturn m, tea.Quit` — versus the Ctrl+C path at model.go:305-314 which sets pendingFlush, calls m.cancelRun(), and returns `m, nil` until the flush lands (model.go:519-521 fires the deferred quit). handleSubmit's pending gate at model.go:837 covers only `command.kind == commandPrompt`.
```

**Suggested fix:** In the commandExit case, mirror the Ctrl+C path: if m.pending && m.activeRunID != 0, call m.cancelRun(), set m.exiting = true, and return (m, nil) so the agentResponseMsg flush handler fires the deferred tea.Quit; only quit immediately when no run is in flight.

#### [medium/wiring] exec session recorder swallows persistence errors — first failed AppendEvent silently disables all session recording
`internal/cli/exec_sessions.go:114`

execSessionRecorder.append latches the first AppendEvent error into recorder.err and then skips every subsequent append, but no caller ever reads recorder.err: runExec (internal/cli/exec.go:405) and runExecSpecDraft (exec_spec.go:87) construct the recorder and never check it, and nothing is written to stderr or the stream-json channel. A transient disk/lock failure early in a --resume/stream-json run therefore silently stops all session persistence — the run still reports its sessionID as resumable, but the history (messages, tool calls, checkpoints' referencing events) is missing, and the user is never told. This is a set-but-never-read field producing silent history loss.

```
exec_sessions.go:114-121: `func (recorder *execSessionRecorder) append(...) {\n\tif recorder.err != nil || recorder.prepared.Store == nil || ... { return }\n\t_, recorder.err = recorder.prepared.Store.AppendEvent(...)\n}` — grep for `recorder.err`/`sessionRecorder.err` shows no other reads anywhere in internal/cli.
```

**Suggested fix:** Surface the failure: on the first error, emit writer.warning("session recording failed: "+err.Error()) (stderr for text mode, warning event for stream-json), and/or check sessionRecorder.err once after agent.Run returns and report it before exiting.

#### [medium/wiring] ask_user events are persisted but dropped on /resume rehydration — questionnaire history lost
`internal/tui/session.go:190`

The TUI persists each ask_user questionnaire as an EventMessage whose payload has role "ask_user", a toolCallId, header, and a questions array (model.go:1197-1200 via askUserSessionPayload). On resume, transcriptRowsFromSessionEvents handles EventMessage by reading payload "content" and skipping the event when it is empty — the ask_user payload has no content key, so every persisted ask_user record is silently dropped from the rehydrated transcript. The dedupe code even anticipates rehydrated ask_user rows (transcript.go:146-148: "Prefer row.id ... it survives rehydration even when row.askUser is nil, so a reloaded ask_user row still dedupes correctly"), but the rehydration path never constructs them. The same payload also yields an empty/garbled line in FormatExecPrompt context, but the user-visible loss is the missing transcript row after /resume.

```
session.go:190-193: `case sessions.EventMessage:\n\tcontent := payloadString(payload, \"content\")\n\tif content == \"\" {\n\t\tcontinue\n\t}` — while transcript.go:202-206 writes `payload := map[string]any{\"role\": \"ask_user\", \"toolCallId\": ..., \"questions\": ...}` with no "content" key.
```

**Suggested fix:** In transcriptRowsFromSessionEvents, before the content check, branch on `payloadString(payload, "role") == "ask_user"` and rebuild a rowAskUser transcriptRow (id from toolCallId, text from header/len(questions), detail from the questions array), mirroring askUserTranscriptRow.

#### [medium/ux] /style stores a preference nothing consumes; /theme, /input-style and /compact are backend-less stubs listed in /help and autocomplete
`internal/tui/session_controls.go:117`

Four registered slash commands have no effect on agent behavior. (1) /style validates and stores m.responseStyle and reports "Style preference is stored for this TUI session", but the only readers are the /style and /context display strings — runAgentWithOptions never threads it into agent.Options (which has no style field), so the response style provably changes nothing about the model's output. (2) /theme and /input-style (commands.go:201-214) resolve to shellOnlyCommandText: "This control is available in the TUI but does not have a backend setting yet." — verified stubs (the old zeroline skin switcher was removed; these survived). (3) /compact with no argument only increments m.compactRequests and prints "Backend: state: pending integration" (session_controls.go:157-167, 244-268); it never triggers the real compaction that exists in internal/agent/compaction.go. All four appear in /help and the autocomplete palette as if functional.

```
session_controls.go:117-122: `m.responseStyle = args ... \"Style preference is stored for this TUI session.\"`; grep shows m.responseStyle read only at command_views.go:155 and session_controls.go:120/132 (display). rendering.go:46-56: shellOnlyCommandText returns "...does not have a backend setting yet." wired from model.go:966-977. session_controls.go:165-166: `m.compactRequests++` then compactText prints `{Title: \"Backend\", Lines: []string{\"state: pending integration\"}}`.
```

**Suggested fix:** Wire /style into the run path (e.g. append a style directive to agent.Options.SystemPrompt in runAgentWithOptions) or label it a display-only preference; either implement /theme//input-style//compact backends or remove them from commandDefinitions so help/autocomplete stop advertising no-ops.

#### [medium/wiring] /effort (TUI) and --reasoning-effort (exec) never reach the provider request — effort is validated, stored, and ignored
`internal/tui/session_controls.go:42`

The TUI /effort command validates the value against the model's supported efforts and tells the user "Reasoning effort preference is stored for this TUI session"; runAgentWithOptions passes it into agent.Options.ReasoningEffort. But the agent loop forwards ReasoningEffort only into tools.RunOptions (for child/specialist runs) and never into the completion request — the zeroruntime request schema has no effort field, as the exec code itself documents: "NOTE: the effective effort is not yet forwarded to the provider request" (exec.go:848-851). So both surfaces accept, validate, and display an effort setting that has zero effect on the actual model reasoning of the current run. The /mode command's effort component is equally inert.

```
loop.go:489 and loop.go:644 are the only consumers: `ReasoningEffort:   options.ReasoningEffort,` inside tools.RunOptions. exec.go:848-851: "NOTE: the effective effort is not yet forwarded to the provider request — the zeroruntime.CompletionRequest / provider wire schemas carry no effort field." session_controls.go:43-48 tells the user the preference is stored for the session.
```

**Suggested fix:** Add a ReasoningEffort field to zeroruntime.CompletionRequest and map it per provider (OpenAI reasoning_effort, Anthropic thinking budget, Gemini thinkingConfig), or change the /effort//mode/exec advisory text to state the value currently only affects spawned child runs.

#### [medium/ux] Cancelling a run (Esc/Ctrl+C) leaves no visible marker in the live transcript despite code comments claiming one is written
`internal/tui/model.go:1067`

cancelRun records a "Run cancelled." EventError into the SESSION store only; it never appends a transcript row or system note. The streamingText interim block is cleared, the spinner stops, and the cancelled goroutine's trailing rows/error are deliberately skipped in the flush path (model.go:498-523) — so on screen the partial answer simply vanishes with no indication the run was interrupted. Multiple comments assert the opposite ("cancelRun ... writes the 'Run cancelled.' marker", "the cancel path already wrote the 'Run cancelled.' marker"), but the marker only becomes visible after a later /resume rehydrates the session's EventError. As a side effect of the same code, the persisted log records the cancellation error BEFORE the cancelled run's flushed tool/checkpoint events, so resumed transcripts show "Run cancelled." above the tool activity it cancelled.

```
model.go:1081-1087: `if m.pending && m.activeSession.SessionID != \"\" { if next, err := (*m).appendSessionEvent(sessions.EventError, map[string]any{\"message\": \"Run cancelled.\"}); ... }` — no reduceTranscript/appendTranscriptRow call anywhere in cancelRun or the KeyEsc handler (model.go:315-340). Comment at model.go:295-296: "cancelRun records the in-flight run into flushRunIDs and writes the 'Run cancelled.' marker, exactly like the Esc path."
```

**Suggested fix:** In cancelRun, also append a visible row: `m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: "Run cancelled."})` (guarded by m.pending), and optionally defer the session EventError append into the flush handler so the persisted ordering matches execution.

#### [medium/perf] appendTranscriptRow copies the whole transcript and re-scans it for dedupe on every append — O(n²) in the streaming hot path
`internal/tui/transcript.go:113`

Every appended row (each agentRowMsg for tool calls/results, each permission event, plus the full msg.rows re-append at run end in the agentResponseMsg handler at model.go:543-545) (a) allocates and copies the entire transcript slice and (b) runs hasTranscriptRow, which computes transcriptRowKey for every existing row. Both are O(n) per append, so a long tool-heavy session degrades quadratically in CPU and allocation: at a few thousand rows, every streamed tool result costs thousands of key computations plus a full slice copy, inside the Bubble Tea Update loop where it directly delays rendering and input handling.

```
transcript.go:113-133: `func appendTranscriptRow(rows []transcriptRow, row transcriptRow) []transcriptRow {\n\tif hasTranscriptRow(rows, row) { return rows }\n\tnext := append([]transcriptRow{}, rows...)\n\tnext = append(next, row)\n\treturn next\n}` and `for _, existing := range rows { if transcriptRowKey(existing) == key { return true } }`; called per-message from model.go:572 (agentRowMsg) and in loops at model.go:534-545.
```

**Suggested fix:** Keep a `seen map[string]struct{}` of row keys on the model (updated alongside the transcript) so dedupe is O(1), and append in place (`append(rows, row)`) — the model is already passed by value through Update, so the defensive full copy is unnecessary; if copy-on-write is desired, rely on append's amortized growth instead of copying every time.

#### [low/wiring] tui.Options.UsageTracker, ReasoningEffort, and ResponseStyle are read by the TUI but never set by any production caller
`internal/tui/options.go:28`

tui.Options is constructed in exactly one production location (internal/cli/app.go:366-386, runInteractiveTUI), which sets Cwd/ProviderName/ModelName/ProviderProfile/Provider/NewProvider/Registry/SessionStore/SandboxStore/AgentOptions/PermissionMode/Notify but never UsageTracker, ReasoningEffort, or ResponseStyle. newModel reads all three (model.go:214, 258, 259), so in every real launch the TUI silently falls back to a fresh in-memory usage tracker, effort "auto", and style "balanced". The fields are exercised only by tests; there is no config or flag path that can ever reach them, so a user cannot pre-seed effort/style for the interactive shell. RuntimeMessageSink is also unset by the CLI but is legitimately wired inside tui.Run (run.go:15-24).

```
options.go:28-33 declares `UsageTracker *usage.Tracker`, `ReasoningEffort modelregistry.ReasoningEffort`, `ResponseStyle string`; app.go:366-386 is the only non-test `tui.Options{` literal (grep) and omits all three; model.go:214-217 falls back: `usageTracker := options.UsageTracker; if usageTracker == nil { usageTracker = usage.NewTracker(...) }`.
```

**Suggested fix:** Either wire them (e.g. pass a config-resolved default effort/style and a shared tracker from runInteractiveTUI) or delete the three Options fields and have newModel construct the defaults directly, so the API does not advertise configuration that can never arrive.

#### [low/bug] Byte-indexed truncation of UTF-8 strings can split multi-byte runes (tool output, session titles)
`internal/tui/transcript.go:303`

Several truncations slice strings at byte offsets: truncateTUIOutput cuts tool output at byte 240 (`output[:limit]`) for every tool-result row text; tuiSessionTitle cuts the session title at byte 80 (session.go:92-94); specImplementationTitle does the same (spec_mode.go:301-303); the CLI's createSessionTitle mirrors it (internal/cli/exec_sessions.go:88-91); sessions.summarizePayload cuts at byte 500 (exec_session.go:193-195). Any CJK/emoji/accented content at the boundary is split mid-rune, producing an invalid UTF-8 tail (\xef\xbf\xbd replacement glyphs in the transcript, malformed JSON-adjacent text in persisted titles/prompts). The codebase already has correct rune/width-aware helpers (truncateRunes, splitAtWidth) used elsewhere.

```
transcript.go:300-303: `if limit <= 0 || len(output) <= limit { return output }\nreturn output[:limit] + \" [truncated]\"`; session.go:92-94: `if len(title) > tuiSessionTitleLimit { title = title[:tuiSessionTitleLimit] }`.
```

**Suggested fix:** Use the existing rune-safe helper (truncateRunes) or `string([]rune(s)[:n])` at these sites; for the 500/240-byte payload previews, back up to a rune boundary with utf8.RuneStart before slicing.

#### [low/dead-code] Dead direct dependencies: glamour (and x/ansi, termenv) declared in go.mod but imported nowhere
`go.mod:10`

github.com/charmbracelet/glamour v1.0.0 is a direct requirement but no Go file in the repo imports it (grep over all *.go returns zero hits) — it drags the chroma/goldmark/bluemonday/gorilla-css tree into go.sum and the module graph for nothing. github.com/charmbracelet/x/ansi and github.com/muesli/termenv are likewise listed in the direct require block with zero imports (they are real transitive deps of lipgloss/bubbletea and would be re-marked `// indirect` by go mod tidy). The suspicion in the audit brief is confirmed: glamour is dead.

```
go.mod:8-15 direct block includes `github.com/charmbracelet/glamour v1.0.0`, `github.com/charmbracelet/x/ansi v0.11.6`, `github.com/muesli/termenv v0.16.0`; `grep -rl "charmbracelet/glamour\|charmbracelet/x/ansi\|muesli/termenv" --include=*.go .` returns no files; `go build ./...` succeeds without them being imported.
```

**Suggested fix:** Run `go mod tidy`: glamour (and its indirect tree) is removed entirely; x/ansi and termenv move to the indirect block.

#### [low/dead-code] Dead production code in the TUI: footer/help helpers, unused transcript actions, unused theme tokens, dead resumeText branch, and the never-wired OnContext callback
`internal/tui/rendering.go:62`

A cluster of production code is reachable only from tests: (1) the entire footer system — defaultCommandFooterText, commandFooterText, footerText, formatCommandFooterText, runState (rendering.go:23-118) — the Lime View renders statusLine instead, and stale comments still reference the removed footer (commands.go:238 'the footer advertises "! bash"', autocomplete.go:57 'the footer advertises "/ commands"'); (2) formatCommandHelpLines/listCommandNames (commands.go:281-305) — test-only; (3) transcript actions actionAppendToolCall/actionAppendToolResult (transcript.go:53-54, 82-99) — no production reducer call; (4) theme tokens selRow, statusOk, statusErr, line2, and the panel2 field (theme.go:67-79) — zero non-test references; (5) resumeText's args!="" branch (command_center.go:44-54) is unreachable — handleResumeCommand only calls resumeText(""); (6) agent.Options.OnContext / MeasureContext (agent/types.go:183, loop.go:101-102) is set by no production caller (neither TUI nor exec), so per-turn context breakdowns are computed for nobody.

```
grep: `footerText|commandFooterText|formatCommandHelpLines|listCommandNames|defaultCommandFooterText` match only rendering.go/commands.go definitions and *_test.go files; `zeroTheme.selRow|statusOk|statusErr|line2|panel2` → 0 non-test hits; `resumeText(` called only at session.go:104 with ""; `OnContext` set nowhere outside internal/agent and tests.
```

**Suggested fix:** Delete the dead helpers, actions, theme fields, and the unreachable resumeText branch; either wire OnContext into the TUI status line (it was designed for exactly that) or remove the callback; update the two stale footer comments.

#### [low/bug] Completed runs never invoke their context cancel function — CancelFunc leak per prompt
`internal/tui/model.go:525`

handleSubmit creates a child context per run (`runCtx, cancel := context.WithCancel(m.ctx)`, model.go:1056) and stores cancel in m.runCancel. On the cancel paths cancelRun() invokes it, but on NORMAL completion the agentResponseMsg handler just nils the field without calling it: every successfully completed turn leaks its CancelFunc, leaving the child context registered on m.ctx for the life of the process (go vet's lostcancel pattern). In a long interactive session this accumulates one leaked context per prompt.

```
model.go:525-527: `m.pending = false\n\tm.runCancel = nil\n\tm.activeRunID = 0` — m.runCancel is dropped without being invoked; contrast cancelRun (model.go:1068-1070) which calls `m.runCancel()` first.
```

**Suggested fix:** In the active-run agentResponseMsg branch, call `if m.runCancel != nil { m.runCancel() }` before setting it to nil (cancelling an already-finished run's context is a harmless no-op).


### CLI core wiring

Audited the zero CLI entry wiring (cmd/zero/main.go, internal/cli/app.go, the exec path: exec.go/exec_parse.go/exec_tools.go/exec_sessions.go/exec_writer.go/exec_spec.go/mcp_tools.go/shutdown.go, provider_setup.go, observability.go, command_center.go) plus the TUI option-consumption side (internal/tui/options.go, run.go, model.go) and the agent-loop deferral gates that the CLI mirrors. Verified clean: every appDeps field set in defaultAppDeps is consumed somewhere (getwd/stdin/userConfigPath/resolve*/newProvider/probeProviderHealth/newSessionStore/loadPlugins/loadHooks/skillsDir/newMCPStore/newSandboxStore/selectSandboxBackend/registerMCPTools/prepareWorktree/detectVerifyPlan/runVerify/runSelfVerify/inspectChanges/commitChanges/runTUI/runEditor/checkUpdate/now), fillAppDeps covers all 24 fields; runInteractiveTUI populates Cwd/ProviderName/ModelName/ProviderProfile/Provider/NewProvider/Registry/SessionStore/SandboxStore/AgentOptions/PermissionMode/Notify, and the unset tui.Options fields are safe by construction (UsageTracker is defaulted in newModel, RuntimeMessageSink is wrapped by tui.Run into program.Send, ResponseStyle defaults to \"balanced\", ReasoningEffort has no config source to drop; Model/Cwd/ContextWindow are filled per-run by the TUI at model.go:1123-1133); version injection is correctly wired via internal/release BuildLdflags to internal/cli.version; the tool_search registration gate in app.go/exec.go matches agent partitionTools (including the disabled-tools threshold-zeroing and the allowlist exemption at loop.go:935-941); notify focus wiring (FocusMsg/BlurMsg/SetFocused) is complete; exit codes 0/1/2/3/130 are consistently produced in text mode and stderr/stdout discipline holds for text and stream-json. Found 8 concrete defects: (1) `zero --skip-permissions-unsafe` silently drops all trailing args (app.go:149); (2) Ctrl+C under `-o json` ends the stdout JSON stream with no error/done terminal event, unlike every other terminal path (exec.go:500, also exec_spec.go:164); (3) `exec --list-tools -o json` ignores the format and prints plain text (exec.go:182); (4) tui.Run swallows program.Run's error, exiting 1 with no diagnostic on the default chat path (tui/run.go:41); (5) the exec path bypasses the injected session store and clock (PrepareExec without Store at exec.go:353, sessions.NewStore at exec_sessions.go:55, time.Now at exec.go:374); (6) session-title truncation slices UTF-8 mid-rune (exec_sessions.go:88); (7) stream-json/stderr output truncation slices runes mid-sequence (exec_writer.go:403/412); (8) providers list shows \"api key: not set\" for AuthHeaderValue-credentialed profiles that providers check accepts (zerocommands/contracts.go:166). No data races found on the audited paths (agent callbacks run synchronously inside agent.Run; notify.Notifier is mutex-guarded; tui.Run's program capture has a happens-before chain through goroutine creation).

#### [medium/ux] `zero --skip-permissions-unsafe` silently discards all trailing arguments
`internal/cli/app.go:149`

The top-level dispatch switches on args[0] only, and the `--skip-permissions-unsafe` branch returns immediately into the interactive TUI without ever looking at args[1:]. A user who combines the flag with anything else — e.g. `zero --skip-permissions-unsafe -p "task"` or `zero --skip-permissions-unsafe exec "task"` — gets an interactive unsafe-mode TUI and their prompt/subcommand is silently dropped, with no 'unexpected argument' diagnostic. Every other surface that accepts this flag (exec) parses it positionally anywhere, so the asymmetry is surprising; the unknown-command path proves the CLI normally diagnoses bad invocations (exit 2).

```
case "--skip-permissions-unsafe":
	// Launch the interactive TUI directly in unsafe mode. ...
	return runInteractiveTUI(stderr, deps, agent.PermissionModeUnsafe)   // args[1:] never examined
```

**Suggested fix:** In that case branch, reject extra args: `if len(args) > 1 { fmt.Fprintf(stderr, "unexpected argument %q after --skip-permissions-unsafe\n", args[1]); return 2 }` before calling runInteractiveTUI.

#### [medium/ux] Ctrl+C with `-o json` ends the JSON event stream with no error/done terminal event
`internal/cli/exec.go:500`

The interrupted branch of runExec special-cases only execOutputStreamJSON. For `-o json`, the protocol on stdout has already emitted {"type":"run_start"} plus text/tool/usage objects, and every other terminal path closes the stream with a terminal object — writer.final emits {"type":"done","exit_code":0} and writeExecProviderError emits {"type":"error"} + {"type":"done","exit_code":3}. On SIGINT, however, the json stream just stops; "Interrupted." goes to stderr and the process exits 130. A machine consumer of `-o json` cannot distinguish an interrupt from a crash/truncated pipe. The identical gap exists in the spec-draft path at internal/cli/exec_spec.go:164-175.

```
if errors.Is(err, context.Canceled) || runCtx.Err() != nil {
	sessionRecorder.append(sessions.EventError, map[string]any{"message": "interrupted"})
	if options.outputFormat == execOutputStreamJSON {
		writer.errorEvent("interrupted", "run cancelled by signal", false)
		writer.runEnd("interrupted", exitInterrupted)
		...
	} else {
		fmt.Fprintln(stderr, "Interrupted.")
	}
	return exitInterrupted
```

**Suggested fix:** Add an execOutputJSON branch mirroring writeExecProviderError: writer.errorEvent("interrupted", "run cancelled by signal", false) followed by writer.writeJSON(map[string]any{"type": "done", "exit_code": exitInterrupted}); apply the same in runExecSpecDraft.

#### [medium/ux] `zero exec --list-tools -o json` ignores the requested JSON format and prints plain text
`internal/cli/exec.go:182`

The --list-tools branch honors stream-json (it wraps the list in run_start/final/run_end events) but for `-o json` it falls through to writeExecToolList, which prints the human-readable "Tools visible to model:" table to stdout. A consumer that asked for json gets unparseable text with exit 0, even though every other exec path (writer.runStart/final/toolResult, writeExecProviderError) emits JSON objects for this format. Secondary nit in the same branch: the stream-json path passes execRunMetadata{} so its run_start event carries empty provider/model/api_model fields even though resolveExecRunMetadata is computed later in the function.

```
if options.listTools {
	if options.outputFormat == execOutputStreamJSON {
		return writeExecStreamJSONFinal(stdout, workspaceRoot, execRunMetadata{}, permissionMode, formatExecToolList(registry, options, permissionMode), exitSuccess)
	}
	if err := writeExecToolList(stdout, registry, options, permissionMode); err != nil {
		return exitCrash
	}
	return exitSuccess
}
```

**Suggested fix:** Add an execOutputJSON branch that emits the tool list as a JSON object (e.g. writeJSONLine(stdout, map[string]any{"type":"tools","tools": visibleExecTools(...)}) followed by {"type":"done","exit_code":0}).

#### [medium/ux] TUI program errors are swallowed: exit code 1 with zero diagnostics on the default chat path
`internal/tui/run.go:41`

tui.Run — the function wired as deps.runTUI and the terminal step of the default `zero` invocation — discards the error returned by program.Run(). If Bubble Tea fails (cannot open the TTY, terminal init failure, renderer error), the process exits 1 having printed nothing to stderr, leaving the user with no clue why the app vanished. Contrast with runInteractiveTUI, which prefixes every other failure with "[zero] ..." on stderr before returning a nonzero code.

```
if _, err := program.Run(); err != nil {
	return 1
}
return 0
```

**Suggested fix:** Print the error before returning: `if _, err := program.Run(); err != nil { fmt.Fprintf(os.Stderr, "[zero] tui error: %v\n", err); return 1 }` (or thread the stderr writer through tui.Options).

#### [low/wiring] Exec path bypasses injected session store and clock (deps seam silently ignored)
`internal/cli/exec.go:353`

appDeps.newSessionStore is the dependency-injection seam used by search (observability.go:117), sessions, spec, usage, the spec-draft exec path (exec_spec.go:51), and the interactive TUI — but the primary exec run never uses it: sessions.PrepareExec is called without populating its Store field (sessions.PrepareExecOptions.Store exists and PrepareExec falls back to NewStore(StoreOptions{}) when nil, internal/sessions/exec_session.go:57-60), and preflightExecSession constructs `sessions.NewStore(sessions.StoreOptions{})` directly (exec_sessions.go:55). Any caller/test that injects a custom store gets exec sessions written to the real default store while the rest of the CLI honors the injection. The same seam-bypass applies to the clock: exec.go:374 uses `time.Now()` and exec_writer.go:416 uses the local `timeNow()` for run IDs even though deps.now is injected and exec_spec.go:65 correctly uses run.deps.now().

```
preparedSession, err = sessions.PrepareExec(sessions.PrepareExecOptions{
	SessionID: options.initSessionID,
	... // no Store: field — PrepareExec falls back to NewStore(StoreOptions{})
})
// exec_sessions.go:55: store := sessions.NewStore(sessions.StoreOptions{})
// exec.go:374: runID, err := streamjson.CreateRunID(time.Now())
```

**Suggested fix:** Pass `Store: deps.newSessionStore()` in PrepareExecOptions, thread deps into preflightExecSession, and use deps.now() for CreateRunID.

#### [low/bug] Session title truncation slices UTF-8 mid-rune
`internal/cli/exec_sessions.go:88`

createSessionTitle truncates the user's prompt at byte offset 80. For any non-ASCII prompt whose 80th byte lands inside a multi-byte UTF-8 sequence (CJK, accented text, emoji), the persisted session title ends with a broken rune; json.Marshal then stores U+FFFD replacement characters in the session metadata, which surfaces as mojibake in `zero sessions`, `/resume`, and stream-json session payloads. The title feeds execSessionTitle for every exec run that creates a session.

```
title := strings.Join(strings.Fields(prompt), " ")
if len(title) > 80 {
	title = title[:80]
}
```

**Suggested fix:** Truncate on a rune boundary, e.g. `if r := []rune(title); len(r) > 80 { title = string(r[:80]) }`, or back up with utf8.RuneStart before slicing.

#### [low/bug] Stream-json and stderr tool-output truncation split runes mid-sequence
`internal/cli/exec_writer.go:412`

truncateForStreamJSONOutput cuts tool output at a fixed byte offset (10 KiB) and truncateForStatus at byte 200; both can bisect a multi-byte UTF-8 rune. For stream-json, the broken trailing bytes are marshalled by streamjson.FormatEvent and become U+FFFD in the protocol output consumed by machine clients; for text mode the stderr "[result] ..." line ends in raw invalid bytes. Tool output is routinely non-ASCII (file contents, grep results), so the boundary case is hit in practice on any sufficiently large multilingual output.

```
func truncateForStreamJSONOutput(value string) (string, bool) {
	if len(value) <= streamJSONToolResultOutputLimit {
		return value, false
	}
	return value[:streamJSONToolResultOutputLimit] + "\n[truncated]", true
}
// truncateForStatus: return compact[:200] + "..."
```

**Suggested fix:** Before slicing, walk back to a rune boundary: `cut := limit; for cut > 0 && !utf8.RuneStart(value[cut]) { cut-- }; return value[:cut] + ...` in both helpers.

#### [low/ux] Providers list/current reports "api key: not set" for profiles authenticated via --auth-header-value
`internal/zerocommands/contracts.go:166`

The provider snapshot computes APIKeySet solely from profile.APIKey, but the CLI's own readiness check (provider_setup.go:407-409 providerProfileHasCredential) treats a non-empty AuthHeaderValue as an equivalent credential, and `zero providers add --auth-header-value` is a documented way to store the credential. The result is contradictory output: `zero providers check` passes for such a profile while `zero providers list` / `zero config` (command_center.go:328 prints "api key: %s") show "api key: not set", telling the user their working provider is misconfigured.

```
APIKeySet: strings.TrimSpace(profile.APIKey) != "",
// vs provider_setup.go:407: return strings.TrimSpace(profile.APIKey) != "" || strings.TrimSpace(profile.AuthHeaderValue) != ""
```

**Suggested fix:** Set APIKeySet with the same predicate as providerProfileHasCredential: `strings.TrimSpace(profile.APIKey) != "" || strings.TrimSpace(profile.AuthHeaderValue) != ""` (or rename the rendered label to "credential").


### MCP & stream-json

Audited internal/mcp (client.go, protocol.go, server.go, network_client.go, registry.go, config.go, schema.go), internal/streamjson, and the cli serve/exec wiring (serve.go, mcp_tools.go, exec.go, exec_writer.go, app.go, extensions.go). 11 concrete defects. Most impactful: (1) the stdio transport implements LSP Content-Length framing instead of MCP's newline-delimited JSON, so both the stdio client and `zero serve --mcp` are incompatible with every standard MCP implementation — a configured real server hangs initialize for 30s then aborts the whole run; (2) the frame reader allocates make([]byte, N) from a peer-controlled Content-Length up to MaxInt64, and the resulting makeslice panic in an unrecovered goroutine crashes the entire process (empirically verified); (3) streamjson's home-grown redaction pattern `sk-[A-Za-z0-9._-]+` mangles ordinary words in every stream-json output event ("task-list" -> "ta[REDACTED]", verified), even though internal/redaction already has correctly bounded patterns. Additional protocol bugs: client readLoop misroutes server->client requests (ping/sampling) as responses on id collision and never answers ping; streamable-HTTP SSE decoding grabs the first event instead of the id-matched response; a 1 MiB SSE scanner cap permanently kills SSE sessions on large messages. Resource/deadlock issues: stdio request write blocks under client.mu with no ctx cancellation (write-side stdio deadlock until Close), and child stderr accumulates unbounded in memory for the server's lifetime. Hygiene: mid-rune byte truncation in stream-json tool output, dead EventRestore schema constant, and the serve-side ping method-not-found response. The pending-map id-correlation/dispatch machinery itself (failAll/removePending/Close ordering, SSE pending channel lifecycle) checked out race-free, and MCP runtime Close() is correctly deferred on all cli paths (exec.go:175, app.go:334, extensions.go:231).

#### [critical/security] Unbounded Content-Length allocation lets a peer crash the whole process
`internal/mcp/protocol.go:69`

messageReader.read() parses the peer-supplied Content-Length with strconv.Atoi (accepts up to MaxInt64 on 64-bit) and only rejects values <= 0, then does `make([]byte, contentLength)` with no upper bound. A single frame `Content-Length: 9223372036854775807\r\n\r\n` causes a `makeslice: len out of range` runtime panic (verified with go run: Atoi succeeds, make panics); smaller huge values (e.g. tens of GB) cause OOM. The panic fires inside Client.readLoop's goroutine (client.go:297) or mcp.Serve's read goroutine (server.go:52), neither of which recovers, so a malicious or buggy MCP server — or any MCP host talking to `zero serve --mcp` — crashes the entire zero process mid-session, losing in-flight TUI/exec state.

```
`parsed, err := strconv.Atoi(strings.TrimSpace(value)); if err != nil || parsed <= 0 { return rpcMessage{}, fmt.Errorf("invalid MCP content length %q", value) }` (protocol.go:58-61) followed by `body := make([]byte, contentLength)` (protocol.go:69). Verified: `make([]byte, 9223372036854775807)` panics with "runtime error: makeslice: len out of range".
```

**Suggested fix:** Add a maximum message size constant (e.g. const maxMessageBytes = 32 << 20) and return an error when contentLength exceeds it before allocating: `if contentLength > maxMessageBytes { return rpcMessage{}, fmt.Errorf("MCP message of %d bytes exceeds limit", contentLength) }`.

#### [high/bug] MCP stdio transport uses LSP Content-Length framing instead of MCP newline-delimited JSON
`internal/mcp/protocol.go:42`

The MCP spec (including the "2024-11-05" protocolVersion this code advertises in client.go:133 and server.go:15) defines the stdio transport as newline-delimited JSON-RPC messages. protocol.go instead implements LSP-style framing: read() loops over header lines until it finds "content-length", and write() emits a Content-Length header block. Consequence on the client side: any standard stdio MCP server (e.g. npx @modelcontextprotocol/server-*) writes bare JSON lines, which read() consumes forever as 'header' lines (strings.Cut on ':' never matches content-length), so initialize() hangs for the full 30s initializeTimeout and then registerMCPToolsForWorkspace aborts the whole exec/TUI run with mcp_error (exec.go:171-175). On the server side, `zero serve --mcp` (README: "expose Zero read-only tools over MCP stdio") emits Content-Length frames that no standard MCP host (Claude Desktop/Code, Cursor, MCP Inspector) can parse, and cannot parse their newline JSON, so the serve feature is unusable with real hosts. The unit tests pass only because both ends use the same private messageReader/messageWriter.

```
read(): `line, err := reader.reader.ReadString('\n') ... if strings.EqualFold(strings.TrimSpace(name), "content-length") { ... } ... if contentLength <= 0 { return rpcMessage{}, fmt.Errorf("missing MCP content length") }` and write(): `fmt.Fprintf(writer.writer, "Content-Length: %d\r\n\r\n", len(body))` (protocol.go:45-66, 88). Client advertises `"protocolVersion": "2024-11-05"` (client.go:133); the MCP spec for that version mandates newline-delimited messages over stdio.
```

**Suggested fix:** Replace the framing in messageReader/messageWriter with newline-delimited JSON: write() should json.Marshal the message and append a single '\n'; read() should read one line (bufio with a generous limit), skip blank lines, and json.Unmarshal it. This fixes both Connect (stdio client) and Serve in one place.

#### [high/ux] streamjson secret patterns mangle ordinary text in every stream-json output event
`internal/streamjson/streamjson.go:310`

FormatEvent runs redactString over every string field of every output event (text deltas, final answers, tool_result output). The pattern `sk-[A-Za-z0-9._-]+` has no left word-boundary and no minimum length, so it matches inside ordinary words: verified that "update the task-list now" becomes "update the ta[REDACTED] now" and "a risk-based approach" becomes "a ri[REDACTED] approach". Likewise `(?i)(bearer\s+)[A-Za-z0-9._-]+` rewrites prose like "Bearer tokens are..." to "Bearer [REDACTED] are...". For a coding agent whose answers routinely contain words like task-list, risk-based, desk-, flask-, every stream-json consumer (editor extensions, automation) receives corrupted text. Notably the repo already has a correct implementation: internal/redaction/redaction.go uses bounded patterns like `\bsk-(?:proj-)?[A-Za-z0-9._-]{12,}\b`, and tool outputs are already scrubbed once at the registry boundary (tools/registry.go scrubResultSecrets) before streamjson re-mangles them.

```
`var secretPatterns = []*regexp.Regexp{ regexp.MustCompile(`sk-[A-Za-z0-9._-]+`), regexp.MustCompile(`(?i)(api[_-]?key["'=:\s]+)[^"',\s)]+`), regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._-]+`), }` (streamjson.go:309-313), applied to all strings via redactValue in FormatEvent (streamjson.go:154). Verified output: `task-list -> "update the ta[REDACTED] now"`.
```

**Suggested fix:** Replace streamjson's ad-hoc secretPatterns with the bounded patterns from internal/redaction (e.g. `\bsk-(?:proj-)?[A-Za-z0-9._-]{12,}\b`, bearer requiring a 12+ char token), or call redaction.RedactString instead of maintaining a divergent copy.

#### [medium/bug] Client readLoop misroutes server-to-client requests as responses (id collision) and never answers ping
`internal/mcp/client.go:302`

readLoop dispatches every message that carries an id to the pending-request map, without checking whether the message is itself a request (Method set). JSON-RPC server-to-client requests (MCP "ping", "roots/list", "sampling/createMessage") use the server's own id space, which typically starts at 0/1 — the same range as client.nextID (starts at 1). When a server request's id collides with a pending client call, the caller's request resolves with the request frame (Error nil, Result empty), so e.g. tools/call silently 'succeeds' with an empty result, and the real response arriving later finds no pending entry and is dropped. Independently, the client never replies to ping requests, so servers that health-check the connection will time out and disconnect.

```
`if message.ID == nil { continue }\n id, ok := rpcMessageID(message.ID) ... responses := client.pending[id] ... responses <- dispatchResult{message: message}` (client.go:302-316) — no `message.Method` check anywhere in readLoop, while rpcMessage carries a Method field (protocol.go:15).
```

**Suggested fix:** In readLoop, before id dispatch: `if message.Method != "" { if message.Method == "ping" && message.ID != nil { _ = client.writer.write(rpcMessage{ID: message.ID, Result: json.RawMessage("{}")}) }; continue }` (the write must go through the same mutex used by request()).

#### [medium/race] Stdio request write blocks indefinitely while holding client.mu and ignores ctx cancellation
`internal/mcp/client.go:246`

Client.request acquires client.mu, then calls client.writer.write(), a blocking pipe write to the child's stdin, and only releases the mutex afterwards. ctx is consulted at entry (line 213) and in the post-write select (line 258), but not during the write. If the MCP server stops draining stdin (e.g. it is busy executing the previous tool while the client sends a request whose JSON body exceeds the ~64 KiB OS pipe buffer — easy with large tool arguments), the write blocks forever: the in-flight CallTool cannot be cancelled by its context (TUI Esc / exec timeout), and every other request() caller queues on client.mu. The only escape is Client.Close() at process shutdown, which closes stdin. The hang_test suite covers read-side hangs but not this write-side stdio deadlock.

```
`client.mu.Lock()\n id := client.nextID ... if err := client.writer.write(rpcMessage{ID: id, Method: method, Params: rawParams}); err != nil { ... }\n client.mu.Unlock()` (client.go:228-255) — writer.write → bufio Flush → blocking os.Pipe write with no ctx selection.
```

**Suggested fix:** Perform the write in a goroutine and select on ctx: send the write result over a channel and, on ctx.Done(), return ctx.Err() after removePending(id) (let the orphaned write finish or fail when stdin closes). Alternatively register a context.AfterFunc(ctx, func(){ stdin.Close() }) style escape for the in-flight write.

#### [medium/perf] Child stderr captured into an unbounded bytes.Buffer for the entire server lifetime
`internal/mcp/client.go:98`

connectStdio sets cmd.Stderr to a bytes.Buffer whose contents are only ever read in the initialize-failure error path. After a successful handshake the buffer stays attached for the whole life of the MCP server process. MCP stdio servers conventionally log to stderr (the spec explicitly designates stderr for logging), so a chatty server in a long-lived TUI session grows this buffer without bound — an unbounded memory leak proportional to everything the server ever logs.

```
`var stderr bytes.Buffer\n cmd.Stderr = &stderr` (client.go:98-99); the only read is `message := strings.TrimSpace(stderr.String())` inside the initialize error branch (client.go:114). Nothing truncates or detaches the buffer after a successful Connect.
```

**Suggested fix:** Replace the bytes.Buffer with a small capped ring writer (e.g. keep the last 8 KiB) used only for the handshake diagnostic, or pipe stderr to io.Discard after initialize succeeds.

#### [medium/bug] Streamable-HTTP SSE response decoding takes the first event instead of the matching response
`internal/mcp/network_client.go:621`

When an HTTP MCP server answers a POST with Content-Type text/event-stream, decodeSSERPCMessage returns the first non-empty "message" event and stops scanning. The streamable-HTTP spec explicitly allows the server to send JSON-RPC notifications (logging, progress) and requests on that stream before the final response. If the first event is such a notification, networkClient.request receives a frame whose ID is nil/different, fails the `rpcIDMatches(message.ID, id)` check at line 210, and the call errors with "response id mismatch" while the real response further down the stream is discarded. Any conforming server that emits notifications/message or progress during a long tools/call breaks every call.

```
`if err := decoder.Decode(&decoded); err != nil { ... }\n found = true\n return false` (network_client.go:617-622) — scanning stops on the first decodable message event with no Method/ID filtering; caller then enforces `if !rpcIDMatches(message.ID, id) { return fmt.Errorf("MCP %s response id mismatch...") }` (network_client.go:210-211).
```

**Suggested fix:** Pass the expected id into decodeSSERPCMessage and keep scanning: skip events where `decoded.Method != ""` or `!rpcIDMatches(decoded.ID, id)`, returning only the frame that is actually the response.

#### [medium/bug] 1 MiB SSE scanner token cap permanently kills the SSE session on large messages
`internal/mcp/network_client.go:637`

scanSSEEvents uses bufio.Scanner with a hard 1 MiB max token size. A single `data:` line longer than 1 MiB (a tools/call result with a big file or payload — the stdio path has no comparable limit, and exec truncates only at the output stage) makes scanner.Scan() fail with bufio.ErrTooLong. For remoteSSEClient this error propagates out of readStream into failPending, which sets streamErr permanently; there is no reconnect, so every subsequent request on that server fails with "token too long" until restart. For the streamable-HTTP POST path it fails that call.

```
`scanner := bufio.NewScanner(reader)\n scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)` (network_client.go:636-637); on error: `if err != nil { ... client.failPending(err); return }` (network_client.go:495-501) and `client.streamErr = err` in failPending (network_client.go:584) with no recovery path.
```

**Suggested fix:** Read SSE lines with a bufio.Reader loop (ReadString('\n') accumulates arbitrarily long lines) or raise the scanner limit to the transport message cap; at minimum treat ErrTooLong as a per-message error rather than a terminal stream error.

#### [low/bug] Stream-json/status truncation slices strings mid-rune, emitting invalid UTF-8
`internal/cli/exec_writer.go:412`

truncateForStreamJSONOutput cuts tool output at a fixed byte offset (10240) and truncateForStatus at byte 200. Both can split a multi-byte UTF-8 rune (e.g. CJK output from grep/read_file), leaving a dangling lead byte. json.Marshal then substitutes U+FFFD, so stream-json consumers see a spurious replacement character appended to truncated tool_result output; the plain-text status path writes raw invalid bytes to stderr.

```
`return value[:streamJSONToolResultOutputLimit] + "\n[truncated]", true` (exec_writer.go:412) and `return compact[:200] + "..."` (exec_writer.go:403) — pure byte slicing with no rune-boundary handling.
```

**Suggested fix:** Back up to a rune boundary before appending the marker: `cut := streamJSONToolResultOutputLimit; for cut > 0 && !utf8.RuneStart(value[cut]) { cut-- }; return value[:cut] + ...` (same for the 200-byte status truncation).

#### [low/dead-code] streamjson.EventRestore is declared but never emitted
`internal/streamjson/streamjson.go:30`

The output event type `EventRestore = "restore"` (paired with CheckpointInfo.FilesRestored/FilesDeleted/Skipped fields that exist to describe a restore) is never produced anywhere: a repo-wide grep finds no writer constructing an Event with Type EventRestore — exec_writer.go emits checkpoint events only. A stream-json consumer implementing the schema will wait for restore events that can never arrive, and the constant plus the restore-specific CheckpointInfo fields are dead weight in the protocol surface.

```
`EventRestore EventType = "restore"` (streamjson.go:30); `grep -rn EventRestore internal` returns only this declaration. exec_writer.go has checkpoint() but no restore writer; docs/STREAM_JSON_PROTOCOL.md documents neither.
```

**Suggested fix:** Either emit a restore event from the checkpoint-restore path that already records sessions restore data, or delete EventRestore (and the restore-only CheckpointInfo fields) from the public schema.

#### [low/bug] MCP server answers "ping" with method-not-found instead of an empty result
`internal/mcp/server.go:129`

toolServer.handle routes every method other than initialize/tools/list/tools/call to the default branch, which returns a -32601 error frame. The MCP spec's ping utility requires the receiver to "respond promptly" with an empty result; hosts that health-check their servers with ping (Claude clients, MCP Inspector) receive an error response and may classify the zero server as unhealthy and tear down the connection.

```
`default:\n    return server.writeError(message.ID, jsonRPCMethodNotFound, "method not found")` (server.go:129-130) — no "ping" case exists in the switch (server.go:109-131).
```

**Suggested fix:** Add `case "ping": return server.writeResult(message.ID, map[string]any{})` to the method switch.


### Leaf packages (search, secrets, git, ...)

Audited internal/providerhealth, repoinfo, search, secrets, redaction, imageinput, contextreport, worktrees, zerogit, and perfbench by reading every source file and verifying suspicions with scratch tests run inside the module (all removed afterwards; package test suites pass). 13 concrete defects found, 4 confirmed empirically. Highest impact: the provider health probe's SSRF blocklist is bypassable via redirects and DNS rebinding, and Go's redirect handling re-sends x-api-key/custom auth headers to cross-host redirect targets (high, security). The secrets scanner's Redact provably leaves a private-key block almost entirely un-redacted when an inner AKIA/AIza-shaped run matches first inside its base64 body, and it misses modern sk-proj OpenAI keys entirely (both currently mitigated end-to-end only by the second redaction layer at the tool-registry boundary). Search has a proven byte-offset drift bug (ToLower length changes) that returns wrong Match offsets and context snippets, plus a global-failure mode where one corrupt session JSONL breaks all searching. zerogit stores git's C-quoted escaped paths verbatim (proven with a non-ASCII filename) and uses byte-based 72-char subject validation/truncation. worktrees' default runner merges stderr into the parsed stdout and never populates its Stderr field. Redaction has a real hot-path perf issue (regexp compiled per RedactString call at the tool-result boundary), a proven shared-pointer-as-[Circular] data-dropping bug, and a dead StackTrace interface. imageinput conflates all open/read errors with \"file not found\". repoinfo, contextreport, and perfbench came out clean — repoinfo in particular handles quotePath, gitlinks, and root-commit age correctly.

#### [high/security] Health probe SSRF blocklist and credentials defeated by redirects and DNS rebinding
`internal/providerhealth/providerhealth.go:285`

connectivityCheck carefully validates the endpoint host against a private/special-use IP blocklist (validateEndpoint resolves the host and checks every address), but the actual request is sent with http.DefaultClient: (1) the transport performs its own, second DNS resolution at dial time, so a rebinding DNS server can pass validation with a public IP and then dial 127.0.0.1/169.254.169.254 (classic TOCTOU; validated IPs are never pinned); (2) the default client follows up to 10 redirects with no per-hop re-validation, so any 3xx from the configured baseURL escapes the blocklist entirely; and (3) Go's redirect logic only strips Authorization/Cookie/WWW-Authenticate on cross-host redirects — the probe's x-api-key (Anthropic kinds), x-goog-api-key (Google), and arbitrary profile.CustomHeaders set by applyAuth are re-sent verbatim to whatever host the redirect names, leaking the API key to an attacker-controlled or internal endpoint. Production callers (internal/cli/observability.go:56, internal/cli/provider_setup.go:99) pass no HTTPClient and no Resolver, so this is the live configuration.

```
client := options.HTTPClient
if client == nil {
	client = http.DefaultClient
}
response, err := client.Do(request)   // providerhealth.go:281-285
...
addrs, err := resolver.LookupNetIP(ctx, "ip", host)   // providerhealth.go:387 — resolved once for validation, never pinned for the dial
...
applyAuth(request, profile, kind)   // providerhealth.go:339 — sets x-api-key / custom headers that Go re-sends on cross-host redirects
```

**Suggested fix:** When options.HTTPClient is nil, build a client with CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse } (a health probe never needs to follow redirects; treat 3xx as a provider error), and a Transport whose DialContext dials only the addresses returned by validateEndpoint (have validateEndpoint return the vetted []netip.Addr and connect to those, re-checking blockedAddrReason at dial time).

#### [medium/security] secrets.Redact leaves a private key un-redacted when an inner pattern matches inside it
`internal/secrets/scanner.go:81`

Redact replaces findings in sorted (type, match) order via strings.ReplaceAll using the match text captured from the ORIGINAL input. When one finding is a substring of another (realistic: an AKIA[0-9A-Z]{16} or AIza... run inside a PEM/OpenSSH base64 body — base64's alphabet contains A-Z and 0-9), the inner type sorts before "private_key_block" ("aws_access_key_id" < "private_key_block"), its replacement rewrites the text inside the block, and the subsequent ReplaceAll for the full private_key_block no longer finds its match — the entire key material is left in the output while the findings list claims the key was redacted. Verified empirically: Redact on a PEM containing "AKIAQEFAASCBKYWGGYZ1" returned the full BEGIN/END block with key body intact, with only the 20 inner chars replaced. The only production caller (internal/tools/bash.go formatBashOutput) is followed by the registry-boundary redaction.RedactString whose privateKeyPattern still catches the block, so the model context is currently protected — but the scanner's own contract ("Redact replaces every detected secret") is violated, and the user-facing "redacted N likely secret(s)" accounting is wrong.

```
for _, f := range findings {
	redacted = strings.ReplaceAll(redacted, f.Match, "[REDACTED:"+f.Type+"]")
}
// observed: "-----BEGIN PRIVATE KEY-----\nMIIEv[REDACTED:aws_access_key_id]2ab\nQUJDREVG\n-----END PRIVATE KEY-----"
```

**Suggested fix:** Before replacing, sort findings by len(f.Match) descending (ties by type/match for determinism) so containing matches are replaced first; or drop findings whose match is a substring of another finding's match.

#### [medium/bug] Search match offsets computed on ToLower'd text are applied to the original text
`internal/search/search.go:345`

findMatch lowercases the indexed text (`normalizedText := strings.ToLower(text)`) and returns byte offsets into that lowered string, but Sessions then slices the ORIGINAL entry.Text with those offsets (buildContext at search.go:128/367) and exports them as Match.Start/End in the JSON result. strings.ToLower changes byte length for several characters (U+212A KELVIN SIGN 3→1 byte, U+0130 2→3 bytes, U+212B, etc.), so all offsets after such a character drift. Verified empirically: text containing three Kelvin signs before the query word produced Match{25,35} whose original-text slice is " here SECR" instead of "SECRETWORD", and a shifted context snippet. buildContext can also cut multi-byte runes at the window edges, emitting invalid UTF-8 into the JSON output.

```
normalizedText := strings.ToLower(text)
if index := strings.Index(normalizedText, query); index >= 0 {
	return Match{Start: index, End: index + len(query)}, true
}
... Context: buildContext(entry.Text, match.Start, match.End, contextChars)  // offsets belong to the lowered text
```

**Suggested fix:** Search and slice the same string: compute lowered := strings.ToLower(entry.Text) once, run findMatch and buildContext both against lowered (documenting that Match offsets refer to the normalized text); additionally clamp buildContext boundaries to rune starts (back up while utf8.RuneStart is false).

#### [medium/ux] One corrupt session aborts the entire /search and `zero search` surface
`internal/search/search.go:108`

Sessions iterates every stored session and hard-fails on the first LoadIndex error (`return Result{}, err`). RebuildIndex calls store.ReadEvents, which returns an error for ANY malformed line in a session's events JSONL (internal/sessions/store.go:546-547) — e.g. a torn final line from a crash mid-append. From then on every search query, for any session, returns only that error in the TUI (`Search\nerror: ...`) and the CLI, until the user manually deletes the broken session directory. A search across N independent sessions should degrade per-session, not globally.

```
index, err := LoadIndex(store, session, LoadOptions{Reindex: options.Reindex, Now: now})
if err != nil {
	return Result{}, err
}
```

**Suggested fix:** On LoadIndex error, skip the session (continue), optionally counting skipped sessions in Result (e.g. a SkippedSessions int field) so the formatter can mention "N sessions unreadable" instead of failing the whole query.

#### [medium/perf] redactURLPasswords recompiles its regexp on every RedactString call (hot path)
`internal/redaction/redaction.go:362`

Every secret pattern in this package is a package-level compiled var except the URL matcher inside redactURLPasswords, which calls regexp.MustCompile on each invocation. RedactString is on the hottest text paths in the program: it runs on every tool result Output, Display.Summary and each Meta value at the registry boundary (internal/tools/registry.go:87/169-184), on loop-intercepted outputs (internal/agent/loop.go:564), on session replay strings (internal/sessions/replay.go:235), on every string visited by RedactValue (used by search indexing for every event payload string), and per-line in TUI command output. Regex compilation allocates and parses each time for zero benefit.

```
func redactURLPasswords(value string, replacement string) string {
	return regexp.MustCompile(`\b(?:https?|wss?|ftp)://[^\s]+`).ReplaceAllStringFunc(value, func(candidate string) string {
```

**Suggested fix:** Hoist to a package-level var alongside the others: `var urlPattern = regexp.MustCompile(`\b(?:https?|wss?|ftp)://[^\s]+`)` and use it in redactURLPasswords.

#### [medium/bug] zerogit parseStatus keeps git's C-quoted paths and combined rename strings verbatim
`internal/zerogit/zerogit.go:212`

Inspect runs `git status --short --untracked-files=all` without -z or -c core.quotePath=false, so git C-quotes any path containing non-ASCII or special characters (verified: a file named `ä ümlaut.txt` is emitted as `"\303\244 \303\274mlaut.txt"` including the literal quotes and octal escapes). parseStatus stores that string as FileChange.Path, so `zero changes inspect --json` reports escaped garbage instead of the real path, and GenerateMessage produces commit subjects like `Update "\303\244 ..."`. Rename records (`RM old -> new`) are likewise stored as the single string `old -> new`. Notably internal/repoinfo/repoinfo.go:84-86 explicitly uses -z for exactly this reason, so the project already knows about the hazard.

```
status, err := gitRawOutput(ctx, runGit, root, "status", "--short", "--untracked-files=all")  // line 118
...
path := strings.TrimSpace(line[3:])  // line 222 — raw, possibly C-quoted, possibly "old -> new"
```

**Suggested fix:** Use `git status --porcelain=v1 -z --untracked-files=all` (NUL-separated, unquoted; rename records carry the two paths as separate NUL-terminated fields) and parse records instead of lines.

#### [medium/wiring] worktrees defaultRunGit mixes stderr into parsed stdout and never fills CommandResult.Stderr
`internal/worktrees/worktrees.go:247`

defaultRunGit uses cmd.CombinedOutput() and stores the merged stream in CommandResult.Stdout, leaving the Stderr field permanently empty. Two consequences: (1) gitOutput's error path `firstNonEmpty(result.Stderr, result.Stdout)` reads a field that is never populated by the default runner (works only by accident because stdout holds the merge); (2) on success, any stderr chatter git emits with exit 0 (warnings, advice, `git worktree add`'s "Preparing worktree …" progress) is concatenated into the stdout that Prepare parses as the repo root / branch / git-common-dir, corrupting paths used to build the worktree location and the same-repo identity check. The sibling package internal/zerogit/zerogit.go:388-407 does this correctly with separate buffers.

```
output, err := command.CombinedOutput()
...
return CommandResult{Stdout: string(output), ExitCode: exitCode}, err  // Stderr never set, stderr merged into Stdout
```

**Suggested fix:** Mirror zerogit.defaultRunGitEnv: attach separate bytes.Buffers to command.Stdout and command.Stderr and return both fields.

#### [low/security] Secret scanner misses modern OpenAI key formats (sk-proj-, sk-svcacct-, keys with -/_)
`internal/secrets/scanner.go:35`

The openai_key pattern `sk-[A-Za-z0-9]{20,}` only matches the legacy all-alphanumeric format. Current OpenAI project/service-account keys (sk-proj-..., sk-svcacct-...) contain hyphens after a short alnum run, so the pattern never reaches 20 chars and Scan returns nothing — verified: Redact("token: sk-proj-abcDEF1234...") returned zero findings and the key unchanged. The sibling package internal/redaction/redaction.go:71 already knows about this shape (`\bsk-(?:proj-)?[A-Za-z0-9._-]{12,}\b`) and the registry boundary catches it, so the model context is still scrubbed; the defect is that this scanner's typed findings and the bash tool's "redacted N likely secret(s)" notice silently skip these keys.

```
{"openai_key", regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`)},  // does not match sk-proj-…; findings=[] in empirical test
```

**Suggested fix:** Align with the redaction package's pattern, e.g. `sk-(?:proj-|svcacct-)?[A-Za-z0-9._-]{20,}` (anchored with \b on both sides to keep precision).

#### [low/bug] RedactValue reports shared (non-circular) pointers/maps as "[Circular]", dropping data
`internal/redaction/redaction.go:222`

The `seen` set used for cycle detection is global to the whole traversal and entries are never removed when a branch completes, so it implements "visited" rather than "on the current path". Any value graph in which two sibling fields reference the SAME pointer or map (a DAG, not a cycle) has every reference after the first replaced by "[Circular]". Verified empirically: struct{A,B *thing}{shared, shared} redacts to map[A:map[Name:ok] B:[Circular]]. Callers feed arbitrary payloads through this for JSON output (internal/cli/sessions.go, skills.go, extensions.go, internal/doctor), so legitimate data is silently dropped from redacted reports.

```
ptr := value.Pointer()
if _, ok := context.seen[ptr]; ok {
	return CircularReference
}
context.seen[ptr] = struct{}{}
return redactReflect(value.Elem(), context, depth+1)  // seen never unwound
```

**Suggested fix:** Treat seen as a path set: after the recursive call returns, delete(context.seen, ptr) (same for the Map case) so only true ancestor cycles report [Circular].

#### [low/dead-code] RedactError stack-trace support is dead code (interface matches nothing)
`internal/redaction/redaction.go:174`

RedactError probes the error chain for `interface{ StackTrace() fmt.Stringer }`. No type in this repository implements StackTrace at all (grep finds only this file), and the de-facto ecosystem convention (github.com/pkg/errors) declares `StackTrace() errors.StackTrace`, which does not satisfy a fmt.Stringer return type, so errors.As can never succeed. RedactedError.Stack is therefore always empty and the field plus the branch are unreachable.

```
var stackTracer interface{ StackTrace() fmt.Stringer }
if errors.As(err, &stackTracer) {
	redacted.Stack = RedactString(stackTracer.StackTrace().String(), options)
}
```

**Suggested fix:** Delete the branch and the Stack field, or change the probe to an interface actually implemented by the project's error types.

#### [low/bug] Commit subject length/truncation is byte-based: rejects valid non-ASCII subjects and can emit invalid UTF-8
`internal/zerogit/zerogit.go:206`

ValidateMessage enforces `len(firstLine) > 72` in BYTES while the error says "72 characters": a 30-character CJK subject (~90 bytes) is rejected even though it is well under 72 characters. truncateSubject slices `value[:69]` at a fixed byte offset, which can cut a multi-byte rune in half and produce an invalid-UTF-8 commit subject when GenerateMessage builds "Update <path>" from a long non-ASCII filename.

```
if len(firstLine) > 72 {
	return fmt.Errorf("commit message subject must be 72 characters or fewer")
}
...
func truncateSubject(value string) string {
	if len(value) <= 72 { return value }
	return strings.TrimSpace(value[:69]) + "..."
}
```

**Suggested fix:** Count and slice runes: `runes := []rune(firstLine); if len(runes) > 72 {...}` and `string(runes[:69]) + "..."` in truncateSubject.

#### [low/wiring] zerogit resolveRunners silently drops env for caller-supplied runners — temp-index isolation lost
`internal/zerogit/zerogit.go:417`

When a caller provides RunGit but not RunGitEnv, resolveRunners synthesizes an EnvRunner that discards the env slice entirely. stagedSnapshotDiff depends on env carrying GIT_INDEX_FILE to point `git add -A` at a throwaway index; with the env dropped, the `add -A` in this nominally read-only Inspect would mutate the caller's REAL index (staging every change and untracked file). Today's production wiring (internal/cli/app.go:125) passes neither runner so the defaults are used and the hazard is latent, but the option pair is exported and the silent env drop is a booby trap for any future caller injecting a logging/sandboxing RunGit.

```
} else if runGitEnv == nil {
	runGitEnv = func(ctx context.Context, dir string, _ []string, args ...string) (CommandResult, error) {
		return runGit(ctx, dir, args...)  // env parameter ignored
	}
}
```

**Suggested fix:** Make the fallback fail loudly instead of dropping state: return an error from Inspect when RunGit is set without RunGitEnv and env-dependent commands are needed, or have the wrapper reject non-empty env (`if len(env) > 0 { return CommandResult{}, errors.New("RunGitEnv required") }`).

#### [low/ux] imageinput reports every open/read failure as "image file not found"
`internal/imageinput/imageinput.go:58`

LoadFile maps three distinct failures — os.Open error (line 58), io.ReadAll error (line 64), and the earlier os.Stat error (line 38) — to the identical message "image file not found: <path>". A permission-denied file, an I/O error, or a file deleted mid-read all tell the user the file doesn't exist, sending them to debug the wrong thing (the file is visibly present). The size/type validation itself is sound (Stat pre-check, LimitReader bound, sniff + allow-list).

```
file, err := os.Open(resolved)
if err != nil {
	return zeroruntime.ImageBlock{}, fmt.Errorf("image file not found: %s", path)
}
...
data, err := io.ReadAll(io.LimitReader(file, MaxImageBytes+1))
if err != nil {
	return zeroruntime.ImageBlock{}, fmt.Errorf("image file not found: %s", path)
}
```

**Suggested fix:** Differentiate: keep "not found" only for errors.Is(err, os.ErrNotExist) and otherwise wrap the real error, e.g. fmt.Errorf("read image %s: %w", path, err).


### Tests & build hygiene

Test/build hygiene audit of zero at /Users/kratos/Downloads/zero-main 2. Verified clean: `go vet ./...` passes, `go build ./...` passes, `go test ./...` passes (571 tests across internal/tui + internal/cli alone), `go test -race ./...` passes, only one skip fires in practice (Windows-only TestDefaultBaseDirFallsBackForWindowsUserProfile) and conditional skips (symlinks, git, model-catalog upgrade targets) all execute on this platform; no TODO/FIXME/HACK markers in code; no build-tag-orphaned tests (only the correct //go:build !windows on process_posix_test.go); workflows and scripts reference real targets — ci.yml/pr-auto-review.yml/release-artifacts.yml invoke `go run ./cmd/zero-release build|smoke|package|verify` and `go run ./cmd/zero-perf-bench --output … --ci`, all of which exist with those exact subcommands/flags, and scripts/install.sh + install.ps1 are exercised by internal/installtest. Defects found: (1) gofmt -l flags 10 files and no workflow runs gofmt/go vet (or -race), so drift lands on main unchecked; (2) the TUI transcript view ignores m.height entirely — bubbletea v1.3.10's standard renderer drops top lines beyond terminal height (standard_renderer.go:186-187), so older rows become invisible with no viewport/scrollback, and the suite only asserts width fitting (TestViewNeverExceedsTerminalWidth), locking in the height-blind render model; (3) a family of byte-slicing truncation helpers can split UTF-8 runes and emit/persist invalid UTF-8 — internal/tui/transcript.go:303 truncateTUIOutput (transcript rows), internal/tui/session.go:93 + spec_mode.go:302 + internal/cli/exec_sessions.go:88 (session titles), internal/cli/exec_writer.go:403/412 (stream-json protocol output and stderr status), internal/cli/cron.go:275 + cron_run.go:183 (cron list/run records) — every covering test (model_test.go:1078, exec_protocol_test.go:518-543) is ASCII-only and structurally cannot catch the rune split, while a correct rune-safe helper (truncateRunes, view.go:402) already exists in-tree; (4) minor dead test code: unused helper commandTestStringSliceContains (commands_test.go:46) and the never-read runtimeMessages slice appended from the agent goroutine in session_test.go:215/226. No golden tests were found that assert affirmatively wrong width math; the width-tier table tests match the implementation's 58/80/100 boundaries.

#### [high/ux] Transcript view ignores terminal height — rows beyond one screen are silently dropped (no viewport/scrollback), and tests only lock in width behavior
`internal/tui/model.go:599`

transcriptView() renders the ENTIRE transcript into one frame every render, and m.height (updated from WindowSizeMsg at model.go:458) is consumed only by the empty-state splash (startup.go:50) — the chat surface never clamps, scrolls, or pages by height. The program runs in Bubble Tea inline mode (run.go builds the program without an alt-screen or viewport). Bubble Tea v1.3.10's standard renderer truncates any frame taller than the terminal by dropping the TOP lines: standard_renderer.go:186-187 `if r.height > 0 && len(newLines) > r.height { newLines = newLines[len(newLines)-r.height:] }`. Once a conversation exceeds one screen, the title bar and all older rows become permanently invisible and unreachable — there is no scrollback because the dropped lines are repainted in place, never committed to the terminal's history. The test suite locks in this render model: width_tiers_test.go TestViewNeverExceedsTerminalWidth checks every frame line against `width` at 7 widths but has no height analogue, and every other view test (model_test.go, session_test.go) asserts substring presence in the full View() string, which passes regardless of whether the user could ever see the content.

```
model.go:599-624: `func (m model) transcriptView() string { … for index, row := range m.transcript { … builder.WriteString(m.renderRow(row, width, rc)) … } }` — no reference to m.height. grep shows m.height read only at startup.go:50 (`height := normalizedStartupHeight(m.height)`). bubbletea@v1.3.10/standard_renderer.go:186-187 trims top lines beyond r.height.
```

**Suggested fix:** Render transcript history through a bubbles/viewport sized to m.height minus the chrome rows (title bar, composer, status), or print completed rows with tea.Println so they persist in native terminal scrollback and keep only the live region in View(). Add a height-dimension test mirroring TestViewNeverExceedsTerminalWidth.

#### [medium/wiring] 10 files fail gofmt and CI has no gofmt/go vet gate to catch them
`.github/workflows/ci.yml:31`

gofmt -l currently flags 10 files (internal/sandbox/safe_command_test.go, internal/tui/autocomplete_test.go, internal/tui/commands.go, internal/tui/image_attach_test.go, internal/tui/model.go, internal/tui/rendering.go, internal/zerocommands/backend_snapshots.go, internal/zerocommands/backend_snapshots_test.go, internal/zerocommands/sandbox_snapshots.go, internal/zerogit/zerogit_test.go). The diffs are real misformatting (e.g. struct-field alignment in internal/tui/commands.go drifted after fields were removed). None of the three workflows (ci.yml, pr-auto-review.yml, release-artifacts.yml) run gofmt or go vet — their steps are only `go test ./...`, `go run ./cmd/zero-release build|smoke|package|verify`, and the perf bench — so formatting drift lands on main unchecked and will keep accumulating. CI also never runs the race detector despite the agent loop and TUI tests being goroutine-heavy (go test -race ./... passes today, so adding it is free).

```
`gofmt -l .` →  internal/sandbox/safe_command_test.go … internal/zerogit/zerogit_test.go (10 files). ci.yml smoke job steps: "- name: Test\n  run: go test ./...\n- name: Build binary\n  run: go run ./cmd/zero-release build\n- name: Smoke binary\n  run: go run ./cmd/zero-release smoke" — no vet/fmt step in any workflow.
```

**Suggested fix:** Run `gofmt -w` on the 10 listed files, then add a hygiene step to the ci.yml smoke job: `go vet ./...` and `test -z "$(gofmt -l .)"` (and preferably switch the Test step to `go test -race ./...` on at least one OS).

#### [medium/bug] truncateTUIOutput byte-slices mid-rune, emitting invalid UTF-8 into transcript rows; the only covering test is ASCII-only
`internal/tui/transcript.go:303`

truncateTUIOutput compares and slices by BYTE length: `if limit <= 0 || len(output) <= limit { … } return output[:limit] + " [truncated]"`. It is applied to raw tool output (tuiToolOutputLimit=240 bytes) at model.go:1410 (toolResultRowText, live runs) and session.go:233 (rehydrated sessions). Any tool result containing multi-byte UTF-8 (CJK source files, emoji, box-drawing output from bash) whose 240th byte falls inside a rune is cut mid-sequence, producing an invalid UTF-8 string that renders as a replacement-glyph artifact in the transcript and is persisted into the row text. The package already has a correct rune-safe helper (truncateRunes, view.go:402) that this code path doesn't use. The only test exercising the limit, model_test.go:1078 TestToolResultRowTruncatesLongOutput, feeds `strings.Repeat("x", tuiToolOutputLimit+20)` — pure ASCII — so it validates the byte-based contract while being structurally unable to catch the rune split.

```
transcript.go:297-304: `func truncateTUIOutput(output string, limit int) string { … if limit <= 0 || len(output) <= limit { return output } return output[:limit] + " [truncated]" }`. model_test.go:1078: `text := toolResultRowText(agent.ToolResult{Name: "read_file", Output: strings.Repeat("x", tuiToolOutputLimit+20)})`.
```

**Suggested fix:** Cut on a rune boundary, e.g. `for limit > 0 && !utf8.RuneStart(output[limit]) { limit-- }` before slicing, or reuse truncateRunes(output, limit). Extend the test with a multi-byte fixture and assert utf8.ValidString on the result.

#### [low/bug] Session titles byte-truncated at 80 can split a UTF-8 rune and persist invalid UTF-8 into session metadata (TUI, spec mode, and exec)
`internal/tui/session.go:93`

tuiSessionTitle truncates the user's prompt with `title = title[:tuiSessionTitleLimit]` (byte slice at 80). A prompt whose 80th byte falls inside a multi-byte rune yields invalid UTF-8 that is stored as the session Title and later marshaled to JSON (encoding/json silently replaces the broken byte with U+FFFD), so /resume lists and exec --resume show a mojibake title. The identical pattern exists at internal/tui/spec_mode.go:301-302 (`if len(title) > tuiSessionTitleLimit { title = title[:tuiSessionTitleLimit] }` in specImplementationTitle) and internal/cli/exec_sessions.go:87-88 (`if len(title) > 80 { title = title[:80] }` in createSessionTitle). No test feeds non-ASCII prompts to any of the three.

```
session.go:90-94: `func tuiSessionTitle(prompt string) string { title := strings.Join(strings.Fields(prompt), " "); if len(title) > tuiSessionTitleLimit { title = title[:tuiSessionTitleLimit] } … }`; spec_mode.go:301-302 and exec_sessions.go:87-88 repeat the byte-slice.
```

**Suggested fix:** Extract one rune-safe title helper (truncate via []rune or utf8.RuneStart backoff) and use it in all three sites: internal/tui/session.go:93, internal/tui/spec_mode.go:302, internal/cli/exec_sessions.go:88.

#### [low/bug] stream-json and status-line output truncation byte-slices mid-rune, corrupting tool output in the machine-readable protocol
`internal/cli/exec_writer.go:412`

truncateForStreamJSONOutput cuts tool-result output at exactly streamJSONToolResultOutputLimit (10*1024) BYTES: `return value[:streamJSONToolResultOutputLimit] + "\n[truncated]"`. If the 10KiB boundary lands inside a multi-byte rune, the string handed to the JSON encoder contains invalid UTF-8 and encoding/json substitutes U+FFFD, so stream-json consumers receive a corrupted trailing character in `output`. truncateForStatus (line 400-406) has the same byte-slice at 200 for the human stderr `[result]` line. The protocol test (exec_protocol_test.go:518 `Output: strings.Repeat("x", streamJSONToolResultOutputLimit+100)` asserted at line 543) is ASCII-only, so the suite pins the byte-limit contract without ever exercising the rune boundary.

```
exec_writer.go:408-413: `func truncateForStreamJSONOutput(value string) (string, bool) { if len(value) <= streamJSONToolResultOutputLimit { return value, false } return value[:streamJSONToolResultOutputLimit] + "\n[truncated]", true }`; exec_writer.go:400-406 truncateForStatus `return compact[:200] + "..."`.
```

**Suggested fix:** Back the cut index up to a rune start before slicing (`for limit > 0 && !utf8.RuneStart(value[limit]) { limit-- }`) in both helpers; add a multi-byte case to TestRunExecStreamJSON tool-result truncation asserting utf8.ValidString(output).

#### [low/bug] cron prompt/error truncation helpers byte-slice mid-rune
`internal/cli/cron.go:275`

promptExcerpt truncates the job prompt for `zero cron list` with `return p[:47] + "…"` — a byte slice that splits multi-byte runes (cron prompts are arbitrary user text; the built-in recipes are ASCII but user prompts need not be). cronTruncate in internal/cli/cron_run.go:179-184 does the same on captured stderr (`return s[:max] + "…"`, called with max=500 at cron_run.go:150) — child-process stderr regularly contains multi-byte characters (e.g. curly quotes in Go tool errors). Both produce invalid UTF-8 in CLI output and in the persisted run record.

```
cron.go:272-278: `func promptExcerpt(p string) string { p = strings.TrimSpace(strings.ReplaceAll(p, "\n", " ")); if len(p) > 48 { return p[:47] + "…" } return p }`; cron_run.go:179-184: `func cronTruncate(s string, max int) string { if len(s) <= max { return s } return s[:max] + "…" }`.
```

**Suggested fix:** Truncate on rune boundaries in both helpers (shared utf8.RuneStart backoff or []rune slice), mirroring internal/tui/view.go truncateRunes.

#### [low/dead-code] Dead test helper: commandTestStringSliceContains is defined but never called
`internal/tui/commands_test.go:46`

commandTestStringSliceContains in internal/tui/commands_test.go is never referenced anywhere in the package (the live tests use the separate stringSliceContains helper from model_test.go:1250). Unused functions compile silently in Go, so this is pure dead weight that suggests an assertion that was planned and never written or was refactored away.

```
commands_test.go:46-53: `func commandTestStringSliceContains(values []string, want string) bool { … }` — `grep -rn commandTestStringSliceContains internal/` returns only the definition line.
```

**Suggested fix:** Delete the function (or, if the duplicate was intentional, replace its one sibling stringSliceContains and use a single shared helper).

#### [low/dead-code] Dead test state: runtimeMessages slice is appended from the agent goroutine but never read
`internal/tui/session_test.go:215`

TestPromptSubmitPersistsPermissionSessionEvents declares `runtimeMessages := []tea.Msg{}` and appends to it inside the RuntimeMessageSink callback (line 226), which runs on the agent goroutine spawned at line 246-248, but the slice is never read by any assertion — the test drives itself entirely off runtimeMessageCh. It is dead state that also models a risky pattern: an unsynchronized slice mutated from a non-test goroutine; if a future edit reads it from the test goroutine it becomes a data race the -race runs would then have to catch. Every other test in the file (via newPermissionTestModel, line 486-488) correctly uses only the channel.

```
session_test.go:215 `runtimeMessages := []tea.Msg{}` … :225-228 `RuntimeMessageSink: func(msg tea.Msg) { runtimeMessages = append(runtimeMessages, msg); runtimeMessageCh <- msg },` — no other reference to the slice in the test.
```

**Suggested fix:** Remove the runtimeMessages slice and the append, leaving `RuntimeMessageSink: func(msg tea.Msg) { runtimeMessageCh <- msg }` to match the helper used by the other permission tests.


### Sessions & usage

Audited internal/sessions (store, checkpoint, rewind/replay, lineage, exec_session, file locks) and internal/usage (tracker, report), plus their wiring into the TUI run loop (internal/tui/model.go, session.go, session_controls.go) and CLI (exec.go, exec_sessions.go, sessions.go, usage.go). The persistence layer's locking design is sound (per-session in-process mutex + flock, *Locked variants, content-addressed blobs with integrity-checked reads, symlink-resolving workspace confinement on both capture and restore), and the usage tracker is only touched from the Bubble Tea goroutine, so no tracker data race exists as wired. The concrete defects cluster in three areas. (1) Durability: no fsync anywhere in the store — a torn final line in events.jsonl permanently bricks a session because ReadEvents hard-fails on any malformed line (breaking resume, fork, rewind, AND the global usage report via collectUsageData's fail-fast loop), and metadata.json is rewritten per event via unsynced tmp+rename, so a crash can zero it and the session silently vanishes from listing. (2) The TUI's cancelled-run flush protocol: after Esc, m.pending clears while the run goroutine still holds its batched session events, so /resume re-targets the flush into the WRONG session, /rewind is permitted and prunes the cancelled run's not-yet-referenced checkpoint blobs then receives the stale events appended after the rewind marker, and new prompts can interleave with the dying run. (3) Accounting: Fork duplicates provider_usage events with rewritten timestamps (double-counted, wrongly-dated usage report), the escalation 'model' field persisted on usage events is never consumed by BuildReport, and exec's session recorder latches its first error silently and unreported. Secondary findings: '/rewind latest' off-by-one leaves a dangling tool_call for the undone mutation in the log/replayed context, O(n^2) transcript append on rehydration, rune-splitting byte truncations feeding prompts/titles, and second-granularity timestamps making resume-latest tie-break to the older session.

#### [high/bug] Torn append (crash mid-write) permanently bricks a session: ReadEvents hard-fails on any malformed JSONL line
`internal/sessions/store.go:547`

appendEventLocked writes events with a plain os.OpenFile(O_APPEND) + Write and no fsync, so a crash or power loss mid-append leaves a partial trailing line in events.jsonl. ReadEvents then refuses to return ANY events: it aborts on the first undecodable line instead of tolerating a torn tail. Because every consumer goes through ReadEvents — PrepareExec resume, TUI /resume, Fork, ApplyRewind (via sortedCheckpointsAfter and truncateEventsLocked), pruneOrphanBlobs, and even `zero usage report` via collectUsageData — one torn line makes the session unresumable, unforkable, unrewindable, and (with no --session filter) breaks the global usage report, with no recovery path in the tool.

```
store.go:546-548: `if err := json.Unmarshal(line, &event); err != nil { return nil, fmt.Errorf("invalid json in zero session %s %s at line %d: %w", sessionID, EventsFile, index+1, err) }` — combined with the unsynced append at store.go:508-517 (`os.OpenFile(store.eventsPath(sessionID), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)` ... `file.Write(append(data, '\n'))` ... `file.Close()`).
```

**Suggested fix:** In ReadEvents, tolerate a malformed FINAL line (truncated tail from a crash): if json.Unmarshal fails on the last non-empty line, return the successfully parsed prefix (optionally with a warning) instead of an error; only hard-fail on corruption in the middle of the file. Optionally add a `zero sessions repair` path that truncates the file at the last valid newline.

#### [high/race] Cancelled run's deferred event flush is appended to whatever session is active at flush time (cross-session contamination)
`internal/tui/model.go:512`

When a run is cancelled (Esc), its goroutine keeps running and later delivers its batched session events in a final agentResponseMsg, which the Update loop persists via m.appendSessionEvents. But appendSessionEvent writes to m.activeSession.SessionID — the session active NOW, not the session the run belonged to. After Esc, m.pending is false, so the user can immediately /resume another session (the commandResume gate at model.go:914 only checks m.pending) or even start a new prompt in the same session (commandPrompt gate at model.go:837 checks `m.pending || m.exiting` only). When the flush lands, the cancelled run's tool_call/tool_result/checkpoint/usage events are permanently written into the wrong session's events.jsonl (and into m.sessionEvents, so they are replayed as context to the agent), or interleaved after the next turn's user message, scrambling the recorded order.

```
model.go:506-512: `if _, flushing := m.flushRunIDs[msg.runID]; flushing { ... m, flushRows = m.appendSessionEvents(flushableSessionEvents(msg.sessionEvents))` — and session.go:46: `event, err := m.sessionStore.AppendEvent(m.activeSession.SessionID, ...)`. agentResponseMsg (model.go:118-130) carries no session id; handleResumeCommand (session.go:116) swaps m.activeSession while flushRunIDs is non-empty.
```

**Suggested fix:** Capture the owning session id in the run closure (it is already in options.SessionID at model.go:1123) and carry it on agentResponseMsg; in the flush path call sessionStore.AppendEvent with that pinned id directly instead of going through m.appendSessionEvent/m.activeSession. Also skip mutating m.sessionEvents for flushes that do not belong to the active session.

#### [high/race] /rewind is allowed while a cancelled run is still flushing: prune deletes its checkpoint blobs, then the late flush re-appends pre-rewind events after the rewind marker
`internal/tui/session_controls.go:176`

handleRewindCommand only guards on m.pending, which cancelRun clears immediately — but the cancelled agent goroutine is still alive: it may still be executing a tool (mutating workspace files concurrently with the restore), and its EventSessionCheckpoint payloads are still un-persisted in its in-memory batch. ApplyRewind then (a) restores files while the dying run may still write them, (b) pruneOrphanBlobsLocked deletes the cancelled run's snapshot blobs because no persisted event references them yet (referencedBlobs reads only events.jsonl), and (c) when the flush finally lands, flushableSessionEvents re-appends the cancelled run's tool_call/checkpoint/usage events AFTER the EventSessionRewind marker — re-polluting the truncated log with events the rewind just dropped, whose checkpoint blob references now dangle (readBlob fails and future rewinds silently report those paths as Skipped). The same un-protected window exists cross-process: a `zero sessions rewind` from the CLI during any in-flight TUI run prunes the TUI's not-yet-referenced blobs, since the TUI batches checkpoint events until end-of-run despite SnapshotForCheckpoint's contract demanding prompt recording.

```
session_controls.go:176-178: `if m.pending { return m, "Rewind\ncannot rewind while a run is in progress." }` — no check of m.flushRunIDs, which cancelRun populates (model.go:1075-1080) while the goroutine keeps running. checkpoint.go:283-305 pruneOrphanBlobsLocked removes any blob not referenced by a persisted event; the cancelled run's checkpoint events are only in msg.sessionEvents until the flush at model.go:512.
```

**Suggested fix:** In handleRewindCommand (and ideally the commandPrompt/commandResume gates), also refuse while `len(m.flushRunIDs) > 0` ("waiting for cancelled run to finish"). Longer term, persist each checkpoint event immediately via CaptureToolCheckpoint (which holds the session lock across blob write + event append) instead of batching it for end-of-run.

#### [medium/bug] No fsync before rename/append: metadata.json (rewritten on every event) can be empty after a crash, silently hiding the session
`internal/sessions/store.go:632`

writeMetadata uses os.WriteFile(tmp) + os.Rename with no file.Sync() before the rename and no directory fsync. On crash/power loss, filesystems with delayed allocation (ext4, others) can commit the rename before the data, leaving a zero-length or partial metadata.json. Since metadata is rewritten on EVERY appendEventLocked (store.go:522), the window recurs constantly during a run. A corrupted metadata.json makes readMetadata fail, Get returns an error, and List silently skips the directory (store.go:325-328) — the session vanishes from `zero sessions list`, /resume, and resume-latest with intact events.jsonl on disk. The same unsynced tmp+rename pattern is in writeBlob (checkpoint.go:188-195), copyBlobs, truncateEventsLocked (replay/rewind.go:220-227), and writeFileAtomic (rewind.go:286-299), and the events append itself is never synced.

```
store.go:631-639: `tmp := fmt.Sprintf("%s.tmp-%d", path, store.idCounter.Add(1)); if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil { ... } if err := os.Rename(tmp, path); err != nil {` — no Sync anywhere in the package (grep for `.Sync()` in internal/sessions returns nothing).
```

**Suggested fix:** Open the temp file explicitly, write, call file.Sync() before Close, then Rename (and fsync the parent directory for full durability). Apply the same to writeBlob/truncateEventsLocked, and call file.Sync() in appendEventLocked before Close. Additionally, make List/Get treat a zero-length metadata.json as recoverable (e.g. surface the session id with a 'damaged' marker) instead of silently skipping it.

#### [medium/bug] TUI '/rewind latest' keeps the dangling tool_call event of the mutation it just undid
`internal/tui/session_controls.go:235`

Both recording paths append the EventToolCall first and the EventSessionCheckpoint immediately after it (TUI: model.go:1223 then 1245; exec: exec.go:443 then 449), so checkpoint.Sequence = toolCall.Sequence + 1. resolveRewindTarget('latest') returns lastCheckpoint - 1 as the keep-through sequence, which is exactly the tool_call's sequence — so ApplyRewind correctly restores the file's before-state but the truncated log still ENDS with the tool_call event for the mutation that was undone, with no result. The rehydrated transcript shows a perpetually unresolved tool call, and FormatExecPrompt / sessionPrompt replays that tool_call as context, telling the model the write happened even though the file was rolled back.

```
session_controls.go:235: `return lastCheckpoint - 1, nil // undo the most recent checkpoint` — while the TUI batch order at model.go:1223-1250 appends EventToolCall and then the checkpoint payload, making the checkpoint's paired tool_call sit exactly at sequence lastCheckpoint-1.
```

**Suggested fix:** Return the sequence BEFORE the checkpoint's paired tool_call: locate the EventToolCall immediately preceding the latest checkpoint and return its sequence minus 1 (or simply lastCheckpoint - 2 given the adjacent recording order, with a fallback when the preceding event is not a tool_call).

#### [medium/bug] Fork duplicates provider_usage events and rewrites their timestamps, double-counting and re-dating usage in `zero usage report`
`internal/sessions/store.go:387`

Fork copies every parent event into the fork via AppendEvent, and appendEventLocked stamps each copy with CreatedAt = now (store.go:495-501). collectUsageData (internal/cli/usage.go:35-53) flattens events from ALL sessions, so after a fork every one of the parent's provider_usage events is counted twice in BuildReport's requests/tokens/cost totals — and the duplicated copies are bucketed under the fork's creation date instead of when the usage actually happened, corrupting the per-day table. Forking an old session makes 'today' show requests/cost that occurred weeks ago, twice.

```
store.go:387-390: `for _, event := range events { if _, err := store.AppendEvent(fork.SessionID, AppendEventInput{Type: event.Type, Payload: event.Payload}); err != nil {` — appendEventLocked sets `CreatedAt: timestamp` (store.go:495-501) where timestamp is store.now(). usage.go:50: `set.events = append(set.events, sessionEvents...)` with no fork dedup; report.go counts every EventUsage.
```

**Suggested fix:** In BuildReport (or collectUsageData), skip the inherited prefix of fork sessions: for Metadata.SessionKind == fork, ignore events with Sequence <= the number of copied events (Fork already records copiedEventCount/forkedFromSequence in metadata and the EventSessionFork payload). Alternatively, preserve the original CreatedAt when Fork copies events and dedupe by original event identity.

#### [medium/ux] One corrupt session aborts the entire `zero usage report`
`internal/cli/usage.go:46`

collectUsageData iterates every session and returns the FIRST ReadEvents error for the whole command, so a single damaged events.jsonl anywhere under the sessions root (e.g. from the torn-append crash case) makes `zero usage report` exit with a crash code, even though the other sessions' data is fully readable. List() already tolerates per-session corruption by skipping; the usage traversal does not.

```
usage.go:46-49: `sessionEvents, err := store.ReadEvents(meta.SessionID); if err != nil { return usageEventSet{}, err }` inside the for-loop over all sessions.
```

**Suggested fix:** Skip-and-warn per session: on ReadEvents error, log a warning to stderr (session id + error) and continue aggregating the remaining sessions instead of returning the error.

#### [medium/wiring] Per-event 'model' field persisted under --allow-escalation is never read by the usage report, mispricing escalated runs
`internal/usage/report.go:17`

exec writes a `model` key into each provider_usage payload when escalation is enabled (internal/cli/exec.go:488-490), precisely because the model can change mid-run. But usageEventPayload only decodes promptTokens/completionTokens/totalTokens, and BuildReport prices every event with the session's Metadata.ModelID — so an event produced by the escalated (more expensive) model is costed at the base model's rate. The data needed for correct pricing is persisted and then ignored; the doc comment on usageEventPayload ('model id ... not persisted') is stale.

```
report.go:17-21: `type usageEventPayload struct { PromptTokens int ...; CompletionTokens int ...; TotalTokens int ... }` (no model field), report.go:102: `modelID := modelBySession[event.SessionID]` — vs exec.go:488-490: `if options.allowEscalation { payload["model"] = currentModel }`.
```

**Suggested fix:** Add `Model string `json:"model"`` to usageEventPayload and prefer it over modelBySession[event.SessionID] when non-empty in BuildReport's cost reconstruction.

#### [medium/wiring] execSessionRecorder latches the first persist failure, silently drops all later events, and the error is never surfaced
`internal/cli/exec_sessions.go:115`

recorder.append sets recorder.err on the first AppendEvent failure and then short-circuits every subsequent append — so after one transient failure (disk full, permissions, lock error) the rest of the run's user/assistant messages, tool calls, results and usage events are silently not recorded. No caller ever reads recorder.err (grep shows it is only referenced inside exec_sessions.go), so `zero exec` exits success while the persisted session is silently truncated; a later --resume replays incomplete context and `usage report` undercounts.

```
exec_sessions.go:114-121: `func (recorder *execSessionRecorder) append(...) { if recorder.err != nil || ... { return } _, recorder.err = recorder.prepared.Store.AppendEvent(...) }` — exec.go checks writer.err repeatedly (exec.go:495 etc.) but never sessionRecorder.err.
```

**Suggested fix:** At run end in runExec, if sessionRecorder.err != nil, emit a warning to stderr (or a stream-json warning event) that session recording failed at event N. Consider not latching: attempt each append independently so one transient failure doesn't drop the remainder of the log.

#### [medium/perf] appendTranscriptRow is O(n) copy + O(n) dedupe scan per row, making transcript growth and session rehydration O(n^2)
`internal/tui/transcript.go:113`

Every appended transcript row first scans the entire transcript for a duplicate key (hasTranscriptRow) and then copies the whole slice (`append([]transcriptRow{}, rows...)`). This runs on the hot path for every streamed agentRowMsg during a run, for the end-of-run replay of msg.rows, and in a tight loop when /resume or /rewind rehydrates a session (`for _, row := range transcriptRowsFromSessionEvents(events) { rows = appendTranscriptRow(rows, row) }` at session.go:127 and session_controls.go:206). Resuming a session with thousands of events does ~n^2/2 row copies plus n^2/2 key comparisons, with visible latency on large sessions.

```
transcript.go:113-120: `func appendTranscriptRow(rows []transcriptRow, row transcriptRow) []transcriptRow { if hasTranscriptRow(rows, row) { return rows } next := append([]transcriptRow{}, rows...); next = append(next, row); return next }` — hasTranscriptRow (122-133) linearly scans all rows per call.
```

**Suggested fix:** Maintain a `seenRowKeys map[string]struct{}` alongside the transcript on the model (rebuilt on clear/rehydrate) for O(1) dedupe, and append in place (`append(rows, row)`) — the defensive full copy is unnecessary when callers always reassign the result; if value-semantics isolation is required, copy only on the rare structural operations, not per append.

#### [low/bug] Naive byte-slice truncation can split multi-byte UTF-8 runes in prompts and titles
`internal/sessions/replay.go:237`

Several truncations slice byte strings at fixed offsets with no rune-boundary check: payloadPreview (`value[:240]`), buildCompactionPrompt (`prompt[:maxChars-len(...)]`, replay.go:174-176), summarizePayload (`text[:500]`, exec_session.go:194-196), tuiSessionTitle (`title[:80]`, internal/tui/session.go:93), and createSessionTitle (`title[:80]`, internal/cli/exec_sessions.go:89). Any non-ASCII payload/prompt (CJK text, emoji, accented filenames) can be cut mid-rune, producing invalid UTF-8 that lands in the compaction summary prompt sent to the model, the resume context block, and the persisted session title in metadata.json (where json.Marshal substitutes U+FFFD replacement characters).

```
replay.go:236-238: `if len(value) > 240 { return value[:240] + "..." }`; exec_session.go:194-196: `if len(text) > 500 { return text[:500] }`; internal/tui/session.go:92-94: `if len(title) > tuiSessionTitleLimit { title = title[:tuiSessionTitleLimit] }`.
```

**Suggested fix:** Truncate on a rune boundary: e.g. `for limit > 0 && !utf8.RuneStart(s[limit]) { limit-- }` before slicing, or iterate with utf8.DecodeRuneInString / use a shared truncateRunes helper at all five sites.

#### [low/bug] Second-granularity RFC3339 timestamps make Latest()/list ordering pick the wrong session on same-second ties
`internal/sessions/store.go:555`

store.timestamp() formats with time.RFC3339 (whole seconds). List sorts by UpdatedAt descending with SessionID ASCENDING as the tie-break, and Latest() takes sessions[0]. Two sessions updated within the same second (common when child/spec sessions are created back-to-back, or in fast scripted runs) tie on UpdatedAt, and the ascending-id tie-break returns the EARLIER-created session (createID embeds an increasing timestamp/nano so later sessions sort higher) — `zero exec --resume-latest` and the TUI's `/resume latest` can resume the older of two sessions created in the same second.

```
store.go:554-556: `func (store *Store) timestamp() string { return store.now().UTC().Format(time.RFC3339) }` and store.go:331-336: `if sessions[left].UpdatedAt == sessions[right].UpdatedAt { return sessions[left].SessionID < sessions[right].SessionID } return sessions[left].UpdatedAt > sessions[right].UpdatedAt`.
```

**Suggested fix:** Format timestamps with time.RFC3339Nano (lexicographic ordering still holds for UTC), or invert the tie-break to `SessionID >` so the later-created id wins on equal UpdatedAt.


### TUI scroll/history architecture deep-dive

AUDIT SCOPE: internal/tui of the "zero" Bubble Tea CLI (bubbletea v1.3.10, bubbles v1.0.0, no alt-screen). 14 verified findings: the scrollback architecture defect (high), Gemini tool-id transcript dedupe collisions (high), 16-line card truncation hiding diffs (high), discarded mid-run assistant text, invisible cancellation, blind composer typing, O(n^2)/per-token full re-render perf, /style dead wiring, diff-detection false positives, indentation-destroying wrapping, resize/first-frame corruption, dead footer code, rune-splitting truncation, and /exit bypassing the checkpoint flush.

WHY HISTORY IS UNREACHABLE TODAY (proven mechanics): run.go builds the Program with only WithContext/WithInput/WithOutput — the standard INLINE renderer, no alt screen, no viewport, and the repo contains zero tea.Println calls. model.View() returns transcriptView(), which renders the ENTIRE transcript plus composer/status as one string every frame. bubbletea v1.3.10 standard_renderer.go:183-188 then drops every line above the last `height` lines ("we can't navigate the cursor into the terminal's scrollback buffer: newLines = newLines[len(newLines)-r.height:]"). Consequences: (a) transcript content above one screen is never written to the terminal AT ALL — there is nothing to scroll up to; (b) the visible region is repainted in place via CursorUp(linesRendered-1), so text selection of even visible history is overwritten by the next frame (per-token agentTextMsg renders + 12fps spinner ticks during runs); (c) on shrink-resize, previously painted lines physically re-wrap, the renderer's logical line count desyncs from physical rows, and stale fragments are pushed into native scrollback above a garbled frame — with the whole UI in one full-height managed region, the entire chat corrupts; the first frame is additionally painted at chatWidth(0)=96 before the initial WindowSizeMsg (tea.go renders the initial view before registering handleResize), wrapping immediately on narrower terminals; (d) long diffs are doubly invisible: cardBodyMaxLines=16 truncates every card body to 16 lines + "… N more lines", and with no scrollback/viewport/expand command those lines are unreachable forever.

TARGET ARCHITECTURE — SETTLED-ROW FLUSH FRONTIER (bubbletea v1.3.x, inline, no alt-screen):
Core idea: completed transcript rows are emitted to native terminal scrollback via tea.Println EXACTLY ONCE; View() holds only the live tail. tea.Println enqueues queuedMessageLines, which the standard renderer prints ABOVE the managed region on the next flush (FIFO, then the terminal owns them: native scrollback, selection, copy all work). Mouse capture stays off (run.go's comment is correct).

1) SETTLED-NESS. Keep []transcriptRow as truth; add `flushed int` (frontier: rows[<flushed] already Println'ed, never re-rendered). A row settles when its visual can never change: rowUser/rowSystem/rowError/rowAskUser/final rowAssistant — immediately on append. rowToolCall — NOT while running (spinner animates per frame; it will collapse): settles when its matching rowToolResult (same runID+callID, today's rcKey) arrives — at which point the call row renders as nothing and the RESULT card is the settled visual — or when its run ends (agentResponseMsg or cancel), settling as the static orphan card ('…' glyph, exactly renderRunningToolCard's non-active branch). rowPermission with Action=prompt — NOT while undecided: settles when the decision event lands (prompt row then skips; the allow/deny one-liner is the visual) or when its run ends. rowToolResult — settled on arrival. Interim streaming text is never a row; the final rowAssistant supersedes it (and per finding 4, segments cleared by a tool call should first be appended as non-final rowAssistant — which settle immediately).

2) FRONTIER ADVANCE + ORDERING. After any Update that mutates the transcript: while flushed < len(rows) && settled(rows[flushed]) — render rows[flushed] through the existing renderRow/rowContext pipeline at chatWidth(m.width), honoring the startsTurn blank-line rule, append to a batch, flushed++. The frontier is a strict prefix pointer: an undecided permission prompt or still-running tool card BLOCKS it; nothing after an unsettled row may flush (this is naturally rare because the agent loop blocks on prompts, but cancelled runs leave orphans — settle them at run end, then flush, then flush the new visible "Run cancelled." system row). Emit each batch as ONE tea.Println of strings.Join(batch, "\n") returned from Update; if multiple Cmds are unavoidable use tea.Sequence (tea.Batch gives no ordering). Never Println the same row twice; the frontier only moves forward. Keep buildRowContext but scope it to rows[flushed:], retaining a small per-run cache of call-row hints/args for results whose call already flushed; the [auto] tag must be resolved before the result card flushes (decision events arrive before the result, so this holds).

3) VIEW() = LIVE TAIL ONLY: rows[flushed:] (running cards, undecided prompt rows), interimBlock/permission modal/ask modal/spec-review card, image chips, composer, suggestion/picker overlays, status line. Print the title bar ONCE at startup via tea.Println (not per frame) or keep it as a single status header. This bounds View to O(live tail): the height clip can no longer hide anything that matters, per-token renders stop walking the whole session (fixes the perf finding structurally), and the composer/status always survive the renderer's bottom-anchored clipping.

4) WIDTH AT FLUSH. Render settled rows at the width in effect when they flush; flushed lines are frozen and the terminal natively rewraps them on resize (same behavior as Claude Code). Two guards: (a) NEVER flush while m.width == 0 (first frame precedes the initial WindowSizeMsg) — queue settled rows until the real width arrives so history is never frozen at the 96-col default; (b) pre-fit every flushed line to the width (fitStyledLine already does this) because queued message lines are not width-tracked by the renderer and physical wraps are permanent. After a resize only the live tail re-renders at the new width.

5) /clear SEMANTICS. Scrollback cannot be un-printed. /clear resets the transcript to initialTranscript AND resets the frontier bookkeeping (flushed = len(new transcript), welcome row never flushes), then either (a) Println a faint '── cleared ──' divider, or (b) send tea.ClearScreen to wipe the visible screen while accepting that older history remains above in scrollback. Never attempt to repaint over already-flushed history. /clear mid-run keeps working: the in-flight run's future rows append after the reset and flush normally.

6) RESUME/REWIND REHYDRATION. All rehydrated rows (runID 0) are settled by construction: prompts are already decided, calls have results (or are orphans), assistant rows are final. Flush the resume summary card plus ALL rehydrated rows immediately in ONE Println batch, leaving an empty live region. This makes huge resumed sessions O(1) per subsequent frame instead of O(history), and the rehydrated history becomes selectable/scrollable native text. Same for /rewind's transcript rebuild (prefix it with the rewind summary row).

7) LONG OUTPUT POLICY. With real scrollback, flush diff/tool cards at full length (or a much higher cap) at settle time; keep a small cap only for the LIVE running-card preview in View. This is what makes long edit diffs reviewable (finding 3).

8) PREREQUISITE CORRECTNESS FIXES that interact with the frontier: (a) unique tool-call ids (Gemini per-request nonce) and/or run-scoped dedupe — otherwise the dedupe layer drops rows the frontier would flush; (b) append streamed segments as non-final assistant rows before clearing streamingText so flushed history contains the full narrative; (c) visible 'Run cancelled.' row at cancel so interrupted runs terminate cleanly in scrollback; (d) composer input.Width set from WindowSizeMsg so the always-live composer is usable.

This yields: full native scrollback and copy/paste of all completed history (emitted once, never repainted), a small flicker-free live region (spinner cards, undecided prompts blocking the frontier, streaming text, composer, status), correct collapse ordering (call→result, prompt→decision) preserved at flush time, resize behavior degraded only to native terminal rewrap of frozen history, and per-frame cost independent of session length.

#### [high/ux] Entire chat history lives inside View(); inline renderer clips it at terminal height, so scrollback/selection of history is impossible
`internal/tui/model.go:584`

The program is created with no alt-screen and no viewport (run.go:26-30 passes only WithContext/WithInput/WithOutput), and `func (m model) View() string { return m.transcriptView() }` re-renders the ENTIRE transcript plus composer/status every frame. There is not a single tea.Println/tea.Printf in the repo, so settled rows are never emitted to native terminal scrollback. Bubble Tea v1.3.10's standard renderer explicitly drops everything above the last `height` lines of the View before painting (standard_renderer.go:183-188), so once the transcript exceeds one screen, all older rows are silently discarded — they are never written to the terminal at all. Scrolling up in the terminal shows whatever preceded `zero`, not chat history; and because the renderer repaints the managed region in place (CursorUp(linesRendered-1)), even the visible window is continuously rewritten, making stable text selection/copy of history impossible despite mouse capture being correctly disabled.

```
model.go:584 `func (m model) View() string { return m.transcriptView() }`; transcriptView (model.go:599-667) iterates `for index, row := range m.transcript` with no height bound (m.height is only read by emptyState). bubbletea@v1.3.10/standard_renderer.go:183-188: `// We drop lines from the top of the render buffer if necessary, as we can't navigate the cursor into the terminal's scrollback buffer.  if r.height > 0 && len(newLines) > r.height { newLines = newLines[len(newLines)-r.height:] }`. grep confirms zero occurrences of tea.Println/WithAltScreen/viewport in internal/.
```

**Suggested fix:** Adopt a settled-row flush frontier (full spec in summary): keep only the live tail (running tool cards, undecided prompts, streaming text, composer, status) in View(), and emit each completed transcript row exactly once into native scrollback via tea.Println at the moment it settles.

#### [high/bug] Transcript dedupe key ignores run/turn, so Gemini's repeating synthetic tool-call IDs drop every tool card after the first turn
`internal/tui/transcript.go:137`

appendTranscriptRow dedupes via transcriptRowKey, which for tool calls/results is `kind:id` and for permission rows `kind:ToolCallID:Action` — with no run or sequence component. The Gemini provider synthesizes IDs `gemini_tool_N` from a per-request counter (`state := streamState{}` is fresh in every StreamCompletion, provider.go:147, 270), so turn 2 of the same agent run re-emits `gemini_tool_1`. hasTranscriptRow then finds turn 1's row with the same key and DROPS turn 2's tool-call row, its result row, and its permission prompt/decision rows. The rendering layer was fixed for exactly this (rcKey includes runID, with a comment naming Gemini's gemini_tool_N), but the append layer was not — the rows never reach the transcript to be rendered. Effect: in any multi-turn Gemini run (and in any session resumed then continued), only the first tool call/result per synthetic ID ever appears; later tools execute invisibly with no card and no permission record on screen.

```
transcript.go:136-139 `case rowToolCall, rowToolResult: if row.id != "" { return fmt.Sprintf("%d:%s", row.kind, row.id) }` and 113-120 `func appendTranscriptRow(... ) { if hasTranscriptRow(rows, row) { return rows } ... }`; providers/gemini/provider.go:268-271 `if id == "" { id = fmt.Sprintf("gemini_tool_%d", syntheticIndex) }` with `state := streamState{}` per request (line 147); rendering.go:124-126 comment: "some providers synthesize ToolCallIDs that repeat across turns (e.g. Gemini's gemini_tool_N)".
```

**Suggested fix:** Make synthetic IDs unique per request in the Gemini provider (e.g. fmt.Sprintf("gemini_tool_%d_%d", requestNonce, syntheticIndex) — the ID only needs to be stable within one stream), and/or scope transcript dedupe to (runID, id) over the rows of the same run only, since dedupe exists solely to reconcile the live agentRowMsg stream with the agentResponseMsg replay of the same run.

#### [high/ux] cardBodyMaxLines=16 permanently hides long diffs and tool output with no expansion or scroll path
`internal/tui/rendering.go:525`

Every tool-result card body (diffs, bash output, grep, read) is capped at 16 lines by capCardLines, collapsing the rest into a '… N more lines' trailer. A 400-line `git diff` or apply_patch result shows 16 lines. Because the architecture has no scrollback (finding 1), no viewport, and no expand keybinding/command, those hidden lines are unreachable for the lifetime of the session — the user cannot review the very edits they are being asked to approve. This breaks the core review loop of a coding agent: the only way to see what changed is to leave the TUI and run git diff manually.

```
rendering.go:524-525 `// cardBodyMaxLines caps every card body; hidden lines collapse into a "… N more lines" trailer.  const cardBodyMaxLines = 16` and 679-686 capCardLines `lines = lines[:cardBodyMaxLines]; return append(lines, ...Render(fmt.Sprintf("… %d more lines", hidden)))`. No expand command exists (commandDefinitions has none) and no scroll mechanism exists.
```

**Suggested fix:** Once settled rows flush to native scrollback (finding 1), flush diff cards at full length (cap only the LIVE running-card preview); short of the full architecture, raise the cap for diff-kind bodies and add an expand toggle (e.g. /expand or a key on the most recent card).

#### [medium/bug] Assistant text streamed before tool calls is discarded — vanishes from the transcript and the session log
`internal/tui/model.go:569`

Mid-run assistant prose (the text a model emits alongside/before its tool calls, e.g. 'Let me check the failing test first') is shown live via streamingText, then erased when the tool-call row arrives: the agentRowMsg handler sets m.streamingText = "" for rowToolCall and never appends the segment as a transcript row. The agent loop returns only the last turn's text as FinalAnswer (loop.go:233 `result.FinalAnswer = collected.Text`), and the TUI's session recording also persists only that final answer (model.go:1356-1362). So every earlier text segment disappears from the screen the instant a tool runs, and is absent from /resume rehydration too. The user watches explanatory text appear and then get destroyed.

```
model.go:567-571: `// a tool call ends the current streamed text segment  if msg.row.kind == rowToolCall { m.streamingText = "" }` — no rowAssistant is appended for the cleared segment; agent/loop.go:197-201 appends collected.Text only to provider messages, and loop.go:233 sets FinalAnswer from the final turn only; model.go:1349-1362 records only result.FinalAnswer as the assistant EventMessage.
```

**Suggested fix:** In the agentRowMsg handler (or in OnToolCall inside runAgentWithOptions), when a tool call arrives and streamingText is non-blank, append it as a non-final rowAssistant (and a corresponding pendingSessionEvent assistant message) before clearing streamingText.

#### [medium/ux] Esc/Ctrl+C cancellation leaves no visible marker in the live transcript
`internal/tui/model.go:1081`

cancelRun appends the 'Run cancelled.' marker ONLY to the persisted session event log (appendSessionEvent with sessions.EventError); nothing is appended to m.transcript. The comments at model.go:296 and :503 claim cancelRun 'writes the "Run cancelled." marker', but on screen the streaming text and spinner simply vanish (streamingText is cleared, pending goes false) with zero indication the run was interrupted, why output stopped, or that Esc worked. The marker only becomes visible later if the user happens to /resume the session (rehydration maps the EventError to a rowError). Tests assert only the session event (session_test.go:751).

```
model.go:1081-1087: `if m.pending && m.activeSession.SessionID != "" { if next, err := (*m).appendSessionEvent(sessions.EventError, map[string]any{"message": "Run cancelled."}); err == nil { *m = next } }` — no reduceTranscript/appendTranscriptRow call anywhere in cancelRun; the Esc handler (model.go:337-339) and Ctrl+C handler add nothing either.
```

**Suggested fix:** In cancelRun, also append a visible row: m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: "Run cancelled."}) (guarded on m.pending so idle Esc stays silent).

#### [medium/ux] Composer never sets textinput.Width, so input longer than the terminal is truncated with the cursor hidden — blind typing
`internal/tui/model.go:227`

textinput.New() is configured (prompt, styles, placeholder) but Width is never assigned anywhere in the package. In bubbles v1.0.0, horizontal scrolling only engages when Width > 0 (`if m.Width <= 0 || uniseg.StringWidth(string(m.value)) <= m.Width` keeps offset at 0 and View renders the full value). composerLine then hard-truncates the rendered line to the terminal width with fitStyledLine, cutting off the tail — which is exactly where the cursor and the characters being typed are. Any prompt longer than ~(width-prompt-hint) cells is typed blind: no cursor, no echo of new characters, and no way to see or edit the end of the input.

```
model.go:227-233 configures the input with no Width; grep confirms `input.Width` / `.Width =` never appears in internal/tui. composerLine (model.go:702-706): `line := input.View(); ... return joinHeaderLine(fitStyledLine(line, width-lipgloss.Width(hint)-2), hint, width)` — fitStyledLine (startup.go:200-208) truncates with '…'. bubbles@v1.0.0/textinput/textinput.go:331 shows scrolling requires Width > 0.
```

**Suggested fix:** On WindowSizeMsg (and at init), set m.input.Width = chatWidth(m.width) - lipgloss.Width(prompt) - reservedHintWidth so the textinput scrolls horizontally and keeps the cursor visible.

#### [medium/perf] Full-transcript re-render on every message plus O(n²) append path
`internal/tui/transcript.go:117`

Two compounding hot paths. (1) Bubble Tea calls View() after every message; View walks and re-styles the ENTIRE transcript (buildRowContext allocates 5 maps and scans all rows, then renderRow runs word-wrapping, regexes, and lipgloss styling per line) — and messages arrive per streamed token (agentTextMsg per delta) and per spinner tick (~12 fps via spinner.MiniDot) for the whole duration of a run. In a long session the per-token render cost is O(total transcript content). (2) appendTranscriptRow copies the whole slice on every append (`next := append([]transcriptRow{}, rows...)`) and hasTranscriptRow recomputes keys over all rows per append, making row appends O(n) copy + O(n) scan, i.e. O(n²) per session even though rows arrive one at a time.

```
transcript.go:113-120: `func appendTranscriptRow(rows []transcriptRow, row transcriptRow) []transcriptRow { if hasTranscriptRow(rows, row) { return rows } next := append([]transcriptRow{}, rows...); next = append(next, row); return next }`; hasTranscriptRow (122-133) loops all rows; model.go:441-446 (one Update per text delta), 447-455 (spinner tick while pending), model.go:610 `rc := buildRowContext(m.transcript)` per render over all rows.
```

**Suggested fix:** The flush-frontier architecture removes the render cost structurally (View only renders the unflushed tail). Independently: append in place (the model is already passed by value through Update, so the defensive full copy is unnecessary — append to the existing slice), and maintain the dedupe key set as a map on the model instead of rescanning all rows.

#### [medium/wiring] /style sets responseStyle that nothing ever reads — the command silently has no effect
`internal/tui/session_controls.go:117`

/style validates the value, stores m.responseStyle, and reports 'Style preference is stored for this TUI session.' But responseStyle is never threaded into agent.Options, the system prompt, or any request: runAgentWithOptions (model.go:1102-1133) sets Registry/PermissionMode/SystemPrompt/SessionID/Model/ReasoningEffort/Cwd/Images/ContextWindow and nothing style-related; a repo-wide grep for ResponseStyle outside internal/tui returns nothing — agent.Options has no such field. The only consumers are display surfaces (/context, /style). Unlike /theme and /input-style (which explicitly answer 'does not have a backend setting yet'), /style claims success, so users selecting 'concise'/'explanatory' get zero behavioral change with no warning.

```
session_controls.go:109-123: `m.responseStyle = args; return m, strings.Join([]string{"Style", "active style: " + m.responseStyle, "Style preference is stored for this TUI session."}, "\n")`. grep -rn ResponseStyle internal/ (excluding tui, tests) yields no hits; runAgentWithOptions never references m.responseStyle.
```

**Suggested fix:** Either thread it into the run (append a style directive to options.SystemPrompt in runAgentWithOptions based on m.responseStyle) or route /style through shellOnlyCommandText like /theme so the no-op is honest.

#### [medium/bug] looksLikeDiff false-positives on any output containing a line starting with '---', breaking bash/generic cards
`internal/tui/view.go:394`

toolCardBody dispatches to diffCardBody whenever looksLikeDiff(detail) is true, checked BEFORE the bash branch (rendering.go:663-668). looksLikeDiff returns true if ANY line has prefix '@@', '+++', or '---'. Output of a bash command that merely contains a '---' separator (YAML document markers, Markdown front matter, test-framework section dividers, `ls` of files named '---…') is therefore rendered as a unified diff card: the bash command line and the exit-status footer are lost (diffCardBody parses neither), the raw 'stdout:'/'stderr:'/'exit_code: 0' protocol lines render as diff metadata in the body, and an empty path renders in the card head. Lines beginning '-' are misclassified as deletions with bogus gutter numbers.

```
view.go:389-400: `func looksLikeDiff(text string) bool { ... for _, line := range strings.Split(text, "\n") { if strings.HasPrefix(line, "@@") || strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") { return true } } ... }`; rendering.go:663-670: `switch { case looksLikeDiff(detail): return diffCardBody(detail, width) ... case name == "bash": return bashCardBody(hint, detail, width)`.
```

**Suggested fix:** Tighten the heuristic: require a hunk header AND a file header pair, e.g. at least one line matching `^@@ -\d` plus one of `^\+\+\+ `/`^--- ` (with trailing space), or for bash results strip the stdout:/stderr:/exit_code: envelope first and only diff-render the stdout section when it matches.

#### [medium/bug] wrapPlainText collapses all internal whitespace, destroying code indentation in assistant answers and streamed text
`internal/tui/rendering.go:261`

wrapPlainText re-tokenizes every line with strings.Fields and rejoins with single spaces, so leading indentation and aligned columns are destroyed. renderAssistantRow (final answers), the interim streaming block, renderUserRow, and wrapDetailBlock all route through it. For a coding agent whose answers routinely include indented code snippets, every final answer's code is flattened ('    return nil' renders as 'return nil'), making multi-level Python/Go snippets unreadable and copy-broken. Tabs are likewise collapsed.

```
rendering.go:255-281: `for _, paragraph := range strings.Split(...) { ... line := ""; for _, word := range strings.Fields(paragraph) { ... line += " " + word ... } }` — strings.Fields drops all leading/internal whitespace; renderAssistantRow (line 324) and interimBlock (model.go:678) call wrapPlainText on raw model text.
```

**Suggested fix:** Preserve leading whitespace per source line: capture the indent prefix before Fields (e.g. indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]), wrap only the remainder to measure-len(indent), and re-prefix each wrapped line (continuations included) with the indent.

#### [medium/ux] Resize is unhandled beyond storing width/height: shrink garbles the whole surface, and the first frame renders at a hardcoded 96 cols
`internal/tui/model.go:456`

On tea.WindowSizeMsg the model only stores m.width/m.height; the next frame reflows at the new width, but everything already painted was emitted at the old width. When the terminal narrows, those physical lines re-wrap, the standard renderer's CursorUp(linesRendered-1) repositioning (computed from logical line counts) lands on the wrong row, and stale wrapped fragments are pushed into scrollback above a duplicated/garbled frame — and since this app's entire UI is one full-height managed region, the whole chat corrupts on every shrink. Separately, Bubble Tea paints the first frame BEFORE the resize handler is registered (tea.go:700 'Render the initial view' precedes p.handleResize()), so the first frame uses m.width==0 → chatWidth(0)=defaultStartupWidth=96: on any terminal narrower than 96 the 96-cell rules and header physically wrap, desyncing the renderer from frame one.

```
model.go:456-459: `case tea.WindowSizeMsg: m.width = msg.Width; m.height = msg.Height; return m, nil`; startup.go:282-290: `func chatWidth(width int) int { if width <= 0 { return defaultStartupWidth } ... }` (defaultStartupWidth=96); bubbletea@v1.3.10/tea.go:699-710 renders the initial view before `p.handlers.add(p.handleResize())`; standard_renderer.go:176-177 `buf.WriteString(ansi.CursorUp(r.linesRendered - 1))` assumes no physical re-wrap.
```

**Suggested fix:** Render a minimal first frame until the first WindowSizeMsg arrives (e.g. return "" or a single status line while m.width==0) so nothing is emitted at the guessed 96-col width; the shrink corruption is bounded by the flush-frontier architecture (only the small live tail is repainted) and can be further mitigated by issuing tea.ClearScreen on width decrease.

#### [low/dead-code] Dead footer/help rendering code and dead message fields
`internal/tui/rendering.go:68`

footerText(), commandFooterText(), formatCommandFooterText() with its pending 'Esc cancel'/'Esc clear' switch, and defaultCommandFooterText (rendering.go:62-118) are never called from production code — the real bottom readout is statusLine() in view.go, which shows none of the advertised command list. formatCommandHelpLines (commands.go:290-292) and indentText (view.go:380-387) are likewise test-only. bashResultMsg.command (command_bash.go:20-22) is populated by runBashEscape but never read by the bashResultMsg handler (model.go:574-576 uses only msg.output). renderFocusedAskUserPrompt's `input string` parameter (rendering.go:490) is never used in the function body. These keep tests green (model_test.go asserts 'Esc cancel') while validating UI text no user ever sees.

```
grep shows footerText/commandFooterText/formatCommandHelpLines/indentText referenced only from *_test.go; model.go:574-576 `case bashResultMsg: m.transcript = reduceTranscript(..., text: msg.output)` ignores msg.command; rendering.go:490 `func renderFocusedAskUserPrompt(prompt pendingAskUserPrompt, input string, width int)` never references input.
```

**Suggested fix:** Delete footerText/commandFooterText/formatCommandFooterText/defaultCommandFooterText/formatCommandHelpLines/indentText (and their tests) or wire the footer into statusLine; drop bashResultMsg.command and the unused input parameter.

#### [low/bug] Byte-index truncation can split UTF-8 runes in session titles and tool-result row text
`internal/tui/transcript.go:303`

truncateTUIOutput slices by byte index (`output[:limit]`), and tuiSessionTitle / specImplementationTitle do the same (`title[:tuiSessionTitleLimit]`, session.go:93, spec_mode.go:302). A prompt or output whose 240th/80th byte falls inside a multi-byte rune (CJK, emoji, accented text) produces an invalid UTF-8 fragment. The session title case matters most: it is persisted via sessionStore.Create and later rendered in /resume cards and JSON-marshalled (invalid bytes become U+FFFD replacement characters in the stored metadata). The rest of the package carefully uses rune-safe truncation (truncateRunes, splitAtWidth), making these two the odd ones out.

```
transcript.go:297-304: `if limit <= 0 || len(output) <= limit { return output } return output[:limit] + " [truncated]"`; session.go:91-94: `if len(title) > tuiSessionTitleLimit { title = title[:tuiSessionTitleLimit] }`; spec_mode.go:301-303 identical pattern.
```

**Suggested fix:** Use the existing truncateRunes helper (view.go:402) in truncateTUIOutput, tuiSessionTitle, and specImplementationTitle.

#### [low/bug] /exit during the Ctrl+C checkpoint-flush wait quits immediately, orphaning the checkpoints the deferred quit exists to protect
`internal/tui/model.go:853`

After Ctrl+C cancels an in-flight run, the model deliberately defers tea.Quit until the cancelled run's agentResponseMsg flushes its EventSessionCheckpoint events (flushRunIDs machinery, extensively documented at model.go:67-75 and 295-314), and handleSubmit blocks NEW PROMPTS while exiting (`if command.kind == commandPrompt && (m.pending || m.exiting)`). But commandExit is not guarded: typing /exit (or /quit) while m.exiting is waiting on the flush returns tea.Quit immediately, dropping the pending agentResponseMsg and orphaning the checkpoint blobs already written to disk — exactly the /rewind breakage the flush mechanism prevents. Same hole applies to /exit while a run is merely pending (no Ctrl+C at all): it quits without cancelling or flushing.

```
model.go:836-839 guards only commandPrompt: `if command.kind == commandPrompt && (m.pending || m.exiting) { return m, nil }`; model.go:853-855: `case commandExit: m.exiting = true; return m, tea.Quit` — no pending/flushRunIDs check, unlike the Ctrl+C path at 305-314.
```

**Suggested fix:** In the commandExit case, mirror the Ctrl+C path: call m.cancelRun() if pending, set m.exiting = true, and return (m, nil) instead of tea.Quit whenever len(m.flushRunIDs) > 0, letting the existing agentResponseMsg handler fire the deferred quit.


### TUI interactions (commands, pickers, sessions)

Audited the zero TUI interaction surfaces (commands.go, command_center.go, command_views.go, command_bash.go, command_output.go, autocomplete.go, picker.go, session.go, session_controls.go, spec_mode.go, plan_command.go, image_attach.go, model_catalog.go) plus the model.go/transcript.go/rendering.go/view.go wiring they depend on, and traced rehydration into internal/sessions (rewind.go, checkpoint.go, exec_session.go) and the Gemini provider. 11 concrete findings. Two high: (1) the transcript dedup key (kind:id, no runID/ordinal) collides with Gemini's per-turn-reset synthesized ToolCallIDs, silently dropping later turns' tool cards live and on /resume rehydration (where all rows carry runID=0) — the rcKey comment acknowledges the exact hazard the dedup layer ignores; (2) a cancelled run's deferred event flush appends to m.activeSession at flush time, so /resume or /spec in the window writes the old run's tool/checkpoint events into the wrong session, with checkpoint payloads referencing blobs stored under the original session — corrupting the new session's log and breaking its /rewind. Mediums: /rewind isn't gated on outstanding flushRunIDs so ApplyRewind prunes the cancelled run's not-yet-referenced checkpoint blobs (the documented orphan-vulnerable window) and rewound-away events reappear after the marker; ask_user requests persist without a content key and are dropped on rehydration while answers are never persisted at all; /exit (/quit) mid-run quits without the cancel/flush protection Ctrl+C implements (and /spec ignores m.exiting); appendTranscriptRow's full-slice copy + linear dedup scan is O(N²) on resume/rehydration. Lows: unreachable resumeText branch plus a never-rendered footer subsystem and stale '! bash'/'/ commands' footer comments (the shell escape and @file picker are undocumented in /help); byte-index truncation splitting multibyte runes in session titles and tool output; /compact's write-only request counter; silent inert UI during the post-Ctrl+C flush wait; and the permission card's unlabeled [esc] chip that actually cancels the whole run. Verified clean: slash-command registration parity (all 23 kinds defined are handled in handleSubmit; autocomplete and /help share commandDefinitions), picker modality (keys swallowed, busy-gated, Esc precedence correct), permission event JSON tags round-trip exactly (toolCallId/name/action match payloadString keys), bash-escape gating behind PermissionModeUnsafe referencing a real flag, the image attach/submit-time vision re-check flow, imageinput.LoadFile bounds, and the sessions-store rewind/prune locking itself.

#### [high/bug] Transcript dedup key ignores runID, silently dropping tool rows for providers with per-turn synthesized ToolCallIDs
`internal/tui/transcript.go:135`

appendTranscriptRow dedupes rows via transcriptRowKey, which for tool call/result rows is only `kind:id` — runID is excluded. Gemini synthesizes ToolCallIDs as gemini_tool_N where N restarts at 1 for every stream (internal/providers/gemini/provider.go:270, `state.syntheticToolIndex` lives on a per-request streamState), so every agent-loop turn reuses gemini_tool_1, gemini_tool_2, … . Consequence in a single live run: turn 1 appends rows keyed 3:gemini_tool_1 / 4:gemini_tool_1; turn 2's tool call and result with the same id hit hasTranscriptRow and are silently dropped — only the first turn's tool cards ever render. The same collision hits rehydration (/resume): all rehydrated rows carry runID=0, so a second run's duplicate-id events are dropped while building the transcript in transcriptRowsFromSessionEvents/handleResumeCommand, and after a resume, NEW live runs (runID 1, 2, …) collide with nothing — but a second resume-then-run in the same TUI process collides live rows against rehydrated runID-0 rows is avoided only by luck of key format; within-run repeats remain broken regardless. Note the code itself documents the hazard: rendering.go:127-134 says rowContext maps are keyed by rcKey(runID,id) precisely because "some providers synthesize ToolCallIDs that repeat across turns (e.g. Gemini's gemini_tool_N)" — but the dedup layer that decides whether a row exists at all never got the same treatment, and even rcKey cannot distinguish two turns of the same run or two rehydrated runs (both runID=0).

```
transcript.go:137-140: `case rowToolCall, rowToolResult:\n  if row.id != \"\" {\n    return fmt.Sprintf(\"%d:%s\", row.kind, row.id)\n  }` consumed by transcript.go:113-119 `func appendTranscriptRow(...) { if hasTranscriptRow(rows, row) { return rows } ... }`; gemini/provider.go:268-271: `id := functionCall.ID\nif id == \"\" {\n  id = fmt.Sprintf(\"gemini_tool_%d\", syntheticIndex)\n}` with syntheticToolIndex on per-stream state; rendering.go:131-134 comment: "some providers synthesize ToolCallIDs that repeat across turns (e.g. Gemini's gemini_tool_N), so a bare id could attribute a decision or a result to a different run's call."
```

**Suggested fix:** Give each row a unique identity instead of content-derived dedup: stamp rows with a per-run monotonically increasing ordinal in runAgentWithOptions, include runID+ordinal in transcriptRowKey (and key rcKey on the same tuple, assigning synthetic run boundaries — e.g. event sequence — to rehydrated rows instead of runID=0). Minimal alternative: in the agentResponseMsg handler, track how many of this run's rows were already delivered via agentRowMsg and append only the remainder, removing the need for content dedup entirely.

#### [high/race] Cancelled-run event flush appends to whichever session is active at flush time, contaminating other sessions and breaking their /rewind
`internal/tui/model.go:512`

When a run is cancelled (Esc/Ctrl+C), its goroutine keeps running and later returns agentResponseMsg; the handler flushes the accumulated session events via m.appendSessionEvents, and appendSessionEvent (session.go:46) writes to m.sessionStore.AppendEvent(m.activeSession.SessionID, …) — the session active NOW, not the session the run belonged to. After cancelling (pending=false) the user can immediately run /resume <other-id> (sets m.activeSession to another session) or /spec (creates a brand-new draft session) while the cancelled goroutine is still unwinding (it can block in the provider HTTP call for seconds). When the flush lands, the old run's tool_call/tool_result/EventSessionCheckpoint events are appended to the WRONG session's events.jsonl. Worse, the checkpoint payloads reference content-addressed blobs stored under the ORIGINAL session's directory (checkpoint.go blobPath(sessionID, hash)), so a later /rewind in the contaminated session calls readBlob with hashes that don't exist there and reports those files as skipped/not recoverable, while the original session's blobs become unreferenced and get deleted by its next pruneOrphanBlobs.

```
model.go:506-513: `if _, flushing := m.flushRunIDs[msg.runID]; flushing { delete(m.flushRunIDs, msg.runID); ... m, flushRows = m.appendSessionEvents(flushableSessionEvents(msg.sessionEvents))`; session.go:46-49: `event, err := m.sessionStore.AppendEvent(m.activeSession.SessionID, sessions.AppendEventInput{...})` — m.activeSession is re-read at flush time; /resume reassigns it at session.go:116 `m.activeSession = *session` and /spec at spec_mode.go:93 `m.activeSession = session`, neither gated on outstanding flushRunIDs.
```

**Suggested fix:** Capture the originating session id in the goroutine (the closure already holds the model copy: `sessionID := m.activeSession.SessionID`), carry it on agentResponseMsg, and have the flush path call sessionStore.AppendEvent with that id directly instead of going through m.activeSession. Optionally also block /resume and /spec while len(m.flushRunIDs) > 0.

#### [medium/race] /rewind allowed while a cancelled run's flush is outstanding — prunes the cancelled run's checkpoint blobs and re-appends rewound-away events
`internal/tui/session_controls.go:176`

handleRewindCommand refuses to rewind only while m.pending is true. After Esc-cancelling a run, pending is false but the run id sits in m.flushRunIDs and its EventSessionCheckpoint events (whose blobs were already written by SnapshotForCheckpoint) have NOT been appended yet — exactly the orphan-vulnerable window the sessions package documents (checkpoint.go:89-97: blobs "are ORPHAN-VULNERABLE — a concurrent pruneOrphanBlobs/ApplyRewind can delete them — until the caller appends an EventSessionCheckpoint"). If the user runs /rewind in that window, ApplyRewind's pruneOrphanBlobsLocked (rewind.go:261) deletes the cancelled run's not-yet-referenced blobs. When the cancelled goroutine finally returns, the flush appends its tool/checkpoint events AFTER the rewind marker: the event log regains events the user just rewound away (they rehydrate on the next /resume), and the flushed checkpoint events reference deleted blobs, so any later /rewind reports those files "skipped (not recoverable)" — the undo capability for the cancelled run's mutations is permanently lost.

```
session_controls.go:176-178: `if m.pending {\n  return m, \"Rewind\\ncannot rewind while a run is in progress.\"\n}` — no check of m.flushRunIDs; rewind.go:261: `_, _ = store.pruneOrphanBlobsLocked(sessionID)` inside ApplyRewind; model.go:75 documents flushRunIDs exists precisely so "the checkpoint blobs already written to disk would [not] be orphaned (breaking /rewind)".
```

**Suggested fix:** In handleRewindCommand, also refuse while len(m.flushRunIDs) > 0 (e.g. "a cancelled run is still flushing; retry in a moment"), mirroring the m.pending guard.

#### [medium/wiring] ask_user exchanges vanish on rehydration and user answers are never persisted
`internal/tui/session.go:190`

The agent goroutine persists an ask_user request as an EventMessage whose payload (askUserSessionPayload, transcript.go:190-211) has role "ask_user", toolCallId, and questions — but no "content" key. transcriptRowsFromSessionEvents' EventMessage branch reads only payloadString(payload, "content") and `continue`s when it is empty, so every persisted ask_user event is silently dropped on /resume: the rehydrated transcript shows no trace of the questionnaire. The dedup comment in transcript.go:148-150 ("Prefer row.id … it survives rehydration … so a reloaded ask_user row still dedupes correctly") describes rehydration behavior that does not exist — no code path ever constructs a rowAskUser from a persisted event. Additionally, the user's ANSWERS are never recorded as a session event at all (OnAskUser only appends the request, model.go:1197-1200), so a resumed session's context (sessions.FormatExecPrompt over ContextEvents) loses what the user answered — the agent sees the questions but not the answers.

```
session.go:190-193: `content := payloadString(payload, \"content\")\nif content == \"\" {\n  continue\n}`; transcript.go:202-205 builds the payload with `\"role\": \"ask_user\", \"toolCallId\": …, \"questions\": …` and no content; model.go:1197-1200 appends only `pendingSessionEvent{Type: sessions.EventMessage, Payload: askUserSessionPayload(request)}` — nothing is appended after `answers := <-answerCh`.
```

**Suggested fix:** In transcriptRowsFromSessionEvents, branch on role=="ask_user" before the content check and rebuild a rowAskUser (id from toolCallId, text/detail from header+questions). In OnAskUser, append a second EventMessage carrying the collected answers after the answer channel delivers.

#### [medium/bug] /exit during an in-flight run quits without cancelling or flushing, orphaning checkpoints; /spec can start a run while exiting
`internal/tui/model.go:853`

The Ctrl+C handler goes to great lengths (model.go:294-314, flushRunIDs, deferred tea.Quit) to keep a cancelled run's checkpoint session events from being lost. But the /exit (alias /quit) command path returns tea.Quit immediately without calling cancelRun or waiting for any flush: a run in flight is abandoned, its goroutine is killed with the program, and the EventSessionCheckpoint events batched in that goroutine are never appended — the blobs already written by SnapshotForCheckpoint stay orphaned and /rewind cannot reference the cancelled run's mutations: exactly the loss the Ctrl+C comments say flushRunIDs exists to prevent. Separately, the exiting guard only covers commandPrompt (`command.kind == commandPrompt && (m.pending || m.exiting)`), so during the Ctrl+C deferred-quit window the user can run /spec, which checks only m.pending (spec_mode.go:22) and starts a NEW agent run that the deferred tea.Quit then aborts mid-flight — orphaning that run's checkpoints too.

```
model.go:853-855: `case commandExit:\n  m.exiting = true\n  return m, tea.Quit` — no cancelRun, no flush wait; contrast model.go:296-305 comment: "quitting now would drop that message, orphaning the checkpoints and breaking /rewind"; model.go:837: `if command.kind == commandPrompt && (m.pending || m.exiting)` — only prompts are gated; spec_mode.go:22: `if m.pending {` — m.exiting not checked.
```

**Suggested fix:** Make commandExit mirror the Ctrl+C path: call m.cancelRun(), set m.exiting, and return tea.Quit only when len(m.flushRunIDs)==0, otherwise defer to the flush-drain branch. Add `|| m.exiting` to handleSpecCommand's run gate.

#### [medium/perf] appendTranscriptRow is O(N) copy + O(N) dedup scan per row — O(N²) on /resume rehydration and long runs
`internal/tui/transcript.go:113`

Every appendTranscriptRow call allocates a brand-new slice and copies the entire existing transcript (`append([]transcriptRow{}, rows...)`), and hasTranscriptRow linearly scans all rows computing transcriptRowKey for each. handleResumeCommand and handleRewindCommand append every rehydrated event row through this path, so resuming a session with N events costs O(N²) copies and O(N²) key computations (a few thousand events → millions of struct copies of ~150-byte rows, gigabytes of memcpy). The same cost applies per agentRowMsg and per row in the agentResponseMsg batch on long-running sessions, since each row append re-copies the whole transcript.

```
transcript.go:113-120: `func appendTranscriptRow(rows []transcriptRow, row transcriptRow) []transcriptRow {\n  if hasTranscriptRow(rows, row) { return rows }\n  next := append([]transcriptRow{}, rows...)\n  next = append(next, row)\n  return next\n}`; hasTranscriptRow (122-133) scans every existing row; called in a loop at session.go:127-129 (`for _, row := range transcriptRowsFromSessionEvents(events) { rows = appendTranscriptRow(rows, row) }`) and model.go:543-545.
```

**Suggested fix:** Append in place (`return append(rows, row)` — every caller already owns the slice it passes in from the Update goroutine) and maintain a map[string]struct{} of seen row keys on the model (rebuilt on /clear, /resume, /rewind) for O(1) dedup.

#### [low/dead-code] Dead code: unreachable resumeText branch, never-rendered footer helpers, and stale comments referencing a footer that no longer exists
`internal/tui/command_center.go:44`

resumeText's `args != ""` branch (returns "requested session: …" guidance) is unreachable: the only caller is handleResumeCommand which calls m.resumeText("") exclusively (session.go:104) and handles non-empty args itself. Beyond that, an entire footer subsystem is never rendered by any view: footerText, commandFooterText, formatCommandFooterText, and defaultCommandFooterText (rendering.go:62-118), plus listCommandNames (commands.go:281), formatCommandHelpLines (commands.go:290), and indentText (view.go:380) have no non-test callers (verified by grep across internal/, excluding _test.go). The comments at commands.go:238 ("the footer advertises '! bash'") and autocomplete.go:56-57 ("the footer advertises '/ commands'") describe UI that doesn't exist — and consequently the "!" shell escape and "@file" picker have zero in-product discoverability: /help (formatGroupedCommandHelp) lists neither.

```
command_center.go:44: `if args != "" { return renderCommandOutput(commandOutput{ Title: \"Sessions\", ... \"requested session: \" + args ...` with the only call site session.go:104 `return m, m.resumeText(\"\")`; grep over internal/ excluding tests shows footerText/commandFooterText/defaultCommandFooterText/listCommandNames/formatCommandHelpLines/indentText only at their definitions; transcriptView (model.go:599-667) renders titleBar/composerLine/statusLine — no footer call.
```

**Suggested fix:** Delete the unreachable resumeText branch and the unused footer/help helpers (or wire footerText into the view if it was meant to render); update the stale comments; add "! <cmd>" and "@file" lines to helpText so the features are discoverable.

#### [low/bug] Byte-index truncation can split multibyte runes in session titles and tool-result text
`internal/tui/session.go:93`

tuiSessionTitle truncates with `title[:tuiSessionTitleLimit]` (byte index 80), specImplementationTitle does the same (spec_mode.go:301-303), and truncateTUIOutput slices `output[:limit]` at byte 240 (transcript.go:297-304). A prompt or spec title containing multibyte UTF-8 (CJK, emoji, accented text) near the boundary gets cut mid-rune, producing invalid UTF-8 that is persisted into session metadata JSON (json.Marshal mangles it to U+FFFD) and shown in /resume cards and transcript row text. The codebase already has a correct rune-safe truncateRunes helper (view.go:402) used elsewhere, so these three are inconsistent stragglers.

```
session.go:92-94: `if len(title) > tuiSessionTitleLimit {\n  title = title[:tuiSessionTitleLimit]\n}`; spec_mode.go:301-303: `if len(title) > tuiSessionTitleLimit {\n  title = title[:tuiSessionTitleLimit]\n}`; transcript.go:300-303: `if limit <= 0 || len(output) <= limit { return output }\nreturn output[:limit] + \" [truncated]\"`.
```

**Suggested fix:** Use truncateRunes(title, tuiSessionTitleLimit) in both title helpers and rune-based slicing in truncateTUIOutput (`string([]rune(output)[:limit])` or reuse truncateRunes).

#### [low/wiring] /compact request state is write-only — nothing consumes it
`internal/tui/session_controls.go:165`

handleCompactCommand increments m.compactRequests, which is read back only by compactText/compactionStatus to display "requested, not yet compacted" and "Backend state: pending integration". No code path ever consumes the request: it never triggers transcript compaction, never writes a sessions EventCompaction (the event type exists in internal/sessions/store.go:37), and never interacts with the agent loop's compaction (which is wired separately via Options.ContextWindow in runAgentWithOptions). The command is registered, autocompletable, and described as "Show or request transcript compaction state", but the "request" half is a counter feeding its own status text — a field set but never meaningfully read.

```
session_controls.go:160-166: `if args != \"\" { return m, \"Compact\\nusage: /compact [status]\" }\nm.compactRequests++\nreturn m, m.compactText(true)`; the only readers are compactText (line 257: `\"requested: \" + boolText(m.compactRequests > 0)`) and compactionStatus (line 270-275); grep shows no other consumer of compactRequests.
```

**Suggested fix:** Either wire the request to an actual action (append a sessions.EventCompaction and/or trigger an agent-loop summarization of m.sessionEvents), or change the command description/output to make explicit that compaction is not yet implemented rather than implying a tracked request will be honored.

#### [low/ux] After Ctrl+C with a hung run the UI is silently inert: no exiting indicator, prompts refused without feedback
`internal/tui/model.go:837`

Ctrl+C during a run sets m.exiting and defers tea.Quit until the cancelled goroutine returns — which can take many seconds if the provider call doesn't honor cancellation promptly. In that window: pending is false so the spinner and "running…" placeholder disappear, nothing in the view indicates the app is shutting down or waiting on a flush, and handleSubmit silently swallows any prompt the user types (`command.kind == commandPrompt && (m.pending || m.exiting)` returns with no transcript notice). The app looks alive (composer accepts text) but does nothing on Enter and then quits at an arbitrary later moment, which reads as a hang followed by a crash.

```
model.go:836-839: `// While exiting (Ctrl+C waiting on the cancelled run's checkpoint flush) ...\nif command.kind == commandPrompt && (m.pending || m.exiting) {\n  return m, nil\n}` — no user-visible message; model.go:311-313 returns `m, nil` after Ctrl+C with pending flush, and transcriptView/statusLine render no exiting state (no reference to m.exiting or m.flushRunIDs anywhere in view code).
```

**Suggested fix:** When m.exiting with len(m.flushRunIDs) > 0, render a status segment like "exiting — flushing cancelled run…" (statusLine or interim block), and append a system note ("shutting down; prompt ignored") instead of silently returning when a prompt is submitted while exiting.

#### [low/ux] Permission card advertises an unlabeled [esc] action whose actual effect is cancelling the entire run
`internal/tui/rendering.go:482`

renderFocusedPermissionPrompt's action row ends with a bare `[esc]` chip with no verb, sitting next to "[a] allow once", "[y] always", "[d] deny". handlePermissionKey handles only a/d/y; Esc falls through to the global KeyEsc branch, which clears the composer and calls cancelRun — killing the whole run, not just declining this one tool call. The spec-review card labels its chip "[esc] cancel" and Esc there cancels only the review, so the same unlabeled chip on the permission card invites the (wrong) assumption that Esc is a soft dismiss/deny; instead the user loses the rest of the turn.

```
rendering.go:478-482: `actions := zeroTheme.badge.Render(\" [a] allow once \") + ... + fill(zeroTheme.faint).Render(\"[esc]\")` — no label; model.go:316-340 KeyEsc: with pendingPermission non-nil none of the earlier modal branches match, so it reaches `if m.pending { m.cancelRun() }`, cancelling the run (handlePermissionKey at model.go:720-731 handles only \"a\",\"d\",\"y\").
```

**Suggested fix:** Label the chip with its real effect ("[esc] cancel run"), or make Esc while pendingPermission resolve the prompt as a deny (m.resolvePermission(permissionDecisionDeny)) and reserve run-cancel for a second Esc.


### Cron, background, ops

Audited internal/cron, background, observability, notify, update, release, doctor, npmwrapper, and installtest (all source read in full; consumers in internal/cli, internal/tui, and internal/specialist traced; two cron findings reproduced with throwaway tests). The cron next-run math is solid for spring-forward DST gaps and century leap-year gaps, but provably double-fires wall-clock schedules during the DST fall-back repeated hour. The cron store has zero inter-process locking: the scheduler's minutes-long read-modify-write clobbers concurrent pause/edit operations and two schedulers (forever-mode plus the documented system-cron --once usage) double-fire jobs. `cron add` has an ordering bug where --recipe silently overrides the user's positional cron expression despite a guard that proves the opposite intent. In background process lifecycle, POSIX kills only the leader PID (Windows tree-kills via taskkill /T) so SIGKILL escalation leaks the specialist's subprocess tree, a second zero process clobbers a live sibling's running-task metadata to error/PID=0 at Manager load, and the post-SIGTERM grace polling can SIGKILL a recycled PID. Smaller items: `zero update --check` is dead on non-ldflags builds (version \"dev\" rejected pre-network), TUI /doctor omits config paths so its config checks always warn while the CLI passes them, byte-indexed truncation splits UTF-8 runes in cron list/error output, and notify's escape sanitizer misses C1 controls. Notify focus tracking (WithReportFocus, SetFocused at launch, Focus/Blur handling), doctor redaction of apiKey details, observability crash recovery wiring, release checksum/packaging path guards, and the npm wrapper/install scripts checked out clean.

#### [high/race] Cron store has no inter-process locking: paused/edited jobs are silently clobbered by the scheduler's stale read-modify-write, and concurrent schedulers double-fire
`internal/cli/cron_run.go:173`

There is no file locking anywhere in internal/cron or the cron CLI (grep for flock/lockfile returns nothing). fireJob captures a Job struct from store.List(), synchronously runs the entire `zero exec` (which can take minutes), then writes the WHOLE stale struct back with store.Update(job). Any concurrent edit made during the run is lost: `zero cron pause <id>` issued from another terminal while the daemon is mid-fire is overwritten back to Status=active when the fire completes, so the paused job silently keeps firing (continuing to spend model tokens). Equally, the documented usage of running `zero cron run --once` under system cron (comment at line 58-61) alongside a forever-mode daemon means two schedulers List() the same due job and both fire it — duplicate agent runs — because nothing serializes NextRunAt advancement. store.writeJob also uses a fixed temp name metadata.json.tmp, so two concurrent writers race on the same temp file.

```
cron_run.go fireJob: `code := exec(args, &outBuf, &errBuf)` ... (minutes later) ... `if err := store.Update(job); err != nil {` — `job` is the pre-exec snapshot including Status/Cwd/Model. store.go writeJob: `tmp := filepath.Join(dir, "metadata.json.tmp")` (fixed name, no lock).
```

**Suggested fix:** Add an advisory file lock around job mutation: acquire <jobdir>/.lock (O_CREATE|flock LOCK_EX on POSIX, LockFileEx on Windows) in fireJob and in cronSetStatus/cronResume; inside the lock re-Get the job, verify Status==active and NextRunAt unchanged before firing/updating, and merge only the fields fireJob owns (FireCount, NextRunAt, Status-pause-on-invalid) into the freshly read record. Also make writeJob use os.CreateTemp for the temp file.

#### [medium/bug] Cron Next double-fires wall-clock schedules on DST fall-back (repeated hour)
`internal/cron/schedule.go:171`

Schedule.Next matches on local wall-clock fields while advancing in absolute time, so on a DST fall-back day every wall-clock instant in the repeated hour occurs twice and Next returns the second occurrence after the first fires. A daily job '30 1 * * *' in America/New_York fires at 01:30 EDT, then fireJob computes Next(fired) which walks minute-by-minute through 01:59 EDT into 01:00 EST (same wall hour repeats) and returns 01:30 EST — one absolute hour later — so the daemon runs the job twice that day. Empirically verified: Next(2026-11-01 00:00 NY) = 01:30 -0400 EDT, then Next of that = 01:30 -0500 EST (1h apart, same wall clock). Vixie cron explicitly suppresses this duplicate; the package doc only claims robustness for spring-forward gaps.

```
case !has(s.minute, t.Minute()):
	t = t.Add(time.Minute)  // crosses 01:59 EDT -> 01:00 EST, hour 1 still matches, returns 01:30 EST
Verified output: "first fire: 2026-11-01 01:30:00 -0400 EDT ... second fire: 2026-11-01 01:30:00 -0500 EST ... DOUBLE FIRE on fall-back day confirmed"
```

**Suggested fix:** After finding a candidate t, detect the repeated-hour case: if the same wall-clock fields already matched earlier in absolute time within the run's advance (i.e., t.Add(-time.Hour) has identical wall clock and is after `after`), skip to the next distinct wall-clock match. Simplest concrete guard in fireJob/Next: compute n := s.Next(after); if n and s.Next(n) share the identical wall-clock representation (n.Format differs only by zone offset) treat the duplicated occurrence as already satisfied and advance past it — or document/accept first-occurrence-only by checking `n.Sub(after) < time.Hour && sameWallClock(n, after)`.

#### [medium/bug] `zero cron add <expr> --recipe R` silently discards the user's cron expression in favor of the recipe's
`internal/cli/cron.go:112`

In cronAdd, the recipe block runs BEFORE the positional argument is assigned to expr. At the recipe block expr is always "" (there is no --expr flag; expr only ever comes from positional[0] at line 123), so the guard `if expr == "" { expr = r.Expr }` — whose existence proves the intent that an explicit expression should win — always takes the recipe's expression. Then `if len(positional) == 1 && expr == ""` is false, so the user-supplied schedule is silently ignored with no error. Empirically verified: `zero cron add "0 12 * * *" --recipe git-recap` stores Expr="*/30 * * * *" (the recipe default), not the requested daily-noon schedule, so the job runs every 30 minutes instead of once a day. The help text (`zero cron add <cron-expr> [--prompt P | --recipe R]`) explicitly invites this combination.

```
if recipe != "" {
	...
	if expr == "" {
		expr = r.Expr
	}
...
}
...
if len(positional) == 1 && expr == "" {
	expr = positional[0]
}
Verified: stored expr = "*/30 * * * *" when user passed "0 12 * * *".
```

**Suggested fix:** Move the positional assignment before the recipe block: `if len(positional) == 1 { expr = positional[0] }` first, then apply the recipe defaults only into still-empty fields. (Or reject the combination explicitly with a usage error.)

#### [medium/bug] POSIX background-task kill terminates only the leader PID; the specialist's subprocess tree leaks on SIGKILL escalation (Windows kills the whole tree)
`internal/background/process_posix.go:50`

A background specialist is a `zero exec --auto high` child that itself spawns tool subprocesses (bash commands, builds, dev servers). launchBackgroundProcess (internal/specialist/exec.go:638) starts it without Setpgid, and terminateProcess signals only the single pid: SIGTERM lets the child clean up via its signal context, but the escalation path — which exists precisely for a child that ignores or cannot process SIGTERM within the 3s grace — issues `process.Kill()` (SIGKILL) on the leader only. SIGKILL cannot be trapped, so the child's context-cancellation cleanup never runs and all its in-flight tool subprocesses are reparented and keep running (e.g. a `sleep`/server started by the specialist survives TaskStop). The Windows implementation deliberately uses `taskkill /T /F` (tree kill), and the codebase already knows the POSIX pattern — internal/config/process_posix.go:11 sets `Setpgid: true` and kills `-pid` — but the background package omits it, so TaskStop/KillRunning behavior diverges between platforms.

```
process_posix.go: `if err := process.Kill(); err != nil && !processGoneError(err)` — single-pid SIGKILL. process_windows.go: `exec.Command("taskkill", "/T", "/F", "/PID", ...)`. specialist/exec.go launchBackgroundProcess: `command := osexec.Command(binaryPath, args...)` with no SysProcAttr. Contrast config/process_posix.go: `cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}` + `syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)`.
```

**Suggested fix:** In launchBackgroundProcess set `command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}` (behind a !windows helper), and in terminateProcess signal the group: try `syscall.Kill(-pid, syscall.SIGTERM)` first and escalate with `syscall.Kill(-pid, syscall.SIGKILL)`, falling back to the single pid if the group signal fails with ESRCH/EPERM.

#### [medium/race] Starting a second zero process clobbers a live sibling's running background-task metadata to error/PID=0 on disk
`internal/background/manager.go:462`

NewManagerWithOptions -> loadTasks -> normalizeLoadedTask unconditionally rewrites any persisted task whose Status is running to Status=error, PID=0, ExitCode=-1 and PERSISTS that change (`if changed { manager.persistTaskLocked(task) }` at line 419). This is intended for restart-after-crash, but it fires whenever ANY second zero process constructs a Manager on the shared default root (Runtime.Manager() lazily calls background.NewManager("") on the first Task/TaskOutput/TaskStop tool use — registry.go wires all three through runtime.Manager). So while session A has a live background specialist running, session B's first use of a Task tool marks A's task error/PID=0 on disk: B's TaskOutput then reports the live task as failed, B's TaskStop can never stop it (PID is zeroed), and the on-disk record is wrong until A's own Wait-goroutine MarkExited happens to rewrite it from A's in-memory copy.

```
if task.Status == StatusRunning {
	task.Status = StatusError
	task.PID = 0
	task.ExitCode = -1
	...
	changed = true
	manager.warnf("marked reloaded running background task %s as error; original process ownership was lost", task.ID)
}
...loadTasks: if changed { if err := manager.persistTaskLocked(task); err != nil {...} }
```

**Suggested fix:** Before declaring ownership lost, probe liveness: if task.PID > 0 and the process answers signal 0 (and optionally a recorded owner-pid/start-time matches), keep the task as running (read-only view) instead of rewriting it; only persist the error downgrade when the PID is demonstrably gone, or record the owning zero PID in the metadata and only let that owner (or a load that finds the owner dead) downgrade running tasks.

#### [low/ux] `zero update --check` always fails on non-release builds: version "dev" is rejected before the network call
`internal/cli/update.go:37`

runUpdate passes the package-level `version` variable (default "dev", only overridden by release ldflags -X internal/cli.version) as CurrentVersion, and update.Check normalizes CurrentVersion FIRST: normalizeVersionTag("dev") fails the `^v?(\d+)\.(\d+)\.(\d+)` pattern, so Check returns `invalid semantic version: dev` before fetching anything. Every `go install`/`go run` user gets "Could not check for updates: invalid semantic version: dev" and can never use the command, even though the latest-release information is independent of the local version.

```
update.go Check: `currentVersion, err := normalizeVersionTag(strings.TrimSpace(firstNonEmpty(options.CurrentVersion, "0.0.0")))` with `var version = "dev"` (app.go:33) and `CurrentVersion: version` (update.go cli line 37); versionPattern = `^v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:[-+].*)?$` does not match "dev".
```

**Suggested fix:** In Check (or runUpdate), treat an unparseable CurrentVersion as "0.0.0": e.g. `if _, err := normalizeVersionTag(options.CurrentVersion); err != nil { options.CurrentVersion = "0.0.0" }` so the check still reports the latest release (optionally noting the local version is a dev build).

#### [low/wiring] TUI /doctor omits config paths, so config.files and config.validation always warn in the TUI while the CLI surface passes them
`internal/tui/command_center.go:17`

The CLI `zero doctor` resolves UserConfigPath/ProjectConfigPath via config.DefaultResolveOptions and passes them into doctor.Run (internal/cli/observability.go:43-46, 64-72), enabling the config.files and config.validation checks. The TUI's /doctor calls doctor.Run with only Now/Runtime/Provider, so even when the TUI was launched from a workspace whose config files exist (and were used to resolve m.providerProfile), the report always shows '[warn] config.files - No explicit Zero config files were inspected' and '[warn] config.validation - No Zero config files were available to validate'. Same command name, materially weaker diagnostics in one surface; a user with a malformed project config sees pass-with-warn in the TUI where the CLI reports the actual line/col failure.

```
report := doctor.Run(doctor.Options{
	Now:      m.now,
	Runtime:  "go",
	Provider: m.providerProfile,
})  // no UserConfig/ProjectConfig/Connectivity, vs cli/observability.go which fills UserConfig/ProjectConfig from config.DefaultResolveOptions(workspaceRoot)
```

**Suggested fix:** Thread the resolved config paths into the TUI (e.g. add UserConfigPath/ProjectConfigPath to tui.Options, populated in runInteractiveTUI from config.DefaultResolveOptions) and pass them to doctor.Run in doctorText().

#### [low/bug] Byte-indexed truncation in promptExcerpt/cronTruncate splits multi-byte UTF-8 runes
`internal/cli/cron.go:275`

promptExcerpt truncates with `p[:47]` after a byte-length check (`len(p) > 48`), and cronTruncate (cron_run.go:183) does `s[:500]`. Both index bytes, not runes, so a prompt or stderr tail containing non-ASCII text (e.g. Japanese or emoji in a job prompt) can be cut mid-rune: `zero cron list` then prints an invalid-UTF-8 mojibake byte before the ellipsis, and the RunRecord.Error written to runs.jsonl gets the invalid bytes coerced to U+FFFD by json.Marshal.

```
func promptExcerpt(p string) string {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\n", " "))
	if len(p) > 48 {
		return p[:47] + "…"
	}
...
func cronTruncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:500 /* s[:max] */] + "…"
```

**Suggested fix:** Truncate on rune boundaries: convert to []rune (or walk with utf8.DecodeRuneInString) before slicing, e.g. `r := []rune(p); if len(r) > 48 { return string(r[:47]) + "…" }`, and the same for cronTruncate.

#### [low/security] notify.sanitizeMessage passes C1 control characters (U+0080–U+009F) into the OSC-9 escape it is meant to protect
`internal/notify/notify.go:134`

sanitizeMessage's stated contract is to drop control characters 'so the message can't break the escape or inject terminal control', but its filter (`r == 0x1b || r == 0x07 || r < 0x20 || r == 0x7f`) only covers C0+DEL. C1 controls U+0080–U+009F pass through: U+009C (ST) terminates the OSC string early in terminals that honor C1 in UTF-8, and U+009B (CSI) can start an injected control sequence. Today every call site passes the constant DefaultMessage strings, so there is no tainted path yet — but Notify(event, message) is the package's public API and the sanitizer exists precisely to make arbitrary messages safe, so the gap is a latent injection vector for any future caller that includes prompt/tool content in the notification body.

```
for _, r := range s {
	if r == 0x1b || r == 0x07 || r < 0x20 || r == 0x7f {
		continue
	}
	b.WriteRune(r)  // U+0080–U+009F (incl. ST/CSI) are written through
```

**Suggested fix:** Extend the filter to C1: `if r == 0x1b || r == 0x07 || r < 0x20 || (r >= 0x7f && r <= 0x9f) { continue }`.

#### [low/race] terminateProcess grace-period polling probes a possibly-reaped PID and can SIGKILL a recycled PID
`internal/background/process_posix.go:38`

After SIGTERM, the launcher's Wait goroutine (specialist/exec.go launchBackgroundProcess) reaps the child as soon as it exits, freeing the PID, while terminateProcess keeps polling `processAlive` (signal 0 via a fresh os.FindProcess handle, which on Unix carries no done-state) for up to 3 seconds and then sends SIGKILL to whatever now owns that PID. The manager carefully guards against stale PIDs BEFORE signalling (markKilledIfStillRunning: 'the pid may be stale, so do NOT signal it'), but the post-SIGTERM window has no such guard: if the OS recycles the PID inside the grace window, signal-0 reports alive and the escalation SIGKILLs an unrelated process. Low probability (PID reuse within 3s) but a real ordering that the codebase's own pre-kill comments acknowledge as a hazard.

```
deadline := time.Now().Add(terminationGracePeriod)
for time.Now().Before(deadline) {
	if !processAlive(process) { return nil }
	...
}
if err := process.Kill(); err != nil && !processGoneError(err) { ... }  // pid may have been reaped by command.Wait() and recycled
```

**Suggested fix:** Have the kill path coordinate with the reaper instead of probing the raw PID: signal through the original *os.Process held by the Wait goroutine (its handle returns ErrProcessDone after reaping), or combine with the Setpgid fix and signal the process group, or re-check manager.Get(taskID).Status != StatusKilled-exited before escalating to SIGKILL.


### TUI core (model, rendering, transcript)

Audited internal/tui core (model.go, run.go, view.go, transcript.go, rendering.go, theme.go, options.go, startup.go) plus the wiring files they depend on (session.go, session_controls.go, spec_mode.go, commands.go, command_*.go, autocomplete.go, picker.go), reading every file in full and verifying against bubbletea v1.3.10 and the Gemini provider sources. 16 concrete findings. Confirmed the known inline-rendering defect: View() emits the whole transcript with no alt screen/viewport/tea.Println, and the standard renderer truncates frames taller than the terminal, so history is permanently lost (high). Three further high-severity correctness bugs: the transcript dedup key omits runID so Gemini's synthesized repeating tool-call IDs silently drop later tool cards live and on /resume (contradicting the run-scoped rcKey the renderer uses for exactly this reason); Ctrl+C pressed after an Esc-cancel quits before the cancelled run's checkpoint session events flush (the precise data loss flushRunIDs exists to prevent); and /exit quits unconditionally mid-run with the same checkpoint-orphaning effect. Medium findings: /spec bypasses the m.exiting run gate; Esc cancellation writes the 'Run cancelled.' marker only to the session log, leaving zero live-transcript feedback; O(n^2) transcript appends (full-slice copy + linear dedup scan per row); full-transcript re-render with per-line regex work on every spinner tick/stream delta; UTF-8 byte-slicing in session titles, spec titles, and tool-output truncation; composer textinput never gets a Width so long input is clipped invisibly instead of scrolling; /style is validated, stored, and displayed but never read by any run path; and wrapPlainText's strings.Fields collapses all indentation in assistant answers (code blocks flattened). Low findings: cancelled runs' usage never reaches the tracker, the ask-user card ignores the live answer it is passed, a cluster of test-only dead footer/help/theme/bash-msg code, and shortenPath's boundary-less home-prefix match. Cancellation plumbing (runID gating, decision-channel unblocking via ctx.Done, spinner tag dedup, sink wiring in run.go) otherwise checked out with no deadlocks or data races found.

#### [high/ux] Inline renderer silently discards transcript taller than the terminal — history is unrecoverable
`internal/tui/run.go:26`

The program runs inline (no tea.WithAltScreen), View() returns the ENTIRE transcript every frame (model.go transcriptView renders all rows), and nothing ever uses tea.Println or a viewport. Bubble Tea v1.3.10's standard renderer truncates any frame taller than the terminal: standard_renderer.go:186-187 'if r.height > 0 && len(newLines) > r.height { newLines = newLines[len(newLines)-r.height:] }'. The dropped top lines are never written to the terminal, so they never reach scrollback. Once a conversation exceeds the window height, the title bar and all earlier turns become permanently invisible with no scroll mechanism (mouse capture is deliberately disabled, there are no scroll keybindings, and /resume rebuilds the same too-tall frame).

```
run.go:26-30 builds programOpts with only tea.WithContext/WithInput/WithOutput (no alt screen, no Println); model.go:599-624 transcriptView loops `for index, row := range m.transcript { ... builder.WriteString(m.renderRow(row, width, rc)) }` and View() at model.go:584 returns it whole. grep confirms zero uses of tea.Println/WithAltScreen/viewport in non-test code.
```

**Suggested fix:** Either flush finalized rows to terminal scrollback via tea.Println (rendering only the live tail + composer in View), or wrap the transcript in a bubbles viewport sized from WindowSizeMsg with scroll keys. tea.Println is the minimal change for an inline UI: print each row once when it becomes final, keep only pending UI in View.

#### [high/bug] Transcript dedup key ignores runID: repeated provider tool-call IDs (Gemini gemini_tool_N) silently drop later tool cards
`internal/tui/transcript.go:139`

transcriptRowKey dedupes tool call/result rows on kind+id only, but the codebase itself documents (rendering.go:124-127) that some providers synthesize ToolCallIDs that repeat across turns, and internal/providers/gemini/provider.go:270 proves it: `id = fmt.Sprintf("gemini_tool_%d", syntheticIndex)` with the index restarting per response. rowContext was given run-scoped keys (rcKey(runID,id)) precisely for this, yet appendTranscriptRow→hasTranscriptRow uses the unscoped key, so the SECOND turn's `gemini_tool_0` call row (and its result row) is treated as a duplicate and never appended — within a single multi-turn run, across runs in one TUI session, and on /resume rehydration (session.go:127-129 also appends through appendTranscriptRow with all rows at runID 0). Users see only the first tool card; later identical-ID calls vanish from the transcript while still executing.

```
transcript.go:137-139: `case rowToolCall, rowToolResult: if row.id != "" { return fmt.Sprintf("%d:%s", row.kind, row.id) }` — no runID. rendering.go:124-127: "some providers synthesize ToolCallIDs that repeat across turns (e.g. Gemini's gemini_tool_N), so a bare id could attribute a decision or a result to a different run's call." gemini/provider.go:268-271: `if id == "" { id = fmt.Sprintf("gemini_tool_%d", syntheticIndex) }`.
```

**Suggested fix:** Include row.runID in the dedup key: `fmt.Sprintf("%d:%d:%s", row.kind, row.runID, row.id)` (same for rowPermission/rowAskUser keys). The live re-delivery dedup (agentRowMsg rows vs the final agentResponseMsg.rows) still works because both carry the same runID. For rehydration, append rows from transcriptRowsFromSessionEvents directly (the event log has no duplicates) or salt each rehydrated row's runID with the event sequence.

#### [high/bug] Ctrl+C after an Esc-cancel quits immediately and drops the pending checkpoint flush
`internal/tui/model.go:311`

The whole flushRunIDs mechanism exists so a cancelled run's final agentResponseMsg is drained and its accumulated session events — including EventSessionCheckpoint payloads whose blobs are already on disk — get persisted (model.go:65-74). But the Ctrl+C handler only defers the quit when a run is CURRENTLY pending: `pendingFlush` is computed from m.pending before cancelRun. If the user pressed Esc to cancel a run (cancelRun sets pending=false and adds the id to flushRunIDs) and then presses Ctrl+C while the cancelled goroutine is still finishing, pendingFlush is false and tea.Quit fires immediately. The in-flight agentResponseMsg is never processed, so the run's tool calls/results, usage, and checkpoint-reference events are never appended — orphaning the checkpoint blobs and breaking /rewind for that run, the exact loss the code's own comments say this machinery prevents. The session_test.go coverage only exercises Ctrl+C while pending, not Esc-then-Ctrl+C.

```
model.go:305-314: `pendingFlush := false; if m.pending && m.activeRunID != 0 { pendingFlush = true }; m.cancelRun(); m.exiting = true; if pendingFlush && len(m.flushRunIDs) > 0 { return m, nil }; return m, tea.Quit` — an Esc-cancelled run leaves m.pending false but len(m.flushRunIDs) > 0, and the condition requires both.
```

**Suggested fix:** Drop the pendingFlush precondition: after cancelRun, defer the quit whenever any flush is outstanding — `if len(m.flushRunIDs) > 0 { return m, nil }`. The existing agentResponseMsg flush branch already fires tea.Quit when m.exiting && len(m.flushRunIDs) == 0.

#### [high/bug] /exit quits immediately during an in-flight run or pending flush, orphaning checkpoint session events
`internal/tui/model.go:853`

handleSubmit gates only commandPrompt on m.pending/m.exiting; slash commands run any time. `case commandExit` returns tea.Quit unconditionally — it neither cancels an active run nor waits for flushRunIDs to drain. Typing /exit (or /quit) while a run is in flight kills the program before the agent goroutine's final agentResponseMsg can be processed, so none of the run's session events (tool calls/results, usage, EventSessionCheckpoint references) are persisted, while the checkpoint blobs SnapshotForCheckpoint already wrote to disk stay orphaned and /rewind breaks for that run — the same data-loss class the Ctrl+C handler carefully defends against three lines up. The same holds if /exit is typed while a previously Esc-cancelled run is still flushing.

```
model.go:853-855: `case commandExit: m.exiting = true; return m, tea.Quit` with no cancelRun() call and no flushRunIDs check, versus the Ctrl+C handler at model.go:294-314 which cancels and defers the quit until the flush lands.
```

**Suggested fix:** Mirror the Ctrl+C path: `case commandExit: m.cancelRun(); m.exiting = true; if len(m.flushRunIDs) > 0 { return m, nil }; return m, tea.Quit`.

#### [medium/bug] /spec can start a new run while the app is exiting, which the deferred tea.Quit then kills mid-flight
`internal/tui/spec_mode.go:22`

handleSubmit blocks new prompt runs during the Ctrl+C deferred-quit window: model.go:836-839 gates `command.kind == commandPrompt && (m.pending || m.exiting)` with a comment explaining a run started now would be aborted by the deferred tea.Quit and orphan its checkpoint blobs. But /spec routes through handleSpecCommand, which checks only m.pending — after Ctrl+C cancels a run (pending=false, exiting=true, flush outstanding) the user can type `/spec task`, a new agent run starts (m.pending=true, new runID), and when the old run's flush drains, the agentResponseMsg handler fires the deferred tea.Quit (model.go:519-521) killing the new spec run mid-flight with none of its session events persisted — exactly the loss the prompt gate prevents.

```
spec_mode.go:22-25 checks only `if m.pending { ... return }` (no m.exiting), then lines 67-77 start a run via runAgentWithOptions; model.go:836-839 gates the equivalent prompt path on `(m.pending || m.exiting)`; model.go:519-521 fires `tea.Quit` as soon as flushRunIDs drains regardless of the new active run.
```

**Suggested fix:** Gate spec drafting on exiting too: `if m.pending || m.exiting { ... }` in handleSpecCommand (and the same guard in approveSpecReview before it starts the implementation run).

#### [medium/ux] Esc-cancelling a run leaves zero feedback in the live transcript (the 'Run cancelled.' marker only goes to the session log)
`internal/tui/model.go:1081`

cancelRun appends the 'Run cancelled.' message as a sessions.EventError to the persisted store only — no transcript row is ever appended. When the user hits Esc mid-run, the spinner/interim block disappears, the partial streamed answer is wiped (streamingText reset at model.go:1095), and the visible transcript shows nothing at all: no error row, no done line, no indication the run was interrupted. The comments at model.go:296 and model.go:503 claim the cancel path 'writes the "Run cancelled." marker', but that only holds for the persisted log — a /resume of the same session later DOES show the rowError (transcriptRowsFromSessionEvents maps EventError to a row), so the live surface and the rehydrated surface disagree.

```
model.go:1081-1087: `if m.pending && m.activeSession.SessionID != "" { if next, err := (*m).appendSessionEvent(sessions.EventError, map[string]any{"message": "Run cancelled."}); err == nil { *m = next } }` — there is no appendTranscriptRow/reduceTranscript call anywhere in cancelRun.
```

**Suggested fix:** In cancelRun, also append a visible row: `m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "Run cancelled."})` (guarded by m.pending so plain Esc-clear doesn't emit it).

#### [medium/perf] appendTranscriptRow copies the whole transcript and rescans it for dedup on every append — O(n²)
`internal/tui/transcript.go:117`

Every single row append — each agentRowMsg during a run, each row in the agentResponseMsg batch, each /resume rehydration row — allocates a brand-new slice and copies all existing rows (`append([]transcriptRow{}, rows...)`), and hasTranscriptRow linearly rescans the transcript computing an fmt.Sprintf key per existing row. Both are O(n) per append, so a long session is O(n²) in both time and allocations, executed on the Update goroutine where it directly delays input handling and rendering. The defensive copy buys nothing: model.Update already reassigns m.transcript and the model is passed by value, so in-place append semantics are safe.

```
transcript.go:113-120: `func appendTranscriptRow(rows []transcriptRow, row transcriptRow) []transcriptRow { if hasTranscriptRow(rows, row) { return rows }; next := append([]transcriptRow{}, rows...); next = append(next, row); return next }` and transcript.go:122-133 hasTranscriptRow looping `for _, existing := range rows { if transcriptRowKey(existing) == key ... }`.
```

**Suggested fix:** Use plain `append(rows, row)` (Go slice append amortizes), and replace the linear dedup scan with a `map[string]struct{}` of seen keys maintained on the model (or only dedup the agentResponseMsg batch against rows of the same runID).

#### [medium/perf] Full transcript re-rendered from scratch every frame (every spinner tick and stream delta), including per-line regex parsing
`internal/tui/model.go:610`

transcriptView rebuilds rowContext (a full transcript scan) and re-renders EVERY row through renderRow on every View() call. While a run is pending, the spinner self-schedules ticks (~10/s) and every agentTextMsg delta also triggers a frame, so each frame redoes: buildRowContext over all rows, word-wrapping with per-word lipgloss.Width calls, and per-line regexp matching in diffCardBody/readCardBody/grepCardBody for every historical tool card — plus interimBlock re-wraps the entire accumulated streamingText per frame (O(answer²) over a stream). None of the rendered output changes for finalized rows; only the spinner glyph and interim block do. On long transcripts this burns CPU continuously during runs and adds latency to every keystroke.

```
model.go:610-624: `rc := buildRowContext(m.transcript); for index, row := range m.transcript { ... builder.WriteString(m.renderRow(row, width, rc)) }` executed in View() (model.go:584); rendering.go:139-183 buildRowContext scans all rows; rendering.go:701/810/908 run hunkHeaderPattern/readNumberedLinePattern/grepMatchPattern per body line; model.go:673-686 interimBlock wraps full streamingText each frame.
```

**Suggested fix:** Cache each row's rendered string keyed by (row identity, width, resolved/auto state) and invalidate only on width change or when its state flips; alternatively pre-render rows once when appended and store the string on transcriptRow. For the interim block, cache wrapped lines and only re-wrap the trailing partial line on new deltas.

#### [medium/bug] UTF-8 strings byte-sliced at fixed limits: session titles and tool-result text can be cut mid-rune
`internal/tui/session.go:93`

Three call sites truncate by byte index on strings that are routinely multi-byte UTF-8: tuiSessionTitle slices the user's prompt at byte 80 (`title[:tuiSessionTitleLimit]`) and persists the result as the session title — a CJK/emoji prompt longer than 80 bytes is stored with a dangling partial rune that renders as a replacement character in /resume session cards and anywhere else the title is shown; specImplementationTitle (spec_mode.go:301-303) does the same to spec titles; and truncateTUIOutput (transcript.go:303) slices tool output at byte 240 (`output[:limit] + " [truncated]"`), producing invalid UTF-8 in transcriptRow.text and in the rehydrated rows built at session.go:233. The file already has the correct helper (truncateRunes, view.go:402) used elsewhere.

```
session.go:91-94: `title := strings.Join(strings.Fields(prompt), " "); if len(title) > tuiSessionTitleLimit { title = title[:tuiSessionTitleLimit] }`; transcript.go:300-303: `if limit <= 0 || len(output) <= limit { return output }; return output[:limit] + " [truncated]"`; spec_mode.go:301-303: `if len(title) > tuiSessionTitleLimit { title = title[:tuiSessionTitleLimit] }`.
```

**Suggested fix:** Use the existing rune-safe helper at all three sites: `title = truncateRunes(title, tuiSessionTitleLimit)` and `return truncateRunes(output, limit) + " [truncated]"` (or guard the byte cut with utf8.RuneStart back-off).

#### [medium/ux] Composer textinput has no Width set: typing beyond the terminal width is clipped invisibly instead of scrolling
`internal/tui/model.go:227`

textinput.New() is used with the default Width of 0 and no code ever sets m.input.Width (grep over internal/tui finds no assignment), including the WindowSizeMsg handler. In bubbles v1, Width==0 disables the input's horizontal-scrolling viewport and View() renders the entire value. composerLine then hard-truncates that line to the terminal width with fitStyledLine, so once the typed prompt is longer than the visible width the cursor and all subsequent characters are cut off at the right edge — the user keeps typing with no visual feedback at all, and pasted long prompts cannot be reviewed or edited beyond the first screenful.

```
model.go:227-233 configures prompt/styles/placeholder but never Width; model.go:702-706: `line := input.View(); if hint == "" { return fitStyledLine(line, width) }; return joinHeaderLine(fitStyledLine(line, width-lipgloss.Width(hint)-2), hint, width)` — ANSI truncation, not scrolling.
```

**Suggested fix:** On tea.WindowSizeMsg (model.go:456-459) set `m.input.Width = chatWidth(msg.Width) - lipgloss.Width(m.input.Prompt) - reservedHintWidth` so the textinput scrolls horizontally to keep the cursor visible.

#### [medium/wiring] /style sets m.responseStyle but nothing ever reads it — the command is a silent no-op
`internal/tui/session_controls.go:117`

Options.ResponseStyle is accepted, validated through defaultedResponseStyle, stored on the model, and mutable via /style with the confirmation 'Style preference is stored for this TUI session' — but a repo-wide grep shows the only consumers are the /style and /context display strings. runAgentWithOptions never threads it into agent.Options (no system-prompt suffix, no field), and the CLI entry (internal/cli/app.go:366) doesn't set it either. Selecting 'concise' or 'explanatory' therefore changes nothing about any response, while the command UX (a validated list of four styles with errors for unknown values) strongly implies it does.

```
session_controls.go:117: `m.responseStyle = args` followed by 'Style preference is stored for this TUI session.'; grep -rn ResponseStyle over internal/ and cmd/ matches only internal/tui display/validation sites (options.go:33, model.go:259, session_controls.go, command_views.go:155) and nothing in internal/agent — agent/system_prompt.go has no style hook.
```

**Suggested fix:** Thread it into the run: in runAgentWithOptions append a style directive to options.SystemPrompt (e.g. map responseStyle → instruction text) — or, until that exists, have /style report it as not yet wired (like /theme's shellOnlyCommandText) instead of claiming the preference is active.

#### [medium/ux] wrapPlainText collapses all whitespace via strings.Fields — assistant answers lose code indentation and alignment
`internal/tui/rendering.go:261`

renderUserRow, renderAssistantRow (the turn's final answer), interimBlock (live stream), and wrapDetailBlock all wrap through wrapPlainText, which splits each line with strings.Fields. Fields drops leading whitespace and collapses every internal run of spaces/tabs to a single space, so any code block, indented list, or column-aligned output in a final answer is flattened: a 4-space-indented Go snippet renders fully left-justified and `a    b` becomes `a b`. For a coding agent whose answers routinely contain code, this visibly corrupts the primary output (newlines survive, but indentation — semantically meaningful in Python/YAML examples — is destroyed).

```
rendering.go:255-261: `for _, paragraph := range strings.Split(...) { ... for _, word := range strings.Fields(paragraph) {` — Fields("    if x {") yields ["if","x","{"], discarding the indent; callers renderAssistantRow (rendering.go:324) and interimBlock (model.go:679) feed final/streaming answers through it.
```

**Suggested fix:** Preserve leading whitespace per line (measure the indent prefix, wrap the remainder, re-prefix continuation lines) or only soft-wrap lines that exceed the measure and pass through shorter lines verbatim: `if lipgloss.Width(paragraph) <= measure { out = append(out, paragraph); continue }`.

#### [low/bug] Cancelled runs' usage events are flushed to the session log but never recorded in the usage tracker
`internal/tui/model.go:512`

The flush branch for a cancelled run persists its session events (including EventUsage payloads) but never calls recordUsageEvent with msg.usageEvents, unlike the active-run branch (model.go:531-537). Tokens and cost consumed by an Esc-cancelled run — often the most expensive kind, cancelled mid-tool-loop — are therefore missing from the status-line `tok · $` readout and /context usage summary for the rest of the TUI session, undercounting real spend.

```
model.go:506-522 handles `flushing` runs with only `m.appendSessionEvents(flushableSessionEvents(msg.sessionEvents))`; msg.usageEvents is ignored on this path, while the matching-runID path at model.go:531-537 loops `for _, event := range msg.usageEvents { m, usageRows = m.recordUsageEvent(msg.usageModelID, event) ... }`.
```

**Suggested fix:** In the flush branch, also iterate msg.usageEvents through m.recordUsageEvent(msg.usageModelID, event) before deleting the runID from flushRunIDs.

#### [low/wiring] renderFocusedAskUserPrompt receives the live input value but never renders it
`internal/tui/rendering.go:490`

The ask-user questionnaire card is called with the composer's current value — `renderFocusedAskUserPrompt(*m.pendingAskUser, m.input.Value(), width)` at model.go:633 — but the `input string` parameter is never referenced in the function body: the card shows the heading, question counter, question text, options, and a key hint, and nothing else. The typed answer is only visible in the composer at the very bottom of the frame, while the card itself says 'type an answer, Enter to submit', so the answer being composed is displaced from the prompt asking for it. The parameter is pure dead wiring as written.

```
rendering.go:490-519: `func renderFocusedAskUserPrompt(prompt pendingAskUserPrompt, input string, width int) string { ... }` — the body builds `lines` from prompt.request fields and a static hint only; `input` does not appear after the signature.
```

**Suggested fix:** Render the in-progress answer inside the card (e.g. `lines = append(lines, fill(zeroTheme.ink).Render("❯ "+input))` above the hint), or drop the parameter and its call-site argument.

#### [low/dead-code] Dead production code: legacy footer/help builders and four theme styles are never used outside tests
`internal/tui/rendering.go:62`

A cluster of pre-Lime UI leftovers survives only because tests still call it: defaultCommandFooterText (rendering.go:62), commandFooterText (64), m.footerText (68) and formatCommandFooterText's 'Esc clear/Esc cancel/Ctrl+C quit' footer (77-118), plus formatCommandHelpLines (commands.go:290) and listCommandNames (commands.go:281) — grep shows no non-test callers for any of them, so no footer of this form is ever rendered by the Lime surface. Likewise theme.go declares and initializes styles that no renderer references: selRow (theme.go:159; renderers use onSel() instead), statusOk/statusErr (164-165), and line2 (126). Also bashResultMsg.command (command_bash.go:21) is populated by runBashEscape but never read by the Update handler (model.go:574-576 uses only msg.output). This dead code misleadingly documents key behavior ('Esc clear  Ctrl+C quit') that no longer matches the rendered UI.

```
grep -rn over internal/tui excluding _test.go: footerText/commandFooterText/formatCommandHelpLines/listCommandNames appear only at their definitions; zeroTheme.selRow/statusOk/statusErr/line2 appear only in theme.go; model.go:574-576 `case bashResultMsg: m.transcript = reduceTranscript(..., text: msg.output)` ignores msg.command.
```

**Suggested fix:** Delete footerText, commandFooterText, formatCommandFooterText, defaultCommandFooterText, formatCommandHelpLines, listCommandNames and their tests; remove selRow, statusOk, statusErr, line2 from tuiTheme; drop the unused command field from bashResultMsg (or render it for correlation).

#### [low/bug] shortenPath matches the home directory as a bare string prefix, mangling sibling paths
`internal/tui/view.go:227`

The title-bar path shortener replaces the home-directory prefix without requiring a path-separator boundary: any cwd that merely begins with the home string is rewritten. With home=/Users/kratos, a workspace at /Users/kratosbackup/proj renders as '~backup/proj' in the title bar — a wrong path that looks like it lives under the user's home.

```
view.go:226-229: `if home, err := os.UserHomeDir(); err == nil && home != "" { if strings.HasPrefix(path, home) { return "~" + path[len(home):] } }` — no check that the next byte is os.PathSeparator or that path == home.
```

**Suggested fix:** Require the boundary: `if path == home { return "~" }; if strings.HasPrefix(path, home+string(os.PathSeparator)) { return "~" + path[len(home):] }`.


### Tools & sandbox

Audited internal/tools and internal/sandbox by reading every tool implementation and the sandbox engine/risk/runner/grants code, and verified the high-impact behaviors with runnable test snippets. The most serious confirmed defects: (1) the bash tool's timeout/kill semantics rely on exec.CommandContext, which I proved does NOT kill the command past the deadline when a child process inherits the stdout/stderr pipes — a `sleep 5 &`-style background job keeps the whole call blocked far past timeout_ms (no process-group kill, no WaitDelay). (2) The macOS sandbox-exec profile denies all /tmp and /dev/null writes (proven), so virtually every wrapped shell command that touches a tempfile or /dev/null fails under the native darwin backend. (3) apply_patch path validation misparses unified-diff body lines beginning with "--- "/"+++ ", so deleted/added content lines get treated as file paths — producing bogus MutationTargets/checkpoint entries and spurious confinement errors. (4) The grep tool's glob filter matches against the workspace-root-relative path while users naturally pass root-relative globs like "*.go", so grep silently returns "No matches" when a subdirectory path is searched. Plus several medium/low wiring, perf, and UX issues (OnSandboxDecision callback is dead code, escalate_model not in any Core* set, O(n*m) destructive-regex scanning, interactive-command false positives inside quoted args, etc.).

#### [high/bug] bash tool timeout does not kill the command when a child inherits the output pipes
`internal/tools/bash.go:104`

The bash tool relies solely on exec.CommandContext(ctx,...) for timeout enforcement: when the deadline fires, Go sends SIGKILL to the direct child (/bin/sh) only. It does NOT put the command in its own process group, sets no cmd.WaitDelay, and never kills the process group. If the shell command backgrounds a process or spawns children that inherit the stdout/stderr *bytes.Buffer pipes, command.Run() blocks on the pipe read until those grandchildren exit — long past timeout_ms. I reproduced this: a 1s context timeout against `/bin/sh -c 'sleep 5 & echo started'` returned only after 5.018s with err=nil (the timeout was completely ineffective). The agent loop and TUI therefore hang for the full child lifetime regardless of the user-specified timeout.

```
commandCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond) ... err = command.Run() — and buildBashCommand does `command := exec.CommandContext(ctx, spec.Name, spec.Args...)` with no SysProcAttr{Setpgid:true} and no command.WaitDelay. Proven: elapsed=5.018s err=<nil> for a 1s timeout.
```

**Suggested fix:** Set command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} (the repo already does this in internal/config/process_posix.go) and on timeout kill the whole process group (syscall.Kill(-pid, SIGKILL)); additionally set command.WaitDelay (Go 1.20+) to a small grace period so Run() returns promptly even when descendants hold the pipes. Apply in both buildBashCommand branches and in sandbox/runner.go's CommandContext.

#### [high/bug] macOS sandbox-exec profile blocks /tmp and /dev/null, breaking most wrapped shell commands
`internal/sandbox/runner.go:262`

sandboxExecProfile emits `(allow file-write* (subpath "<workspaceRoot>"))` when EnforceWorkspace is true, with `(deny default)`. This denies writes to /dev/null, /dev/stdout, $TMPDIR, and /tmp — paths that virtually every real shell command (and the Go toolchain, git, compilers, `cmd > /dev/null`) needs. I verified on this darwin host: under exactly that profile, `echo hi > /dev/null` fails with `/dev/null: Operation not permitted` and `mktemp` fails with `Operation not permitted`. So when the native darwin sandbox-exec backend is selected (the default on macOS), wrapped bash commands break pervasively, not just destructive ones.

```
writeRule = `(allow file-write* (subpath "` + sandboxProfileString(workspaceRoot) + `"))` with header `(deny default)`; no allowance for /dev/null, (literal "/dev"), or the system temp dir. Repro: `sandbox-exec -p '...subpath "/tmp/zero-audit-ws"...' /bin/sh -c 'echo hi > /dev/null'` -> Operation not permitted; `mktemp` -> Operation not permitted.
```

**Suggested fix:** Add the standard device/temp write allowances to the profile, e.g. `(allow file-write-data (literal "/dev/null") (literal "/dev/stdout") (literal "/dev/stderr") (literal "/dev/dtracehelper"))`, `(allow file-write* (subpath "/private/tmp") (subpath "/private/var/folders"))` and TMPDIR, plus `(allow file-write* (regex #"^/dev/tty"))`, alongside the workspace subpath.

#### [high/bug] apply_patch misparses unified-diff body lines starting with '--- '/'+++ ' as file paths
`internal/tools/apply_patch.go:161`

patchPathsFromLine treats ANY line beginning with `--- ` or `+++ ` as a diff file header and extracts a 'path' from field[1]. But these prefixes also occur inside hunk bodies: a removed line whose content is `-- drop this comment` appears as `--- drop this comment`, and an added line `++ note` appears as `+++ note`. This corrupts both confinement validation (validatePatchPaths) and MutationTargets (changedFilesFromPatch). I verified: a patch with body line `--- drop this comment` yields changedFiles `[f.sql drop]` (the bogus 'drop' target), and a body line `--- /etc/passwd was here` makes validatePatchPaths reject a perfectly valid patch with `patch path "/etc/passwd" must stay inside the workspace`. The same wrong path set flows into MutationTargets, so /rewind checkpoints the wrong files (or fails to checkpoint the real one).

```
if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") { fields := strings.Fields(line); if len(fields) >= 2 { return []string{stripPatchPrefix(fields[1])} } } — with no requirement that the line sit in a file-header position. Repro: changedFilesFromPatch(".", patch) = [f.sql drop]; validatePatchPaths(...) on body `--- /etc/passwd was here` => error.
```

**Suggested fix:** Only interpret `---`/`+++` lines as headers when they form a real header pair (a `+++ ` line immediately following a `--- ` line, or gated by a preceding `@@`/`diff --git`/index marker). Track hunk state while scanning so lines inside @@ hunks are never parsed as paths; prefer the `diff --git` names when present.

#### [high/bug] grep glob filter is applied to root-relative paths, silently dropping matches under a subdirectory search
`internal/tools/grep.go:224`

When grep is given a `path` subdirectory plus a `glob`, the glob is matched against the path RELATIVE TO THE WORKSPACE ROOT (confineGrepFile returns Rel(resolvedRoot, file)), not relative to the searched directory. A natural call like {path:"internal", glob:"*.go"} therefore tests the matcher `^[^/]*\.go$` against `internal/main.go`, which fails because of the `internal/` prefix, so grep reports 'No matches' even though matching files exist. I verified: grep with path=internal, glob=*.go over a file internal/main.go containing the pattern returns status=ok, output "No matches found." Users must instead write glob "**/*.go" or "internal/*.go", which is non-obvious and inconsistent with the glob tool (which matches relative to its cwd).

```
files, err := grepFiles(resolvedRoot, target, globMatcher) and inside grepFiles: relative, _, ok := confineGrepFile(resolvedRoot, path) ... if globMatcher == nil || globMatcher.MatchString(relative). Repro: pattern=needle path=internal glob=*.go over internal/main.go -> "No matches found."
```

**Suggested fix:** Match the glob against the path relative to the searched `target` directory (filepath.Rel(target, path)) rather than relative to the workspace root, mirroring the glob tool's cwd-relative semantics. Keep the root-relative form only for the output label/confinement.

#### [medium/wiring] escalate_model tool is implemented and registry-aware but never included in any Core* tool set
`internal/tools/registry.go:224`

escalate_model (escalate_model.go) is a complete tool whose Meta["escalate_to_model"] is explicitly consumed by the agent loop (loop.go:523 lifts it into RequestedModel for a mid-run provider switch). But none of CoreReadOnlyTools/CoreWriteTools/CoreShellTools/CoreNetworkTools/CoreTools register it, so via the standard Core* construction path the model can never call it and the loop's escalation handling is dead. The consumer side (RequestedModel wiring) exists but the producer is never registered in the core surface, a producer/consumer wiring mismatch.

```
CoreTools() composes only CoreReadOnlyTools+CoreWriteTools+CoreShellTools+CoreNetworkTools; none reference NewEscalateModelTool. Meanwhile loop.go:523 sets `RequestedModel: result.Meta["escalate_to_model"]` and escalate_model.go is the only producer of that key.
```

**Suggested fix:** If escalation is a supported feature, register NewEscalateModelTool() in the appropriate Core* set (or document where it is registered, e.g. exec/specialist wiring) so the loop's RequestedModel path is reachable; otherwise remove the dead RequestedModel handling.

#### [medium/bug] Interactive-command detector flags pager/REPL names that appear inside quoted arguments
`internal/sandbox/safe_command.go:138`

DetectInteractiveCommand splits on `|` (and other operators) BEFORE stripping quotes, and the single-word program scan (the second loop) does not anchor on quoting. A command like `git commit -m "tidy | less noise"` is split on the literal pipe inside the quoted message, producing a segment `less noise` whose first program is `less`, so the whole command is wrongly blocked as an interactive pager. I verified: DetectInteractiveCommand(`git commit -m "tidy | less noise"`, "darwin") returns Interactive=true, Command="less". The multi-word interactiveSegments path was deliberately hardened against this (commandBody anchoring), but the single-word program path and the operator split are not quote-aware, so legitimate commit messages, echo strings, and sed scripts containing these tokens are refused.

```
splitShellSegments uses strings.NewReplacer("|","\n", ...) on the raw normalized command with no quote handling, then the second loop does `first := firstProgram(fields); program, ok := interactivePrograms[first]`. Repro: `git commit -m "tidy | less noise"` -> interactive=true command="less".
```

**Suggested fix:** Tokenize with quote awareness (or strip/ignore quoted spans) before splitting on shell operators, so pipes/semicolons inside single- or double-quoted strings are not treated as segment boundaries and program names inside quoted arguments are not scanned as executables.

#### [low/dead-code] OnSandboxDecision callback option is set up to fire but is never wired by any caller (dead code)
`internal/tools/registry.go:108`

RunOptions.OnSandboxDecision is invoked (in a recover-guarded goroutine) on every sandbox evaluation, but a repo-wide search shows no caller ever sets it: the only references are the field declaration and the call site in registry.go. The agent loop builds RunOptions at loop.go:481 and askUserFallbackResult without ever populating OnSandboxDecision, and there is no other RunOptions literal that sets it. The goroutine, panic-recovery, and the whole async-notify path are therefore unreachable plumbing — a maintenance hazard and a (small) per-call allocation/branch that never does anything.

```
grep across the repo: the only OnSandboxDecision occurrences are registry.go:19 (field), :108 (`if options.OnSandboxDecision != nil`), and :116 (the call). No RunOptions{...} literal in internal/agent, internal/tui, internal/cli, internal/specialist sets it.
```

**Suggested fix:** Either wire OnSandboxDecision from the agent loop / CLI to feed sandbox decisions to observers, or delete the field and its goroutine call site since SandboxDecision is already returned synchronously on Result and consumed by the loop.

#### [low/perf] Destructive/network/installer classification recompiles N regexes over the full command on every shell call
`internal/sandbox/risk.go:53`

matchesDestructive runs the large destructiveCommandPattern plus a loop over destructiveExtraPatterns (5 more regexes) against the entire command string, and Classify additionally runs networkCommandPattern and pipedInstallerPattern. While the regexes are package-level (compiled once), they are all evaluated unconditionally on every bash Classify call, and several are heavy alternations with backtracking-prone constructs like `(-[A-Za-z]*r[A-Za-z]*f|...)` and `(-\S+\s+)*`. For very long commands (e.g. a multi-kilobyte here-doc or generated script passed as a single `command` arg) this is an O(pattern_count * len) hot path on the latency-critical permission gate. Not catastrophic, but it scans the whole untrimmed command repeatedly.

```
func matchesDestructive: `if destructiveCommandPattern.MatchString(command)` then `for _, pattern := range destructiveExtraPatterns { if pattern.MatchString(command) ... }`; Classify also calls networkCommandPattern.MatchString(command) and pipedInstallerPattern.MatchString(command) on the same full string. The `(-\S+\s+)*0?777` style sub-patterns are quadratic-prone.
```

**Suggested fix:** Cap the scanned length (e.g. classify only the first few KB plus the command head), short-circuit on a cheap prefix check (only run the rm/chmod/chown alternations when the command contains those tokens), and combine the extra patterns into one alternation to reduce passes.

#### [low/bug] Grant store prefix/substring safety relies on exact-key map but Lookup trims while writer key may not match normalized name
`internal/sandbox/grants.go:146`

Grant() stores under grant.ToolName (already TrimSpace'd via createGrant), but Lookup() reads state.Grants[strings.TrimSpace(toolName)] while readState() validates/normalizes keys by the raw map key (not trimmed). If a grants file is hand-edited or produced by another writer with a key that has surrounding whitespace, normalizeStoredGrant compares grant.ToolName!=name using the raw key and will error the whole file load, while a request for the trimmed name silently won't match. The matching is exact-key (good — no prefix pitfall), but the trim asymmetry between write path (trims) and the stored map key (not re-keyed after trim) means a tool granted as " bash " can never be looked up as "bash" and vice versa without a file-load error. Low severity because the normal in-process Grant path trims consistently, but cross-writer/edited files behave inconsistently.

```
Grant: `state.Grants[grant.ToolName] = grant` (grant.ToolName trimmed). Lookup: `grant, ok := state.Grants[strings.TrimSpace(toolName)]`. readState iterates `for name, grant := range state.Grants` and ValidateToolName(name)/normalizeStoredGrant(name,grant) key off the un-retrimmed map key.
```

**Suggested fix:** Re-key entries on the trimmed/normalized tool name during readState (delete the old key, insert the trimmed one) so the stored map key always equals the canonical name Lookup uses, eliminating the write/read trim asymmetry.

#### [low/ux] read_file rune-width line numbering can misalign and read_file returns Truncated but no truncation marker in output
`internal/tools/read_file.go:89`

When max_lines truncates the selection, read_file sets Result.Truncated=true but the Output gives no in-band indication that lines were dropped — the header reports `lines start-last of total` which looks like a normal bounded read, so a model consuming only Output (truncation flag is metadata) cannot tell the read was capped and may assume it saw everything up to last. The grep/glob tools have the same Truncated-flag-only behavior but at least grep's content stops at head_limit visibly; read_file's header actively implies completeness for the shown range. This can cause the agent to act on a partial file believing it is complete.

```
selected = selected[:maxLines]; truncated = true ... header := fmt.Sprintf("File: %s (lines %d-%d of %d)", relativePath, startLine, lastLine, total) — the only signal that more lines were withheld within the requested range is Result.Truncated, never surfaced in Output.
```

**Suggested fix:** When truncated, append an explicit trailing marker to Output (e.g. `… (truncated at max_lines=N; M more lines in range)`) so the model sees the cap regardless of whether it inspects the Truncated metadata.


### Runtime & providers

Audit of internal/zeroruntime, internal/providers (openai/anthropic/gemini/providerio + factory), internal/providercatalog, and internal/modelregistry in the zero CLI. All files were read in full; package tests and go vet pass, so all findings are latent defects. Highest-impact: (1) reasoning effort is a fully modeled, user-facing feature (mode presets, --reasoning-effort, registry effort data) that no provider adapter ever serializes — smart vs precise modes send byte-identical requests (high, wiring); (2) the shared SSE idle watchdog only resets on completed data payloads, so comment keep-alives (e.g. OpenRouter ': PROCESSING' heartbeats) are invisible and healthy long requests are aborted as idle after 90s (medium); (3) the registry caps gpt-4.1 — the default model — at 16,384 output tokens versus the API's 32,768 (its own mini/nano entries use 32,768), and that value is wired into max_completion_tokens, truncating long generations (medium); (4) the Gemini adapter never parses cachedContentTokenCount, so CachedInputTokens is structurally zero for Google models and the catalog's Gemini cached-input pricing is unreachable, overstating costs (medium); (5) the OpenAI adapter drops tool calls that have a complete name+arguments but no id (triggering the agent's retry loop), while zeroruntime's tested empty-ID collector machinery is unreachable from every adapter (medium, wiring). Lower severity: claude-haiku-3.5 wrongly flagged vision-capable (real model is text-only; reachable via config-pinned model since the factory resolves without deprecation fallback), HTTP 529 classified as rate-limit but excluded from the 429/503 retry policy, the dead isImplicitOpenAI condition in the provider factory, and the never-used Descriptor.Public field. Usage normalization (EffectiveInputTokens/NormalizeUsage/CollectStream accumulation), Anthropic cache accounting (input+cache_read+cache_creation summed with cached clamped as a subset), retry idempotency policy, SSE parsing, auth-header handling, and secret redaction were checked and found sound; remaining catalog data (context windows, prices, capabilities for active models) matches published provider values.

#### [high/wiring] Reasoning effort is modeled, validated, and surfaced everywhere but never serialized into any provider request
`internal/providers/openai/types.go:3`

modelregistry defines ReasoningEfforts/DefaultReasoningEffort per model, EffectiveReasoningEffort resolves requested efforts, modes.go ships presets that differ ONLY by effort (smart = sonnet-4.5/medium vs precise = sonnet-4.5/high, 'Careful, high-effort reasoning'), the CLI accepts --reasoning-effort and prints coercion notices ('using high instead'), and the TUI shows the effective effort — but no provider wire type carries it: chatCompletionRequest has no reasoning_effort field, messagesRequest has no thinking/budget block, and generateContentRequest's generationConfig has only MaxOutputTokens. zeroruntime.CompletionRequest itself has no effort field, and the factory never forwards options. So /mode precise and /mode smart produce byte-identical provider requests, and --reasoning-effort high is a silent no-op for every model — the user-facing feature advertises behavior the wire layer cannot deliver. A code comment in cli/exec.go acknowledges the gap, but nothing user-facing does (the notice text implies the effort takes effect).

```
openai/types.go:3-10 chatCompletionRequest fields are Model/Messages/Tools/MaxCompletionTokens/Stream/StreamOptions only; anthropic/types.go:3-13 messagesRequest has no thinking field; gemini/types.go:10-12 `type generationConfig struct { MaxOutputTokens int }`; grep for reasoning/thinking/budget across internal/providers returns nothing; cli/exec.go:848-850 'NOTE: the effective effort is not yet forwarded to the provider request — the zeroruntime.CompletionRequest / provider wire schemas carry no effort field.'; modes.go:57-62 the precise preset differs from smart only by Effort.
```

**Suggested fix:** Add a ReasoningEffort field to zeroruntime.CompletionRequest, plumb it through providers.Options/the factory, and map it per adapter (OpenAI: reasoning_effort; Anthropic: thinking budget_tokens derived from effort; Gemini: generationConfig.thinkingConfig.thinkingBudget). Until then, gate the modes/--reasoning-effort UX with an explicit 'effort is advisory only' warning so users are not misled.

#### [medium/bug] SSE comment keep-alives never reset the 90s idle watchdog, so heartbeating upstreams are aborted as idle
`internal/providers/providerio/providerio.go:184`

ScanSSEDataWithContext resets the idle timer only when a completed data payload crosses the channel: resetIdle() is called solely in the `case item, ok := <-payloads` branch. But scanSSEPayloads silently discards SSE comment lines (`if strings.HasPrefix(line, ":") { continue }`) and blank lines flush with no payload. Gateways that keep the connection alive with comment heartbeats while the model is queued or thinking — OpenRouter (in providercatalog) emits `: OPENROUTER PROCESSING` comments precisely for this — therefore look 'idle' to the watchdog even though bytes are actively arriving. After defaultStreamIdleTimeout (90s, all three adapters) the stream is cancelled and surfaced as 'provider stream error: idle timeout ... (upstream stopped sending data)', killing a healthy long-running request mid-turn.

```
providerio.go:90-92 `if strings.HasPrefix(line, ":") { continue }` (comments dropped before producing a payload); providerio.go:172-184 `case item, ok := <-payloads: ... resetIdle(); if !handle(item.data)` — resetIdle is reachable only from a data payload, never from a comment/blank heartbeat line.
```

**Suggested fix:** Treat any received line as liveness: have scanSSEPayloads invoke an onActivity callback (or send a zero-value tick on the payloads channel) for comment and blank lines so the consumer calls resetIdle(), or wrap response.Body in a reader that resets the idle timer on every successful Read.

#### [medium/bug] Registry caps gpt-4.1 output at 16,384 tokens; the API max is 32,768 (the file itself uses 32,768 for mini/nano)
`internal/modelregistry/catalog.go:27`

The catalog entry for gpt-4.1 — the DefaultModelID — declares MaxOutputTokens 16,384, while OpenAI's published limit for the whole GPT-4.1 family is 32,768 output tokens (and lines 28-29 of this same file correctly use 32,768 for gpt-4.1-mini and gpt-4.1-nano, making the flagship entry internally inconsistent). This is not display-only data: providers/factory.go resolveProfile copies entry.ContextLimits.MaxOutputTokens into openai.Options.MaxTokens, and openai/provider.go openAIRequest serializes it as max_completion_tokens. Every gpt-4.1 request is therefore hard-capped at half the model's real output budget, so long generations (big file writes, large diffs) are truncated with finish_reason "length" at 16,384 tokens when the model could have produced 32,768.

```
catalog.go:27 `openAIModel("gpt-4.1", "GPT-4.1", "gpt-4.1", ..., ContextLimits{ContextWindow: 1_047_576, MaxOutputTokens: 16_384}, ...)` vs catalog.go:28 `"gpt-4.1-mini" ... MaxOutputTokens: 32_768` ; factory.go:133 `maxOutputTokens: entry.ContextLimits.MaxOutputTokens` ; openai/provider.go:325-327 `if provider.maxTokens > 0 { mapped.MaxCompletionTokens = provider.maxTokens }`.
```

**Suggested fix:** Change the gpt-4.1 entry to ContextLimits{ContextWindow: 1_047_576, MaxOutputTokens: 32_768} to match the API limit and the sibling entries.

#### [medium/bug] Gemini adapter never reads cachedContentTokenCount, so CachedInputTokens is always 0 and the catalog's Gemini cached pricing is unreachable
`internal/providers/gemini/types.go:88`

Gemini's usageMetadata includes cachedContentTokenCount (populated by the implicit prompt caching that Gemini 2.5 models perform automatically), but the adapter's usageMetadata struct only parses promptTokenCount/candidatesTokenCount/thoughtsTokenCount, and emitDone builds the TokenUsage with no CachedInputTokens. Meanwhile modelregistry/catalog.go defines CachedInputPerMillion for every Google model (0.125/0.25 tiers for 2.5-pro, 0.03 for flash, 0.01 for flash-lite) and CalculateCost (cost.go:67-74) only discounts tokens reported in usage.CachedInputTokens. Since that field is structurally always zero for Gemini, every cached prompt token is billed at the full input rate in cost estimates — agent sessions (which re-send a stable system prompt and tool defs each turn, exactly what implicit caching hits) systematically overstate Gemini costs, and the catalog's cached rates are dead data on this path.

```
types.go:88-93 `type usageMetadata struct { PromptTokenCount int ...; CandidatesTokenCount int ...; ThoughtsTokenCount int ...; TotalTokenCount int ... }` (no cachedContentTokenCount) ; provider.go:254-258 `zeroruntime.TokenUsage{ InputTokens: state.inputTokens, OutputTokens: state.outputTokens, ReasoningTokens: state.reasoningTokens }` (CachedInputTokens never set) ; catalog.go:38 `ModelCost{InputPerMillion: 0.3, CachedInputPerMillion: 0.03, ...}` for gemini-2.5-flash.
```

**Suggested fix:** Add `CachedContentTokenCount int \`json:"cachedContentTokenCount"\`` to usageMetadata, track it in streamState alongside the other counters, and pass it as CachedInputTokens in emitDone's NormalizeUsage call (Gemini's promptTokenCount already includes cached tokens, matching the runtime's cached-is-a-subset model).

#### [medium/wiring] OpenAI adapter drops fully-formed tool calls that lack an id instead of synthesizing one; zeroruntime's empty-ID collector support is unreachable
`internal/providers/openai/tool_state.go:95`

toolState refuses to dispatch any tool call without BOTH id and name: applyDelta returns early (`if call.id == "" || call.name == "" || call.ended { return }`) and closeOpen emits StreamEventToolCallDropped for a call that has a complete name and complete JSON arguments but no id. The agent loop responds to DroppedToolCalls by appending a retry nudge (loop.go:347-352), so an OpenAI-compatible backend that legally omits tool_call ids loops forever on 'retry the tool call' — every retry is dropped again. This directly contradicts the runtime layer, which built and tested dedicated machinery for empty-ID tool calls (helpers.go:146-235 synthetic keys; order_test.go TestCollectStreamDoesNotMergeDistinctEmptyIDCalls, empty_toolcall_test.go TestCollectStreamEmptyIDDeltaBeforeStartIsAdopted): no adapter can ever emit an empty ToolCallID (Anthropic requires id, Gemini synthesizes `gemini_tool_%d`), so that collector machinery is unreachable dead wiring while the one adapter that encounters id-less calls discards them.

```
tool_state.go:95-100 `if call.id == "" || call.name == "" { if call.id != "" || call.name != "" || call.arguments != "" { call.ended = true; sendEvent(..., StreamEventToolCallDropped) } continue }` — a call with name+arguments but empty id is dropped; contrast gemini/provider.go:268-271 `if id == "" { id = fmt.Sprintf("gemini_tool_%d", syntheticIndex) }`.
```

**Suggested fix:** In closeOpen (and at start-emission time in applyDelta), synthesize an id when name is present but id is empty — e.g. `call.id = fmt.Sprintf("openai_tool_%d", index)` mirroring the Gemini adapter — so the call dispatches and the echoed tool_call_id round-trips; keep the drop only for genuinely nameless calls.

#### [low/bug] claude-haiku-3.5 is flagged vision-capable, but claude-3-5-haiku-20241022 does not support image input
`internal/modelregistry/catalog.go:36`

The claude-haiku-3.5 entry includes ModelCapabilityVision, but Anthropic's claude-3-5-haiku-20241022 is a text-only model (Anthropic's model table lists Claude 3.5 Haiku without vision; image input on it returns a 400 invalid_request_error). The vision gates (modelregistry.SupportsVision via cli/exec.go:262 and tui/image_attach.go) will therefore approve attaching images, and the Anthropic adapter will serialize them as image blocks (anthropic/provider.go:391-399), producing a hard API error for the whole request instead of the intended drop-and-warn behavior. Reachability is narrow — ResolveWithFallback redirects the deprecated model in the --model and /model flows — but providers.resolveProfile uses registry.Get with no deprecation redirect, so a config-profile pinned to claude-haiku-3.5 still runs the real model with the wrong capability flag.

```
catalog.go:36 `anthropicModel("claude-haiku-3.5", ..., []ModelCapability{ModelCapabilityVision, ModelCapabilityPromptCache}, nil, ...)` ; vision gate: vision.go:13-15 `return registry.SupportsCapability(modelID, ModelCapabilityVision)` ; no-fallback path: providers/factory.go:113 `if entry, ok := registry.Get(model); ok {` (Get, per resolve.go:9-10, "does NOT apply deprecation fallbacks").
```

**Suggested fix:** Remove ModelCapabilityVision from the claude-haiku-3.5 entry so SupportsVision returns false and the existing drop-and-warn path handles images for that model.

#### [low/dead-code] isImplicitOpenAI is provably unreachable at its only call site (dead condition in profile resolution)
`internal/providers/factory.go:118`

In resolveProfile, the registry-hit branch does `if !explicitProvider || isImplicitOpenAI(profile, providerKind)`. explicitProviderKind returns ("", false) exactly when both profile.ProviderKind and profile.Provider are blank after trimming; isImplicitOpenAI requires those same two fields to be blank. So whenever explicitProvider is true (the only case where the second disjunct is evaluated), isImplicitOpenAI's `strings.TrimSpace(string(profile.ProviderKind)) == "" && strings.TrimSpace(profile.Provider) == ""` conjuncts are necessarily false — the function can never return true where it is called, and the condition reduces to `!explicitProvider`. The apparent intent (let a defaulted kind=openai profile be re-pointed at a registry model from another provider) never fires: an env-derived OpenAI profile (config/resolver.go:426 sets ProviderKind explicitly) combined with -m claude-sonnet-4.5 hits the 'belongs to anthropic, not openai' error at line 127 instead.

```
factory.go:118 `if !explicitProvider || isImplicitOpenAI(profile, providerKind) {` ; factory.go:147-157 explicitProviderKind returns false only when both ProviderKind and Provider trim to "" ; factory.go:159-164 `return providerKind == config.ProviderKindOpenAI && strings.TrimSpace(string(profile.ProviderKind)) == "" && strings.TrimSpace(profile.Provider) == "" && strings.TrimSpace(profile.BaseURL) == ""` — mutually exclusive with explicitProvider==true.
```

**Suggested fix:** Either delete isImplicitOpenAI and simplify the condition to `if !explicitProvider`, or implement the apparent intent by testing the default-profile shape directly (e.g. providerKind==openai && profile.BaseURL=="") without the always-false ProviderKind/Provider emptiness conjuncts.

#### [low/dead-code] Descriptor.Public is declared but never written or read anywhere
`internal/providercatalog/catalog.go:46`

The Descriptor struct exposes a `Public bool` field, but no constructor (openAI/anthropic/google/localOpenAI/openAICompat/anthropicCompat/transportDescriptor) sets it and a repo-wide grep finds no reader — unlike the neighboring RequiresAuth/Local/UsesAmbientAuth fields, which are consumed by zerocommands/contracts.go, cli/command_center.go, tui/picker.go, and providerhealth. Every descriptor therefore reports Public=false, and any future consumer keying off it would silently treat all providers as non-public.

```
catalog.go:46 `Public              bool` — sole occurrence of `.Public`/`Public:` for this type across internal/ and cmd/ (grep over non-test Go files returns only this declaration).
```

**Suggested fix:** Delete the Public field, or wire it: set it in the constructor helpers for the intended descriptors and consume it where the catalog is surfaced (contracts/picker).

#### [low/bug] HTTP 529 (Anthropic overloaded) is classified as a rate-limit error but excluded from the retry policy that retries 429/503
`internal/providers/providerio/retry.go:107`

ClassifiedError treats 529 as a rate-limit-class status (`case http.StatusTooManyRequests, http.StatusServiceUnavailable, 529: prefix = "rate limit error: "`), and Anthropic documents 529 overloaded_error as its transient backpressure signal — semantically identical to 503 ('the server explicitly did NOT accept the request', the package's own stated criterion for safe replay). Yet ShouldRetryStatus only matches 429 and 503, so Anthropic overload responses bypass SendWithRetry's backoff and surface immediately as a turn-ending 'rate limit error', while the equivalent condition on OpenAI (429) is retried up to 3 times. Anthropic users get strictly worse resilience for the same class of transient failure the codebase already labels retryable.

```
retry.go:107-109 `func ShouldRetryStatus(code int) bool { return code == http.StatusTooManyRequests || code == http.StatusServiceUnavailable }` vs providerio.go:200-203 `case http.StatusTooManyRequests, http.StatusServiceUnavailable, 529: prefix = "rate limit error: "`.
```

**Suggested fix:** Add 529 to ShouldRetryStatus (`|| code == 529`) — it satisfies the same not-accepted/no-duplicate-work invariant as 503 — and update the policy comment.


### Specialist, spec mode, verify

Audited internal/specialist, specmode, review, selfverify, verify, and testrunner plus their wiring (cli exec/app, tui spec_mode/model, background manager, sessions store, streamjson, PR-review workflow). All packages build, vet clean, and pass tests. Eight concrete defects found. Highest impact: (1) specialist child sessions launched without a Task `description` get a garbage/empty AgentName (derived from --session-title, which BuildArgs only emits when description is non-empty), permanently breaking Task resume for those sessions; (2) specialist children are always spawned with `--auto high` → PermissionModeUnsafe, so in the TUI's Ask mode a single approved Task prompt silently grants the worker specialist unprompted bash/write access — undisclosed in the permission card and asymmetric with the exec surface, which gates Task registration behind parent unsafety; (3) a check-then-append dedupe race lets the background onExit goroutine and TaskOutput polls double-record specialist stop/usage events (double-counted tokens), aggravated by app.go constructing the Executor with a nil SessionStore so each accounting call uses a fresh Store; (4) ParseStream's 1 MiB scanner cap fails the whole (successful) specialist run when the child emits one oversized stream-json line — reachable because tool_call events embed untruncated write_file arguments. Plus dead code (verify.RunLoop duplicated by selfverify.Run; specialist formatTaskOutput), an inconsistent SetPID-failure cleanup path that deletes the prompt file under a running child and skips terminal accounting, and byte-index title truncation that can split UTF-8 runes. Spec-mode draft/review lifecycle (submit_spec control meta → StopReasonSpecReviewRequired → TUI/CLI approve/reject), review-pipeline markdown/env parsing, and testrunner command construction/result parsing otherwise checked out: meta-control spoofing is closed (MCP meta is fixed-key), spec path containment is correct and symlink-safe, and the go/bun/node/pytest/cargo parsers match the commands testrunner constructs.

#### [high/wiring] Specialist sessions launched without a description are unresumable (AgentName never recorded / recorded as garbage)
`internal/specialist/exec.go:166`

Task resume depends on the child session's AgentName, which the child exec derives exclusively from --session-title via specialistAgentName(title) (internal/cli/exec.go:363, internal/cli/exec_sessions.go:103-112). BuildArgs only passes --session-title when the optional Task `description` argument is non-empty. When description is omitted (it is not in the tool schema's Required list — task_tool.go:55 requires only "prompt"), the child falls back to execSessionTitle(prompt), which is the first 80 bytes of the WRAPPED prompt: "# Specialist Invocation Specialist: worker ...". specialistAgentName then cuts at the first ':' and stores AgentName = "# Specialist Invocation Specialist". A later Task{resume: <child-id>} run hits runResume → loadManifest("# Specialist Invocation Specialist") → "specialist ... not found" (or, for titles without ':', the "does not identify a specialist" error at exec.go:241-244). So every specialist spawned without a description produces a child session that can never be resumed, and `zero sessions` shows the wrapped-prompt garbage as the title.

```
internal/specialist/exec.go:166-168: `if description := strings.TrimSpace(input.Description); description != "" { args = append(args, "--session-title", strings.TrimSpace(input.Manifest.Metadata.Name)+": "+description) }` — no else branch. internal/cli/exec.go:363: `AgentName: specialistAgentName(options.sessionTitle)`. internal/cli/exec_sessions.go:96-100 falls back to createSessionTitle(prompt) when --session-title is absent, and the prompt is always WrapSystemPrompt output beginning "# Specialist Invocation\n\nSpecialist: <name>..." (envelope.go:10-11). internal/specialist/exec.go:241-247 then rejects resume: `specialistName := strings.TrimSpace(session.AgentName); if specialistName == "" { return ... "does not identify a specialist" }` / loadManifest(specialistName) fails for the garbage name.
```

**Suggested fix:** In BuildArgs, always emit --session-title: when description is empty, pass the manifest name alone (e.g. `args = append(args, "--session-title", name)`), so specialistAgentName resolves to the real specialist name. Alternatively add a dedicated --agent-name flag set from input.Manifest.Metadata.Name and stop deriving AgentName from the display title.

#### [medium/security] Specialist children always run with --auto high (PermissionModeUnsafe): one Task approval silently grants unprompted shell/write access
`internal/specialist/exec.go:150`

BuildArgs and BuildResumeArgs hard-code `--auto high` for every specialist child, regardless of the parent's permission mode. In the child exec, "high" resolves to agent.PermissionModeUnsafe (internal/cli/exec_tools.go:84-85), which sets permissionGranted=true for every tool (internal/agent/loop.go:439) — and headless exec wires no OnPermissionRequest, so no sandbox/permission prompt can ever fire in the child. The TUI registers the Task tool unconditionally (internal/cli/app.go:325/409), so in the default Ask mode a user who approves a single "Task" prompt (whose Safety.Reason only says "Spawns a Zero specialist sub-agent process.") has actually authorized the built-in `worker` specialist (tools: read-only, edit, execute — includes bash, write_file, apply_patch; builtin.go:9-13) to run arbitrary shell commands and file writes with zero per-action prompting, while the same bash call in the parent TUI session would prompt every time. The exec surface partially acknowledges this by only registering Task when the parent itself is high/unsafe (app.go:412-420), but the TUI has no such gate and no disclosure.

```
internal/specialist/exec.go:150: `args = append(args, "--auto", "high", "--output-format", "stream-json")` (same at line 195 for resume). internal/cli/exec_tools.go:84-85: `case "high": mode = agent.PermissionModeUnsafe`. internal/agent/loop.go:439: `permissionGranted := permissionMode == PermissionModeUnsafe`. internal/cli/app.go:404-409 registers specialist tools for the interactive TUI with no autonomy gate, vs shouldRegisterExecSpecialistTools (app.go:412-420) which requires `options.skipPermissionsUnsafe || autonomy == "high"` for exec.
```

**Suggested fix:** Derive the child's --auto level from the parent run's permission mode (e.g. pass "medium" unless the parent is already unsafe), or at minimum change TaskTool.Safety().Reason to disclose that the sub-agent executes its allowlisted tools (including bash for worker) without further prompts, so the TUI permission card tells the truth about what is being approved.

#### [medium/race] Race: duplicate specialist stop/usage accounting events — check-then-append dedupe is not atomic and runs concurrently from onExit and TaskOutput
`internal/specialist/accounting.go:72`

recordBackgroundTaskAccounting is invoked from two concurrent paths: the background process's onExit goroutine (internal/specialist/exec.go:325-339) and every TaskOutput poll of a finished task (internal/specialist/output_tool.go:148-151, which runs on the agent-loop goroutine). Both paths dedupe via specialistEventExists() — a full ReadEvents scan — followed by a separate AppendEvent. Nothing holds the session lock across the check and the append (sessions.Store.AppendEvent locks only for the append itself, store.go:464-478), so the two goroutines can both observe "no stop/usage event yet" and both append, double-counting the child's token usage in the parent session and duplicating specialist_stop events. The race is even wider in production because app.go:409 builds the Executor with SessionStore=nil, so each call constructs a fresh Store via accountingStore() (accounting.go:186-191) and even the per-Store in-process mutex offers no serialization — only the per-append flock. As a side note, readOutput re-runs this accounting (two full event-log reads) on every poll of a completed task.

```
internal/specialist/accounting.go:80-91: `if specialistEventExists(store, input.ParentSessionID, sessions.EventUsage, input.ChildSessionID, summary.RunID) { return false, nil } ... appendSpecialistSessionEvent(store, ...)` — same pattern in recordSpecialistStop (accounting.go:31-47). Concurrent callers: exec.go:330-338 (onExit closure: `executor.recordBackgroundTaskAccounting(task, summary)`) and output_tool.go:149-151 (`if task.Status != background.StatusRunning { Executor{SessionStore: tool.SessionStore}.recordBackgroundTaskAccounting(task, summary) }`).
```

**Suggested fix:** Add a Store-level atomic "append if not exists" operation that holds lockSession across the ReadEvents check and appendEventLocked, and route specialist accounting through it. Simpler alternative: make onExit the sole writer of stop/usage accounting and drop the recordBackgroundTaskAccounting call from OutputTool.readOutput (or guard it with a sync.Once keyed by task id on the Runtime).

#### [medium/bug] Foreground specialist run fails entirely when any child stream-json line exceeds 1 MiB (untruncated tool_call args)
`internal/specialist/streamer.go:43`

ParseStream uses a bufio.Scanner capped at 1 MiB per line, and runChildProcess treats any ParseStream error as a failure of the whole specialist run (exec.go:624-627 returns the error; runBuiltArgs then returns ExecResult{}, err and the Task tool reports an error). The child's stream-json writer truncates tool_result output to 10 KiB, but tool_call events embed the FULL parsed arguments with no truncation (internal/cli/exec_writer.go:92-100: `Args: parseToolCallArgs(call.Arguments)`), and the final event embeds the full final answer. A worker specialist that calls write_file with >1 MiB of content emits a single tool_call JSON line larger than the scanner limit, so the parent gets "bufio.Scanner: token too long" and reports the entire (actually successful) child run as failed, discarding its final answer.

```
internal/specialist/streamer.go:42-43: `scanner := bufio.NewScanner(reader); scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)`. internal/specialist/exec.go:624-627: `events, err := ParseStream(...); if err != nil { return ChildRunResult{...}, err }`. internal/cli/exec_writer.go:98: tool_call event carries `Args: parseToolCallArgs(call.Arguments)` untruncated (contrast toolResult at line 158: `output, truncated := truncateForStreamJSONOutput(result.Output)`).
```

**Suggested fix:** Either raise/remove the scanner cap (use bufio.Reader.ReadString or json.Decoder over the stream) and skip-with-warning on individually unparsable lines instead of failing the whole run, or truncate tool_call Args in execEventWriter.toolCall the same way tool_result output is truncated.

#### [low/dead-code] verify.RunLoop / LoopOptions / LoopReport are dead code — production retry loop is reimplemented in selfverify
`internal/verify/verify.go:215`

verify.RunLoop (with LoopOptions.OnFailure, Attempt, LoopReport) is referenced nowhere in production code; the only caller is its own test (internal/verify/verify_test.go:196-207). The CLI's `zero verify --attempts N` path goes through selfverify.Run (internal/cli/workflows.go:113, internal/cli/app.go:124), which duplicates the same attempt loop with its own Attempt/Report types. Two parallel implementations of the retry loop invite divergence (they already differ: RunLoop has no StopReason and a differently-shaped failure callback).

```
grep over the repo: RunLoop/LoopOptions/LoopReport appear only in internal/verify/verify.go:83-111,215-244 and internal/verify/verify_test.go:196-207. Production wiring: internal/cli/app.go:124 `runSelfVerify: selfverify.Run` and internal/cli/workflows.go:113 `deps.runSelfVerify(context.Background(), plan, selfverify.Options{...})`.
```

**Suggested fix:** Delete RunLoop, LoopOptions, LoopReport, and Attempt from internal/verify (moving the one test to selfverify), or refactor selfverify.Run to delegate to verify.RunLoop so there is a single loop implementation.

#### [low/dead-code] formatTaskOutput is dead code
`internal/specialist/output_tool.go:244`

formatTaskOutput(task, data) has zero callers anywhere in the repo (including tests); the live path is summarizeTaskData + formatTaskOutputSummary called directly from readOutput. It silently drops the second return value of summarizeTaskData's StreamResult/exit handling duplication and will rot.

```
`grep -rn "formatTaskOutput\b" .` returns only the definition at internal/specialist/output_tool.go:244: `func formatTaskOutput(task background.Task, data string) string { summary, rawLines := summarizeTaskData(data, task.ExitCode); return formatTaskOutputSummary(task, summary, rawLines) }`.
```

**Suggested fix:** Delete the function.

#### [low/bug] SetPID failure path deletes the prompt file out from under a running child and records no terminal accounting/status
`internal/specialist/exec.go:346`

In runBackground, after the child process has been successfully launched, a SetPID persistence failure makes the tool return an error AND immediately calls cleanupBackgroundPromptFile, which deletes the temp prompt file (via Runtime.UntrackPromptFile → cleanupPromptFile) while the just-launched child may not yet have read it (`zero exec --file <path>` fails with "prompt file not found"). Unlike every other failure branch in this function, it also skips manager.UpdateStatus(StatusError) and recordSpecialistStop, so the task stays "running" in the background manager with the in-memory PID lost on restart, and the parent session has a specialist_start event with no matching stop.

```
internal/specialist/exec.go:346-350: `if pid > 0 { if err := manager.SetPID(built.SessionID, pid); err != nil { executor.cleanupBackgroundPromptFile(built.SessionID, built.PromptFile); return ExecResult{}, err } }` — contrast the launch-failure branch at lines 340-345 which calls UpdateStatus(StatusError) and recordSpecialistStop before returning.
```

**Suggested fix:** On SetPID failure, do not delete the prompt file (the onExit callback already cleans it up when the child exits); instead log/record the error, call recordSpecialistStop(...,"error",...), and either kill the orphaned child or leave the task running but report the degraded state in the tool output.

#### [low/bug] Spec/session titles are truncated by byte index, splitting multi-byte UTF-8 runes
`internal/tui/spec_mode.go:301`

specImplementationTitle truncates the model-supplied spec title with `title[:tuiSessionTitleLimit]` — a byte slice on a string that can contain multi-byte UTF-8 (spec titles are free text from submit_spec). A title longer than 80 bytes whose 80th byte falls inside a rune produces invalid UTF-8 in the stored session title, which is then persisted in session metadata and rendered in the TUI/sessions list. The same byte-slicing pattern exists in tuiSessionTitle (internal/tui/session.go:92-93) and createSessionTitle (internal/cli/exec_sessions.go:87-89), both fed by arbitrary user prompts.

```
internal/tui/spec_mode.go:300-304: `if len(title) > tuiSessionTitleLimit { title = title[:tuiSessionTitleLimit] } return title + " implementation"`. internal/tui/session.go:92-93 and internal/cli/exec_sessions.go:87-89 contain the identical `title[:80]` byte slice.
```

**Suggested fix:** Truncate on a rune boundary, e.g. `if utf8.RuneCountInString(title) > limit { title = string([]rune(title)[:limit]) }`, or walk back from the byte limit with utf8.RuneStart; apply the same helper in all three sites.


### Agent loop (internal/agent)

Audited internal/agent (loop.go, types.go, compaction.go, compaction_preserve.go, context_measurement.go, guardrails.go, system_prompt*.go) plus the zeroruntime stream collector, providerio/Anthropic streaming, and the TUI/exec call sites they wire into. The run loop's core mechanics are solid: tool_use/tool_result pairing is preserved on every abort path (appendAbortedToolResults), compaction widens the preserved suffix to an assistant boundary so strict providers accept the result, ask_user cancellation correctly aborts with typed context errors, deferred-tool loading and the repeated-failure guard are coherent, and no goroutine leaks were found (providers select on ctx in SendEvent; channels are buffered and closed). Eleven concrete defects: (1) HIGH — the truncation/content-filter pipeline (FinishReason/Truncated) is produced by all three providers and collected by zeroruntime but never consumed by the loop or any surface, so max_tokens-clipped answers are silently returned as complete final answers and truncated tool-call JSON is dispatched; (2) MEDIUM — an 'always allow' permission decision is converted into a denial whenever grant persistence fails, and persistence always fails with a nil Sandbox; (3) MEDIUM — estimateTokens ignores image attachments (and the compaction trigger ignores tool-definition tokens), undercounting the context budget so proactive compaction fires late or never on image-heavy runs; (4) MEDIUM — compaction summarizer provider calls bypass OnUsage, making token/cost telemetry wrong; plus LOW findings: flattened context.Canceled identity on mid-stream cancel (the loop's own ctx.Err() check is unreachable for that path), dead OnContext/MeasureContext wiring with zero consumers, a duplicated items-schema mapping, dead requestEvent stores and an unreachable fallbackPermissionEvent, a UTF-8 rune-splitting byte truncation of project guidelines in the system prompt, retried text after mid-stream compaction never reaching streaming surfaces (transcript diverges from model context), and O(n^2) string accumulation in the stream collector hot path.

#### [high/wiring] Truncated/filtered responses (FinishReason) are produced by every provider but never consumed by the agent loop
`internal/agent/loop.go:233`

All three providers normalize abnormal stop reasons (OpenAI finish_reason=length/content_filter, Anthropic stop_reason=max_tokens, Gemini MAX_TOKENS/SAFETY) onto StreamEvent.FinishReason, and zeroruntime.CollectedStream carries it with a dedicated Truncated() helper. The agent loop never reads either: a response clipped at the output-token cap or withheld by a safety filter is appended to the transcript and returned as result.FinalAnswer exactly like a normal completion, with no warning to the user or nudge to the model. Worse, when a tool call's streamed JSON arguments are cut mid-stream by max_tokens, the collector still flushes the partial call, and the loop dispatches it, producing a misleading 'Failed to parse arguments' tool error and burning retry turns. grep confirms no consumer of FinishReason/Truncated() exists in internal/agent, internal/tui, internal/cli, or internal/sessions — the entire normalization pipeline is dead at the loop boundary.

```
loop.go:141 collects the stream (`collected := zeroruntime.CollectStreamWithOptions(...)`) and the only fields ever read are Error, Text, ToolCalls, DroppedToolCalls; line 233 then does `result.FinalAnswer = collected.Text` unconditionally. Meanwhile zeroruntime/helpers.go:24 defines `func (collected CollectedStream) Truncated() bool { return collected.FinishReason != "" }` and e.g. internal/providers/anthropic/provider.go:281 emits `StreamEventDone, FinishReason: state.finishReason`. `grep -rn "FinishReason|Truncated()" internal/agent internal/tui internal/cli internal/sessions` (non-test) returns nothing.
```

**Suggested fix:** After collection, check `collected.Truncated()`. Minimal: when FinishReason==length and the turn had no tool calls, append a user-role notice ('your previous reply was truncated at the token cap; continue') and continue the loop instead of returning it as FinalAnswer; when it must return, surface the truncation on Result (e.g. Result.FinishReason) so the TUI/exec writer can warn. Also skip dispatching tool calls from a length-truncated turn.

#### [medium/ux] "Always allow" permission decision is converted into a denial when grant persistence fails (always, when Options.Sandbox is nil)
`internal/agent/loop.go:468`

When the user answers a permission prompt with PermissionDecisionAlwaysAllow, the loop sets permissionGranted=true but then requires persistPermissionGrant to succeed; on any error it emits a deny event and returns a denied tool result — the tool the user just explicitly approved does not run. persistPermissionGrant unconditionally errors when options.Sandbox is nil ('sandbox engine is not configured'), so any embedder that wires OnPermissionRequest without a sandbox engine gets a guaranteed denial for every always-allow answer, and even in the TUI/exec (which do wire an engine) a transient grant-store write failure turns an explicit user approval into a refusal fed back to the model as 'Permission denied'.

```
loop.go:466-471: `case PermissionDecisionAlwaysAllow: permissionGranted = true; grant, err := persistPermissionGrant(call.Name, decisionReason, options); if err != nil { emitDeniedPermission(options, call, requestEvent, "failed to persist permission grant: "+err.Error()); return deniedPermissionResult(call, ...), nil }` and loop.go:721-723: `if options.Sandbox == nil { return sandbox.Grant{}, errors.New("sandbox engine is not configured") }`.
```

**Suggested fix:** On persistPermissionGrant failure, keep permissionGranted=true and run the tool for this call (the user approved it), recording the persistence failure as a non-fatal note (e.g. in the permission event's DecisionReason) instead of returning deniedPermissionResult.

#### [medium/bug] estimateTokens ignores image attachments (and the compaction trigger ignores tool definitions), so the context budget undercounts
`internal/agent/compaction.go:58`

estimateTokens counts only Content and tool-call name/argument characters. Message.Images — which the TUI seeds onto the first user turn and which cost on the order of 1k-1.6k tokens per image on real providers — contribute ~0 (just the 4-token per-message overhead). maybeCompact compares this estimate against 0.8×ContextWindow, so an image-heavy conversation crosses the real window well before the estimate crosses the threshold; proactive compaction never fires and the run instead hits the provider's context-limit error, relying on the one-shot reactive recover (which is consumed after a single use per run). The same trigger also excludes advertised tool-definition tokens (MeasureContext counts them separately via estimateToolTokens, but maybeCompact only calls estimateTokens(messages)), further delaying the trigger on MCP-heavy registries.

```
compaction.go:58-70: `for _, message := range messages { total += len(message.Content) / 4; for _, call := range message.ToolCalls { ... } total += 4 }` — no reference to message.Images; compaction.go:234: `size := estimateTokens(messages)` is the entire trigger input, while context_measurement.go:53 has a separate estimateToolTokens that the trigger never uses.
```

**Suggested fix:** Add a per-image constant to estimateTokens (e.g. `total += 1500 * len(message.Images)` or a bytes-derived heuristic), and include the advertised-tools estimate in the maybeCompact threshold comparison (pass the partitioned definitions' estimateToolTokens into the trigger).

#### [medium/wiring] Compaction summarizer provider calls bypass OnUsage — their token spend is invisible to usage accounting
`internal/agent/compaction.go:376`

summarizeMessagesOnce performs a real provider call whose input is the entire elided middle of the conversation (potentially tens of thousands of tokens, and summarizeWithFallback can recurse into multiple such calls), but it collects via CollectStream with no OnUsage callback. The TUI records usage rows and session EventUsage entries exclusively from Options.OnUsage, and exec's writer does the same, so every compaction's token consumption is silently dropped from telemetry and cost reporting. This contradicts the loop's own stated invariant on the reactive retry path ('OnUsage IS kept so token telemetry/budgeting still counts the successful retry', loop.go:180-181): the retry is counted but the compaction that enabled it is not.

```
compaction.go:376: `collected := zeroruntime.CollectStream(ctx, stream)` inside summarizeMessagesOnce, and the deliberate comment at compaction.go:314-315: 'The summary stream intentionally does NOT forward OnText / OnUsage callbacks, so compaction stays invisible on the user-facing surface.' OnText suppression is correct for display, but OnUsage suppression makes billing/telemetry wrong.
```

**Suggested fix:** Thread Options.OnUsage into summarizeClosure/summarizeMessagesOnce (use CollectStreamWithOptions(ctx, stream, CollectOptions{OnUsage: onUsage})) while continuing to omit OnText, so summarizer token counts land in the same usage stream as every other provider call.

#### [low/bug] Mid-stream context cancellation is returned as a flattened errors.New string; the ctx.Err() identity check is unreachable for that path
`internal/agent/loop.go:188`

When the run context is canceled while a response is streaming, CollectStreamWithOptions sets collected.Error to ctx.Err().Error() ('context canceled'). The loop returns errors.New(collected.Error) at line 188-191 BEFORE the `if ctx.Err() != nil` check at line 192, so the returned error loses its identity: errors.Is(err, context.Canceled) is false. Today's callers compensate (exec.go:500 and exec_spec.go:164 add `|| runCtx.Err() != nil`; the TUI discards the message via runID mismatch), but the loop's own cancellation contract is inconsistent — the ask_user abort path carefully preserves context.Canceled identity (loop.go:602) while the much more common mid-stream cancel does not, and any future caller relying on errors.Is will misclassify a user cancel as a provider error.

```
zeroruntime/helpers.go:84-86: `case <-ctx.Done(): collected.Error = ctx.Err().Error()`; loop.go:188-195: `if collected.Error != "" { result.Messages = copyMessages(messages); return result, errors.New(collected.Error) }` followed only afterwards by `if ctx.Err() != nil { ... return result, ctx.Err() }`.
```

**Suggested fix:** Check `ctx.Err()` before the collected.Error return (return result, ctx.Err() when non-nil), or have CollectStreamWithOptions carry the typed error so the loop can return the original context error instead of a re-stringified copy.

#### [low/dead-code] OnContext / MeasureContext context-utilization pipeline has no consumer on any surface
`internal/agent/types.go:183`

Options.OnContext is documented as the hook 'so a surface (TUI/CLI) can show context utilization', and the loop dutifully computes MeasureContext(messages, request.Tools, options.ContextWindow) once per turn when it is set. No caller in the repository ever sets OnContext — not the TUI (model.go's runAgentWithOptions), not exec.go, not exec_spec.go — so the callback never fires, ContextBreakdown is never displayed anywhere, and the per-turn measurement code is effectively dead. The only external user of this package's measurement surface is contextreport, which calls BuildSystemPromptPreview and re-implements its own token estimation rather than using MeasureContext.

```
types.go:183 `OnContext func(ContextBreakdown)` and loop.go:101-103 `if options.OnContext != nil { options.OnContext(MeasureContext(...)) }`; `grep -rn "OnContext" --include=*.go .` (non-test) matches only these two sites — no surface assigns it.
```

**Suggested fix:** Wire it: have the TUI set OnContext and render UsedFraction in the status line (the data is already computed), or delete the field, MeasureContext's per-turn call, and ContextBreakdown until a consumer exists.

#### [low/dead-code] Duplicate items-schema assignment in propertyToRuntimeMap
`internal/agent/loop.go:1072`

propertyToRuntimeMap maps property.Items into schema["items"] twice — at lines 1063-1064 and again identically at lines 1072-1074. The second block recomputes propertyToRuntimeMap(*property.Items) (a recursive walk) and overwrites the same key with an equal value, so it is pure waste executed for every array-typed property of every advertised tool on every turn.

```
loop.go:1063-1064 `if property.Items != nil { schema["items"] = propertyToRuntimeMap(*property.Items) }` followed at loop.go:1072-1074 by the byte-identical `if property.Items != nil { schema["items"] = propertyToRuntimeMap(*property.Items) }`.
```

**Suggested fix:** Delete the second `if property.Items != nil { ... }` block (lines 1072-1074).

#### [low/dead-code] Dead stores to requestEvent after always-allow, and unreachable fallbackPermissionEvent
`internal/agent/loop.go:473`

In the PermissionDecisionAlwaysAllow success branch the loop sets requestEvent.GrantMatched = true and requestEvent.Grant = &grant, but requestEvent is never read after the switch — the post-run OnPermission event is rebuilt from scratch via buildPermissionEvent(call, tool, args, ..., sandboxDecision) at line 501, so the grant annotation never reaches any consumer. Separately, fallbackPermissionEvent (line 861) is unreachable in practice: it only runs when buildPermissionEvent returns ok=false at line 452-455, which requires decision==nil AND safety.Permission==PermissionAllow, but the guarding shouldRequestPermission (line 694-702) already requires Permission==PermissionPrompt, making the !ok branch impossible at that call site.

```
loop.go:473-474 `requestEvent.GrantMatched = true; requestEvent.Grant = &grant` with no subsequent read of requestEvent; loop.go:451 gate `shouldRequestPermission(tool, permissionGranted, preflightDecision)` requires `tool.Safety().Permission == tools.PermissionPrompt` (line 695), while buildPermissionEvent's only ok=false return for a nil decision is the `default:` branch (line 830-831) reached when Permission is neither Deny nor Prompt.
```

**Suggested fix:** Either propagate the grant into the post-run event (e.g. attach decision/grant to the rebuilt event) or remove the two dead assignments; remove fallbackPermissionEvent and the !ok branch, or replace with a direct construction if defensive coverage is wanted.

#### [low/bug] Project-guidelines truncation can split a multibyte UTF-8 rune in the system prompt
`internal/agent/system_prompt.go:89`

workspaceContext caps an oversized AGENTS.md/ZERO.md at maxProjectContextBytes with a raw byte slice: content[:maxProjectContextBytes]. If the 8 KiB boundary lands inside a multibyte rune (non-ASCII guidelines: CJK text, emoji, box-drawing characters), the system prompt ends with an invalid UTF-8 sequence that json.Marshal will replace with U+FFFD on the provider wire. The sibling code in compaction_preserve.go (capBody, line 156-169) does this correctly by walking back to utf8.RuneStart — this path simply doesn't.

```
system_prompt.go:88-90: `if len(content) > maxProjectContextBytes { content = content[:maxProjectContextBytes] + "\n… (truncated)" }` versus capBody's `for limit > 0 && !utf8.RuneStart(body[limit]) { limit-- }` in compaction_preserve.go:165-166.
```

**Suggested fix:** Walk the cut point back to a rune boundary before slicing, mirroring capBody: `limit := maxProjectContextBytes; for limit > 0 && !utf8.RuneStart(content[limit]) { limit-- }; content = content[:limit] + "\n… (truncated)"` (same fix applies to internal/contextreport/contextreport.go:236).

#### [low/ux] Reactive mid-stream retry never forwards the retried assistant text to OnText, so streaming surfaces show the aborted partial text instead
`internal/agent/loop.go:183`

When a context-limit error surfaces mid-stream, the loop compacts and retries the turn, deliberately collecting the retry without OnText to avoid duplicating the already-streamed prefix. But for an intermediate (tool-calling) turn, the TUI renders assistant text only from OnText deltas (agentTextMsg) — result.FinalAnswer is rendered only for the run's final turn. So after a mid-stream retry, the user's transcript permanently shows the first attempt's truncated partial text while the model's actual context (messages, line 197-201) contains the different, complete retried text; the two can disagree arbitrarily. The code comments acknowledge the duplicate-avoidance choice but the result is a transcript that doesn't match what the model said.

```
loop.go:177-185: 'Omit OnText on the reactive retry: when the original error surfaced MID-stream, partial text was already forwarded ... The retried text is captured in collected.Text and becomes the turn's assistant message.' — `collected = zeroruntime.CollectStreamWithOptions(ctx, retryStream, zeroruntime.CollectOptions{OnUsage: options.OnUsage})` with no OnText; the TUI streams rows solely from OnText for non-final turns (model.go:1136-1141, 1398-1403).
```

**Suggested fix:** Track how many bytes of text were already emitted before the failure and, on the retry, forward collected text through OnText after suppressing/communicating the replacement (e.g. emit a marker delta like '\n[retrying after compaction]\n' followed by the retried text), so surfaces display the text the model actually produced.

#### [low/perf] Streamed text accumulation is O(n^2): collected.Text += delta copies the whole buffer on every text event
`internal/zeroruntime/helpers.go:104`

CollectStreamWithOptions accumulates assistant text with string concatenation per delta. Providers emit deltas of a few bytes to a few dozen bytes, so a long response of N bytes arriving in k deltas copies O(N*k) bytes total (quadratic in response length for fixed delta size). For a 64 KB response in ~4k deltas that is ~130 MB of memcpy and garbage per turn, on the hot path of every agent turn and every compaction summarization. Tool-call argument accumulation has the same pattern (`collector.calls[key].Arguments += fragment`, line 202).

```
helpers.go:104: `case StreamEventText: collected.Text += event.Content` inside the per-event loop, and helpers.go:202: `collector.calls[key].Arguments += fragment`.
```

**Suggested fix:** Accumulate into a strings.Builder (one per stream for text, one per in-flight call for arguments) and materialize the string once at flush/return.


### CLI subcommands

Audited the zero CLI subcommand surfaces in internal/cli (changes, usage, doctor, cron/cron run, repo-info, sessions/rewind/resume, serve, config/models/providers command center, specialist, spec, search, update) plus their backing packages (internal/cron, internal/sessions, internal/search, internal/zerogit, internal/update, internal/doctor, internal/repoinfo, internal/redaction). Found 11 concrete defects, 5 of them verified by running targeted tests or the built binary: (1) CRITICAL — `zero search` computes match byte offsets on strings.ToLower(text) but slices the original text, producing invalid-UTF-8/misaligned context and a reproducible slice-bounds panic (verified: \"slice bounds out of range [221:206]\"); (2) HIGH — `zero cron add \"<expr>\" --recipe X` silently discards the explicit cron expression in favor of the recipe's (verified: stored */30 instead of the requested 0 6 * * *), so jobs fire on the wrong schedule with exit 0; (3) HIGH — `zero usage` aborts entirely (exit 2) outside a git repository because the supplemental net-LOC git inspection error is fatal (verified live); (4) MEDIUM — `zero update --check` can never succeed on a `go build` binary (\"invalid semantic version: dev\", verified live); (5) MEDIUM — `zero cron run` startup reconcile silently cancels --run-now jobs instead of firing them (verified by test); (6) MEDIUM perf — `zero sessions tree` re-reads every session's metadata.json per tree node (O(nodes x sessions) disk I/O). Plus five low findings: cron run records drop failure reasons (stream-json errors go to discarded stdout), mid-rune byte truncation in cron list/run records/session titles, exec bypassing the injected deps.newSessionStore (half-wired dependency), `zero search --session-id <missing>` silently succeeding with 0 results, and `zero doctor` printing apiKey: [REDACTED] whether or not a key is set (verified live). No findings fabricated from style preferences; each includes the exact code evidence and a minimal fix.

#### [critical/bug] zero search panics (slice out of range) and emits misaligned/invalid-UTF-8 context due to ToLower byte-offset mismatch
`internal/search/search.go:346`

findMatch computes byte offsets on `strings.ToLower(text)` but buildContext (and the Match{Start,End} reported in --json output) applies those offsets to the ORIGINAL text. Unicode simple case mapping changes byte length (U+0130 'İ' 2B → 'i' 1B shrinks; U+023A 'Ⱥ' 2B → U+2C65 'ⱥ' 3B grows), so offsets diverge. Verified two failure modes with tests: (1) shrink direction — context window is misaligned and starts mid-rune, producing invalid UTF-8 ("\xb0İİİ…") that does not even contain the match; (2) grow direction — `buildContext` panics `slice bounds out of range [221:206]` for text containing ~100 'Ⱥ' before the match, crashing `zero search` (caught only by the top-level observability.Recover, which writes a crash report and exits 1). Session event text is arbitrary user/model/tool content, so non-ASCII triggering text is realistic.

```
func findMatch(text string, query string, terms []string) (Match, bool) {
    normalizedText := strings.ToLower(text)
    if index := strings.Index(normalizedText, query); index >= 0 {
        return Match{Start: index, End: index + len(query)}, true
...
func buildContext(text string, start int, end int, contextChars int) string {
    left := start - contextChars ... return strings.TrimSpace(text[left:right])

Test output: "BUG CONFIRMED: buildContext panicked: runtime error: slice bounds out of range [221:206]" for strings.Repeat("Ⱥ",100)+" hello"
```

**Suggested fix:** Make offsets refer to one string: search over the lowered text and also slice the lowered text for context, or (better) clamp `left`/`right` into [0,len(text)] with `left = min(left,right)` AND compute match offsets on the original string (e.g. use a case-folding search such as scanning with strings.EqualFold over windows, or build the index entries lowercased once at index time so offsets are self-consistent).

#### [high/bug] cron add silently ignores explicit cron expression when --recipe is also given
`internal/cli/cron.go:112`

In cronAdd, the recipe block runs BEFORE the positional expression is consumed. With both a positional <cron-expr> and --recipe (a combination the help text explicitly documents: `zero cron add <cron-expr> [--prompt P | --recipe R]`), the recipe's expression is assigned first (`if expr == "" { expr = r.Expr }`), so the later `if len(positional) == 1 && expr == ""` guard is false and the user's explicit schedule is silently dropped. Verified with a test: `zero cron add "0 6 * * *" --recipe git-recap` stores Expr="*/30 * * * *" and exits 0 — the job fires every 30 minutes instead of daily at 06:00, with no warning.

```
if recipe != "" { ... if expr == "" { expr = r.Expr } ... }
if len(positional) == 1 && expr == "" { expr = positional[0] }  // never runs when a recipe set expr

Test output: add exit=0; stored job expr="*/30 * * * *" for args {"add", "0 6 * * *", "--recipe", "git-recap"}
```

**Suggested fix:** Assign the positional expression before applying recipe defaults: move `if len(positional) == 1 && expr == "" { expr = positional[0] }` (and the extra-args check) above the `if recipe != ""` block, so the recipe only fills expr when the user did not supply one.

#### [high/bug] zero usage hard-fails outside a git repository (entire token report aborted by the net-LOC helper)
`internal/cli/usage.go:121`

runUsage unconditionally calls deps.inspectChanges (zerogit.Inspect) to compute the supplemental net-LOC estimate, and returns a usage error if it fails. zerogit.Inspect returns `not a git repository: ...` whenever the cwd is not inside a git work tree, so the command's PRIMARY function — summarizing token usage and estimated cost from persisted sessions — is completely unavailable outside a git repo. Verified live: running `zero usage` in a non-git directory prints `[zero] not a git repository: fatal: not a git repository...` and exits 2 (a usage-error code for an environmental condition). The help text frames net LOC as a supplemental "working-tree diff proxy" estimate, not a prerequisite.

```
summary, err := deps.inspectChanges(context.Background(), zerogit.InspectOptions{Cwd: workspaceRoot})
if err != nil {
    return writeExecUsageError(stderr, err.Error())
}

Live run in /tmp (non-git): "[zero] not a git repository: fatal: not a git repository (or any of the parent directories): .git" exit=2
```

**Suggested fix:** Degrade gracefully: on inspectChanges error, proceed with diff = zerogit.DiffStat{} (net LOC 0 / "n/a") and optionally print a one-line notice to stderr, instead of aborting the report; reserve exit 2 for real argument errors.

#### [medium/bug] zero update --check always fails on source/dev builds: "invalid semantic version: dev"
`internal/update/update.go:135`

update.Check normalizes Options.CurrentVersion through normalizeVersionTag and returns an error when it is not semver. The CLI passes the package-level `version` variable (internal/cli/app.go:33 `var version = "dev"`), which is only overridden by -ldflags in release builds (internal/release/release.go:258). For anyone who built with `go build`/`go install` (the normal contributor path), `zero update --check` can never succeed: verified live, it prints `[zero] Could not check for updates: invalid semantic version: dev` and exits 1. The fallback `firstNonEmpty(options.CurrentVersion, "0.0.0")` only catches the empty string, not the actual default "dev".

```
currentVersion, err := normalizeVersionTag(strings.TrimSpace(firstNonEmpty(options.CurrentVersion, "0.0.0")))
if err != nil {
    return Result{}, err
}

Live run of a `go build` binary: "zero dev" / "[zero] Could not check for updates: invalid semantic version: dev"
```

**Suggested fix:** Treat a non-semver current version as "0.0.0" instead of failing: e.g. `cv, err := normalizeVersionTag(...); if err != nil { cv = "0.0.0" }` (optionally note "current version unknown (dev build)" in Format output) so the latest-release lookup still works.

#### [medium/bug] cron run start-up reconcile silently cancels --run-now jobs
`internal/cli/cron_run.go:115`

`zero cron add ... --run-now` persists NextRunAt = now and prints "next run <now>" to the user. But when the foreground scheduler is started later with plain `zero cron run` (no --catch-up), reconcileOverdue classifies any NextRunAt before the current minute as "strictly overdue" and reschedules it to the next cron slot WITHOUT firing. So a --run-now job added more than a minute before the scheduler starts never gets its promised immediate run. Verified with a test: job added 08:00 with --run-now, reconcile at 08:05 moved NextRunAt to 09:00 with FireCount=0 and no output. The skip-backlog behavior is reasonable for ordinary overdue schedules, but it contradicts the explicit --run-now request and the message `cron add` printed.

```
// cronAdd: if runNow { next = now() } ... fmt.Fprintf(stdout, "Added cron job %s (%s); next run %s.\n", ...)
// cronRun (forever mode): if !catchUp { reconcileOverdue(store, now, ids, stderr) }
// reconcileOverdue: if !j.NextRunAt.Before(nowMin) { continue } ... j.NextRunAt = nxt (no fire)

Test output: after add: next=2026-06-09 08:00:00; after reconcile(08:05): next=2026-06-09 09:00:00 fired=0
```

**Suggested fix:** Persist a flag (e.g. Job.RunOnce or a sentinel) for --run-now jobs and have reconcileOverdue skip them (let fireDue fire them once), or bound the reconcile to jobs whose NextRunAt matches their schedule (a NextRunAt that is not a valid slot of the expression is a pending run-now request and should fire).

#### [medium/perf] zero sessions tree is O(nodes x sessions) disk reads (full store re-list per tree node)
`internal/sessions/lineage.go:140`

Store.tree recurses over the child tree and calls ListChildren for every node; ListChildren calls store.List(), which os.ReadDir()s the entire session root and ReadFile+json.Unmarshal's EVERY session's metadata.json, then filters by ParentSessionID. For a store with S sessions and a tree of T nodes, `zero sessions tree` performs S*T metadata file reads and JSON parses (plus a Get per node). Session stores grow unbounded over time (every exec stream-json run creates one), so this hot path degrades quadratically; with a few thousand accumulated sessions a single tree command does millions of file reads.

```
func (store *Store) tree(sessionID string, seen map[string]bool) (TreeNode, error) {
    ...
    children, err := store.ListChildren(sessionID)   // ListChildren -> store.List() -> read EVERY metadata.json
    ...
    for _, child := range children {
        childNode, err := store.tree(child.SessionID, seen)  // recurses, re-listing the whole store each time
```

**Suggested fix:** Load the store once: call store.List() a single time in Tree, build a map[parentID][]Metadata index, and recurse over that in-memory index instead of calling ListChildren (and Get) per node.

#### [low/bug] cron run records lose the failure reason: exec errors go to the discarded stdout stream
`internal/cli/cron_run.go:150`

fireJob forces the child run to `--output-format stream-json`. In that mode runExec emits error events to STDOUT (writeStreamJSONError / writer.errorEvent write stream-json lines to stdout) and prints nothing to stderr. fireJob captures both streams but records `rec.Error` only from errBuf and discards outBuf entirely, so for the common failure class (provider errors, usage errors surfaced as stream-json) the persisted RunRecord has a non-zero ExitCode but an empty Error, and the run's output is lost. `zero cron` therefore cannot tell the operator why a scheduled job failed.

```
var outBuf, errBuf strings.Builder
code := exec(args, &outBuf, &errBuf)
rec := cron.RunRecord{...}
if code != 0 {
    rec.Error = cronTruncate(strings.TrimSpace(errBuf.String()), 500)
}
// outBuf is never read again; runExec stream-json errors are written to stdout (writeStreamJSONError(stdout, ...))
```

**Suggested fix:** On non-zero exit, fall back to extracting the last stream-json error event (or the tail of outBuf) when errBuf is empty, e.g. rec.Error = firstNonEmpty(trim(errBuf), lastErrorEventMessage(outBuf), tail(outBuf)).

#### [low/bug] promptExcerpt/cronTruncate byte-slice strings mid-rune, emitting invalid UTF-8
`internal/cli/cron.go:275`

promptExcerpt truncates the prompt for `zero cron list` with a raw byte slice `p[:47]`, and cronTruncate (cron_run.go:183) does the same with `s[:500]` for stored run errors. For multibyte prompts (any non-ASCII text — accented words, CJK, emoji) the cut can land mid-rune, printing a replacement-garbage byte sequence in the list output and persisting invalid UTF-8 into runs.jsonl. Same defect class exists in createSessionTitle (internal/cli/exec_sessions.go:88, `title[:80]`), which stores a potentially rune-split session title in metadata.json.

```
func promptExcerpt(p string) string {
    p = strings.TrimSpace(strings.ReplaceAll(p, "\n", " "))
    if len(p) > 48 {
        return p[:47] + "…"
    }
...
func cronTruncate(s string, max int) string { if len(s) <= max { return s } return s[:max] + "…" }
```

**Suggested fix:** Truncate on rune boundaries: convert to []rune (or walk with utf8.DecodeRuneInString) before slicing, e.g. `r := []rune(p); if len(r) > 48 { return string(r[:47]) + "…" }`; apply the same to cronTruncate and createSessionTitle.

#### [low/wiring] exec resume/fork bypasses the injected session store (deps.newSessionStore never used by exec)
`internal/cli/exec_sessions.go:55`

appDeps.newSessionStore is the designated injection point for the session store and is honored by sessions, search, usage, spec and the TUI (tui.Options.SessionStore). But the exec path ignores it twice: preflightExecSession constructs `sessions.NewStore(sessions.StoreOptions{})` directly, and runExec calls sessions.PrepareExec without setting PrepareExecOptions.Store (exec.go:353), so PrepareExec also falls back to `NewStore(StoreOptions{})`. Any test or future configuration that swaps newSessionStore (custom root, fakes) gets inconsistent behavior: `zero sessions list` reads one store while `zero exec --resume` validates and writes against another. Today both default to the same XDG path, so this is latent, but it makes the dep a half-wired option.

```
// preflightExecSession (exec_sessions.go:55)
store := sessions.NewStore(sessions.StoreOptions{})
// runExec (exec.go:353) — no Store field:
preparedSession, err = sessions.PrepareExec(sessions.PrepareExecOptions{ SessionID: options.initSessionID, Title: sessionTitle, ... })
// vs every other command: store := deps.newSessionStore()
```

**Suggested fix:** Pass the injected store through: in runExec use `store := deps.newSessionStore()`, hand it to preflightExecSession (change its signature to accept *sessions.Store) and set `Store: store` in PrepareExecOptions.

#### [low/ux] search --session-id with a nonexistent session silently succeeds with 0 results
`internal/search/search.go:309`

resolveSessions returns `([]sessions.Metadata{}, err)` when store.Get errors OR returns nil; for a well-formed but nonexistent session id, Get returns (nil, nil), so the search proceeds against zero sessions and `zero search --session-id zero_does_not_exist foo` prints `No local session events matched "foo". Searched 0 sessions.` and exits 0. The user's filter target not existing is an error condition (a typo'd id looks identical to "no matches"), and every other session-id-taking command (sessions children/lineage/tree, exec --resume) reports "Zero session not found".

```
func resolveSessions(store *sessions.Store, sessionID string) ([]sessions.Metadata, error) {
    if sessionID == "" {
        return store.List()
    }
    session, err := store.Get(sessionID)
    if err != nil || session == nil {
        return []sessions.Metadata{}, err   // session==nil, err==nil -> empty result, success
    }
```

**Suggested fix:** Return an explicit error when the session is missing: `if session == nil { return nil, fmt.Errorf("zero session not found: %s", sessionID) }` so the CLI surfaces it instead of reporting an empty successful search.

#### [low/ux] doctor always prints apiKey: [REDACTED] whether or not a key is configured
`internal/doctor/doctor.go:134`

providerConfigCheck puts the raw key value under the details key "apiKey"; the check() redaction pass replaces the VALUE with "[REDACTED]" purely because the key NAME is sensitive (redaction.redactReflect: `if IsSensitiveKey(key) { out[key] = replacement }`), regardless of whether the value is empty. Verified live: `zero doctor` prints `apiKey: [REDACTED]` identically with OPENAI_API_KEY set and unset. For a health-check command whose whole purpose is diagnosing provider setup, this is actively misleading — an operator with a missing key sees output implying one is configured. The command-center surface already solved this with a boolean ("api key: set/not set").

```
return check("provider.config", ..., map[string]any{
    "name": profile.Name, "provider": profile.ProviderKind, "baseURL": profile.BaseURL, "model": profile.Model,
    "apiKey": profile.APIKey,
})

Live output with OPENAI_API_KEY="" AND with a key set, both: "apiKey: [REDACTED]"
```

**Suggested fix:** Report presence, not the value: replace the detail with `"apiKeySet": strings.TrimSpace(profile.APIKey) != ""` (key name not in the sensitive list, boolean value), matching providers' apiKeyState() output.


### Config, hooks, plugins, skills

Audited internal/config, internal/hooks, internal/plugins, internal/skills, and internal/zerocommands by reading every source file in full and tracing each public API to its production callers.\n\nMost significant findings:\n- Wiring (high): The entire hooks execution surface (Select, AuditStore append, ConfigStore write methods) has no production caller — hooks are loadable and shown as 'enabled' by `zero hooks list` but never fire on any event, and there is no CLI command to create/edit them.\n- Security (high): `zero mcp list --json` marshals raw config.MCPServerConfig (Env/Headers) through key-name-based redaction, leaking opaque secret values under non-standard keys; the purpose-built zerocommands.MCPServerSnapshot (which strips env/headers to counts) is never used.\n- Wiring (medium): ResolvedConfig.MCP is computed but never read, and MCP servers emitted by a provider command are merged by Resolve() yet dropped by ResolveMCP() (the path the runtime actually registers from).\n- Perf (medium): hooks AuditStore.append re-reads and re-parses the full audit JSONL on every write (O(n^2)).\n\nPlus dead-code/validation/durability items: the bulk of the zerocommands snapshot library (sandbox + hook/plugin/MCP snapshots) is unused, config/contracts.go is unused, skills.Get/Duplicates are unused (duplicate-name collisions never warned), plugin vs standalone hook-event validators disagree, maxTurns<=0 is silently ignored, and the config writer overwrites in place (non-atomic) unlike the hooks writer.\n\nKey files: /Users/kratos/Downloads/zero-main 2/internal/hooks/hooks.go, /Users/kratos/Downloads/zero-main 2/internal/config/resolver.go, /Users/kratos/Downloads/zero-main 2/internal/cli/extensions.go, /Users/kratos/Downloads/zero-main 2/internal/zerocommands/backend_snapshots.go, /Users/kratos/Downloads/zero-main 2/internal/config/writer.go.

#### [high/wiring] Hooks subsystem is loaded and listed but never executed (no event dispatch, no edit command)
`internal/hooks/hooks.go:372`

The hooks package implements a full execution/audit/store-write surface — Select() (event/matcher dispatch), AuditStore.AppendStarted/AppendCompleted, and ConfigStore.Upsert/Remove/SetEnabled — but none of these are ever called from production code. The only non-test consumer of the package is hooks.LoadConfig (wired into `zero hooks list` and the backend snapshots). There is no agent-loop hook runner, and `runHooks` only registers a `list` subcommand. As a result a user can create ~/.config/zero/hooks.json or .zero/hooks.json, see them reported as "enabled" by `zero hooks list`, yet the configured beforeTool/afterTool/sessionStart/etc. commands never fire on any event, and there is no CLI path to add/enable/disable a hook (ConfigStore is unreachable). The feature appears functional but is inert.

```
`func Select(config Config, input SelectInput) []Definition` (hooks.go:372) plus NewAuditStore/NewConfigStore have zero non-test callers (verified by grep across the repo). The CLI wires only `loadHooks: hooks.LoadConfig` (internal/cli/app.go:107) and `runHooks` exposes only `case "list":` (internal/cli/extensions.go:94). No code in internal/agent, internal/tools, or internal/cli ever calls hooks.Select or AuditStore.Append*.
```

**Suggested fix:** Wire hooks.Select into the agent tool-execution loop (beforeTool/afterTool) and session lifecycle, executing selected commands with a bounded timeout and recording AuditStore events; add `zero hooks add/enable/disable/remove` subcommands that call ConfigStore. If execution is intentionally deferred, mark the package and `zero hooks list` output as 'discovery only / not yet executed' (as plugins.go already documents) so the enabled/disabled state is not misleading.

#### [high/security] `zero mcp list --json` leaks MCP server env/header secret values instead of using the redacting MCPServerSnapshot
`internal/cli/extensions.go:186`

The headless `zero mcp list --json` command serializes the raw config.MCPServerConfig map (including Env and Headers, which commonly carry auth tokens for the spawned server) through the generic reflect-based redaction.RedactValue. That redactor only masks a map value when its KEY normalizes to one of a fixed allow-list (api_key, token, auth_token, ...) or when the value matches a known token FORMAT regex (sk-, github_pat_, AIza, JWT, AWS). An opaque secret under a non-standard key — e.g. {"env":{"NOTION_TOKEN":"secret_abc123xyz"}} or {"headers":{"X-Custom-Auth":"abc123def456"}} — is neither key-matched (normalizes to notion_token / x_custom_auth, not in the set) nor format-matched, so it is emitted in clear. The zerocommands.MCPServerSnapshot type was purpose-built to prevent exactly this (its doc states env/headers are summarized as redacted key COUNTS so 'a token in MCP_AUTH_TOKEN never reaches the headless JSON output'), but the CLI never uses it. The doc explicitly targets PR/CI automation, where this output may be shared.

```
internal/cli/extensions.go:186 `Servers map[string]config.MCPServerConfig` then :188 `writePrettyJSON(stdout, redaction.RedactValue(payload, redaction.Options{}))`. config.MCPServerConfig has `Env map[string]string` / `Headers map[string]string` (internal/config/types.go:144-146). redaction sensitiveKeys is a fixed exact-match set (internal/redaction/redaction.go:33) and IsSensitiveKey does exact normalized lookup only (redaction.go:91). The unused safe alternative MCPServerSnapshot records only EnvKeyCount/HeaderCount (internal/zerocommands/backend_snapshots.go:30-31).
```

**Suggested fix:** Build the `zero mcp list --json` payload from zerocommands.MCPServerSnapshots(...) (which strips env/header values to counts and runs URL through stripURLCredentials) instead of marshaling raw config.MCPServerConfig, or omit Env/Headers from the JSON entirely.

#### [medium/wiring] ResolvedConfig.MCP is computed but never read; provider-command MCP servers are silently dropped
`internal/config/resolver.go:95`

Resolve() merges MCP configuration from user, project, the provider command, and overrides into ResolvedConfig.MCP, but no caller ever reads resolved.MCP. The runtime registers MCP tools via deps.resolveMCPConfig → config.ResolveMCP, a separate function that merges only user + project + overrides and never invokes the provider command. Consequently (a) the resolved.MCP field is dead, and (b) any `mcp.servers` returned by a ZERO_PROVIDER_COMMAND are merged by Resolve yet never actually started, because the registration path uses ResolveMCP which omits the provider command. The two resolution functions therefore disagree on the effective MCP server set.

```
resolver.go:90-99 returns ResolvedConfig{... MCP: cfg.MCP ...}; provider-command config is merged at resolver.go:49 via mergeConfig which merges MCP at resolver.go:142. ResolveMCP (resolver.go:102-117) iterates only UserConfigPath/ProjectConfigPath + overrides.MCP and never calls LoadProviderCommand. The runtime uses ResolveMCP via internal/cli/app.go:92-98 (resolveMCPConfig) and internal/cli/mcp_tools.go:22. Grep finds zero reads of `resolved.MCP` / a Resolve-result `.MCP` field anywhere outside the config package.
```

**Suggested fix:** Either drop the MCP field from ResolvedConfig (and its merge in Resolve) to avoid the misleading dead value, or have ResolveMCP run the provider command (and its MCP merge) so the inspected and registered MCP server sets match the agent's resolved config.

#### [medium/perf] AuditStore.append is O(n^2): it re-reads and re-parses the entire audit JSONL on every append
`internal/hooks/hooks.go:503`

Each AppendStarted/AppendCompleted call invokes store.append, which calls ReadEvents() to read the whole audit file, JSON-unmarshal every line, and scan for the max Sequence just to assign the next sequence number, before appending one line. For an append-only, unbounded audit log this makes writing N events O(N^2) total work and O(file size) per write. Because two hook-execution records (started + completed) are written per tool call, a long session would re-parse the entire growing log on every tool invocation once hooks are wired.

```
hooks.go:499-535: `func (store *AuditStore) append(...)` calls `events, err := store.ReadEvents()` (line 503), loops `for _, existing := range events { if existing.Sequence > highest ...}` (508-512) to compute `event.Sequence = highest + 1`, then opens the file O_APPEND and writes a single line. ReadEvents (476-497) reads the whole file and json.Unmarshals every non-blank line.
```

**Suggested fix:** Track the last sequence number in the AuditStore struct (read once at construction) and increment it under the mutex, or read only the final line of the file, instead of reparsing the entire log on every append.

#### [low/dead-code] zerocommands snapshot library is largely dead: sandbox plan/decision/policy + hook/plugin/MCP snapshots have no production consumer
`internal/zerocommands/sandbox_snapshots.go:104`

A substantial part of the zerocommands snapshot surface is never used outside tests: the entire sandbox_snapshots.go (SandboxPolicySnapshot/Risk/Violation/Backend/Plan/Decision builders) and, in backend_snapshots.go, HookSnapshots, PluginSnapshots, MCPServerSnapshots and NewBackendLifecycleSnapshot. The CLI `zero hooks list`/`zero plugins list`/`zero mcp list` build their own ad-hoc payloads from raw types and the CLI `zero sandbox` formats policy fields by hand, while the TUI exposes no hooks/plugins/MCP views at all. The package presents itself as the shared data layer for TUI + headless + CI, but only ConfigSnapshot, ProviderSnapshot, ModelSnapshot, ProviderCatalogSnapshot, SessionSnapshot(s)/Tree, and SandboxGrantSnapshots are actually wired; the redaction-aware snapshots that would prevent leaks (see the MCP finding) are bypassed.

```
Grep for SandboxPlanSnapshot/SandboxDecisionSnapshot/SandboxPolicySnapshot/SandboxBackendSnapshot/SandboxRiskSnapshot/SandboxViolationSnapshot/BackendLifecycleSnapshot/MCPServerSnapshot/PluginSnapshot/HookSnapshot across internal/cli, internal/tui, internal/doctor, cmd (excluding tests) returns no matches. CLI sandbox output is hand-built, e.g. internal/cli/sandbox.go:155 `"max_autonomy: " + string(policy.MaxAutonomy)`.
```

**Suggested fix:** Route the headless hooks/plugins/mcp/sandbox commands (and any TUI backend views) through the corresponding zerocommands snapshot builders so the redaction/shape guarantees are actually enforced, or remove the unused builders to avoid a false sense of a shared, redacting data layer.

#### [low/dead-code] internal/config/contracts.go (ContractGap API) is unused dead code
`internal/config/contracts.go:12`

DefaultContractGaps, FindContractGapsByMilestone, and the ContractGap type are exported and tested but have no production consumer anywhere in the repository. The file enumerates 'contract gaps' (env keys, provider timeout/retry/maxOutputTokens, permissions.mode, sandbox.policy) that nothing reads, so it carries no behavior and risks drifting out of sync with the real config surface.

```
Grep for DefaultContractGaps / FindContractGapsByMilestone / ContractGap across the repo (excluding tests and contracts.go itself) returns 0 references. config/contracts.go:12 `func DefaultContractGaps() []ContractGap`.
```

**Suggested fix:** Remove contracts.go (and its test) or wire the gap list into `zero doctor`/config validation so it is actually surfaced; an unconsumed contract list provides no guarantees.

#### [low/dead-code] skills.Duplicates is never surfaced and skills.Get is unused; duplicate-name collisions are silently dropped
`internal/skills/skills.go:91`

skills.go documents that when two skills declare the same frontmatter name the loser is dropped (first-directory-wins) and that callers should use Duplicates() to warn the user. But neither Duplicates nor Get has any production caller — the skill tool and `zero skills` both call Load/List, which discard the duplicate report. So a shadowed skill is silently dropped with no warning anywhere, and the documented mitigation is dead.

```
skills.go:91 `func Duplicates(dir string) ([]DuplicateName, error)` and skills.go:217 `func Get(...)` have no non-test callers (grep). Production consumers use only skills.Load (internal/tools/skill.go:64) and skills.List (internal/cli/skills.go:56); the load() return value `duplicates` is never propagated to the user.
```

**Suggested fix:** Have `zero skills list` (and/or the skill tool) call Duplicates and print a warning for shadowed skills, or remove Get/Duplicates if the collision behavior is intended to stay silent.

#### [low/bug] Plugin and standalone hook validators disagree on the allowed hook event set
`internal/plugins/plugins.go:893`

plugins.parseHookEvent accepts only beforeTool, afterTool, sessionStart, sessionEnd, whereas hooks.parseEvent additionally accepts specialistStart and specialistStop. A plugin manifest that declares a specialistStart/specialistStop hook (a valid hook event for a standalone hooks.json) is rejected with a misleading 'Expected beforeTool, afterTool, sessionStart, or sessionEnd' error, even though the hooks subsystem defines those events. The two hook-event enumerations have drifted.

```
internal/plugins/plugins.go:893 `case HookBeforeTool, HookAfterTool, HookSessionStart, HookSessionEnd:` and :896 error text omit specialist events; internal/hooks/hooks.go:812 `case EventBeforeTool, EventAfterTool, EventSessionStart, EventSessionEnd, EventSpecialistStart, EventSpecialistStop:` includes them.
```

**Suggested fix:** Add HookSpecialistStart/HookSpecialistStop to the plugins HookEvent constants and parseHookEvent (and the error message) so plugin-declared hooks accept the same event set as standalone hooks.json.

#### [low/bug] config maxTurns <= 0 is silently ignored rather than validated
`internal/config/resolver.go:136`

mergeConfig, mergeProjectConfig, and applyOverrides only apply MaxTurns when the source value is > 0. A user who writes "maxTurns": 0 or a negative value in config (or passes a zero/negative override) gets no error and silently keeps the default of 12, with no feedback that their setting was discarded. This is a 'value accepted but ignored' gap; unlike deferThreshold (which errors on negative) and notify/sandbox (which validate), maxTurns swallows out-of-range input.

```
resolver.go:136-138 `if src.MaxTurns > 0 { dst.MaxTurns = src.MaxTurns }` (also resolver.go:162-164 and applyOverrides resolver.go:484-486). There is no error branch for MaxTurns <= 0 in Resolve, in contrast to the deferThreshold check at resolver.go:57-59.
```

**Suggested fix:** Reject negative maxTurns with an explicit error in Resolve (mirroring the deferThreshold guard) and decide/document the meaning of 0, instead of silently falling back to the default.

#### [low/bug] config writer performs a non-atomic in-place write of the user config
`internal/config/writer.go:53`

writeConfigFile marshals the whole config and calls os.WriteFile, which truncates the destination then writes. A crash or full disk between truncate and the final write leaves the user's config.json truncated/corrupted. The hooks package writer for the analogous file already does the safe thing (write to a temp file then os.Rename), so this is an inconsistent and avoidable durability risk for the primary provider/credentials config.

```
writer.go:48-55 `data, _ := json.MarshalIndent(...); data = append(data, '\n'); os.WriteFile(path, data, 0o600)` — direct overwrite, no temp+rename. Compare hooks.WriteConfig (internal/hooks/hooks.go:256-264) which writes `tempPath` then `os.Rename(tempPath, resolved)`.
```

**Suggested fix:** Write to a sibling temp file (same dir, 0600) and os.Rename onto the target, matching hooks.WriteConfig, so a partial write can never clobber an existing valid config.
