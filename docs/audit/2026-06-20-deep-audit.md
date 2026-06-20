# Zero — Deep Adversarial Code Audit (2026-06-20)

- **Target:** `github.com/Gitlawb/zero` @ `origin/main` commit `bfbdbb1` (a terminal coding agent: Go, Bubble Tea v2 TUI, surface-agnostic core feeding TUI / headless `exec` / MCP server / cron).
- **Toolchain:** Go 1.25.0 (toolchain go1.26.4). 72 packages.
- **Method:** Fresh checkout of `origin/main` (the audit-batch worktree lags main). Recon → build/vet/`-race` gates → 13-subsystem deep read (each reconciling its slice of the prior audit docs) → adversarial re-verification of every finding by independent skeptics (2 for high, 1 otherwise) that re-read guards/locks/callers to try to *disprove* it; only survivors are reported. Findings are grounded in current code with `path:line` + quoted evidence; several were empirically reproduced with throwaway tests.
- **Scope note:** Audit only — no source was modified.

## 1. Executive summary

The tree is in markedly better shape than the 2026-06-10 audit: of ~150 prior findings, **64 are fully fixed and 14 partially fixed** in current `main` (atomic config write, durable session metadata, cross-process cron/hooks/oauth locks, MCP framing + alloc cap, SSRF DNS-rebind pinning, sandbox quote-aware interactive detection, FinishReason consumption, transcript O(n²) + per-frame re-render, UTF-8 rune-safe truncation across the board, secret-pattern anchoring, and more). Build and vet are clean; the race detector found **zero data races** across 67/68 packages.

The residue clusters into four recurring root causes (§5). The single most important new item is **one security finding (M10):** the provider health-probe forwards `x-api-key`/`x-goog-api-key`/custom headers across an HTTP redirect to a public host — a credential-exfil vector the recently-added DNS-rebind/internal-redirect hardening does not cover. The lone high-severity item (**H1**) is a trust/correctness gap: four of six documented, user-configurable hook events are never dispatched. The remaining mediums are durability (events.jsonl not fsync'd; PTY last-chunk drop), cron edge cases (DST double-fire, claim double-grant, `--run-now` cancelled), connection-lifecycle gaps (remote-bridge idle deadline, Shutdown can't close bridge conns, MCP SSE never reconnects), a sandbox over-block false-positive, a stream-json prose-mangling regex, and a TUI streaming O(n²).

### Severity rollup

| Severity | Count |
|---|---|
| Critical | 0 |
| High | 1 |
| Medium | 15 |
| Low | 17 |
| Info | 2 |
| **Total (surviving)** | **35** |

Prior-audit reconciliation: **64 fixed · 14 partial · 12 still-open** (§4). Two drafted findings were dropped in adversarial review (§4.4).

## 2. Build / vet / test results (verbatim)

Run in the fresh `origin/main` checkout, Go-native only (per AGENTS.md), with test isolation `HOME`/`XDG_CONFIG_HOME` pointed at temp dirs and `CI=1`.

```
$ go build ./...
(no output) — exit 0  ✅ clean

$ go vet ./...
(no output) — exit 0  ✅ clean

$ go test ./... -race -count=1
ok   <67 packages>            ✅
--- FAIL: TestIndependentExecCommandConstructorsShareDefaultManager (1.01s)
    exec_command_test.go:43: expected shared manager to find completed session, got ... session_id:"1000" ...
--- FAIL: TestExecCommandReturnsSessionAndWriteStdinPollsCompletion (1.02s)
    exec_command_test.go:82: expected exit_code 0, got ... session_id:"1000" ...
--- FAIL: TestExecCommandReturnsExitCodeWhenCommandCompletesDuringInitialYield (1.01s)
    exec_command_test.go:99: completed command must not return session_id, got ... session_id:"1000" ...
FAIL github.com/Gitlawb/zero/internal/tools   13.664s
exit 1
```

**Race detector: 0 data races reported in any package.** The 3 `internal/tools` failures are **`-race`-only timing flakes, not product defects**: they pass cleanly 3× without `-race` (`go test ./internal/tools -run <them> -count=3` → `ok`), and fail under `-race` only because the test's fixed 10ms yield window is too short when the binary runs ~5–10× slower under race instrumentation — the command is still running when the window elapses, so the tool *correctly* returns a still-running `session_id` instead of an `exit_code`. This is a test-robustness gap (the suite is not `-race`-clean) recorded under Confidence notes (§7); it is the same exec-session timing family that recently churned in PRs #270/#273. (A genuine, separate `-race`-detectable data race in this package — `lastUsedAt` — is reported as **L15**, found by inspection because no test exercises it.)

## 3. Findings by severity

> Each finding was re-verified against current code by independent skeptics; the evidence below is what withstood that challenge. Locations are `path:line` relative to the repo root.

