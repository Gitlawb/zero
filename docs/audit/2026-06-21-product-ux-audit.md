# Zero — Product / UX / TUI Audit (2026-06-21)

- **Target:** `github.com/Gitlawb/zero` @ `origin/main` commit `f7ee189` — a terminal coding agent (Go, Bubble Tea v2 TUI; one event stream feeding TUI / headless `exec` / MCP server / cron).
- **Lens:** product, UX, TUI/visual correctness, and open-source-release readiness, judged from a **stranger who just cloned the repo**. The code/security audit is separate (`docs/audit/2026-06-20-deep-audit.md`) and is **not** repeated here.
- **Scope note:** audit only — no source changed.

## 1. Executive summary

Zero is a **functionally mature** agent with a thoughtfully-built TUI (memoized rendering, width tiers, light/dark themes, a real reduce-motion opt-out, color-independent diff/permission encoding, and a genuinely good guided setup wizard). The gap is the **release/packaging and last-mile UX layer**, and it is not yet shippable as open source.

Two **release blockers**: there is **no `LICENSE` file** (the repo is legally all-rights-reserved despite being marketed as open source), and the **headline binary-install path is dead** — `scripts/install.sh` / `install.ps1` resolve `Gitlawb/zero`'s GitHub Releases, which 404 (no release has ever been published), so the only working install is `go run` — which then trips a **Go-version mismatch** (README says 1.24, `go.mod` requires 1.25). A new user on a machine without Go 1.25 cannot get to first run.