### H1 · Four of six hook events (sessionStart/sessionEnd/specialistStart/specialistStop) are validated and user-configurable but NEVER dispatched
- **Severity / category:** high / dead-inert  · **Subsystem:** hooks
- **Location:** `internal/hooks/dispatch.go:112; internal/agent/loop.go:914,937`
- **Evidence:**
```
KnownEvents() returns all six (hooks.go:888) and IsValidEvent gates `zero hooks add` (hooks_manage.go:297-298) advertising all six in help (hooks_manage.go:315 "beforeTool, afterTool, sessionStart, sessionEnd, specialistStart, specialistStop"). But the only two production call sites of Dispatcher.Dispatch are loop.go:915 (`Event: hooks.EventBeforeTool`) and loop.go:938 (`Event: hooks.EventAfterTool`). grep over internal/+cmd/ for `.Dispatch(` finds exactly those two; no dispatch ever passes EventSessionStart/EventSessionEnd/EventSpecialistStart/EventSpecialistStop (the specialist lifecycle in internal/specialist/accounting.go:38,60 emits sessions.EventSpecialistStart/Stop to the SESSION store, never to hooks.Dispatcher).
```
- **Impact:** Across a whole session a user (or plugin) who configures a sessionStart/sessionEnd/specialistStart/specialistStop hook — e.g. a sessionStart hook that loads secrets/env, or a sessionEnd hook that flushes/cleans up, or a specialist audit hook — gets a green `zero hooks add` and a hook that `zero hooks list` shows as enabled, yet the command is silently never executed for the entire run. This is a correctness/trust gap: lifecycle automation users believe is wired (cleanup, audit, env priming) just does not happen, with no error surfaced. This is the same class as the prior 2026-06-10 high finding 'loaded but never executed' — the beforeTool/afterTool half was fixed; the other four events remai
- **Fix:** Either (a) actually dispatch these events from their lifecycle points: call options.Hooks.Dispatch with EventSessionStart at session/agent-run start, EventSessionEnd at run teardown, and EventSpecialistStart/Stop alongside the sessions events in internal/specialist/accounting.go; or (b) if only beforeTool/afterTool are intended to be runnable, remove the other four from KnownEvents()/IsValidEvent and the CLI help so users cannot configure dead hooks. Option (a) is preferable since the events are already a documented surface.

### M1 · streamjson api_key secret pattern still mangles ordinary prose/output in every stream-json event
- **Severity / category:** medium / robustness  · **Subsystem:** cli-usage-streamjson
- **Location:** `internal/streamjson/streamjson.go:314`
- **Evidence:**
```
regexp.MustCompile(`(?i)(api[_-]?key["'=:\s]+)[^"',\s)]+`)  — the ["'=:\s]+ class lets a plain space follow apikey, so the next word is captured. Verified: redactString("The function apikey: foo is documented here") -> "...[REDACTED] is documented..."; redactString("The user's apiKey value spans...") -> "...apiKey [REDACTED] spans...". The sibling sk-/bearer patterns were anchored, but this one was not.
```
- **Impact:** FormatEvent runs redactValue->redactString over EVERY string field of EVERY emitted stream-json event (text-event Delta = model output, tool_result Output, final Text, warning/error Message). Any time the model's answer, a tool's output, a diff, a log line, or documentation contains the literal token "api_key:"/"apiKey "/"api-key=" followed by a word, a chunk of that non-secret content is silently replaced with [REDACTED] in the machine-readable protocol a downstream consumer parses. Over a session that discusses configs/auth (common for a coding agent) this corrupts arbitrary output with no warning. This is the unfinished half of prior finding M48.
- **Fix:** Require an actual credential shape after the key marker rather than 'any next word': drop bare whitespace as a value delimiter (use `(api[_-]?key)\s*[=:]\s*["']?` for the prefix) and require a credential-length body, e.g. `[A-Za-z0-9._-]{12,}` (mirror the sk-/bearer anchoring already applied). Add a prose regression case ("the api_key: foo setting" must survive) to TestRedactStringDoesNotOverMatchProse.

### M2 · ResolvedConfig.MCP is computed but never read; ZERO_PROVIDER_COMMAND MCP servers are silently dropped
- **Severity / category:** medium / correctness  · **Subsystem:** config-plugins-skills
- **Location:** `internal/config/resolver.go:106`
- **Evidence:**
```
Resolve() merges provider-command output (LoadProviderCommand at resolver.go:54 + mergeConfig at :58, which calls mergeMCPConfig) and sets `MCP: cfg.MCP` (resolver.go:106). But `grep -rn 'resolved.MCP' internal/cli` returns nothing — no caller reads ResolvedConfig.MCP. Runtime MCP registration goes through the separate ResolveMCP (resolver.go:115-133), which only seeds DefaultMCPServers + merges UserConfigPath/ProjectConfigPath/Overrides.MCP and never calls LoadProviderCommand or touches options.ProviderCommand.
```
- **Impact:** A provider command (ZERO_PROVIDER_COMMAND) that emits an mcp.servers block has those servers folded into Resolve()'s result but the runtime registers from ResolveMCP, so provider-command-supplied MCP servers are never registered for the whole session — the user sees no MCP tools from them and no error. ResolvedConfig.MCP is also dead weight.
- **Fix:** Have ResolveMCP also merge LoadProviderCommand(options.ProviderCommand).MCP (between the file merges and the overrides merge), or make the runtime read resolved.MCP from the single Resolve() path. Drop ResolvedConfig.MCP if it stays unread.

### M3 · Project-checked-in slash commands auto-load and expand-to-prompt with no provenance marker or confirmation; Command.Project is dead
- **Severity / category:** medium / robustness  · **Subsystem:** config-plugins-skills
- **Location:** `internal/tui/user_commands.go:29`
- **Evidence:**
```
model.go:473 `usercommands.Load(usercommands.DefaultPaths(cwd, userConfigDir))` auto-loads `<cwd>/.zero/commands/*.md` at TUI startup. handleUserCommand (user_commands.go:29-33) does `prompt := usercommands.Expand(cmd.Template, args)` then `m.launchPrompt(prompt)` — no confirmation, no display of whether the command is project- or user-sourced. The provenance field exists (usercommands.go:120 `Project: project`) but `grep -rn '\.Project' internal/tui internal/usercommands` shows it is only ever WRITTEN, never read. Project commands shadow user commands of the same name (Load merges project after user, byName overwrite).
```
- **Impact:** Opening Zero inside a cloned/untrusted repo silently loads that repo's `.zero/commands/*.md`. A repo can name a command after a common one (`/test`, `/fix`, `/pr`) so the user invoking it expands attacker-authored instructions and submits them to the model verbatim, driving the tool-enabled agent — with no indication the command body came from the repo rather than the user's own config. The Project flag built to distinguish this is never surfaced.
- **Fix:** Surface provenance: in the command list/help and on first invocation of a project-sourced command, show it is repo-sourced (and ideally one-time confirm), using the already-present Command.Project flag; or scope auto-load of project commands behind a trust prompt like other workspace-trust gates.

### M4 · DST fall-back double-fire: the collapse guard is dead on the real scheduler path (sub-minute `fired`)
- **Severity / category:** medium / correctness  · **Subsystem:** cron
- **Location:** `internal/cron/schedule.go:205`
- **Evidence:**
```
Guard: `if sameWallClockMinute(t, after) && after.Truncate(time.Minute).Equal(after) { t = t.Add(time.Minute) } else { return t }`. But fireJob feeds raw wall-clock time: `fired := now()` (cron_run.go:136) then `sched.Next(fired)` (cron_run.go:190), and reconcileOverdue uses `sched.Next(now())` (cron_run.go:122) — none truncate to the minute. With real time.Now() the second clause `after.Truncate(time.Minute).Equal(after)` is false, so the collapse never engages. Reproduced: Next(2026-11-01 01:30:15 EDT) for `30 1 * * *` = 2026-11-01 01:30:00 EST (06:30 UTC) — same day, one absolute hour later.
```
- **Impact:** On the annual US/EU DST fall-back day, a daily job scheduled in the repeated wall-clock hour (e.g. `30 1 * * *` in a DST zone, NextRunAt stored in that location) runs twice that day: after firing at 01:30 EDT the post-exec advance computes NextRunAt=01:30 EST one hour later, and the next tick fires it again (claimFire's `current.NextRunAt.After(fired)` does not block it because the second 01:30 EST instant is not after the stored 01:30 EST). Duplicate scheduled agent run = duplicate model-token spend. The unit tests pass because they pass a minute-aligned `after`; the production caller never does.
- **Fix:** Truncate the fire instant to the minute before computing the next slot, so the guard's precondition holds for the real caller: in fireJob set `fired := now().Truncate(time.Minute)` (or pass a truncated value into sched.Next at cron_run.go:122/190 and claimFire). Alternatively make Next collapse the fall-back repeat whenever the candidate shares wall-clock minute with `after` AND is a later absolute time, independent of after's sub-minute precision, adjusting the strictly-after contract accordingly.

### M5 · claimFire grants the slot to BOTH concurrent schedulers when the schedule cannot advance (invalid/exhausted spec)
- **Severity / category:** medium / concurrency  · **Subsystem:** cron
- **Location:** `internal/cli/cron_run.go:256`
- **Evidence:**
```
claimFire only advances NextRunAt when the expr parses AND Next is non-zero: `if sched, perr := cron.Parse(current.Expr); perr == nil { if nxt := sched.Next(fired); !nxt.IsZero() { current.NextRunAt = nxt } }` but unconditionally leaves `claimed := true` for an active, due job. For an unadvanceable schedule NextRunAt is left unchanged, so a second caller still sees Status==active and `!NextRunAt.After(fired)` and also returns claimed=true. Reproduced by calling claimFire twice with no fire in between: `claim1=true claim2=true`.
```
- **Impact:** Two concurrent schedulers (the documented `cron run --once` under system cron overlapping a forever-mode `cron run`, cron_run.go:61-63) both claim and both run `zero exec` for the same fire of a job whose Expr is impossible/exhausted (e.g. `0 0 30 2 *` stored directly per TestCronRunPausesUnadvanceableJob, a hand-edited metadata.json, or a finite spec that has run out near the 9-year search cap). Both spawn an agent before either pauses the job in its post-exec Mutate. This is the exact double-fire the claim mechanism exists to prevent; the prior concurrent test masks it only because the fake exec returns instantly so the first fire pauses the job before the second claims — a real minutes-lo
- **Fix:** Make claimFire fail the claim for an unadvanceable schedule: if Parse errors or Next(fired) is zero, set `claimed = false` (let a single deterministic caller pick it up) or advance NextRunAt to a sentinel/pause it inside the same locked Mutate so the loser observes the change. The winner's post-exec block already pauses it; the claim just needs to not hand the same un-advanced slot to a second runner.

### M6 · `cron add --run-now` job is silently cancelled by forever-mode startup reconcile instead of firing
- **Severity / category:** medium / correctness  · **Subsystem:** cron
- **Location:** `internal/cli/cron_run.go:118`
- **Evidence:**
```
cronAdd --run-now sets `next = now()` (cron.go:159) and prints `next run <now>`. reconcileOverdue (run at forever-startup when !catchUp, cron_run.go:71) treats any NextRunAt before the current minute as strictly overdue and reschedules without firing: `nowMin := now().Truncate(time.Minute) ... if !j.NextRunAt.Before(nowMin) { continue } ... j.NextRunAt = nxt; store.Update(j)`. The Job struct carries no run-now marker, so the immediate request is indistinguishable from an ordinary backlog. Reproduced: --run-now job added at 08:00, `cron run` started at 08:05 → NextRunAt moved to 09:00, FireCount=0, no output.
```
- **Impact:** A user who runs `zero cron add ... --run-now` and then starts the foreground scheduler more than a minute later never gets the promised immediate run; the job silently waits until its next regular slot. Contradicts both the --run-now contract and the message cronAdd printed. Still open from the prior audit (docs/audit/2026-06-10-deep-audit.md:2208).
- **Fix:** Persist a one-shot intent (e.g. a RunNow bool on Job cleared on first fire, or set NextRunAt to a future-safe sentinel that reconcile fires rather than skips). In reconcileOverdue, exempt jobs explicitly marked run-now (fire them in the first fireDue pass) instead of treating their now() NextRunAt as stale backlog.

### M7 · Remote bridge clears the connection deadline before the daemon handshake, so an authenticated-but-idle peer pins a connection slot forever
- **Severity / category:** medium / resource-perf  · **Subsystem:** daemon
- **Location:** `internal/daemon/remote/bridge.go:223`
- **Evidence:**
```
// Clear the handshake deadline: a session may stream...
_ = conn.SetDeadline(time.Time{})
...
b.server.ServeConn(conn) // performs the daemon handshake + one command
--- server.go:225-231 (reached via ServeConn) ---
func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()
	hello, err := ReadControl(conn) // no deadline
```
- **Impact:** The bridge bounds only the auth handshake (line 181) and then fully clears the conn deadline. After a peer passes auth it is handed to ServeConn -> handleConn, which does ReadControl(conn) for the daemon hello and again for the command with NO read deadline and no per-command timeout. A peer that completes the auth handshake (valid token) but then never sends the daemon hello (or sends hello but never the command) blocks its handler goroutine indefinitely while holding one of the b.sem slots (default MaxConnections=32). 32 such stalled connections exhaust the bridge: every other remote client is refused 'at capacity' (bridge.go:164-168) until process restart. This is an authenticated-client
- **Fix:** Do not fully clear the deadline before ServeConn. Either keep a (larger) per-command deadline for the daemon hello+command exchange in the session path, or set a fresh deadline covering the daemon handshake and clear it only once streaming begins. Simplest: in bridge.go before ServeConn, set conn.SetDeadline(now+handshakeTimeout) for the session path and let streamToClient clear it, OR add a read deadline inside handleConn around the two ReadControl handshake calls.

### M8 · Server.Shutdown cannot close remote bridge connections (ServeConn bypasses trackConn), so a stalled remote handshake-read survives Shutdown
- **Severity / category:** medium / robustness  · **Subsystem:** daemon
- **Location:** `internal/daemon/server.go:222`
- **Evidence:**
```
func (s *Server) ServeConn(conn net.Conn) { s.handleConn(conn) }
--- vs the local accept loop (server.go:136-141) ---
s.trackConn(conn)
s.wg.Add(1)
go func() { defer s.wg.Done(); defer s.untrackConn(conn); s.handleConn(conn) }()
--- Shutdown only closes tracked conns (server.go:181-183) ---
for c := range s.conns { _ = c.Close() }
```
- **Impact:** The D3 fix (Shutdown closes every open connection so a handler blocked on a read returns) only covers connections accepted by the LOCAL Serve loop, which calls trackConn. The remote bridge enters via ServeConn, which calls handleConn directly and never registers the conn in s.conns. The streaming phase is still interruptible (streamToClient selects on <-s.done), but a remote connection blocked in the pre-stream ReadControl handshake (combined with the no-deadline gap above) is NOT closed by Shutdown and is NOT covered by s.wg, so it leaks until process exit and keeps occupying a bridge slot through the entire graceful-drain window. The asymmetry means the carefully-built D3/D4/D5 shutdown gu
- **Fix:** Have ServeConn register the conn for shutdown closure too: call s.trackConn(conn)/defer s.untrackConn(conn) inside ServeConn (or expose a tracked variant the bridge uses), so Shutdown's conns-close loop unblocks a stalled remote handshake read as it does for local connections. Pair with a handshake read deadline (previous finding) for defense in depth.

### M9 · MCP SSE client never reconnects: any stream close permanently kills the session
- **Severity / category:** medium / robustness  · **Subsystem:** mcp
- **Location:** `internal/mcp/network_client.go:517`
- **Evidence:**
```
readStream end: `client.failPending(fmt.Errorf("MCP SSE stream closed for server %s", client.server.Name))`; failPending sets `client.streamErr = err` (line 593); request() gate: `if client.streamErr != nil { ... return err }` (lines 243-246). openStream is invoked exactly once, only from connectRemoteSSE at connect (line 77) — grep finds no other caller and no reconnect loop. registry.go connects each server once at startup and reuses the client for the whole session with no reconnect-on-error.
```
- **Impact:** For an SSE-transport MCP server, the persistent GET stream closing for ANY reason during a session — clean server-side EOF (the normal `MCP SSE stream closed` path), an idle/proxy timeout, a network blip, or an over-8MiB SSE line tripping bufio.ErrTooLong — sets a permanent streamErr. Every subsequent tools/call and tools/list for that server then returns that error for the rest of the process lifetime; the only recovery is restarting Zero (or `zero mcp enable` per tui hint). A long agent session that idles past a server keep-alive window silently loses all of that server's tools.
- **Fix:** On a non-cancelled stream termination, attempt a bounded re-open: clear streamErr/endpointURL, call openStream again with backoff (e.g. mirror agent/reconnect.go's maxStreamReconnects + exponential backoff), and only set a permanent streamErr after retries are exhausted or ctx is done. Re-send `notifications/initialized` after a successful re-open so the server re-establishes session state.

### M10 · providerhealth probe re-sends x-api-key / x-goog-api-key / CustomHeaders to a cross-host redirect target (credential exfil to a public attacker host)
- **Severity / category:** medium / security  · **Subsystem:** oauth-providers
- **Location:** `internal/providerhealth/providerhealth.go:430`
- **Evidence:**
```
CheckRedirect: func(req *http.Request, via []*http.Request) error { if len(via) >= maxConnectivityRedirects {...}; return validateEndpoint(req.Context(), req.URL.String(), resolver) }  — re-validates the redirect HOST against the private/special-use blocklist but never strips auth headers. applyAuth (line 529-561) sets DefaultAuthHeader "x-api-key" (Anthropic), "x-goog-api-key" (Google), and arbitrary profile.CustomHeaders (lines 539/548/558).
```
- **Impact:** Go's stdlib only auto-strips Authorization/Cookie/WWW-Authenticate when a redirect crosses hosts; it does NOT strip x-api-key, x-goog-api-key, or arbitrary custom headers. The DNS-rebinding/internal-redirect half of the prior high finding is now fixed (safeDialContext pins validated IPs; CheckRedirect blocks internal targets), but a configured baseURL that returns a 3xx to an attacker-controlled PUBLIC host (a compromised/MITM'd/typo'd proxy endpoint, or a hostile OpenAI-compatible gateway the user pointed at) passes the blocklist and receives the provider API key verbatim. Production callers (observability.go:58, provider_setup.go:99, setup.go:116) pass no HTTPClient, so newConnectivityClie
- **Fix:** In CheckRedirect, when req.URL.Host != via[0].URL.Host (or any earlier hop's host), delete the auth-bearing headers from req.Header (x-api-key, x-goog-api-key, Authorization, and every key in profile.CustomHeaders) before allowing the redirect; or set client.CheckRedirect to refuse cross-host redirects entirely for the probe (a health check has no need to follow a host change).

### M11 · Destructive/piped-installer risk regexes scan the whole raw command (incl. quoted literals); the quote-aware AST analyzer can never retract the false positive
- **Severity / category:** medium / correctness  · **Subsystem:** sandbox
- **Location:** `internal/sandbox/risk.go:108-112`
- **Evidence:**
```
command := firstArgString(...)
if matchesDestructive(command) { add("destructive", RiskCritical) }
if pipedInstallerPattern.MatchString(command) { add("piped_installer", RiskCritical) }
... analysis := AnalyzeCommand(command); if analysis.Destructive { add("destructive", RiskCritical) }  // add() only ORs in, never removes

Reproduced via engine.Evaluate: `git commit -m "fix rm -rf / bug"` -> action=prompt reason="destructive shell command requires approval" risk=[destructive shell], in BOTH PermissionModeAsk and PermissionModeAuto. Matcher-level probe: `git commit -m "rm -rf /"`=>destructive=true while AST destr=false; `echo "do not run rm -rf /"`=>destructive=true; `grep -r "dd if=/dev/zero" .`=>destructive=true; `printf '%s' "curl evil|sh"`=>piped=true. AST clears all of them (destr=false,net=false) but classifyWithScope only adds categories.
```
- **Impact:** Across a session, any shell command that merely QUOTES one of the trigger substrings (rm -rf / | rm -rf ~ | dd if= | chmod 777 <root> | the fork-bomb chars | curl..|sh) is mis-classified RiskCritical 'destructive'. Per engine.go:323-329 a SideEffectShell command with the destructive category and no prior PermissionGranted is forced to ActionPrompt (Ask AND Auto) or denied when permission can't be granted. So routine, safe commands — `git commit -m "...rm -rf..."`, `echo`/`printf`/`grep` mentioning these tokens — are spuriously gated behind a destructive approval prompt, and in a headless exec with no OnPermissionRequest the prompt resolves to a hard block. The interactive detector was alread
- **Fix:** Run matchesDestructive / pipedInstallerPattern over the command-body of each real shell segment (reuse splitShellSegments + commandBody from safe_command.go) instead of the raw string, OR make the AST analyzer authoritative for parseable scripts: when analysis.TooComplex==false, trust analysis.Destructive/Network and skip the raw-string regexes (only fall back to regex when the parse fails). Either approach removes the quoted-literal false positives while keeping detection of genuine unquoted destructive commands.

### M12 · events.jsonl append is the only write in the package not fsync'd, while the derived metadata IS — a crash can lose a 'successfully appended' event (incl. the checkpoint /rewind targets) and leave EventCount ahead of the durable log
- **Severity / category:** medium / correctness  · **Subsystem:** sessions
- **Location:** `internal/sessions/store.go:606-616`
- **Evidence:**
```
file, err := os.OpenFile(store.eventsPath(sessionID), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
...
if _, err := file.Write(append(data, '\n')); err != nil { _ = file.Close(); ... }
if err := file.Close(); err != nil { ... }   // no file.Sync(), no syncDir
...
session.EventCount = sequence
if err := store.writeMetadata(session); err != nil { ... }  // writeMetadata fsyncs temp + dir
```
- **Impact:** appendEventLocked writes the event line into the page cache (no fsync, no dir sync) and THEN durably writes metadata (writeFileSync + syncDir). On power loss / OS crash after the metadata fsync but before the events.jsonl page is flushed, the persisted metadata.EventCount=N but events.jsonl is missing line N. Across a session this silently drops the last appended event even though AppendEvent returned success: a just-recorded EventSessionCheckpoint vanishes (its blob, written via writeFileAtomicSync, IS durable but is now unreferenced, so /rewind cannot target it and the file content is orphaned), and the user's last turn (message/tool_call/usage) is lost on resume. It also inverts the seque
- **Fix:** Make the append durable like every other write: after file.Write, call file.Sync() before Close (and ideally syncDir(sessionPath) on first create of events.jsonl). Cheapest: replace the open/append with an fsync'd append helper, e.g. f.Write(...); if err := f.Sync(); err != nil {...}; f.Close(). This restores the ordering guarantee that the log is at least as durable as the metadata derived from it.

### M13 · Mailbox stale-lock break compares the lock file to itself, not to the snapshot observed as stale — can delete a healthy fresh lock (split-brain writers)
- **Severity / category:** medium / concurrency  · **Subsystem:** specialist-swarm
- **Location:** `internal/swarm/mailbox.go:371`
- **Evidence:**
```
if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > lockStaleAfter {
	stale, _ := os.ReadFile(lockPath)
	if data, rerr := os.ReadFile(lockPath); rerr == nil && string(data) == string(stale) {
		os.Remove(lockPath)
	}
	continue
}
```
- **Impact:** The comment claims the break is 'conditional on the file's content being unchanged since we observed it,' but `stale` and `data` are two consecutive reads of the SAME file microseconds apart, so the equality check is essentially always true and provides no protection. If a holder crashes and leaves a stale lock, two (or more) waiters can each observe it stale and each call os.Remove(); meanwhile a third writer may legitimately re-create the lock via O_EXCL between the two removes. The second waiter's Remove then deletes that fresh, validly-held lock, after which both that holder and the waiter (on its next O_EXCL create) hold the lock simultaneously — two concurrent writers to the same per-a
- **Fix:** Capture the lock content at Stat time and only remove if it is byte-identical AND the ModTime is unchanged at remove time; better, make the break ownership-aware like release(): read the token, then remove via an atomic rename-then-unlink or re-stat-and-verify-mtime guard. Simplest robust fix: snapshot (content, modTime) when first seen stale, sleep a short grace, re-stat, and only Remove if both still match — so a freshly rotated lock (new mtime/content) is never deleted.

### M14 · PTY exec session can drop its final output chunk on exit (copy goroutine unsynchronized with markDone), then the session is removed
- **Severity / category:** medium / concurrency  · **Subsystem:** tools
- **Location:** `internal/tools/exec_command.go:515-526 (with exec_pty_linux.go:34-37)`
- **Evidence:**
```
startSession's Wait goroutine: `err := command.Wait(); ... if session.cleanup != nil { session.cleanup() }; plan.Cleanup(); cancel(); session.markDone(...)`. For a TTY session the only thing copying PTY output into the buffer is the fire-and-forget goroutine in exec_pty_linux.go:34 `go func() { _, _ = io.Copy(output, master) }()`. command.Stdout/Stderr are the *os.File slave (exec starts NO copy goroutine and WaitDelay does not cover our own goroutine), so command.Wait() returns as soon as the process exits — before io.Copy has necessarily drained the master FD. markDone then closes `done`. collect (line 541-545) does a final drainString after doneClosed, but if the copy goroutine has not yet written the tail, that drain misses it; run()/RunWithOptions then call `tool.manager.remove(session.id)` (lines 465, 676) so the session is gone and no later poll can recover the lost tail.
```
- **Impact:** Across a session, the last lines a TTY (tty:true) command prints right before it exits can be silently dropped from the tool result the model sees — e.g. a test runner's final PASS/FAIL summary or a build's last error line — and because the exited session is immediately removed, the model cannot poll for it. Pipe-mode (tty:false) is unaffected because exec's own Wait drains its os.Pipe. Linux-only (other platforms fall back to pipe via exec_pty_fallback.go).
- **Fix:** Make the PTY copy goroutine observable: have startPTYProcess return a `copied <-chan struct{}` (closed when io.Copy returns), and in the Wait goroutine block on it (with a bounded timeout matching bashWaitDelay) AFTER cleanup() closes master and BEFORE markDone, so all buffered PTY output is in the buffer before `done` is closed and the session is eligible for removal.

### M15 · Streaming live block re-parses the entire growing answer markdown on every spinner/fade tick (cumulative O(N^2) per turn)
- **Severity / category:** medium / resource-perf  · **Subsystem:** tui-render
- **Location:** `internal/tui/model.go:2079`
- **Evidence:**
```
interimBlock: `lines := renderAssistantMarkdownText(text, assistantMeasure(width), width)` where `text := strings.TrimRight(m.streamingText, "\n")`. streamingText only grows (model.go:1185 `m.streamingText += msg.delta`) and is cleared solely at turn end (1411/1522/3208). interimBlock is called from the per-frame body item closure (transcript_selection.go:245 `lines: viewLines(m.interimBlock(width))`). The spinner self-reschedules while pending (model.go:1212-1238, gated on m.pending) and the fade ticks every 150ms (streaming_fade.go:79), so the full markdown parse+wrap runs ~12x/sec.
```
- **Impact:** Settled transcript rows are memoized via defaultRenderCache (rendering.go:190-197), but the streaming block is NOT cached. For a long single answer (e.g. a model emitting a multi-hundred/thousand-line file or table as prose), each ~80-150ms tick re-runs renderAssistantMarkdownText over the whole accumulated buffer — fence scanning, table parsing, per-line inline wrap with lipgloss.Width. Cost per frame is O(current length); over the turn the work is O(N^2/delta). On a slow/large stream the TUI visibly chews CPU and the frame rate drops the longer the answer gets, exactly when responsiveness matters most.
- **Fix:** Memoize the rendered streaming lines keyed by (streamingText length or a content hash, width); only re-render when streamingText or width actually changed since the last frame. The fade is applied per-line AFTER the markdown render (styleStreamingLine, model.go:2087), so the expensive markdown step can be cached while the cheap per-line color pass still runs each tick. Alternatively bound the markdown render to the visible tail (last height lines) since the viewport clips anyway.

### L1 · Reactive-compaction retry and max-turns final-answer call bypass streamWithReconnect (no transient-disconnect retry)
- **Severity / category:** low / resource-perf  · **Subsystem:** agent-loop
- **Location:** `internal/agent/loop.go:205,253,518`
- **Evidence:**
```
stream, err = provider.StreamCompletion(ctx, request) // reactive connect-failure retry
retryStream, retryStreamErr := provider.StreamCompletion(ctx, retryRequest) // reactive mid-stream retry
stream, err := provider.StreamCompletion(ctx, ...) // finalAnswerAfterMaxTurns
```
- **Impact:** The main turn connect goes through streamWithReconnect (2 retries with backoff on EOF/reset/502/503/timeout), but the post-compaction retry calls and the max-turns final-answer call call provider.StreamCompletion directly. A single transient upstream hiccup on exactly those calls (e.g. the final summary after a long autonomous/cron run, or the retried turn right after a context-limit compaction) fails the whole run with no reconnect, re-burning the entire session's tokens — the precise failure mode streamWithReconnect was added to prevent.
- **Fix:** Route these three calls through streamWithReconnect(ctx, provider, request, reconnectNoticeFor(options)) as well; they are pre-content connects so no OnText duplication risk, matching the existing reconnect contract.

### L2 · OnContext / MeasureContext / estimateToolTokens context-utilization pipeline has no production consumer
- **Severity / category:** low / dead-inert  · **Subsystem:** agent-loop
- **Location:** `internal/agent/loop.go:164-166`
- **Evidence:**
```
if options.OnContext != nil {
	options.OnContext(MeasureContext(messages, request.Tools, options.ContextWindow))
}
```
- **Impact:** Repo-wide grep shows zero `OnContext:` assignments outside agent tests (cli/tui/exec never set it), so MeasureContext and its private estimateToolTokens (a near-duplicate of compaction.go's estimateToolDefTokens) are computed for nobody in production and ship as permanently inert plumbing. Carries a maintenance hazard (two divergent tool-token estimators) and a tiny per-turn branch. Matches the prior low finding — still open.
- **Fix:** Either wire OnContext from the TUI/exec to drive a context-utilization bar, or delete OnContext + MeasureContext + ContextBreakdown + estimateToolTokens and keep the single estimateToolDefTokens used by compaction.

### L3 · zero --skip-permissions-unsafe still silently drops trailing positional args (e.g. a prompt)
- **Severity / category:** low / robustness  · **Subsystem:** cli-usage-streamjson
- **Location:** `internal/cli/app.go:234`
- **Evidence:**
```
moreDirs, rest, err := splitLeadingAddDirFlags(args[1:]) ... for _, arg := range rest { if arg == "--add-dir"... { return error } } ... return runInteractiveTUI(stderr, deps, agent.PermissionModeUnsafe, append(append([]string{}, addDirs...), moreDirs...)). `rest` is iterated only to reject a misplaced --add-dir; every other trailing arg is discarded and the interactive TUI (which takes no positional prompt) is launched.
```
- **Impact:** `zero --skip-permissions-unsafe "fix the bug"` silently discards the prompt and drops the user into the interactive TUI with no error or warning, so a scripted/one-shot unsafe invocation appears to hang/ignore input. Documented in-code as intentional ('were ignored on this path... and still are'), but it is still a silent UX drop and unchanged since the prior audit (prior M81 / app.go:149).
- **Fix:** If `rest` contains any non-flag tokens, fail loudly ("--skip-permissions-unsafe launches the interactive TUI and takes no prompt; use `zero --skip-permissions-unsafe exec -p ...` or pipe input") instead of discarding them, matching the loud-rejection style already used for misplaced --add-dir two lines above.

### L4 · usercommands frontmatter model:/agent:/mode: are parsed and documented but never applied
- **Severity / category:** low / dead-inert  · **Subsystem:** config-plugins-skills
- **Location:** `internal/usercommands/usercommands.go:125`
- **Evidence:**
```
parseCommand fills cmd.Model (usercommands.go:125) and cmd.Agent (usercommands.go:126-129 from `agent:`/`mode:`), and the package doc (lines 8-9) advertises `model:` as a real override. But the sole invocation path, handleUserCommand (tui/user_commands.go:29-33), calls launchPrompt(prompt) — and `launchPrompt(prompt string)` (model.go:3020) takes only the prompt; cmd.Model/cmd.Agent are never read by any caller (`grep` for the fields outside the struct/parse shows only the parse assignment).
```
- **Impact:** A user who writes `model: <x>` or `agent: <x>` in a `.zero/commands/foo.md` frontmatter, per the documented feature, gets it silently ignored — the command always runs on the current model/agent. The fields are inert surface that mislead users into thinking per-command model routing works.
- **Fix:** Either honor cmd.Model/cmd.Agent in handleUserCommand (route the launched prompt through the model/agent switch before launchPrompt) or remove the fields and the package-doc `model:` line.

### L5 · Runtime skill tool discards duplicate-name collisions (no user-facing warning)
- **Severity / category:** low / dead-inert  · **Subsystem:** config-plugins-skills
- **Location:** `internal/plugins/activate.go:376`
- **Evidence:**
```
skillTool.Run does `merged, _ := MergedSkillsLoaded(tool.defaultDir, tool.pluginRoots)` (activate.go:376), discarding the DuplicateName slice that mergeSkills computes (activate.go:311). The CLI `zero skills list` path now DOES surface dups (cli/skills.go:69-81), but the agent-facing skill tool silently drops a shadowed same-named skill.
```
- **Impact:** When a plugin skill and a user skill (or two plugin roots) share a frontmatter name, the loser is silently dropped at agent runtime and the model/user is never told which skill body it actually got — a shadowed skill disappears with no diagnostic during a session.
- **Fix:** When the skill tool resolves a name that had a collision, append a one-line note to the tool Result (or emit a startup warning) listing the shadowed Loser path, reusing the already-returned DuplicateName slice.

### L6 · pause/resume do an unlocked Get then a locked Update (non-atomic read-modify-write)
- **Severity / category:** low / concurrency  · **Subsystem:** cron
- **Location:** `internal/cli/cron.go:215`
- **Evidence:**
```
cronSetStatus: `job, err := store.Get(args[0])` (Get takes no lock, store.go:150) ... `job.Status = status; store.Update(job)` (Update takes the per-job lock, store.go:170-183). cronResume (cron.go:234-253) is the same Get-then-Update shape, overwriting the whole struct including FireCount/NextRunAt from the pre-read snapshot.
```
- **Impact:** If a fireJob post-exec Mutate lands between resume's unlocked Get and resume's locked Update, the fire's FireCount++ and advanced NextRunAt are clobbered by resume's stale snapshot (resume recomputes NextRunAt from now() so scheduling stays sane, but FireCount silently regresses). The pause race is benign because fireJob re-reads Status under its lock and honors an external pause; resume has no such re-read. Low frequency (user-driven), cosmetic FireCount loss, no double-fire.
- **Fix:** Route pause/resume through store.Mutate so the read and the field update happen under the same per-job lock: read current, set Status (and for resume recompute NextRunAt from current.Expr), preserving current.FireCount, and write — instead of Get+Update on a snapshot.

### L7 · Single-instance lock refuses startup on PID reuse (bare-PID lock file, no identity/start-time)
- **Severity / category:** low / robustness  · **Subsystem:** daemon
- **Location:** `internal/daemon/lock.go:58`
- **Evidence:**
```
pid, perr := readPidFile(path)
if perr == nil && pid > 0 && isAlive(pid) {
	return nil, fmt.Errorf("%w (pid %d)", ErrAlreadyRunning, pid)
}
```
- **Impact:** The lock file stores only a bare PID. If a daemon dies uncleanly and the OS later recycles its exact PID for an unrelated live process, acquireLock sees isAlive(pid)==true and refuses to start with ErrAlreadyRunning, even though no zero daemon is running. The reclaim path (reclaimStaleLock, D6) is sound for the genuinely-dead case and the rename-aside race is well handled, but it never triggers here because the recycled PID reads as alive. Recovery requires manual lock removal. Low probability (exact PID reuse) and self-inflicted only after an unclean crash, but it is a real availability edge the bare-PID format cannot disambiguate.
- **Fix:** Record more than the PID in the lock file (e.g. PID + process start time, or a daemon-specific marker / boot id) and treat a live PID as the holder only when the recorded identity matches. On mismatch, treat as stale and reclaim via the existing rename-aside path.

### L8 · Plugin hook event set disagrees with hooks package: plugins reject specialist events and cannot register them
- **Severity / category:** low / robustness  · **Subsystem:** hooks
- **Location:** `internal/plugins/plugins.go:38-42,893-899; internal/plugins/activate.go:412-423`
- **Evidence:**
```
hooks.go parseEvent (hooks.go:901-911) accepts six events. plugins.go HookEvent consts (lines 38-42) declare only HookBeforeTool/HookAfterTool/HookSessionStart/HookSessionEnd, and parseHookEvent (plugins.go:893-898) switches on exactly those four, rejecting specialist* with "Expected beforeTool, afterTool, sessionStart, or sessionEnd." mapHookEvent (activate.go:412-423) likewise maps only those four and returns ok=false otherwise.
```
- **Impact:** A plugin manifest declaring a specialistStart/specialistStop hook is rejected as a schema error even though hooks.json (user/project) accepts the same value. The two config surfaces for the identical Dispatcher have divergent valid-event sets, so plugin authors hit an inconsistent contract. Low because (per the finding above) those events do not actually fire from any surface today, so the practical effect is a confusing validation error rather than lost execution. This is the still-open prior reverification low (2026-06-13 line 97).
- **Fix:** Once the dead events are resolved (finding above), make the two sets agree: drive plugin validation/mapping off hooks.KnownEvents()/IsValidEvent (or add HookSpecialistStart/Stop consts + cases) so a single source of truth defines which events are valid, eliminating the drift.

### L9 · Stdio request write blocks under client.mu with no ctx awareness
- **Severity / category:** low / robustness  · **Subsystem:** mcp
- **Location:** `internal/mcp/client.go:282`
- **Evidence:**
```
request() locks client.mu at line 264 and, still holding it, calls `client.writer.write(rpcMessage{...})` at line 282 before unlocking at 291. writer.write (protocol.go:148-163) does a blocking bufio Write+Flush to the child's stdin pipe with no context. Only the response wait (select at 293) is ctx-aware.
```
- **Impact:** If a misbehaving child stops draining its stdin, the OS pipe buffer (~64 KiB) fills and write() blocks while holding client.mu, serializing and stalling every other in-flight/queued request on that server with no ctx-cancel escape. Mitigated because Close() uses a separate closeMu (not mu) and closes stdin, which unblocks the stuck write — so it is recoverable rather than a hard deadlock; impact is limited to a pathological non-reading child.
- **Fix:** Keep id allocation + pending registration under mu but perform the write outside mu (or write under a dedicated writeMu), and bound it: run the write in a goroutine and select on ctx.Done() so a stalled pipe surfaces ctx.Err() to the caller instead of blocking peers.

### L10 · Child stderr capture keeps the head, dropping the likely-relevant tail on crash
- **Severity / category:** low / robustness  · **Subsystem:** mcp
- **Location:** `internal/mcp/client.go:106`
- **Evidence:**
```
boundedBuffer.Write: `if remaining := b.cap - b.buf.Len(); remaining > 0 { ... b.buf.Write(p[:remaining]) ... }` — once cap (64 KiB) is reached, all further bytes are discarded; only the earliest bytes are retained. connectStdio reads stderr only on initialize failure (lines 150-154).
```
- **Impact:** Diagnostics quality: a server that prints a verbose startup banner (>64 KiB) and then dies with an error message at the end shows only the banner in the wrapped initialize error; the actual fatal line is dropped. Not a correctness/security issue, but it can make MCP startup failures hard to diagnose.
- **Fix:** Prefer a ring buffer that retains the LAST cap bytes (or head+tail split) so the terminal error is preserved; the bound is unchanged.

### L11 · ChatGPT account-id silently omitted (no warning) when the id_token claim exists but is the wrong shape
- **Severity / category:** low / robustness  · **Subsystem:** oauth-providers
- **Location:** `internal/provideroauth/chatgpt.go:227`
- **Evidence:**
```
if ns, ok := claims[chatgptAuthClaimNamespace].(map[string]any); ok { if value, ok := ns[chatgptAccountClaim].(string); ok { return strings.TrimSpace(value), nil } }  ... return "", nil  — a present-but-non-string nested claim (or a present namespace with a missing key) falls through to `return "", nil`, the same as 'no id_token'. The caller (line 154-162) only warns on claimErr!=nil and only sets Account when account!="".
```
- **Impact:** If OpenAI ever changes the claim's JSON shape (e.g. nests it one level deeper, or makes it a number), extraction returns ("", nil) with no error: token.Account stays empty, the chatgpt-account-id header is omitted on every Codex call (codex.go:172 sets it only when ok&&account!=""), and the user gets Cloudflare 401s on every request with zero diagnostic — neither the login warning nor a parse error fires. The current 401 is then indistinguishable from an expired token.
- **Fix:** Distinguish 'claim absent' from 'claim present but unusable': when claims[ns] exists but yields no non-empty string account id, return a non-nil error (e.g. 'account-id claim present but not a string') so the existing warning path at chatgpt.go:159 fires and the user is told to re-auth.

### L12 · Latest()/List() order by second-granularity UpdatedAt with a SessionID lexical tiebreak; same-second updates with user-supplied IDs can resume the wrong session
- **Severity / category:** low / correctness  · **Subsystem:** sessions
- **Location:** `internal/sessions/store.go:332-337,708-710`
- **Evidence:**
```
func (store *Store) timestamp() string { return store.now().UTC().Format(time.RFC3339) }  // second precision
...
if sessions[left].UpdatedAt == sessions[right].UpdatedAt { return sessions[left].SessionID < sessions[right].SessionID }
return sessions[left].UpdatedAt > sessions[right].UpdatedAt
```
- **Impact:** UpdatedAt is RFC3339 (1s resolution). When two sessions are touched in the same wall-clock second (rapid back-to-back exec runs, or cron firing several jobs), the tiebreak is lexical SessionID, not real recency. exec_sessions.go:76 store.Latest() (used to resume the most recent session) and session.go:170 LatestResumable() can therefore land on the wrong session. For store-generated IDs the ID embeds UnixNano so lexical order roughly tracks recency, but for caller-supplied SessionIDs (Create/Fork/Child accept an explicit SessionID) the order is arbitrary, so `--continue`-style resume can attach to a stale session. Low: narrow timing window and mostly mitigated for auto-generated IDs.
- **Fix:** Persist a higher-resolution UpdatedAt (RFC3339Nano) so same-second ties are broken by true time, and keep SessionID only as a final deterministic tiebreak; or carry a monotonically increasing per-store update counter into the sort key.

### L13 · Scheduler leaks a per-job context when a bounded job completes via MaxRuns
- **Severity / category:** low / resource-perf  · **Subsystem:** specialist-swarm
- **Location:** `internal/swarm/scheduler.go:280`
- **Evidence:**
```
runs := job.incRuns()
if max := job.schedule.MaxRuns; max > 0 && runs >= max {
	return
}
```
- **Impact:** Each scheduled job gets its own ctx, cancel := context.WithCancel(s.ctx) (Add, line 182). On the MaxRuns-completion path run() returns without ever calling job.cancel(); only Cancel() and Close() invoke it. The derived context (and the goroutine the runtime spawns to propagate parent cancellation) is retained until the whole scheduler/Swarm context is cancelled at session end. A long session that adds many bounded daily/interval jobs accumulates one leaked context per completed job. Bounded by session lifetime and small, but it is a real, avoidable leak the codebase elsewhere is careful about.
- **Fix:** Add `defer job.cancel()` at the top of run() (alongside the existing `defer s.wg.Done()` / `defer s.forget(job.id)`), so the job's context is released on every exit path including MaxRuns completion.

### L14 · terminateProcess returns success without killing surviving group members when the leader PID was already reaped
- **Severity / category:** low / robustness  · **Subsystem:** specialist-swarm
- **Location:** `internal/background/process_posix.go:55`
- **Evidence:**
```
target := pid
if pgid, err := syscall.Getpgid(pid); err == nil {
	if pgid == pid {
		target = -pid
	}
} else if processGoneError(err) {
	return nil // already gone
}
```
- **Impact:** launchBackgroundProcess (specialist/exec.go:797) runs a command.Wait() goroutine that reaps the background specialist leader as soon as it exits. If the leader exits on its own but had forked a detached grandchild into the same process group, a later TaskStop calls terminateProcess(pid): Getpgid(reaped pid) returns ESRCH, the code treats it as 'already gone' and returns nil — never signalling the surviving group. TaskStop reports success while grandchildren (e.g. a dev server the sub-agent started and the parent zero exec exited without reaping) keep running. The full-tree kill the M6 fix added only works while the leader is still alive to identify the group.
- **Fix:** Persist the child's pgid at launch (it equals the pid under Setpgid) and, when the leader is gone, fall back to syscall.Kill(-pgid, ...) to sweep any survivors instead of returning nil immediately on a reaped leader.

### L15 · Data race on execSession.lastUsedAt: prune reads it under manager.mu while touch writes it under session.mu
- **Severity / category:** low / concurrency  · **Subsystem:** tools
- **Location:** `internal/tools/exec_command.go:123 vs :278`
- **Evidence:**
```
sessionToPruneLocked (called from store under manager.mu) sorts with `sessions[i].lastUsedAt.Before(sessions[j].lastUsedAt)` (line 123), reading lastUsedAt with only manager.mu held. touch() writes it under a DIFFERENT lock: `session.mu.Lock(); session.lastUsedAt = time.Now(); session.mu.Unlock()` (lines 277-279). snapshot() correctly reads it under session.mu (line 291), but the prune path does not. The two paths use disjoint mutexes, so there is no happens-before between the write and the read.
```
- **Impact:** When a new exec_command at the session cap triggers eviction (store -> sessionToPruneLocked) concurrently with a write_stdin poll calling touch() on another live session (or with removeCompletedLater/stopAll churn), this is a genuine data race on a time.Time (a multi-word struct), which `go -race` would flag and which can yield a torn/garbage timestamp that mis-selects the prune victim. Unexercised by current tests (no test drives concurrent touch+prune), so it is latent rather than observed.
- **Fix:** Read lastUsedAt through the session lock in the prune comparator (snapshot a `[]struct{id int; last time.Time}` by calling a small locked getter per session before sorting), or store lastUsedAt as an atomic.Int64 of UnixNano accessed the same way from both touch and prune.

### L16 · truncateRunes/middleTruncate truncate by rune COUNT but are used with display-WIDTH budgets — wide/CJK content overshoots its budget
- **Severity / category:** low / correctness  · **Subsystem:** tui-render
- **Location:** `internal/tui/view.go:918`
- **Evidence:**
```
truncateRunes: `runes := []rune(text); if len(runes) <= limit { return text }; ... return string(runes[:limit-1]) + "…"` — purely rune-count based. middleTruncate (startup.go:138-153) likewise slices `runes[:front]`/`runes[len-back:]`. Both are called with display-width budgets: diffCardBody `truncateRunes(strings.TrimPrefix(line, "+"), textBudget)` (rendering.go:1511), then diffBodyLine pads `pad := textBudget - lipgloss.Width(text)` (rendering.go:1536); toolCardHead `truncateRunes(arg, maxInt(12, width/3))` (1336) and `middleTruncate(shown, maxInt(16, width/2))` (1328); grep/read/bash card budgets (1594/1628/1711).
```
- **Impact:** For a diff/grep/tool line of double-width (CJK/emoji) characters, truncateRunes keeps up to `limit` runes each ~2 cells wide, so the kept text can be ~2x its display budget. In diffBodyLine the band pad then computes negative and is skipped, so the add/del tint band no longer fills to the card edge and rows of CJK code render with uneven/short solid bands. Overflow past the card width is contained because toolCard re-clamps every body line with the width-aware fitStyledLine (rendering.go:1387) and the head with fitStyledLine (1380); the residual impact is cosmetic misalignment of the diff/tool band for wide-char content, not layout breakage.
- **Fix:** Add a display-width-aware truncate (loop accumulating lipgloss.Width per rune until budget, reserving 1 cell for the ellipsis) and use it where the argument is a width budget; or route these unstyled budgets through the existing width-aware fitStyledLine/truncateStyledLine primitives instead of rune-count truncateRunes.

### L17 · Cancelled run's usage events/rows are applied to whatever session is active when the goroutine returns, even after /resume switched sessions
- **Severity / category:** low / correctness  · **Subsystem:** tui-render
- **Location:** `internal/tui/model.go:1319`
- **Evidence:**
```
In the agentResponseMsg `msg.runID != m.activeRunID` (flushing) branch, the session-EVENT flush is correctly routed to the recorded session (`if flushSessionID == m.activeSession.SessionID { appendSessionEvents } else { appendSessionEventsTo(flushSessionID, ...) }`, model.go:1335-1338). But the usage events just above are applied unconditionally to the current model: `for _, event := range msg.usageEvents { m, usageRows = m.recordUsageEvent(...); for _, row := range usageRows { m.transcript = appendTranscriptRow(m.transcript, row) } }` (1319-1325) — no flushSessionID gating.
```
- **Impact:** If the user cancels an in-flight run and then /resume's a DIFFERENT session before the cancelled run's goroutine returns its accumulated response, the cancelled run's usage tracker delta and any usage transcript rows land in the now-active different session's in-memory transcript/usage readout. Narrow timing window, in-memory display only (the durable session-event flush is already correctly routed), so impact is a transient mis-attributed usage line, not persisted corruption.
- **Fix:** Only record usage events and append usage rows when `flushSessionID == m.activeSession.SessionID`; when the run was recording into another session, persist its usage to that session's tracker (or drop the display rows) rather than mutating the active model.

### I1 · Local control socket handleConn has no read deadline (owner-only mitigates)
- **Severity / category:** info / robustness  · **Subsystem:** daemon
- **Location:** `internal/daemon/server.go:228`
- **Evidence:**
```
hello, err := ReadControl(conn)
if err != nil { return }
...
cmd, err := ReadControl(conn) // both reads unbounded
```
- **Impact:** The locally-accepted control connection performs the hello + command reads with no deadline. Unlike the remote bridge this is gated by an owner-only Unix socket (secureSocketParent/hardenSocketFile), so the only peer able to stall it is the socket owner — the same principal that controls the daemon — making it a non-threat in practice. Noted because the same unbounded-read code is shared with the remote path (where it IS exploitable, see the medium findings); a defensive deadline here would make handleConn safe regardless of how the conn was obtained.
- **Fix:** Optionally add a modest read deadline around the two handshake ReadControl calls in handleConn so the protection is intrinsic to the function rather than relying on the caller (local socket perms / remote bridge handshake bound).

### I2 · Audit append failures are silently swallowed by the dispatcher
- **Severity / category:** info / robustness  · **Subsystem:** hooks
- **Location:** `internal/hooks/dispatch.go:223-249`
- **Evidence:**
```
recordStarted (line 227 `_, _ = dispatcher.audit.AppendStarted(...)`) and recordCompleted (line 240 `_, _ = dispatcher.audit.AppendCompleted(...)`) discard the error. AppendStarted/AppendCompleted can fail on lock-acquire timeout (lock.go:86 "timed out acquiring audit lock" after 10s) or any I/O/marshal error.
```
- **Impact:** Over a session, if the cross-process audit lock is contended/stranded (a crashed holder's lock not yet 60s stale, or a permissions issue under XDG_DATA_HOME), every hook still runs and the tool proceeds normally, but the audit JSONL silently gains no record — there is no log line, metric, or diagnostic that the security/compliance audit trail dropped events. For a feature whose stated purpose is an auditable record of policy-hook executions, silent gaps undermine the trail. Informational because the dispatcher fails OPEN by design and the audit store is optional.
- **Fix:** Surface a one-time/rate-limited warning (or a diagnostic on DispatchOutcome) when an audit append errors, so an operator can tell the audit trail is incomplete rather than assuming the absence of a record means the hook did not run.

## 4. Reconciliation with prior audit docs

Reconciled against `docs/audit/2026-06-10-deep-audit.md`, `2026-06-10-deep-audit-status.md`, and `2026-06-13-reverification.md`. Each prior item for an audited area was re-checked against current code. Summary: **64 fixed, 14 partial, 12 still-open** (deduplicated across overlapping subsystem reports). The still-open and partial items are the ones that matter for remediation; the 12 still-open are all re-derived above as current findings (or noted as out-of-scope dead code).

#### Still open (12)

| Subsystem | Prior finding | Location | Evidence |
|---|---|---|---|
| agent-loop | Dead stores to requestEvent after always-allow, and unreachable fallbackPermissionEvent | `internal/agent/loop.go:473` | In the AlwaysAllow case requestEvent.GrantMatched/Grant are still set at loop.go:776-778 but the model-visible event is rebuilt fresh from the sandbox decision at 830-840, and fallbackPermissionEvent (loop.go:1694) is st |
| agent-loop | Reactive mid-stream retry never forwards retried assistant text to OnText | `internal/agent/loop.go:183` | The reactive retry still collects with only OnUsage (loop.go:264-266), deliberately omitting OnText to avoid duplicating the already-forwarded partial; documented at 258-263. Behavior unchanged (intentional trade-off). |
| agent-loop | OnContext / MeasureContext context-utilization pipeline has no consumer on any surface | `internal/agent/types.go:183` | Grep finds no `OnContext:` assignment outside agent tests; MeasureContext is only called at loop.go:165 behind the never-set OnContext guard. Reported again above as a current low finding. |
| cli-usage-streamjson | zero --skip-permissions-unsafe silently discards all trailing arguments (M81) | `internal/cli/app.go:149` | app.go:234-243 — `rest` is only scanned for misplaced --add-dir; all other trailing args are dropped and the TUI is launched. Confirmed still-open at 2026-06-13 reverification and unchanged in current code. |
| config-plugins-skills | ResolvedConfig.MCP is computed but never read; provider-command MCP servers are silently dropped | `internal/config/resolver.go:95` | resolver.go:106 still sets MCP: cfg.MCP with zero readers in internal/cli (grep 'resolved.MCP' empty); ResolveMCP (resolver.go:115-133) still seeds defaults+file+overrides only and never calls LoadProviderCommand, so ZER |
| config-plugins-skills | maxTurns<=0 is silently ignored | `internal/config/resolver.go:144,177,506` | All three merge sites still gate on `> 0` (resolver.go:152,191,539) and Resolve() still has no MaxTurns validation, unlike deferThreshold (:66-68) and maxTeamSize (:70-72); a 0/negative value is silently replaced by defa |
| config-plugins-skills | plugin vs standalone hook-event validators disagree | `internal/plugins/plugins.go:894` | plugins.parseHookEvent (plugins.go:894) + mapHookEvent (activate.go:412) still accept only the four non-specialist events; hooks.parseEvent/IsValidEvent (hooks.go:888,906) still accept six. The sets still disagree. |
| config-plugins-skills | config/contracts.go (ContractGap API) is unused dead code | `internal/config/contracts.go:12` | Per the 2026-06-13 reverification (line 95), ContractGap/DefaultContractGaps/FindContractGapsByMilestone are referenced only by contracts_test.go; no production change in this area since (outside my named focus but noted |
| cron | cron run start-up reconcile silently cancels --run-now jobs | `internal/cli/cron_run.go:115` | reconcileOverdue (cron_run.go:105-130) still reschedules any NextRunAt before nowMin without firing, with no run-now exemption, and Job has no run-now marker. Reproduced: --run-now job (NextRunAt=now at add) pushed to ne |
| hooks | Plugin/hooks event-set disagreement (plugin parseHookEvent accepts only 4 events; hooks accepts 6) | `internal/plugins/plugins.go:894 (2026-06-13 reverification l` | plugins.go:38-42 still declares only 4 HookEvent consts and parseHookEvent (plugins.go:893-898) still switches on those 4, while hooks.go parseEvent (hooks.go:901-911) accepts all 6. Re-derived above as a low finding. |
| sandbox | OnSandboxDecision callback option is set up to fire but never wired by any caller (dead code) | `internal/tools/registry.go:108` | This field lives in internal/tools/registry.go, outside the sandbox package's audited scope; the 2026-06-13 reverification (line 107) confirms repo-wide grep still finds OnSandboxDecision only at the declaration, nil-che |
| sessions | Second-granularity RFC3339 timestamps make Latest()/list ordering pick the wrong session on same-second ties | `internal/sessions/store.go:555` | store.go:709 still formats with time.RFC3339 (1s) and List tiebreak (332-336) is lexical SessionID; re-reported as the low finding above. |

#### Partially fixed (14)

| Subsystem | Prior finding | Location | Evidence |
|---|---|---|---|
| cli-usage-streamjson | streamjson secret patterns mangle ordinary text in every stream-json output event (M48) | `internal/streamjson/streamjson.go:310` | sk- and bearer patterns are now word-boundary + length anchored (streamjson.go:313,317) and TestRedactStringDoesNotOverMatchProse covers task-list/bearer; but the api[_-]?key pattern at streamjson.go:314 still over-match |
| config-plugins-skills | Hooks subsystem is loaded and listed but never executed (no event dispatch, no edit command) | `internal/hooks/hooks.go:372` | Now partially wired: beforeTool/afterTool DO dispatch from the agent loop (internal/agent/loop.go:914,937) and a CLI edit surface exists (internal/cli/hooks_manage.go `zero hooks add/...`). But the other four events (ses |
| config-plugins-skills | skills.Get/Duplicates unused; duplicate-name collisions never warned | `internal/skills/skills.go:91 + consumers` | skills.Duplicates is now consumed by the CLI list path (internal/cli/skills.go:69-81 emits per-collision warnings to stderr), so collisions ARE surfaced for `zero skills list`. But the agent-facing skill tool still drops |
| cron | Cron store has no inter-process locking: paused/edited jobs clobbered; concurrent schedulers double-fire | `internal/cli/cron_run.go:173` | A real cross-process lock now exists (internal/cron/lock.go: O_EXCL sibling <id>.lock with atomic stale reclaim) and is taken by Update/Mutate/Remove/AppendRun (store.go:174,196,219,266). fireJob now claims-before-fire u |
| cron | Cron Next double-fires wall-clock schedules on DST fall-back (repeated hour) | `internal/cron/schedule.go:171` | A fall-back collapse guard was added (schedule.go:191-209, sameWallClockMinute) and is covered by TestNextDSTFallBackDoesNotRepeatHour. But the guard only engages when `after` is exactly minute-aligned (schedule.go:205 ` |
| hooks | Hooks subsystem is loaded and listed but never executed (no event dispatch) | `internal/hooks/hooks.go:372 (2026-06-10 deep audit)` | The core gap is closed for the two tool events: Dispatcher.Dispatch is wired into the agent loop at internal/agent/loop.go:914 (beforeTool, with veto routed through blockedByHookResult at loop.go:955) and loop.go:937 (af |
| mcp | Client readLoop misroutes server-to-client requests as responses (id collision) and never answers ping | `internal/mcp/client.go:302` | client.go now has a single readLoop (331-355) that dispatches strictly by `client.pending[id]` and DROPS unmatched id-bearing messages (responses==nil) instead of corrupting a waiter, and skips id==nil notifications (338 |
| mcp | Stdio request write blocks indefinitely while holding client.mu and ignores ctx cancellation | `internal/mcp/client.go:246` | The unbounded RESPONSE wait was fixed: mu is released at client.go:291 before the ctx-aware select on the response channel (293-311), so a hung server no longer holds the lock during the wait. But the WRITE itself (line  |
| mcp | 1 MiB SSE scanner token cap permanently kills the SSE session on large messages | `internal/mcp/network_client.go:637` | maxSSEEventBytes raised to 8MiB (network_client.go:658) and scanner.Buffer uses it (662), plus an aggregate multi-line data cap (707-713), so the common large-message case no longer trips ErrTooLong. But a single SSE lin |
| oauth-providers | Health probe SSRF blocklist and credentials defeated by redirects and DNS rebinding | `internal/providerhealth/providerhealth.go:285` | DNS rebinding TOCTOU is fixed: safeDialContext (providerhealth.go:442-474) re-resolves at dial time, validates every resolved addr, and dials the validated IP literal via dialValidatedAddrs. Internal-redirect escape is f |
| oauth-providers | OpenAI adapter drops tool calls lacking an id; agent loops forever on retry | `internal/providers/openai/tool_state.go:95` | tool_state.go:235-240 closeOpen now emits StreamEventToolCallDropped for an id-less/name-less call that streamed an id/name/arguments, and loop.go:300-306 converts a dropped-call-with-no-other-calls turn into a single re |
| sessions | No fsync before rename/append: metadata.json can be empty after a crash, silently hiding the session | `internal/sessions/store.go:632` | writeMetadata (store.go:779-804) now writes a temp file via writeFileSync (fsyncs data), os.Rename, then syncDir(parent) — metadata is fully crash-safe. BUT the sibling events.jsonl append (store.go:606-616) still has no |
| specialist-swarm | Post-SIGTERM grace polling can SIGKILL a recycled PID | `internal/background/process_posix.go:38-50` | terminateProcess now resolves pgid up front and signals the group (-pid) only when pid leads its own group, returning nil early if Getpgid reports the leader already gone (process_posix.go:55-61); group-leader targeting  |
| tools | escalate_model tool is implemented and registry-aware but never included in any Core* tool set | `internal/tools/registry.go:224` | Still absent from CoreReadOnly/Write/Shell/Network/CoreTools sets (registry.go:204-266) and from the TUI registry (cli/app.go:660 newCoreRegistryScoped only iterates CoreToolsScoped). It IS now registered, but only in he |

#### Fixed (64)

| Subsystem | Prior finding | Location | Evidence |
|---|---|---|---|
| agent-loop | Truncated/filtered responses (FinishReason) are produced by every provider but never consumed by the agent loop | `internal/agent/loop.go:233` | FinishReason is now consumed at loop.go:284 (result.FinishReason = collected.FinishReason) and loop.go:502/539, exposed via Result.TruncationNotice() (types.go:278-292), and surfaced to users in both internal/tui/model.g |
| agent-loop | "Always allow" permission decision is converted into a denial when grant persistence fails (always, when Options.Sandbox | `internal/agent/loop.go:468` | PermissionDecisionAlwaysAllow case (loop.go:763-779) now sets permissionGranted=true unconditionally and only attempts persistPermissionGrant when options.Sandbox != nil, ignoring a persist error (the call is honored reg |
| agent-loop | estimateTokens ignores image attachments and the compaction trigger ignores tool definitions, so the context budget unde | `internal/agent/compaction.go:58` | estimateTokens adds len(message.Images)*imageTokenEstimate (compaction.go:114); maybeCompact now takes the exposed tools and counts estimateToolDefTokens(tools) into both the threshold and shrink checks (compaction.go:33 |
| agent-loop | Compaction summarizer provider calls bypass OnUsage — their token spend is invisible to usage accounting | `internal/agent/compaction.go:376` | summarizeMessagesOnce now calls CollectStreamWithOptions(ctx, stream, CollectOptions{OnUsage: onUsage}) (compaction.go:501); onUsage is threaded from options.OnUsage via newCompactionState -> summarizeClosure. |
| agent-loop | Mid-stream context cancellation is returned as a flattened errors.New string; the ctx.Err() identity check is unreachabl | `internal/agent/loop.go:188` | loop.go:272-275 now checks `if ctx.Err() != nil { return result, ctx.Err() }` BEFORE the `if collected.Error != ""` return at 276-279, so a canceled stream returns the wrapped context.Canceled and errors.Is succeeds. |
| agent-loop | Duplicate items-schema assignment in propertyToRuntimeMap | `internal/agent/loop.go:1072` | propertyToRuntimeMap (loop.go:1978-1980) assigns schema["items"] exactly once under `if property.Items != nil`. |
| agent-loop | Project-guidelines truncation can split a multibyte UTF-8 rune in the system prompt | `internal/agent/system_prompt.go:89` | projectGuidelines walks back to a rune boundary before cutting: `for cut > 0 && !utf8.RuneStart(content[cut]) { cut-- }` (system_prompt.go:255-258); compaction_preserve.go capBody does the same (304-307). |
| cli-usage-streamjson | Ctrl+C with -o json ends the JSON event stream with no error/done terminal event (M82) | `internal/cli/exec.go:500` | exec.go:567-595 emits a terminal error + done event for both execOutputStreamJSON (errorEvent+runEnd) and execOutputJSON (writeJSONLine type=error then type=done, exit_code=130) on context cancellation. |
| cli-usage-streamjson | zero exec --list-tools -o json ignores the requested JSON format and prints plain text (M83) | `internal/cli/exec.go:182` | exec.go:206-219 branches on outputFormat: stream-json -> writeExecStreamJSONFinal, json -> writeExecToolListJSON (exec_tools.go:103), else plain text. |
| cli-usage-streamjson | Per-event 'model' field persisted under --allow-escalation is never read by the usage report, mispricing escalated runs  | `internal/usage/report.go:17` | report.go:138-141 prefers payload.Model (set on escalation runs) before falling back to modelBySession; exec.go:555-556 writes payload["model"]=currentModel only under options.allowEscalation, and currentModel is reassig |
| cli-usage-streamjson | One corrupt session aborts the entire zero usage report (M101) | `internal/cli/usage.go:46` | usage.go:47-51 — a ReadEvents error increments set.skipped and continues instead of returning, so a single unreadable session no longer aborts the report. (Minor: set.skipped is never surfaced to the user — info-level on |
| cli-usage-streamjson | Fork duplicates provider_usage events, double-counting usage in zero usage report (M100) | `internal/sessions/store.go:387` | store.go:431-436 — Fork explicitly skips EventUsage when copying parent events into the fork ('Do NOT copy usage accounting into the fork'), so report aggregation over parent+fork no longer double-counts. |
| cli-usage-streamjson | zero usage hard-fails outside a git repository (M72) | `internal/cli/usage.go:121` | usage.go:122-131 — net-LOC is best-effort: resolveWorkspaceRoot/inspectChanges errors are swallowed and DiffStat degrades to zero, so the token report still renders outside a git repo. |
| cli-usage-streamjson | zero mcp list --json leaks MCP server env/header secret values (M74) | `internal/cli/extensions.go:186` | extensions.go:220-223 redacts via redactMCPServerConfigs (env/header values -> [REDACTED], URL creds via redactMCPURL at 236-256) and then RedactValue before writePrettyJSON; the plain-text path also runs RedactString (2 |
| cli-usage-streamjson | redactURLPasswords recompiles its regexp on every RedactString call (M93) | `internal/redaction/redaction.go:362` | urlWithCredsPattern is now a package-level var (redaction.go:424) compiled once; redactURLPasswords (426) reuses it. |
| cli-usage-streamjson | redaction opaque/custom-scheme Authorization not redacted (M12) | `internal/redaction/redaction.go:92` | headerPattern makes the scheme optional and captures the whole value to EOL (redaction.go:92, 186-194); opaque_auth_test.go verifies opaque tokens, custom schemes, and Proxy-Authorization are redacted while a known schem |
| cli-usage-streamjson | secrets.Redact leaves a private key un-redacted when an inner pattern matches inside it (M90) | `internal/secrets/scanner.go:81` | scanner.go:91-99 replaces longest matches first (order sorted by len desc) so the whole PEM PRIVATE KEY block is redacted before any shorter nested match corrupts it. |
| cli-usage-streamjson | Secret scanner misses modern OpenAI key formats / private key block header (M158) | `internal/secrets/scanner.go:35` | scanner.go:41 openai_key body is `sk-[A-Za-z0-9_-]{20,}` (covers sk-proj-/sk-svcacct- and -/_), and scanner.go:44 matches the full BEGIN..END PRIVATE KEY block. |
| cli-usage-streamjson | Stream-json / status / tool-output truncation slices strings mid-rune (M153/M155/M165) | `internal/cli/exec_writer.go:412` | exec_writer.go:401-431 — truncateForStatus and truncateForStreamJSONOutput both call cutRuneBoundary, which walks back to the last UTF-8 rune boundary at or before n. |
| config-plugins-skills | config writer performs a non-atomic in-place write of the user config | `internal/config/writer.go:53` | writeConfigFile (writer.go:149-184) now uses os.CreateTemp in the target dir, tmp.Chmod(0o600), Write, Close, then os.Rename(tmpPath, path) with a deferred os.Remove cleanup (lines 161-182) — the documented write-to-temp |
| config-plugins-skills | parseThinkTags setting silently dropped on every profile merge (L17) | `internal/config/resolver.go (mergeProfile)` | mergeProfile now preserves it: resolver.go:420-422 `if next.ParseThinkTags != nil { base.ParseThinkTags = next.ParseThinkTags }`, and it is read end-to-end by providers/factory.go:97-98 (parseThinkTagsForProfile) into op |
| config-plugins-skills | Plugin-declared skill paths are not merged into skill lookup (inert) | `internal/skills/skills.go (Load single-root)` | Plugin skills are now merged at runtime: plugins.NewSkillTool (activate.go:340) overlays default dir + plugin SkillRoots via mergeSkills, and it is registered when SkillRoots are present (cli/plugin_activate.go:53-54), i |
| cron | cron add <expr> --recipe R silently discards the user's cron expression | `internal/cli/cron.go:112` | cronAdd now consumes the positional expression BEFORE applying recipe defaults: `if len(positional) == 1 && expr == "" { expr = positional[0] }` (cron.go:113-115) precedes the recipe block whose guard is `if expr == "" { |
| cron | cron run records lose the failure reason: exec errors go to the discarded stdout stream | `internal/cli/cron_run.go:150` | fireJob now recovers the failure detail from the stream-json error event on stdout: `if streamErr := extractStreamJSONError(outBuf.String()); streamErr != "" { detail = streamErr }` then `rec.Error = cronTruncate(detail, |
| cron | promptExcerpt/cronTruncate byte-slice strings mid-rune (invalid UTF-8) | `internal/cli/cron.go:275` | promptExcerpt now cuts on a rune boundary: `cutRuneBoundary(p, 47) + "…"` (cron.go:278), and cronTruncate does `cutRuneBoundary(s, max) + "…"` (cron_run.go:272) instead of the prior raw byte slice. |
| hooks | AuditStore.append is O(n^2): re-reads and re-parses the entire audit JSONL on every append | `internal/hooks/hooks.go:503 (2026-06-10) / reverified still-` | append() no longer calls ReadEvents(); it calls lastSequence() (hooks.go:515) which os.Open + Stat + ReadAt only the trailing 8KB window (hooks.go:550-602: `const tailWindow = 8 * 1024`, `file.ReadAt(buf, start)`), parse |
| hooks | Cross-process sequence collision on the shared audit JSONL (read-then-append not atomic across processes) | `internal/hooks/hooks.go append (implied by 2026-06-13 line 6` | append() now wraps the lastSequence()+write in a cross-process O_EXCL file lock via store.lockAudit() (hooks.go:509-513 -> lock.go:33-90), with stale-lock reclaim (lock.go:97-108) mirroring cron/oauth. TestAuditStoreSequ |
| mcp | Unbounded Content-Length allocation lets a peer crash the whole process | `internal/mcp/protocol.go:69` | protocol.go:17 defines maxMessageBytes=64MiB; readHeaderFramed caps Content-Length (lines 121-123: `if parsed > maxMessageBytes { return ... exceeds ... limit }`) before `body := make([]byte, contentLength)` (138); readL |
| mcp | MCP stdio transport uses LSP Content-Length framing instead of MCP newline-delimited JSON | `internal/mcp/protocol.go:42` | read() (protocol.go:54-76) now reads newline-delimited JSON by default (isJSONStart -> decodeMessage) and only falls back to LSP Content-Length framing when the first line is not JSON; write() (148-163) emits one JSON ob |
| mcp | zero mcp list --json leaks MCP server env/header secret values | `internal/cli/extensions.go:186` | extensions.go:222 wraps cfg.Servers in redactMCPServerConfigs (236-255), which replaces every Env and Header value with `[REDACTED]` and runs redactMCPURL on the URL (252) before writePrettyJSON; redactMCPURL (mcp_tools. |
| mcp | Child stderr captured into an unbounded bytes.Buffer for the entire server lifetime | `internal/mcp/client.go:98` | stderr is now `&boundedBuffer{cap: maxStderrCapture}` (client.go:134, cap=64KiB at line 91); boundedBuffer.Write (103-115) discards bytes past cap. (Tail-vs-head retention is a separate low finding, not unboundedness.) |
| mcp | Streamable-HTTP SSE response decoding takes the first event instead of the matching response | `internal/mcp/network_client.go:621` | decodeSSERPCMessage (network_client.go:612-652) now skips events whose decoded message has a non-empty Method (server-initiated notifications/requests) — `if candidate.Method != "" { return true }` (636-638) — and return |
| oauth-providers | Gemini adapter never reads cachedContentTokenCount; CachedInputTokens always 0 | `internal/providers/gemini/types.go:88` | gemini/types.go:107 now declares `CachedContentTokenCount int \`json:"cachedContentTokenCount"\``; provider.go:225 `state.cachedTokens = payload.UsageMetadata.CachedContentTokenCount` and provider.go:283 emits `CachedInp |
| oauth-providers | HTTP 529 classified as rate-limit but excluded from the 429/503 retry policy | `internal/providers/providerio/retry.go:107` | retry.go:108-110 `func ShouldRetryStatus(code int) bool { return code == http.StatusTooManyRequests // code == http.StatusServiceUnavailable // code == 529 }` — 529 now retried; doc comment updated to include Anthropic ' |
| sandbox | macOS sandbox-exec profile blocks /tmp and /dev/null, breaking most wrapped shell commands | `internal/sandbox/runner.go:262` | runner.go:412-436 sandboxWritableDevices (/dev/null,/dev/zero,/dev/stdin,...) and sandboxWritableSubpaths (/tmp,/private/tmp,/var/tmp,/var/folders,/dev/fd) are now emitted by seatbeltWriteRule (runner.go:559-581, AllowTe |
| sandbox | apply_patch misparses unified-diff body lines starting with '--- '/'+++ ' as file paths | `internal/tools/apply_patch.go:161` | risk.go:291-327 patchHeaderPaths now tracks hunk state (inHunk, oldRemaining/newRemaining from parsePatchHunkCounts) and decrements per body line, so '--- '/'+++ ' header parsing at risk.go:319-324 is only reached OUTSID |
| sandbox | Interactive-command detector flags pager/REPL names inside quoted arguments (splitShellSegments not quote-aware) | `internal/sandbox/safe_command.go:138` | safe_command.go:474-583 splitShellSegments is now a full quote/substitution-aware scanner (inSingle/inDouble state, substStack for $()/backtick, backslash escapes); operators inside quotes are literal. Detection also anc |
| sandbox | Grant store prefix/substring safety: Lookup trims while stored writer key may not be normalized (trim asymmetry => load  | `internal/sandbox/grants.go:146` | grants.go:556-568 readState rebuilds a `normalized` map keyed by `key := strings.TrimSpace(name)` and re-keys every bucket; normalizeStoredGrant(key, grant) compares against the trimmed key (grants.go:614). Lookup/Revoke |
| sandbox | Destructive/network/installer classification recompiles N regexes per shell call | `internal/sandbox/risk.go:53` | risk.go:12-55 destructiveCommandPattern, pipedInstallerPattern, unparseableNetworkPattern, and destructiveExtraPatterns are all package-level regexp.MustCompile vars compiled once at init, not per call. |
| sessions | Torn append (crash mid-write) permanently bricks a session: ReadEvents hard-fails on any malformed JSONL line | `internal/sessions/store.go:547` | store.go:677-704 now computes tornTailPossible (trailing byte != newline) and lastNonEmpty, and breaks (drops the partial record) only when the malformed line is the last non-empty line AND the file lacks a trailing newl |
| sessions | Fork duplicates provider_usage events and rewrites their timestamps, double-counting and re-dating usage in `zero usage  | `internal/sessions/store.go:387` | Fork (store.go:430-442) explicitly `if event.Type == EventUsage { continue }` so usage events are never copied into the fork; usage/report.go BuildReport iterates every session's events (cli/usage.go collectUsageData), s |
| sessions | `zero sessions tree` is O(nodes x sessions) disk reads (full store re-list per tree node) | `internal/sessions/lineage.go:140` | Tree (lineage.go:127-167) now does a single store.List() up front, indexes byID and childrenByParent in memory, and treeFrom (169-189) recurses over the in-memory maps with no per-node disk scan. |
| sessions | Naive byte-slice truncation can split multi-byte UTF-8 runes in prompts and titles (replay) | `internal/sessions/replay.go:237` | replay.go:382-393 cutPromptRuneBoundary backs up while !utf8.RuneStart(s[n]) before slicing; it is used at the prompt-truncation sites (310-314) and payloadPreview (376), so replay/preview truncation is rune-safe. |
| sessions | execSessionRecorder.append latches AppendEvent errors into recorder.err but no caller reads it (silent session-persisten | `internal/cli/exec_sessions.go:116-124` | warnIfRecordingFailed (exec_sessions.go:128-132) now surfaces recorder.err to stderr and is deferred by both runExec (exec.go:464) and the spec-draft path (exec_spec.go:90). |
| specialist-swarm | Specialist sessions launched without a description are unresumable (AgentName never recorded / recorded as garbage) | `internal/specialist/exec.go:166` | exec.go:250-255 now always emits --session-title: when Description is empty it passes the bare manifest name (`args = append(args, "--session-title", name)`), so specialistAgentName resolves to the real specialist name a |
| specialist-swarm | Specialist children always run with --auto high (PermissionModeUnsafe): one Task approval silently grants unprompted she | `internal/specialist/exec.go:150` | specialistAutonomy() (exec.go:125-132) maps only an explicit 'unsafe' parent mode to 'high' and everything else (including empty) to 'low'; BuildArgs/BuildResumeArgs (exec.go:230,282) call it with input.PermissionMode. T |
| specialist-swarm | Race: duplicate specialist stop/usage accounting events — check-then-append dedupe is not atomic | `internal/specialist/accounting.go:72` | recordSpecialistStop and appendSpecialistUsageRollup now route through appendSpecialistEventOnce → Store.AppendEventUnlessExists (accounting.go:60,105,160-172), which performs the existence check and the append atomicall |
| specialist-swarm | Foreground specialist run fails entirely when a child stream-json line exceeds 1 MiB | `internal/specialist/streamer.go:43` | ParseStream (streamer.go:41-73) now uses bufio.NewReader + ReadString('\n') with no per-line cap, and the live foreground path runChildProcess (exec.go:735-759) likewise uses bufio.NewReader.ReadString. The 1 MiB Scanner |
| specialist-swarm | SetPID failure path deletes the prompt file under a running child and records no terminal accounting/status | `internal/specialist/exec.go:346` | runBackground SetPID-failure branch (exec.go:451-466) now terminates the orphaned child (background.TerminateProcess(pid), joining any kill error), calls manager.UpdateStatus(StatusError) and recordSpecialistStop, in add |
| specialist-swarm | POSIX background-task kill terminates only the leader PID; the specialist's subprocess tree leaks on SIGKILL escalation | `internal/background/process_posix.go:50` | launchBackgroundProcess now calls background.ConfigureChildProcessGroup(command) before Start (exec.go:791), which sets Setpgid (process_posix.go:24-29), and terminateProcess signals the negative PID (the whole group) wh |
| specialist-swarm | formatTaskOutput is dead code | `internal/specialist/output_tool.go:244` | grep across the repo finds no `formatTaskOutput(` definition or call; the only formatter in use is formatTaskOutputSummary, called from readOutput (output_tool.go:154). The dead function was removed. |
| tools | bash tool timeout does not kill the command when a child inherits the output pipes | `internal/tools/bash.go:104` | bash.go:119 now calls hardenProcessLifetime(command); bash_proc_unix.go:28-44 sets Setpgid + WaitDelay=bashWaitDelay(2s) + Cancel that SIGKILLs the whole negative-pid process group, so a backgrounded child can no longer  |
| tools | grep glob filter is applied to root-relative paths, silently dropping matches under a subdirectory search | `internal/tools/grep.go:224` | grepFiles now matches the glob against the path relative to the SEARCH directory: grep.go:307-311 computes `globPath = filepath.ToSlash(rel)` via `filepath.Rel(target, path)` and matches globMatcher against that, so `glo |
| tools | OnSandboxDecision callback option is set up to fire but is never wired by any caller (dead code) | `internal/tools/registry.go:108` | registry.go no longer declares or invokes OnSandboxDecision anywhere; RunOptions (registry.go:15-43) has no such field and RunWithOptions (registry.go:93-171) propagates decisions via res.SandboxDecision instead. The dea |
| tools | read_file returns Truncated but no truncation marker in output; rune-width line numbering can misalign | `internal/tools/read_file.go:89` | read_file.go:118-123 now appends an explicit marker on truncation: `[truncated: N more line(s) in the requested range not shown; set start_line=... to continue]`. Line-number padding uses ASCII strconv.Itoa width (read_f |
| tools | lsp_navigate workspace confinement (recently fixed) — verify | `internal/tools/lsp_navigate.go` | lsp_navigate.go:85 resolves the model path via resolveScopedReadPath BEFORE reading or handing it to the LSP manager (line 90 readWorkspaceFile(absPath), line 97 req.Path=absPath), so a '..'/absolute path cannot open a f |
| tui-render | wrapPlainText collapses internal whitespace, destroying code indentation/alignment in assistant answers | `internal/tui/rendering.go:261 (also 2026-06-13 reverify rend` | rendering.go:399-409 now detects preformatted/columnar bodies (an internal run of >=2 spaces) and emits them verbatim via splitPreservingWidth (459-479) instead of strings.Fields, preserving every space; leading indent i |
| tui-render | appendTranscriptRow O(n) copy + O(n) dedup scan per row → O(n^2) append and /resume rehydration | `internal/tui/transcript.go:113/117 (multiple rows)` | appendTranscriptRow now appends in place (transcript.go:99-108, comment notes the old O(n^2) is gone). The prior reverification gap — hasTranscriptRow still linear-scanning per keyed row during rehydration — is closed fo |
| tui-render | Full transcript re-rendered from scratch every frame, including per-line regex parsing | `internal/tui/model.go:610/124` | renderRowMode/renderRowDetailed now memoize per-row output via the LRU defaultRenderCache (rendering.go:172-197) keyed on width/flush/bodyCap/cwd/row fields/rc hints (render_cache.go:126-174); only rows tied to the activ |
| tui-render | looksLikeDiff false-positives on any output containing a line starting with '---' | `internal/tui/view.go:394` | view.go:897-916 now requires a real hunk header (hunkHeaderPattern) OR BOTH '+++ ' and '--- ' file headers before returning true; a lone '---' line no longer hijacks bash/generic output into the diff renderer. |
| tui-render | truncateTUIOutput byte-slices UTF-8 mid-rune, emitting invalid UTF-8 | `internal/tui/transcript.go:303` | truncateTUIOutput (transcript.go:436-445) now calls cutRunes (449-460), which walks back to the nearest UTF-8 rune boundary via utf8.RuneStart before slicing, so the transcript/session log can no longer receive a split m |
| tui-render | Cancelled-run event flush appends to whichever session is active at flush time (cross-session contamination) | `internal/tui/model.go:512` | flushRunIDs now maps runID -> recording sessionID (model.go:161, set at cancel 3177-3180), and agentResponseMsg routes the durable flush to that session: `if flushSessionID == m.activeSession.SessionID { appendSessionEve |
| tui-render | Transcript dedupe key omits runID — repeated provider tool-call IDs silently drop later tool rows | `internal/tui/transcript.go:135/137/139` | transcriptRowKey bakes runID into every key, e.g. tool rows return fmt.Sprintf("%d:%d:%s", row.kind, row.runID, row.id) (transcript.go:157-181), with a comment explaining Gemini's repeating synthetic gemini_tool_N ids; w |
| tui-render | Resize unhandled beyond storing width/height; composer not width-bounded (blind typing) | `internal/tui/model.go:456/227` | tea.WindowSizeMsg (model.go:1249-1267) stores width/height, resets the streaming fade line-age mapping that a re-wrap would invalidate (1256-1257), sizes the composer for horizontal scroll `m.input.SetWidth(maxInt(20, ch |
### 4.4 Findings dropped in adversarial review

Two drafted findings did not survive re-verification and are explicitly withdrawn:

| Subsystem | Claimed | Why dropped |
|---|---|---|
| agent-loop | Compactor context-window budget frozen at run start, not updated on mid-run model escalation | Mechanics accurate, but the user-visible failure mode is unreachable today — the escalation path that would change the window does not feed back into the frozen budget in a way that produces wrong behavior. Not a defect. |
| config-plugins-skills | `maxTurns <= 0` silently dropped with no validation error | Disproven: a 0/negative `maxTurns` is replaced by the documented default (30) by design, not silently mis-handled; there is no incorrect behavior. (The "no explicit validation error" observation is retained only as the low-priority still-open consistency note, not as a defect.) |

## 5. Root-cause synthesis

The 35 surviving findings cluster into four underlying causes:

**A. Pattern/substring matching on un-structured text where a structure-aware path exists but isn't authoritative.** The sandbox destructive/piped-installer classifier scans the *raw* command string (M11) even though a quote-aware AST analyzer runs alongside — but the analyzer can only *add* risk categories, never retract a regex false positive. The stream-json `api_key` redactor (M1) matches "any next word" after the marker. Both over-match real content (a `git commit -m "…rm -rf…"`, prose containing `apiKey foo`). Fix posture: make the structured parser authoritative when the parse succeeds; anchor patterns to credential/command shape.

**B. "Success" returned before the durable/drain step completes.** `AppendEvent` returns success after a page-cache write with no `fsync`, while the *derived* metadata is fsync'd — so a crash can leave `EventCount` ahead of the durable log and lose the last event, including the checkpoint `/rewind` targets (M12). A PTY exec session's `command.Wait()` returns and the session is removed before the fire-and-forget copy goroutine has drained the master FD, dropping the command's final output (M14). The hook dispatcher discards audit-append errors (I2). Fix posture: tie the success signal to completion of the durable write / output drain.

**C. Surfaces shipped ahead of (or drifting from) their wiring.** Four hook events are validated, documented, and `zero hooks add`-able but never dispatched (H1); `ResolvedConfig.MCP` is computed but unread so provider-command MCP servers are dropped (M2); usercommands `model:`/`agent:` frontmatter is parsed/documented but ignored (L4); project slash-commands auto-load with a built-but-unused provenance flag (M3); `OnContext`/`MeasureContext` has no consumer (L2); plugin vs hooks event-set validators disagree (L8); the runtime skill tool drops dup-name collisions silently (L5). Fix posture: a single source of truth for valid events; either wire the surface or remove it from config/CLI/docs.

**D. Lifecycle-edge handling asymmetric with the happy path.** Idle/close/exhausted/redirect edges are under-handled: the remote bridge clears the conn deadline before the daemon handshake so an authenticated-idle peer pins a slot forever (M7) and `Shutdown` can't even close bridge conns because `ServeConn` bypasses `trackConn` (M8); the MCP SSE client never reconnects after any stream close (M9); cron double-fires on DST fall-back (M4), double-grants a claim when the schedule can't advance (M5), and silently cancels `--run-now` on reconcile (M6); the swarm scheduler leaks a per-job context on `MaxRuns` completion (L13); the mailbox stale-lock break compares the file to itself instead of the observed-stale snapshot (M13); and the health probe forwards credentials across a cross-host redirect (M10 — the security item, an edge of the same "redirect handling" family the DNS-rebind fix addressed for the internal case but not the cross-host-public-credential case).

## 6. Prioritized remediation order

1. **M10 — strip auth headers on cross-host redirect in the health probe** (only security finding; credential exfil to a public host).
2. **H1 — wire or remove the four un-dispatched hook events** (trust/correctness; silent no-op of a documented automation surface).
3. **M12 — `fsync` the events.jsonl append** (data-loss of the last event, including `/rewind` checkpoints).
4. **M11 — make the sandbox AST authoritative / segment the destructive regexes** (false-positive hard-blocks safe commands in headless exec).
5. **M14 — synchronize the PTY copy goroutine with session exit** (model loses a command's final output).
6. **M4 / M5 / M6 — cron: DST double-fire, claim double-grant on unadvanceable spec, `--run-now` reconcile** (duplicate/again missed scheduled agent runs = token spend / broken contract).
7. **M7 / M8 / M9 — connection lifecycle: bridge handshake deadline, `ServeConn` tracking for Shutdown, MCP SSE bounded reconnect** (slot exhaustion / lost MCP tools mid-session).
8. **M1 — anchor the stream-json `api_key` redactor** (corrupts machine-readable output).
9. **M2 / M3 / M13 / M15 — provider-command MCP drop, project-command provenance, mailbox stale-break, TUI streaming O(n²)**.
10. **Lows / infos** as hygiene — notably **L15** (genuine `lastUsedAt` data race; add a `-race` test), **L1** (route the three remaining stream connects through `streamWithReconnect`), and the dead/inert cleanups (L2/L4/L5/L8).

## 7. Confidence notes (what could not be fully verified)

- **`-race` suite is not green:** 3 `internal/tools` exec-session tests fail under `-race` due to fixed-timing yield-window assumptions (characterized in §2 as flakes, confirmed by passing 3× without `-race`). I could not make those tests deterministic without modifying source (audit only). No data race underlies them; the one real race in that package (**L15**) was found by inspection because no test exercises concurrent `touch()`+prune.
- **Platform-specific backends only skimmed:** Windows sandbox/process files (`*_windows.go`), `landlock_linux.go`, `seccomp_*.go`, `log_monitor_darwin.go`, and the OS keyring backends (`secret_access_*.go`) were read shallowly — outside the symlink/parsing/risk/locking focus. A dedicated Windows/Linux-LSM pass is advisable.
- **Not deep-read (out of stated focus):** `internal/tools/web_fetch.go` & `web_search.go` (network tools), Anthropic provider internals, the plugin/skill **install/checksum/lockfile** paths, and TUI overlays (`mouse.go`, picker/wizard/onboarding, `command_center.go` specifics). Build/vet cover them; behavior was not audited.
- **Daemon prior-audit reconciliation is necessarily empty:** the `D1–D11` labels referenced in current daemon code comments come from a review not present in the three on-disk `docs/audit/*.md`, so "fixed since" for the daemon is against code comments, not those docs. The daemon findings here (M7/M8/L7/I1) are re-derived against current code.
- **Reproductions:** M4, M5, M6, M11 were empirically reproduced with throwaway tests against the current tree (then removed). M10, M12, M14, M9, H1 are verified by code reading + caller/grep tracing, not a live exploit. Severity calibration for M10 is *medium* (requires a malicious/compromised/typo'd `baseURL` that 3xx-redirects to an attacker host) rather than high; if a deployment commonly points providers at untrusted gateways, treat it as high.

---

## 8. Remediation status — `fix/audit-2026-06-20`

Implemented in staged commits on branch `fix/audit-2026-06-20` (one finding/tight-cluster per commit; each gated build/vet/gofmt + `-race` on the affected package; full `-race` suite green on the final tree). Each finding was re-confirmed against current code before fixing.

### Fixed (15 findings + 1 test-robustness)

| ID | Sev | Status | Note |
|---|---|---|---|
| M10 | medium | ✅ Fixed | strip auth headers (x-api-key/x-goog-api-key/custom) on cross-host redirect in the health probe |
| M14 | medium | ✅ Fixed | join the PTY copy goroutine before markDone/remove so a command's final output isn't dropped on exit (also fixes the flaky `TestExecCommandTTYSessionAcceptsInputOnLinux`) |
| M12 | medium | ✅ Fixed | fsync events.jsonl append so the log is as durable as its metadata |
| M4 | medium | ✅ Fixed | feed `sched.Next` a minute-aligned instant so the DST fall-back collapse guard engages |
| M5 | medium | ✅ Fixed | pause an unadvanceable job inside the claim so two schedulers can't both fire it |
| M7 | medium | ✅ Fixed | bound the daemon control handshake with a read deadline (idle remote peer can't pin a slot) |
| M8 | medium | ✅ Fixed | `ServeConn` tracks the conn so `Shutdown` can close a stalled remote handshake |
| M13 | medium | ✅ Fixed | atomic rename-with-verify mailbox stale-lock break (no self-compare; no split-brain) |
| M1 | medium | ✅ Fixed | anchor the streamjson `api_key` redaction so prose isn't mangled |
| I1 | info | ✅ Fixed | local control socket handshake now deadline-bounded (same fix as M7) |
| L15 | low | ✅ Fixed | snapshot exec-session `lastUsedAt` under its lock — fixes a real `-race` data race |
| L13 | low | ✅ Fixed | `defer job.cancel()` releases the scheduler's per-job context on MaxRuns completion |
| L1 | low | ✅ Fixed | route the post-compaction / max-turns connects through `streamWithReconnect` |
| L3 | low | ✅ Fixed | `--skip-permissions-unsafe` rejects trailing positional args instead of dropping them |
| L11 | low | ✅ Fixed | error on a present-but-wrong-shape chatgpt account-id claim so the login warning fires |
| (report §2) | — | ✅ Fixed | de-flaked the 3 exec-session timing tests so `go test ./... -race` is green |

### Deferred (larger/riskier than a clean stage — rationale)

| ID | Sev | Why deferred |
|---|---|---|
| H1 | high | Wiring `sessionStart/End` + `specialistStart/Stop` correctly is a cross-surface design change: the dispatcher lives only in `agent.Options` (fires per-tool), so session boundaries differ across exec/TUI/daemon and the specialist events need the parent dispatcher threaded through the child boundary. The audit's alternative (delete the events) would disable a documented feature (against the guardrails). Needs a focused design pass, not a surgical edit. |
| M9 | medium | MCP SSE reconnect is risky concurrency (re-open + re-`initialize`d session state) and not deterministically unit-testable; warrants its own change with a fault-injection harness. |
| M11 | medium | Making the sandbox AST authoritative is security-sensitive — a wrong call under-blocks a genuinely destructive command. Needs an extensive "still blocks real destructive commands" matrix before flipping the precedence; the guardrails forbid weakening confinement to land a fix. |
| M2 | medium | Merging provider-command MCP into `ResolveMCP` is a real wiring decision (which resolve path the runtime should read); deferred to avoid guessing the intended single source of truth. |
| M3, M6 | medium | Project-command provenance marker (UX) and cron `--run-now` one-shot marker (schema field) are design choices better made deliberately. |
| M15 | medium | Streaming-block memoization is perf-only (no correctness risk) and fiddly in the TUI render hot path. |
| L2, L4, L5, L6, L7, L8, L9, L10, L12, L14, L16, L17, I2 | low/info | Lower-value hygiene / cross-cutting (dead-code removal decisions, cross-platform lock identity, RFC3339Nano format change, TUI width math, minor concurrency hardening). Batchable in a follow-up. |

### Skipped (not a real defect)
- The two findings the audit itself dropped in §4.4 (compactor frozen-budget; `maxTurns<=0`) were already withdrawn and not implemented.