Beyond that, the recurring themes are **docs/claims drifting from the code** (a `/usage` slash command and a `--theme` flag that don't exist; a keyless local-model on-ramp the health probe blocks), **error messages that don't guide recovery** (no-key errors dump a raw OpenAI URL instead of pointing at `zero setup`; `doctor` reports "Overall: pass" with no credential and leaks Go `map[...]` syntax), and **low-contrast theme tokens carrying real content** (faint/faintest fail WCAG while holding line numbers, diff headers, and help text).

### Severity rollup (35 findings, post-dedup)

| Severity | Count |
|---|---|
| Critical | 2 |
| High | 11 |
| Medium | 11 |
| Low | 5 |
| Info | 6 |

(Counts merge cross-area duplicates — LICENSE, install-404, Go-version, and the no-provider-setup-pointer cluster each appeared from multiple area finders.)

## 2. Environment & method

- **Build:** `go build ./cmd/zero` → clean on go1.26.4; binary run as `/tmp/zero` with **isolated config** (`HOME`/`XDG_CONFIG_HOME` → temp dirs) so findings reflect a fresh machine, not local state.
- **Ran (real output captured):** `zero --help`, `doctor`, `exec` (no provider / bad key / unreachable URL / empty prompt), `setup <provider> --verify` (no key / wrong key, openai + ollama), `providers check --connectivity`, `config`, `version`; bad-flag / bad-mode / bad-effort / malformed-config error paths; the existing TUI visual tests (`command_card_contrast`, `theme_select`/luminance, `width_tiers`, `wrap_whitespace`, `theme_reprobe`) — all pass; throwaway Go tests measuring display-cell widths of the truncation helpers with CJK input; WCAG contrast ratios computed from the theme hex tokens; a `creack/pty` capture of a monochrome onboarding frame to diff SGR codes across `NO_COLOR` values.
- **Terminal profiles:** color/`NO_COLOR`/profile behavior was checked via the `colorprofile` dependency + one pty capture; **truecolor/256/16-color and a physically light terminal were not driven frame-by-frame** (see Confidence notes).
- **Could not capture:** live interactive TUI frames — Bubble Tea v2's alternate-screen buffer yields only escape setup/teardown to `script`/`expect`. Rendered first-run wizard, resize redraw, streaming fade, and narrow-tier painting are assessed **from code + the passing visual tests** and marked unverified where relevant.
- **Adversarial pass:** every finding faced 1–2 skeptics that re-ran/re-read to disprove it; 2 were dropped (see §A.dropped). Several of my own first-pass guesses were corrected (exit codes are correct; meaning never depends on color alone; a reduce-motion opt-out does exist).

## 3. "First 5 minutes" — new-user walkthrough & verdict

1. **Clone, read README.** Strong pitch ("terminal coding agent you fully own", "no telemetry"). Two install options offered as peers: a binary one-liner and `go run ./cmd/zero`.
2. **Try the binary one-liner** (`scripts/install.sh`) — the natural choice without Go. → **Dead-ends** with a curl 404 / "could not read tag_name" (no GitHub Release exists). [C1]
3. **Fall back to `go run ./cmd/zero`** per the README ("requires Go 1.24+"). On Go 1.24 → **build error** `go.mod requires go >= 1.25.0`. [H2] (Works on 1.25+.)
4. **First launch with no key.** Headless `exec` prints a clear, exit-coded error — but it names only `OPENAI_MODEL`/`OPENAI_API_KEY` and a raw `platform.openai.com` URL, **never `zero setup` or `zero auth`** (the commands built for this moment). [H/M cluster] The guided `zero setup` wizard itself is excellent [I1] — if the user discovers it.
5. **Verify setup** (`zero setup openai --verify`) with no key yet → "**The provider rejected the API key**" — but no key was sent. [M1] Or try the README's headlined **keyless local-model path** (Ollama at localhost) and run `doctor`/`--verify` → "**localhost hosts are blocked**" + "make sure the server is running" (self-contradictory). [H1]
6. **Run `zero doctor`** to sanity-check → "**Overall: pass**" even with no provider credential [H], and an unreadable `missing: map[gopls:...]` Go-map blob [H].

**Verdict: a stranger cannot reliably reach a working first session in 5 minutes.** Not because the agent is weak — it's the install/license/version/onboarding-error plumbing around it. Once a user is past setup with a real key, the core loop and TUI are solid.

## 4. Findings by area

### A. First-run & onboarding

#### C1 · Both README binary-install paths point at a repo that 404s — `scripts/install.sh` and `install.ps1` dead-end for every new user

**Status (2026-06-21):** Fixed (in-repo) — `622e4b4`. README/INSTALL demote the binary path to "coming soon" and lead with `go run`; release-artifacts.yml now publishes a GitHub Release on a `v*` tag. Publishing the release + making the repo public are MANUAL maintainer steps (repo owner/name already matched go.mod).
- **Severity:** critical · also: G-oss-readiness
- **Where:** `README.md:55-57, docs/INSTALL.md:52, scripts/install.sh:4 (ZERO_REPO default Gitlawb/zero)`
- **Evidence:** curl -s -o /dev/null -w '%{http_code}' https://api.github.com/repos/Gitlawb/zero -> 404. /releases, /releases/latest, /tags all return {"message":"Not Found","status":"404"}. install.sh:106 resolves api_url="${ZERO_GITHUB_API%/}/repos/${ZERO_REPO}/releases/latest" then install.sh:111 `[ -n "$tag" ] || fail "could not read tag_name from $api_url"`. With curl --fail (install.sh:80) the API call itself errors out first. README presents this as the 'or install a release binary' path alongside go run.
- **Impact:** A stranger who picks the README's headline binary-install path (the natural choice on a machine without Go) hits an immediate, opaque failure ('could not read tag_name' or a curl --fail HTTP error). There is no published release, so the documented one-liner cannot succeed for anyone. Only the `go run ./cmd/zero` path works.
- **Fix:** Publish at least one GitHub release with the documented archive+.sha256 assets under the correct public repo, and fix the ZERO_REPO default / README links to that repo's real owner/name. Until releases exist, demote the binary-install path in README/INSTALL to 'coming soon' and lead with `go run ./cmd/zero`.

#### C2 · No LICENSE file; README says license 'being finalized' — OSS-release blocker (confirming established fact, still true @ f7ee189)

**Status (2026-06-21):** Skipped — license choice deferred by the maintainer ("leave the license portion now"). LICENSE / README §License / package.json license remain a MANUAL step.
- **Severity:** critical · also: G-oss-readiness
- **Where:** `README.md:330-332; repo root (no LICENSE* file)`
- **Evidence:** `ls LICENSE*` -> no matches. README.md:332: 'License is being finalized; a `LICENSE` file will be added before the public release.'
- **Impact:** A new user cloning the repo has no legal grant to use, modify, or redistribute. For an 'open-source' project this blocks adoption/redistribution and is a release blocker.
- **Fix:** Add a LICENSE file (e.g. Apache-2.0/MIT) and update README §License to name it.

#### H1 · Local-model verification/health commands fail with a misleading 'localhost hosts are blocked' error — breaks the README's headlined keyless path

**Status (2026-06-21):** Fixed — `24c8de3`. Loopback allowed only for a user-configured local base_url; redirects + other private ranges stay blocked.
- **Severity:** high
- **Where:** `internal/providerhealth/providerhealth.go:374-377 (also :81,:94 loopback prefixes); surfaced by `zero setup <local> --verify`, `zero providers check <local> --connectivity`, `zero doctor``
- **Evidence:** `zero setup ollama --verify` -> `[zero] setup verification failed: Could not reach the provider endpoint. Check the base URL ... and that the server is running. (provider connectivity URL is unsafe: localhost hosts are blocked)`. `zero providers check ollama --connectivity` -> status: fail / `provider.connectivity: provider connectivity URL is unsafe: localhost hosts are blocked` / exit 3. validateEndpoint() hard-rejects normalized=='localhost' and 127.0.0.0/8 + ::1. Guard is confined to providerhealth probe path (grep shows blockedAddrReason/endpointSafetyError only in providerhealth + unrelated web_fetch.go) so actual `zero exec` chat again
- **Impact:** README §Why Zero and Quick start headline 'Local models need no key at all' as the easiest on-ramp. A new user who runs Ollama/LM Studio at localhost:11434 and then runs setup --verify / doctor / providers check (all suggested) sees FAIL with a self-contradictory message ('make sure the server is running' + 'localhost is blocked'), and the 'next' hint tells them to re-run the exact command that will keep failing. The keyless p
- **Fix:** Allow loopback/localhost in the provider connectivity probe for explicitly user-configured local providers (the SSRF guard is meant for fetched/remote URLs, not the user's own configured base_url). At minimum, special-case local provider kinds to skip the loopback block, and drop the contradictory 'server is running' wrapper when the real cause is the loopback policy.

#### H2 · go.mod requires `go 1.25.0` (+ toolchain go1.26.4) but README badge and Quick start say Go 1.24 — a Go 1.24 user cannot build from source

**Status (2026-06-21):** Fixed — `622e4b4`. README badge + quick start now say Go 1.25+.
- **Severity:** high · also: G-oss-readiness
- **Where:** `go.mod:3 (`go 1.25.0`), go.mod:4 (`toolchain go1.26.4`); README.md:16 badge 'Go-1.24', README.md:52 'requires Go 1.24+'`
- **Evidence:** go.mod: `go 1.25.0` / `toolchain go1.26.4`. README.md:16 `![go](https://img.shields.io/badge/Go-1.24-...)`, README.md:52 `# run from source (requires Go 1.24+)`. A Go 1.24.x toolchain refuses a module declaring `go 1.25.0` unless auto-toolchain download is enabled (GOTOOLCHAIN=auto + network); offline/pinned 1.24 users get a 'go.mod requires go >= 1.25.0' build error.
- **Impact:** The README explicitly invites Go 1.24 users to `go run ./cmd/zero` (the ONLY working install path given the missing releases). They hit a build failure on the documented version, with no hint that 1.25 is actually required. Builds confirmed working only on go1.26.4 in this env.
- **Fix:** Make the docs match go.mod: change the badge and Quick-start note to 'requires Go 1.25+'. (Or, if 1.24 support is intended, lower the go.mod directive to 1.24 and verify it compiles.)

#### M1 · `zero setup <provider> --verify` with NO key set reports 'The provider rejected the API key' — but no key was ever sent

**Status (2026-06-21):** Fixed — `42b7123`. `--verify` distinguishes "no API key found" from "rejected".
- **Severity:** medium
- **Where:** `internal/cli/setup.go:59-77 (verify branch); verified via runtime`
- **Evidence:** With OPENAI_API_KEY unset: `zero setup openai --verify` -> '... setup verification failed: The provider rejected the API key. Double-check the key value (and that it is for this provider), then re-run setup. (Provider openai requires API credentials.)' — identical message to the WRONG-key case (OPENAI_API_KEY=sk-wrongkey123).
- **Impact:** Confusing for a first-timer who ran setup without exporting a key yet (a very common ordering): the tool says the key was 'rejected'/'double-check the key value' when the real problem is there is no key at all. Sends them debugging a key they never set.
- **Fix:** Distinguish the missing-credentials case from the rejected-credentials case: when no key/env is present, say 'No API key found — export OPENAI_API_KEY (or pass --api-key-env) then re-run', and reserve 'rejected the key' for actual 401s.

#### I1 · The guided setup wizard copy itself is well-written and self-explanatory (source-level; live frames unverified)

**Status (2026-06-21):** Info — no action (positive finding).
- **Severity:** info
- **Where:** `internal/tui/onboarding.go:1191-1650`
- **Evidence:** Strings include 'Welcome to Zero' / 'A terminal agent for changing real code.' (1202-1204), a Safety panel 'Zero asks before running shell commands or changing files. Unsafe mode stays off unless you explicitly enable it. Default: ask before risky work.' (1191-1196), clear step headers ('Choose a provider', 'Choose a model', 'Paste your <name> API key' with 'Saved keys stay in your user config' / 'Blank uses <ENV> from your shell'), model-ID examples (1311), and a consistent footer ('↑/↓ choose  Enter continue  q quit'). Non-interactive `zero setup <p>` output is also good: prints config path + concrete 'next:' steps + a runnable 'try this:'
- **Impact:** Positive: the wizard and non-interactive setup are a strong on-ramp; the onboarding problems above are in the surrounding install/license/version/verify plumbing, not the wizard copy.
- **Fix:** None — keep. (Recommend capturing real wizard frames via a pty harness in CI since alt-screen content isn't visible to script/expect, to guard the rendering that could not be verified here.)


### B. Interaction flows & UX

#### H3 · /input-style is a fully inert command — registered, in /help, autocompletes, but does nothing

**Status (2026-06-21):** Fixed — `1b2e453`. Command + kind + handler + dead helper removed.
- **Severity:** high
- **Where:** `internal/tui/commands.go:288-296 (registry) + internal/tui/model.go:2964-2969 (handler) + internal/tui/rendering.go:43-53 (output)`
- **Evidence:** Handler: `case commandInputStyle: ... text: shellOnlyCommandText(command.name)`. shellOnlyCommandText renders a warning card whose only body line is "This control is available in the TUI but does not have a backend setting yet." Captured from a test I ran: `command-card input-style / status: warning / State / This control is available in the TUI but does not have a backend setting yet. / hint: use /help to inspect active commands`. grep confirms NO other input-style logic exists: `grep -rn 'input-style|inputStyle|InputStyle' internal --include='*.go' | grep -v _test` returns only the registry entry + this handler. Yet it is listed in /help (S
- **Impact:** A new user discovers /input-style via `/` autocomplete or /help (where its description, "Show input style state", implies it surfaces real configuration), runs it, and gets a warning card admitting it's a no-op stub. This is the one remaining surfaced-but-inert slash command (the sibling stubs /style, /compact, /theme, /effort flagged in the earlier code audit are now actually wired on current main). It erodes trust in the com
- **Fix:** Remove the /input-style entry from commandDefinitions (commands.go) and drop the commandInputStyle kind + its case in model.go, so /help and autocomplete stop advertising it. Re-add it only when a backend setting exists.

#### H4 · README's TUI slash-command table advertises /usage, which is not a slash command (resolves to 'unknown command')

**Status (2026-06-21):** Fixed — `e68d6cb`. `/usage` removed from the README slash table.
- **Severity:** high
- **Where:** `README.md:89 (`| \`/doctor\` \`/usage\` \`/config\` | health, cost, and config without leaving the chat |`) under the "## The TUI" slash-command table`
- **Evidence:** The registry has no /usage (full name+alias dump from commands.go contains /doctor, /config, but no /usage). I ran a test: `parseCommand("/usage").kind == commandUnknown` (PASS), and `formatGroupedCommandHelp()` does NOT contain /usage (`HELP has ... /usage=false`). `zero --help` shows `usage  Summarize token usage and estimated cost` exists ONLY as a CLI subcommand. So a user who types `/usage` in-chat (per the README) hits the commandUnknown path (model.go:2976) -> "unknown command: /usage" (unless they happen to have authored .zero/commands/usage.md, which a new user has not). The table's caption literally promises "...cost...without leavi
- **Impact:** A brand-new user reads the only slash-command reference in the README, types /usage to check token cost mid-session, and gets an error. The advertised in-chat cost readout does not exist as a slash command; the real feature is the separate `zero usage` CLI subcommand, which contradicts the table's 'without leaving the chat' promise.
- **Fix:** Either add a /usage slash command (kind+handler showing the same token/cost summary as the `zero usage` subcommand) or remove `/usage` from the README:89 table and document `zero usage` as a CLI command instead.

#### L1 · Ctrl+E in the composer is hijacked by mouse-release toggle, breaking the emacs end-of-line binding (while Ctrl+A=home still works)

**Status (2026-06-21):** Deferred — low (Ctrl+E composer binding).
- **Severity:** low
- **Where:** `internal/tui/model.go:818-825 (top-level Ctrl+E intercept) vs internal/tui/composer.go:223-225 (composer Ctrl+E = end-of-line)`
- **Evidence:** The top-level key switch handles `case keyCtrl(msg, 'e'):` and unconditionally toggles m.mouseReleased then `return`s, before the composer dispatch (model.go:1140 `applyComposerKey`) is ever reached. composer.go:223 declares `case keyIs(msg, tea.KeyEnd) || keyCtrl(msg, 'e'): state.cursor = composerLineEnd(state)` — that Ctrl+E arm is therefore unreachable from the composer. By contrast Ctrl+A/K/U/W are NOT intercepted at top level (`grep -n "keyCtrl(msg, 'a'|'e'|'k'|'u'|'w')" model.go` returns ONLY line 818 = Ctrl+E), so they reach the composer normally. The `?`-help overlay (keybinding_help.go:53) documents Ctrl+E only as 'release the mouse
- **Impact:** A user with emacs/readline muscle memory: Ctrl+A correctly jumps to line start, but Ctrl+E (expected: jump to line end) instead toggles mouse-capture mode and prints a 'Mouse released' system notice — a surprising, asymmetric behavior in the input editor.
- **Fix:** Either remove the dead Ctrl+E arm from composer.go:223 (leaving only tea.KeyEnd), or gate the top-level Ctrl+E mouse-toggle so it only fires on an empty/inactive composer (like the `?` overlay does at model.go:982) and let Ctrl+E reach the composer when editing text; then update keybinding_help.go accordingly.

#### I2 · Two permission decisions share hotkey 'y' but are mutually exclusive by tool type (no actual collision)

**Status (2026-06-21):** Info — no action (no real collision).
- **Severity:** info
- **Where:** `internal/tui/permission_prompt.go:45-48 + internal/agent/loop.go:1737-1742,1798-1805`
- **Evidence:** permissionOptions maps both AlwaysAllowPrefix and AlwaysAllow to hotkey "y". The dedup in permissionOptions is keyed by decision action, not hotkey, so if both appeared in one prompt both 'y' rows would render. However availablePermissionDecisions only appends AlwaysAllowPrefix for shell tools with a command prefix (loop.go:1738), and AlwaysAllow only when permissionSupportsPersistentDecision(toolName) is true — which returns false for `bash/exec_command/write_stdin/apply_patch` (loop.go:1800). So a shell prompt gets AlwaysAllowPrefix=y and a non-shell prompt gets AlwaysAllow=y; they never co-occur.
- **Impact:** None today — verified mutually exclusive by tool type. Noting it because the safety relies on a non-local invariant (the loop.go gating); a future tool that is both a shell tool AND persistent-decision-eligible would surface two 'y' rows where the first match wins, silently shadowing the second.
- **Fix:** Optional defensive measure: de-duplicate by hotkey in permissionOptions (or assign distinct hotkeys) so the invariant is enforced at the render layer rather than depending on the agent's decision-set gating.


### C. TUI rendering & visual correctness

#### M5 · middleTruncate (diff card file paths) overflows the budget on wide-rune paths

**Status (2026-06-21):** Deferred — explicitly out of scope for this PR (wide-rune truncation budget; outer width clip already prevents frame breakage per I4).
- **Severity:** medium
- **Where:** `internal/tui/startup.go:138 middleTruncate (used for the diff card path head at internal/tui/rendering.go:1471 with budget innerWidth/2)`
- **Evidence:** middleTruncate is also rune-count based (`if len(runes) <= limit`, then string(runes[:front])+"…"+string(runes[len-back:])). Ran the real fn: `middleTruncate(strings.Repeat("路",40), 20)` returns 39 cells for a 20-cell budget (test log: `middleTruncate(20) -> cells=39 (want <=20)`).
- **Impact:** A file path containing CJK characters in the diff card header renders ~2x its budget; the outer fitStyledLine re-clips it so the card edge survives, but the head/right-aligned counts (+N/-N) collide or the path is cut harder than intended — the path becomes harder to read for non-Latin filenames.
- **Fix:** Make middleTruncate measure with lipgloss.Width and cut front/back halves with splitAtWidth on the reversed/forward string, or fall back to the width-aware truncateRunes once that is fixed.

#### I4 · Wide-rune handling is correct in prose/markdown/table/user-prompt wrap and at the outer card fit — overflow does NOT break the card frame

**Status (2026-06-21):** Info — no action (verified good).
- **Severity:** info
- **Where:** `internal/tui/rendering.go:381 wrapPlainText / :442 splitAtWidth / :463 splitPreservingWidth; internal/tui/startup.go:213 fitStyledLine / :223 truncateStyledLine`
- **Evidence:** These all measure via lipgloss.Width / per-glyph glyphWidth (e.g. truncateStyledLine line 260 accumulates glyphWidth and also tracks OSC-8 link state so truncation can't leak a hyperlink). toolCard re-fits every body line to innerWidth (rendering.go:1387) so even the buggy truncateRunes output cannot push the card past the terminal width — verified: a 100-col CJK diff card produced all lines at exactly 100 cells. Assistant prose is also capped at 96 cols (assistantMeasureCap, rendering.go:360) for readability on wide terminals. WindowSizeMsg (model.go:1249) resizes composer and resets fade state cleanly.
- **Impact:** Positive: the layout is robust against frame-breaking; the wide-rune bug manifests as silent content loss inside the card rather than a corrupted UI. Documents why the high-severity finding is content-loss, not layout-break.
- **Fix:** No change needed; noted so the truncateRunes fix is scoped to content fidelity, and to record that interactive frames themselves remain UNVERIFIED (no pty).


### C/E. Color, theme & accessibility

#### H5 · faintest fails WCAG even the 3.0 UI floor in BOTH themes; it carries functional content (line numbers, diff @@/+++/--- headers, tool args, separators)

**Status (2026-06-21):** Fixed — `cc3b5e3`. faintest raised to WCAG AA (>=4.5) in both themes.
- **Severity:** high
- **Where:** `internal/tui/theme.go:135 (dark faintest #3a3a40), :168 (light faintest #9b9ba3); consumed as zeroTheme.faintest / diffMeta / addLineNum / delLineNum / toolArg`
- **Evidence:** Computed WCAG ratios: light faintest #9b9ba3 on panel #ececed = 2.34; dark faintest #3a3a40 on panel #0e0e10 = 1.71, and on the unpainted canvas #070708 = 1.78. AA-normal needs 4.5, the relaxed UI/large floor is 3.0 — these are below even 3.0. faintest renders diffMeta (theme.go:223: '@@ hunks, +++/--- headers') and the diff gutter line numbers (theme.go:226-227), which are functional, not decorative.
- **Impact:** On a brand-new user's terminal, diff hunk headers, line numbers, and tool argument hints are near-invisible to anyone with even mild low vision, and washed out for everyone on a bright screen. The existing test passes because TestLightPaletteContrastAndHierarchy only asserts a luminance DELTA (panelL-inkL>=0.5) and monotonic ordering — it never checks a WCAG ratio, so these sub-floor values ship green.
- **Fix:** Darken light faintest toward ~#6e6e76 (>=3.0 on #ececed) and lighten dark faintest toward ~#5a5a62 (>=3.0 on #0e0e10/#070708); then add a test asserting actual WCAG ratio >= 3.0 for every token used to carry text, not just a luminance delta.

#### H6 · faint is sub-AA in both themes yet carries instructional/navigation text (help footer, MCP wizard hints, composer placeholder, working timer)

**Status (2026-06-21):** Fixed — `cc3b5e3`. faint raised to WCAG AA in both themes.
- **Severity:** high
- **Where:** `internal/tui/theme.go:134 (dark faint #5b5b63), :167 (light faint #78787f); used at keybinding_help.go:89, mcp_add_wizard_view.go:26-160, mcp_manager.go:362/443, model.go:2111/2151/2231`
- **Evidence:** WCAG: dark faint #5b5b63 on panel = 2.87 (below the 3.0 UI floor); light faint #78787f on panel = 3.71 (below AA-normal 4.5). It renders real instructions: keybinding_help.go:70 'keybindingHelpFooter = "? or Esc to close · /help for slash commands"' shown via zeroTheme.faint (the user's only on-screen cue for how to exit the help overlay), mcp_manager.go:362 the nav legend 'type search up/down navigate Enter action Esc close', and the composer placeholder (model.go:2231).
- **Impact:** The dismiss hint, navigation legends, MCP setup guidance, and input placeholder are hard to read for low-vision users and dim for everyone — a new user trying to learn the keys is reading the help in the lowest-contrast color in the UI.
- **Fix:** Raise faint to clear 4.5 (light) / >=3.0 (dark) on its panel, e.g. dark #6f6f78, light #5f5f66. Reserve sub-AA grays strictly for non-text decoration (rules/borders), which already have separate tokens (line/line2).

#### M2 · Light-theme accent (4.34) is below AA for normal text, despite a test that claims to verify light accent contrast

**Status (2026-06-21):** Fixed — `cc3b5e3`. Light accent #4d7a08->#477006 (AA); theme test now asserts a true WCAG ratio.
- **Severity:** medium
- **Where:** `internal/tui/theme.go:169 (light accent #4d7a08); test internal/tui/theme_select_test.go:83`
- **Evidence:** WCAG: light accent #4d7a08 on panel #ececed = 4.34 (AA-normal needs 4.5). accent renders the user gutter '❯' (theme.go:38/210), bash prompt (theme.go:48/218), spinner, and focus. The test at theme_select_test.go:83 only asserts 'panelL - relLum(accent) < 0.25' (a luminance delta, not a contrast ratio), so 4.34 passes. Dark accent #caff3f is fine (16.4).
- **Impact:** On a light terminal the brand prompt glyph and bash gutter sit just under the readability bar for small text. Borderline rather than broken, but it is the most-repeated colored glyph on screen.
- **Fix:** Darken light accent slightly (e.g. #436a07 → ~4.7) to clear AA-normal, and change the test to assert a real WCAG ratio (>=4.5 for text-bearing accent) instead of a luminance delta.

#### M3 · NO_COLOR honored only for strconv.ParseBool-style values; NO_COLOR=yes / NO_COLOR=anything leaves the UI in full color, violating the no-color.org spec

**Status (2026-06-21):** Fixed — `7fc296b`. NO_COLOR with any non-empty value forces the Ascii profile.
- **Severity:** medium
- **Where:** `dependency github.com/charmbracelet/colorprofile@v0.4.3 env.go:115-117 (envNoColor uses strconv.ParseBool); zero never sets WithColorProfile (internal/tui/run.go:25-35), so it inherits this`
- **Evidence:** Drove /tmp/zero under a pty across NO_COLOR variants and diffed emitted SGR: 'NO_COLOR=1' -> colorSGR=false (306 bytes, monochrome), 'NO_COLOR=true' -> false, but 'NO_COLOR=yes' -> colorSGR=true (676 bytes, full color) and 'NO_COLOR=foo' -> colorSGR=true. Source: colorprofile env.go:116 'noColor, _ := strconv.ParseBool(env.get("NO_COLOR"))'. The no-color.org spec says NO_COLOR present with ANY non-empty value must disable color. (NO_COLOR=1 correctly produced a readable bold-only frame — color degrades cleanly when the value is recognized.)
- **Impact:** A user who follows the common convention 'export NO_COLOR=yes' (or any non-bool truthy value) still gets a full-color TUI, defeating their accessibility/preference setting. Silent and surprising.
- **Fix:** Don't rely on the dependency's ParseBool: detect NO_COLOR yourself (present and non-empty => force colorprofile.Ascii via tea.WithColorProfile) in run.go, matching no-color.org. At minimum document that only NO_COLOR=1/true work today.

#### M4 · Documented-in-code --theme flag does not exist; Options.Theme is read but never populated, so the only non-interactive theme control is the undocumented ZERO_THEME env var

**Status (2026-06-21):** Fixed — `3b73074`. Real `--theme {auto|dark|light}` flag wired to Options.Theme; stale comment corrected.
- **Severity:** medium
- **Where:** `internal/tui/theme.go:12 and theme_select.go:18 (both claim a '--theme flag'); internal/tui/options.go:52-54 (Theme field); internal/tui/model.go:559 (reads it)`
- **Evidence:** Binary: 'zero --theme light -p hi' -> 'unknown command "--theme"'. 'zero --help' global Flags list has no theme flag. grep for assignments to Options.Theme / '.Theme =' across cmd/ and internal/ returns ZERO hits — the field is only ever READ (model.go:559 resolveThemeMode(options.Theme, ...)). Yet theme_select.go:18 comments 'the --theme flag, threaded via Options.Theme' and theme.go:12 says startup selection happens via '--theme'.
- **Impact:** A user reading the code (or expecting the documented flag) cannot pick a theme at launch via --theme. They must discover ZERO_THEME (documented nowhere) or use /theme after the TUI is already up. The stale comments actively mislead.
- **Fix:** Either wire a real --theme {auto|dark|light} flag that sets Options.Theme, or delete the --theme references from theme.go:12 and theme_select.go:18 and document ZERO_THEME + /theme as the supported controls.

#### L2 · Accessibility/customization controls (NO_COLOR, ZERO_THEME, ZERO_NO_FADE, motion opt-out) are completely undocumented

**Status (2026-06-21):** Fixed — `3cd0a50`. README "Accessibility & appearance" section documents NO_COLOR/ZERO_THEME/--theme//theme/ZERO_NO_FADE.
- **Severity:** low
- **Where:** `README.md / docs/ (grep for NO_COLOR|ZERO_THEME|ZERO_NO_FADE|--theme|reduce.motion returns nothing); controls exist at streaming_fade.go:28-44 and theme_select.go:19`
- **Evidence:** grep -rni 'NO_COLOR|ZERO_THEME|ZERO_NO_FADE|--theme|reduce.motion' README.md docs/ => no matches. The controls themselves are real and tested: streaming_fade.go:28-44 honors ZERO_NO_FADE and auto-disables fade over SSH/tmux/16-color/no-TTY; /theme and ZERO_THEME switch palettes.
- **Impact:** A user who finds the streaming-text fade or color distracting (motion sensitivity) has no way to know ZERO_NO_FADE exists; a NO_COLOR/light-terminal user can't discover ZERO_THEME. The a11y story is decent in code but invisible to the new user it would help.
- **Fix:** Add a short 'Accessibility / Appearance' section to the README listing NO_COLOR (note the value caveat), ZERO_THEME=auto|dark|light, /theme, and ZERO_NO_FADE (reduce-motion opt-out).

#### I3 · Meaning does not depend on color alone, and there IS a reduce-motion opt-out — both verified

**Status (2026-06-21):** Info — no action (verified good).
- **Severity:** info
- **Where:** `internal/tui/rendering.go:1512/1516 (diff signs), :1200-1203 (permission ✓/✗), view.go:277-285 (mode labels), :996 (PERMISSION text badge); streaming_fade.go:28-44 (ZERO_NO_FADE)`
- **Evidence:** Diffs emit explicit sign columns: rendering.go:1512 diffBodyLine(...,"+",...) for adds and :1516 "−" for dels, so add/del survive color-stripping. Permission outcome uses glyphs ✓ (rendering.go:1200) / ✗ (:1203), the card is gated by a text 'PERMISSION' badge (:996), and permission MODE shows a text label 'auto-approve'/'ask'/'unsafe' (view.go:277-285) not color-only. Streaming fade has a reduce-motion path: ZERO_NO_FADE plus auto-disable on SSH/tmux/ANSI/no-TTY (streaming_fade.go:29-43); the spinner has no separate opt-out but is a single glyph, not a fade. NO_COLOR=1 pty frame rendered fully readable in bold-only.
- **Impact:** Positive: the core surfaces (diffs, permissions, modes) remain interpretable for colorblind users and under NO_COLOR. Recorded so it's not re-flagged; the only motion gap is the spinner having no independent disable.
- **Fix:** Optional: extend the reduce-motion opt-out to also still the spinner glyph when ZERO_NO_FADE (or a NO_COLOR/reduce-motion signal) is set, for full motion-sensitivity coverage.


### D. Consistency & polish

#### H7 · Bad/missing API key produces a mangled, self-contradicting error paragraph (Redact corrupts the provider's own help text)

**Status (2026-06-21):** Fixed — `8b9b479`. 401/403 lead with an actionable curated message; Redact only scrubs token-shaped Bearer words.
- **Severity:** high
- **Where:** `internal/providers/providerio/providerio.go:234-249 (Redact); surfaced via exec/-p with no/invalid key`
- **Evidence:** Live: `/tmp/zero -p "hi" --model not-a-real-model` -> `[zero] auth error: You didn't provide an API key. You need to provide your API key in an Authorization header using authorization [REDACTED] (i.e. Authorization: authorization [REDACTED] or as the password field (with blank username) if you're accessing the API from your browser and are prompted for a username and password. You can obtain an API key from https://platform.openai.com/account/api-keys.` The raw 401 prose contains the literal word 'Bearer' twice as INSTRUCTION text; Redact() (line 242-247: `if EqualFold(TrimRight(words[index],":"),"Bearer")` -> rewrite this+next word) treats
- **Impact:** A brand-new user whose only mistake is a missing/typo'd key gets an incomprehensible, grammatically-broken paragraph that (a) reads like a bug, (b) gives browser-username/password advice irrelevant to a CLI, (c) names platform.openai.com even when they aren't trying to use OpenAI, and (d) never tells them to run `zero auth` / `zero setup`. This is the single most likely error a first-run user hits.
- **Fix:** Don't pass raw upstream auth-error bodies through verbatim. For 401/403 emit a curated message: `auth error: your <provider> API key is missing or invalid. Run \`zero auth\` or set <ENV>=...`. Keep the raw body only behind a --verbose/debug flag. Also tighten Redact so the 'Bearer' heuristic only fires on token-shaped following words (e.g. matches sk-/long base64), not arbitrary prose.

#### H8 · doctor leaks raw Go map[...] syntax for ANY nested-map detail — confirmed on lsp.servers AND config.validation (shared formatDetails bug)

**Status (2026-06-21):** Fixed — `bb4b348`. formatDetailValue renders nested maps as "k: v"; no more raw map[...].
- **Severity:** high
- **Where:** `internal/doctor/doctor.go:277 (formatDetails uses %v on the value)`
- **Evidence:** lsp.servers: `missing: map[gopls:install with \`go install golang.org/x/tools/gopls@latest\` ... pyright-langserver:install with \`npm install -g pyright\` ...] | present: map[rust-analyzer:on PATH]`. config.validation (with a malformed config.json): `/tmp/zbad/.zero/config.json: map[col:4 error:invalid config JSON: invalid character 't' looking for beginning of object key string line:1]` — the actual error sentence is buried inside a Go map dump. Root cause line 277: `fmt.Sprintf("%s: %v", ..., redaction.RedactValue(value, ...))` where value is a map[string]any.
- **Impact:** Builds on the established lsp leak: this is not isolated to one check — every doctor detail whose value is a nested map renders as unreadable Go syntax. The MOST useful diagnostics (which LSP to install, what's wrong with your config + the line/col) are exactly the ones mangled into `map[...]`. New users running `zero doctor` to fix a problem get gibberish.
- **Fix:** In formatDetails, special-case map[string]any values: render them as indented `key: value` sub-lines (or `k=v, k=v`) instead of `%v`. For config.validation specifically, hoist line/col/error into a one-line human string (`line 1, col 4: invalid character 't' ...`).

#### M6 · Inconsistent validation of enum-like flags: --mode rejects bad values with a great message, --reasoning-effort silently ignores garbage

**Status (2026-06-21):** Deferred — medium not in this PR's scope (crit/high + OSS files + contrast). Tracked for a follow-up (validate --reasoning-effort like --mode).
- **Severity:** medium
- **Where:** `internal/cli/exec.go:902 (mode) vs exec.go:965-998 (reasoning effort)`
- **Evidence:** `zero -p hi --mode bogus` -> `[zero] unknown mode "bogus". Valid modes: smart, deep, fast, large, precise.` (exit 2 — exemplary microcopy). But `zero -p hi --reasoning-effort wat` -> `gpt-4.1 does not support reasoning effort; ignoring --reasoning-effort wat` then proceeds; `wat` is never validated as a value (the message only fires because gpt-4.1 lacks the capability — on a model that DOES support effort, an invalid value like `wat` would pass through unchecked).
- **Impact:** Inconsistent error contract across sibling flags. A typo in --reasoning-effort is silently swallowed instead of corrected with the valid set (low/medium/high), so users think their setting applied when it didn't.
- **Fix:** Validate --reasoning-effort against the allowed enum the same way --mode does, with a `Valid efforts: low, medium, high.` message; only fall back to the capability-skip notice when the value itself is valid.

#### L3 · Brand capitalization inconsistent across one CLI surface (ZERO / Zero / zero) and `zero version` prints no real version

**Status (2026-06-21):** Deferred — low (brand capitalization + real version string).
- **Severity:** low
- **Where:** ``zero --help` header vs body; `zero version``
- **Evidence:** `zero --help` first line: `ZERO terminal coding agent`; the 10 command descriptions all say `Zero` (e.g. 'List Zero model registry entries'); `zero version` -> `zero dev`. So one help screen shows three casings, and the release binary reports its version as the literal word `dev`.
- **Impact:** Cosmetic inconsistency a design-conscious user notices immediately; `zero dev` on a fresh clone gives no way to tell which build they have when filing issues.
- **Fix:** Pick one brand casing (recommend 'Zero') and use it everywhere including the --help header. Stamp a real version (git describe / tag) into the build via ldflags so `zero version` is meaningful.

#### I5 · Empty states and existing polish tests are genuinely clean

**Status (2026-06-21):** Info — no action (verified good).
- **Severity:** info
- **Where:** `providers/sessions/skills/plugins/usage commands; internal/tui/*_test.go`
- **Evidence:** Empty-state copy is clear and path-aware: `No Zero skills found in /tmp/.../skills.`, `No local Zero plugins loaded.`, `No Zero sessions found.`, and `usage` renders a tidy zero table with `n/a (net LOC <= 0)` guards. `providers` even flags `api key: not set`. Visual tests TestLightPaletteContrastAndHierarchy / TestWidthTierSegments / TestViewNeverExceedsTerminalWidth / TestWrapPlainTextPreservesAlignedWhitespace / TestThemeAutoReProbesBackground / TestHelpRoutesThroughStyledCard / TestStyleCommandCardContentRowTwoTonesCommands all PASS.
- **Impact:** Positive: card alignment, width tiers, light/dark contrast, whitespace-preserving wrap, and theme re-probe are regression-guarded and look correct. The polish problems are concentrated in error/warning microcopy, not layout or empty states.
- **Fix:** No change needed; keep these tests. Minor terminology nit to consider later: `config` says `active provider: none` while `providers` lists openai as the default keyless profile — reconcile the two so 'active' vs 'default' is unambiguous.


### F. Product completeness & robustness

#### H9 · `zero doctor` reports "Overall: pass" with NO provider credential — the one tool meant to verify keys gives a false all-clear

**Status (2026-06-21):** Fixed — `bb4b348`. provider.config fails (not pass) for a remote provider with no credential; keyless local stays pass.
- **Severity:** high
- **Where:** `internal/doctor/doctor.go:136-156 (providerConfigCheck) + :83-84 (report.OK only flips on StatusFail) + :188 (connectivity is warn/skipped). Reproduced via `zero doctor`.`
- **Evidence:** With unset OPENAI_API_KEY and empty config: `Overall: pass` ... `[pass] provider.config - Provider config loaded for openai.\n  ... credentialConfigured: not set ... model: gpt-4.1` ... `[warn] provider.connectivity - Connectivity probe skipped. Run zero doctor --connectivity`. In code, providerConfigCheck returns StatusPass whenever the profile is non-empty (the built-in openai/gpt-4.1 default is non-empty), regardless of `credential := "not set"`; report.OK is false ONLY if some check is StatusFail (line 83-84).
- **Impact:** README says `zero doctor` will "verify config, keys, and connectivity." A brand-new user who runs it before their first prompt sees a green "Overall: pass", concludes setup is done, then `zero exec` immediately fails with a raw upstream auth error. The diagnostic actively misleads instead of catching the single most common new-user misconfiguration.
- **Fix:** In providerConfigCheck, when credential=="not set" AND the provider kind requires a key (i.e. not a local/keyless runtime like ollama/lmstudio), return StatusWarn (or StatusFail) with help text pointing to `zero setup` / `zero auth`. That makes Overall reflect "not actually usable yet" and gives the user the next action.

#### H10 · No-provider / missing-key error never names zero's own onboarding (`zero setup`/`zero auth`) — it dumps a raw OpenAI HTTP message telling the user to visit platform.openai.com

**Status (2026-06-21):** Fixed — `42b7123`. exec + TUI no-provider errors point at `zero setup` / `zero auth`.
- **Severity:** high · also: A-onboarding, D-polish
- **Where:** `internal/providers/providerio/providerio.go:218-232 (ClassifiedError) + internal/config/resolver.go:740 (silent default to openai/gpt-4.1). Reproduced via `zero exec "hello"` with no config.`
- **Evidence:** `zero exec "hello"` (no key) → stderr: `[zero] auth error: You didn't provide an API key. You need to provide your API key in an Authorization header ... You can obtain an API key from https://platform.openai.com/account/api-keys.` (exit 3). The README's literal first example is `zero exec "fix the failing test in ./pkg"`. grep for `zero setup|zero auth|no provider configured` across internal/cli, internal/agent, internal/providers error paths returns nothing in this path — the only zero-added text is the `auth error: ` prefix.
- **Impact:** An exec-first / CI user who copy-pastes the README's headline command before running the wizard gets a wall of OpenAI-branded text steering them to OpenAI's website, with zero mention that zero has a `zero setup` wizard or `zero auth`, and no hint that it silently defaulted to OpenAI. Onboarding dead-ends for anyone not using the interactive TUI.
- **Fix:** When the resolved profile has no credential (credential not set) and the request fails auth, prepend a zero-owned line before the upstream text, e.g.: `No provider credential found. Run \`zero setup\` (interactive) or set OPENAI_API_KEY / ANTHROPIC_API_KEY / GEMINI_API_KEY. Local models need no key — see \`zero providers list\`.` Detect the no-credential case at the agent/exec boundary rather than relying on the upstream 401 b

#### M7 · Provider errors (auth / rate limit) are classified but give the user no recovery action

**Status (2026-06-21):** Largely addressed by H7 (`8b9b479`) — auth errors now name a recovery action (`zero auth`). Remaining per-class recovery hints DEFERRED.
- **Severity:** medium
- **Where:** `internal/providers/providerio/providerio.go:218-231 (ClassifiedError); rendered raw in TUI via internal/tui/model.go:1403-1409 (rowError text = msg.err.Error()) and to exec stderr.`
- **Evidence:** ClassifiedError only adds a category prefix: `auth error: `, `rate limit error: `, `provider error: `. The TUI error row is `transcriptRow{kind: rowError, text: msg.err.Error(), final: true}` — the verbatim classified string, no appended next-step. Confirmed live: rate-limit/auth bodies are the raw upstream message with only the prefix.
- **Impact:** On a 429 the user sees `rate limit error: <upstream text>` with no guidance (wait? `/model` to a cheaper or different provider? it already retried?). On auth failure, no pointer to `/provider`/`zero auth`. The failure is legible as a *category* but not *recoverable* without the user already knowing zero's surface. (Reconnect for transient disconnects in reconnect.go DOES surface a `[connection lost — reconnecting N/2…]` notice
- **Fix:** Append a short, category-specific recovery hint when rendering rowError / exec error: auth → `Run \`zero auth\` or check your API key, or /provider to switch.`; rate limit → `Zero already retried with backoff; wait or use /model to switch provider.` Keep it one line; the classifier already knows the category.

#### M8 · TUI has no slash command to discover or manage subagents/cron/skills/plugins — features the README markets prominently

**Status (2026-06-21):** Deferred — larger feature (discovery slash commands for subagents/cron/skills/plugins); out of this PR's scope.
- **Severity:** medium
- **Where:** `internal/tui/commands.go (full registry: /spec and /mcp exist; no /specialist, /cron, /skills, /plugins, /subagent). CLI subcommands exist (zero specialist|cron|skills|plugins per `zero --help`).`
- **Evidence:** grep over commands.go for `spec|specialist|cron|mcp|skill|plugin|subagent` matches only `/mcp` (:174) and `/spec` (:197). README §Why Zero headlines "Subagents", "Spec mode", "Scheduled agents", and "Extensible — skills, plugins, hooks"; the TUI command table only lists /spec /plan. The ? overlay (keybinding_help.go:58-61) references specialist *cards* but only as a reactive drill-in, not a way to list/create them.
- **Impact:** A user living in the interactive TUI (the default `zero` with no args) can type `/` and will never surface cron, skills, plugins, or specialist management. These read as CLI-only features even though README presents them as first-class. Discoverability of half the headline feature set is gated on reading the README's Commands section and dropping out of the TUI.
- **Fix:** Add thin TUI slash commands that shell into the existing CLI surfaces (or at least informational `/specialist`, `/cron`, `/skills`, `/plugins` that print status + the CLI command to manage them), and list them in `/help`. Even read-only listing closes the discoverability gap.

#### I6 · Strong robustness wins worth preserving: network-down framing, mid-stream reconnect, output truncation, and width-safe rendering

**Status (2026-06-21):** Info — no action (verified good).
- **Severity:** info
- **Where:** `internal/providers (upstream-unreachable message), internal/agent/reconnect.go, internal/tools/exec_command.go:802-812, internal/tui/rendering.go:1161-1233; tests in internal/tui.`
- **Evidence:** Unreachable base URL → `[zero] upstream unreachable: the model server could not connect to 127.0.0.1:9 (connection refused). The request never reached the model — this is a network failure ... Verify the host is reachable (DNS/proxy/VPN/firewall) ...` (exit 3). reconnect.go retries connect-time disconnects (eof/reset/timeout/502/503) twice with backoff and surfaces `[connection lost — reconnecting N/2…]`. exec output truncates head+tail with `\n[zero] output truncated\n`. Tool cards cap at 16 live / 400 flushed lines and collapse noisy output. Visual tests PASS: TestViewNeverExceedsTerminalWidth, TestTinyTierSingleSegmentAndRailLessCards, Tes
- **Impact:** These are genuinely good for a new user: network failures are legible and actionable, transient hiccups self-heal, and huge tool output won't flood the transcript or overflow narrow terminals. Listed so they are not regressed by fixes to the items above.
- **Fix:** None — keep. The findings above should reuse this same quality bar (the unreachable-host message is the model for what auth/rate-limit/no-key errors should look like).


### G. Open-source readiness

#### H11 · No SECURITY.md and no private vulnerability-reporting path

**Status (2026-06-21):** Fixed — `634f23f`. SECURITY.md with a private GitHub-advisory reporting path.
- **Severity:** high
- **Where:** `/tmp/zero-ux/ (no SECURITY.md at root or .github); CONTRIBUTING.md (no disclosure section)`
- **Evidence:** find for security.md => none. grep -niE 'vulnerab|disclos|CVE|report.*privately' across CONTRIBUTING.md/README.md/INSTALL.md => only an unrelated 'security issue' mention in CONTRIBUTING:92 and a firecrawl privacy note. For a coding agent that executes shell, edits files, and handles API keys/secrets, there is no documented way to report a vuln.
- **Impact:** A security researcher who finds a sandbox-escape or secret-leak in an agent that runs arbitrary commands has no private channel and will either drop a public issue (0-day exposure) or go silent. GitHub also won't surface a 'Security policy' for the repo.
- **Fix:** Add SECURITY.md (root or .github/) with a private reporting address or GitHub Security Advisories link and a supported-versions/response-time note.

#### M9 · No issue templates and no PR template (.github/) despite CONTRIBUTING mandating their use

**Status (2026-06-21):** Fixed — `634f23f`. .github issue (bug/feature/config) + PR templates added.
- **Severity:** medium
- **Where:** `/tmp/zero-ux/.github/ (only workflows/); CONTRIBUTING.md:49 ('Open an issue using the appropriate issue template')`
- **Evidence:** find .github -type f => only the 4 workflow YAMLs. No .github/ISSUE_TEMPLATE/, no PULL_REQUEST_TEMPLATE. Yet CONTRIBUTING.md:49 says 'Open an issue using the appropriate issue template' and the whole policy hinges on an `issue-approved` label workflow.
- **Impact:** A newcomer told to 'use the appropriate issue template' finds none — bug reports arrive unstructured (the exact low-signal reports CONTRIBUTING says will be closed), and the documented contribution process is self-contradictory on first contact.
- **Fix:** Add .github/ISSUE_TEMPLATE/bug_report.yml + feature_request.yml (matching the fields CONTRIBUTING already enumerates at lines 122-145) and a PULL_REQUEST_TEMPLATE.md that prompts for the `Fixes #123` issue link.

#### M10 · No CODE_OF_CONDUCT.md

**Status (2026-06-21):** Fixed — `634f23f`. CODE_OF_CONDUCT.md (Contributor Covenant 2.1) added.
- **Severity:** medium
- **Where:** `/tmp/zero-ux/ (none at root or .github)`
- **Evidence:** find for code_of_conduct* => none.
- **Impact:** Standard OSS-readiness/community-health item missing; GitHub's community profile flags it and some users/orgs treat its absence as a maturity signal. Low functional impact but expected for a public release.
- **Fix:** Add CODE_OF_CONDUCT.md (e.g. Contributor Covenant) with a contact for enforcement.

#### M11 · No CHANGELOG and source builds report version 'dev' — `zero update` has nothing to compare against

**Status (2026-06-21):** Partially fixed — `634f23f` adds CHANGELOG.md; build-time version stamping (still `dev`) is DEFERRED.
- **Severity:** medium
- **Where:** `/tmp/zero-ux/ (no CHANGELOG); internal/update/update.go:138-141; `/tmp/zero --version` => 'zero dev'`
- **Evidence:** `/tmp/zero --version` => `zero dev`. update.go:138-141: 'Source/dev builds carry a non-semver version ("dev"); ... currentVersion = "0.0.0"'. No CHANGELOG file anywhere. README.md:147 advertises `zero update` and INSTALL.md:91-98 documents `zero update --check`.
- **Impact:** A user running a source build sees version 'dev' and `zero update` treats it as 0.0.0 (will always claim an update is available once any release exists). With no CHANGELOG, a user can't see what changed between versions before updating. Weakens the documented update flow.
- **Fix:** Add a CHANGELOG.md (Keep-a-Changelog style) and document the release/versioning process; consider stamping the real version via -ldflags in the release builder so installed binaries report a semver.

#### L4 · README has no screenshot/GIF for a TUI-first product

**Status (2026-06-21):** Deferred — MANUAL maintainer step (add a screenshot/GIF; not auto-generated).
- **Severity:** low
- **Where:** `/tmp/zero-ux/README.md:1-21 (only ASCII logo + shields.io badges)`
- **Evidence:** grep -niE '!\[|\.png|\.gif|demo|screenshot|asciinema' README.md => only the 4 shields.io badge images; no product screenshot or terminal recording. README.md:38 promises 'A TUI that feels premium'.
- **Impact:** A newcomer evaluating a 'premium TUI' coding agent on the README/repo page can't see what it looks like before investing in a build — significant conversion/first-impression cost for a terminal-UI product.
- **Fix:** Add a screenshot or asciinema/GIF of the TUI (setup wizard + a chat turn) near the top of the README.

#### L5 · npm package name is the un-scoped generic 'zero' — almost certainly unpublishable as-is

**Status (2026-06-21):** Deferred — low (npm package name).
- **Severity:** low
- **Where:** `/tmp/zero-ux/package.json:2`
- **Evidence:** package.json: `"name": "zero"`. README.md:279 calls the npm wrapper a real install path ('the npm wrapper just delegates to it'). bin/zero.js exists as the wrapper.
- **Impact:** `npm publish` of a bare common word like `zero` will collide with an existing package / be rejected, so the advertised npm distribution can't ship under this name. Minor since npm isn't the primary documented path, but it's a release blocker for that channel.
- **Fix:** Scope the package (e.g. @gitlawb/zero) or pick an available name, and document the actual npm install command in the README (currently none is shown).

### Dropped in adversarial verification

| Claim | Why dropped |
|---|---|
| Wide-rune (CJK/emoji) diff/code/grep content truncated by rune-count, "half the line silently lost" | The mechanism (rune-count budget in `truncateRunes`) is real, but the **outer `fitStyledLine(line, innerWidth)` clip** (`rendering.go:1387`) is display-width-aware and absorbs the over-selection: an end-to-end width sweep showed **zero** content loss vs a width-correct budget and the ellipsis is preserved. No user-visible harm → invalid as a UX defect (kept only as a minor `middleTruncate` budget note, M). |
| Default `firecrawl` MCP server 401s on "every first run", breaking the keyless web-search promise | **Split verdict** — one verifier reproduced an **intermittent ~20%** keyless 401 (Firecrawl-side flakiness, surfaced as a mangled Bearer-redacted warning); another got HTTP 200 + 25 tools registered and the warning not emitted by `zero setup`. Not reproducible enough to assert → recorded as Info below, not a finding. |

> **Info (intermittent, unverified):** the keyless `firecrawl` default endpoint returned a 401 on a minority of identical requests in one run; if it flakes, the warning is rendered mangled (the Bearer-redaction eats the message tail). Worth a real-world soak test of the "no API key" web-search claim before launch.

## 5. Open-source readiness checklist

| Item | Status | Note |
|---|---|---|
| **LICENSE** | ❌ FAIL | Missing; README says "being finalized." **Release blocker.** No SPDX headers, no `package.json` license field either. (C2) |
| Working install path | ❌ FAIL | Binary install (`install.sh`/`.ps1`) 404s — no GitHub Release; CI never creates one. `go run` works but needs Go 1.25. (C1) |
| README — newcomer quality | ⚠️ PARTIAL | Good what-it-is/quickstart/config prose + telemetry/privacy statement; but stale Go-version, a `/usage` slash + `--theme` flag that don't exist, and **no screenshot/GIF** for a TUI-first product. |
| CONTRIBUTING.md | ⚠️ PARTIAL | Present and detailed — but mandates issue/PR templates that don't exist. |
| CODE_OF_CONDUCT.md | ❌ FAIL | Missing. |
| SECURITY.md + vuln path | ❌ FAIL | Missing; no private disclosure channel — a baseline expectation for a tool that runs shell/edits files. (H) |
| Issue templates / PR template | ❌ FAIL | `.github/` has only workflows. |
| CHANGELOG + versioning | ❌ FAIL | No CHANGELOG; binaries report `dev` → `zero update` always claims an update. |
| CI coverage | ✅ PASS | Strong: 3-OS matrix, gofmt/vet/govulncheck/deadcode. |
| Telemetry / privacy statement | ✅ PASS | "No telemetry" stated; Firecrawl keyless-routing disclosed. |
| Dependency-license compatibility | ✅ PASS | No copyleft deps linked into the binary; Firecrawl AGPL is network-only and README's reasoning is correct. |
| Reproducible build | ⚠️ PARTIAL | `go build` reproducible on Go 1.25+; binary distribution non-functional. |

## 6. Root-cause synthesis

1. **The release/packaging layer was never finished.** The product is mature but the OSS scaffolding isn't: no LICENSE, no published release (install scripts dead-end), no SECURITY/CoC/CHANGELOG/templates, version pinned at `dev`. Code shipped ahead of distribution. → C1, C2, and most of area G.
2. **Docs/marketing copy drifted from the implementation.** README claims Go 1.24 (needs 1.25), a `/usage` slash command and a `--theme` flag that don't exist, and a keyless local-model on-ramp the SSRF probe blocks; accessibility env vars are undocumented. The pitch outran the code. → H1, H2, H4, M (theme flag), L (a11y env docs).
3. **Raw core errors reach users without a "what to do next" UX layer.** The surface-agnostic core's classified/raw errors surface verbatim: no-key dumps an OpenAI URL instead of `zero setup`; "rejected the key" when none was sent; `doctor` says "pass" with no credential and prints Go `map[...]`; provider errors carry no recovery action. → the no-provider cluster, M1, doctor findings, provider-error finding.
4. **Theme aesthetics undercut the readability bar.** `faint`/`faintest` fail WCAG yet carry functional content (line numbers, diff `@@`/`+++`/`---`, help footer, placeholders); light `accent` is sub-AA. → the contrast findings.

## 7. Prioritized fix order for the OSS launch

**MUST ship (launch blockers):**
1. **Add a `LICENSE`** and name it in README. (C2)
2. **Make an install path actually work** — publish a GitHub Release with the documented assets under the real repo (and fix `ZERO_REPO`/links), or demote binary-install to "coming soon" and lead with `go run`. (C1)
3. **Fix the Go-version claim** (one-line: README → "Go 1.25+"). (H2)
4. **Add `SECURITY.md`** with a private vuln-reporting path. (H)
5. **Stop misleading on first contact:** remove the inert `/input-style` (H3) and the README's nonexistent `/usage` slash + `--theme` flag (H4, M); or wire them.
6. **Unblock the keyless local-model on-ramp** — allow loopback in the provider connectivity probe for user-configured local providers. (H1)
7. **Onboarding errors guide recovery:** no-provider/bad-key → point to `zero setup`/`zero auth`, distinguish *missing* key from *rejected* key, and don't dump a raw OpenAI URL. (no-provider cluster, M1)
8. **`doctor` must not say "Overall: pass" with no credential**, and must stop leaking Go `map[...]`. (H, H)

**SHOULD (strongly recommended before a public launch):**
- Lift `faint`/`faintest` (and light `accent`) to meet a contrast bar — they carry real content. (H, H, M)
- Add `CODE_OF_CONDUCT.md`, `CHANGELOG.md`, issue/PR templates; honor `NO_COLOR=<any value>` per spec; document `NO_COLOR`/`ZERO_THEME`/`ZERO_NO_FADE`. (G + M/L cluster)
- Add a README screenshot/GIF (TUI-first product). (L)
- Give the TUI discoverability of cron/specialists/skills/plugins (slash commands or a `/help` pointer). (M)
- Provider errors: append a recovery action; pre-soak-test the keyless Firecrawl path.

**NICE-TO-HAVE:** scope the npm package name; fix the `Ctrl+E` composer binding shadowed by mouse-toggle; normalize brand capitalization + stamp a real `version`; `middleTruncate` wide-rune budget. (L cluster)

## 8. Confidence notes (what could not be fully verified)

- **Live interactive TUI frames are UNVERIFIED.** Bubble Tea v2's alt-screen defeats `script`/`expect` (only escape setup/teardown captured). The rendered first-run wizard, resize redraw, streaming fade, and narrow-tier painting are assessed from render code + the **passing** visual tests, not captured frames. One color check did succeed via `creack/pty` + an OSC-11 reply (monochrome onboarding frame). **Recommend a pty golden-frame harness in CI** to lock the rendering this audit could only read.
- **Contrast** ratios are computed deterministically from the theme hex tokens (solid), but "readable on a *physically* light terminal" was exercised with a dark OSC-11 reply, so the light-palette verdict rests on the hex values + the luminance tests, not a real light terminal.
- **The Go 1.24 build failure** is reasoned from the `go.mod` directive (only go1.26.4 present in this env), not reproduced on a real 1.24 toolchain.
- **Truecolor / 256 / 16-color downsampling** is library-handled (`colorprofile` writer) and verified by code, not by per-profile pty captures.
- **GitHub Release existence** for `Gitlawb/zero` was probed via the public API (404 for repo/releases/tags) but the environment has no authenticated GitHub access; the repo may be private/unpublished rather than absent — either way the documented install path cannot succeed for a public stranger today.
- **Firecrawl keyless 401** could not be settled (intermittent; see §dropped) — needs a real soak test.
