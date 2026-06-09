# ZERO · Lime TUI rebuild — staged prompt

You are working in a fresh checkout of `github.com/Gitlawb/zero` (Go 1.24,
Bubble Tea v1.3.10, Lip Gloss, Bubbles v1.0.0, Glamour — all in go.mod; add NO
new dependencies).

Mission: **remove both existing TUI designs entirely** — the default cyan
shell (`internal/tui/theme.go` palette + the centered splash in `startup.go`)
AND the whole `zeroline` skin system — and replace them with ONE design:
**Lime**, the near-black, lime-accent chat surface prototyped in
`docs/design/zero_tui_lime.html` (commit the prototype there first; it is the
visual source of truth). The prototype is a web mock — this spec is the
authoritative terminal translation of it. Behavior must not regress: every
slash command, prompt flow, overlay, and safety property that works today
must work after.

This is a fresh clone: file names below were verified against a recent
snapshot, but ALWAYS recon before editing. If a path or symbol has moved,
adapt — do not blindly apply.

**Gate (run after EVERY stage, commit + tag before continuing):**
```
go vet ./...
go test ./...
go run ./cmd/zero-release build
```
Stage 4 additionally runs `go run ./cmd/zero-release smoke`. Never tag a red
tree. AGENTS.MD applies: Go-native commands only.

---

## Design spec — Lime

### Palette — `internal/tui/theme.go` becomes the ONLY place colors exist
| Token      | Hex       | Use |
|------------|-----------|-----|
| bg         | `#070708` | canvas (terminal default bg; do not paint full-bleed) |
| panel      | `#0e0e10` | card backgrounds (tool/diff/read/grep/sessions rows) |
| panel2     | `#121215` | card header rows, picker rows |
| panel3     | `#17171b` | selected/hovered row bg |
| line       | `#242429` | default borders, rules, status separators |
| line2      | `#2e2e34` | emphasized borders |
| ink        | `#ececee` | primary text |
| muted      | `#8b8b93` | secondary text, assistant interim prose |
| faint      | `#5b5b63` | hints, metadata |
| faintest   | `#3a3a40` | line numbers, separators, tool args |
| accent     | `#caff3f` | brand lime: prompts, badge, spinner, focus, final rail |
| green      | `#5dd1a4` | success, diff add sign, ✓ |
| red        | `#ff7a7a` | errors, diff del sign, ✗, deny |
| amber      | `#ffc25c` | permission surfaces, warnings, auto badge |
| blue       | `#7db4ff` | grep file locations, local-model dot |
| addBg      | `#15201d` | diff added-line bg (solid stand-in for green @9% over panel) |
| delBg      | `#241819` | diff deleted-line bg (solid stand-in for red @9%) |
| permBg     | `#1c1915` | permission card bg (amber @6% over panel) |
| selBg      | `#1d2114` | selected picker/suggestion row (accent @8%) |
| addInk     | `#bdeed7` | added-line text |
| delInk     | `#f2c4c4` | deleted-line text |
| onAccent   | `#000000` | text on accent or amber fills |

Terminals cannot alpha-blend or glow — the four solid tints above ARE the
translation of the prototype's rgba/glow effects; do not attempt gradients,
shadows, or blur. Truecolor hex throughout; lipgloss downsamples on limited
terminals (keep the existing explanatory comment style in theme.go).

Keep the `tuiTheme` single-value pattern but rebuild the field set around
these tokens (style per role: badge, userPrompt, sayText, finalRail, toolName,
toolTarget, toolArg, autoTag, statusOk/Err, diffAdd/Del/Meta, permBadge,
permRisk, grepLoc, bashPrompt, stGroup, etc.). No hex outside theme.go.

### Layout — single-column chat surface (matches the existing architecture)
```
zero / ~/dev/zero                    anthropic/claude-sonnet-4-5 · 200K
────────────────────────────────────────────────────────────────────────
❯ add a --version flag to the cli                              (user)
I'll add a --version flag…▌                          (interim, muted)
╭ grep internal/cli  flag\.|RegisterFlag                ⠋ ──────────╮
│ internal/cli/root.go:41  fs := flag.NewFlagSet("zero"…           │
╰ 3 matches ───────────────────────────────────────────────────────╯
╭ edit internal/cli/root.go                    auto  ✓ ────────────╮
│ internal/cli/root.go            new file          +6 −1          │
│   41 │   fs.BoolVar(&o.Version, "version", false, …   (addBg)    │
╰──────────────────────────────────────────────────────────────────╯
│ Done — the CLI now prints its version.       (final: accent rail)
● done · 2 tools · 8.4s                                  (done line)

❯ describe a task for zero…                                (composer)
────────────────────────────────────────────────────────────────────────
● anthropic │ claude-sonnet-4-5 │        │ 12.4K tok │ interactive │ ⏵⏵ auto
```
Render order stays: title line → rule → transcript → composer → rule →
status line. NO alt-screen change, NO panes, NO mouse — keep `run.go`
exactly as-is including its no-mouse-capture comment.

### Title bar (replaces `headerBar`)
Left: badge — `lipgloss` style bg=accent fg=onAccent bold, content ` 0 ` —
then `zero` (ink, bold, lowercase), ` / ` (faintest), cwd via existing
`shortenPath` (muted), git branch (faint) when present. Right:
`provider/model` (ink) + ` · ctx` (faint) where ctx comes from
`modelregistry.ContextLimits.ContextWindow` formatted like `200K`. Reuse the
existing width-candidate fallback pattern (`startupHeaderLine`) so segments
drop gracefully: full → drop ctx → drop cwd → badge+model only. Below it,
one rule line in `line`.

### Empty state (DELETE the splash entirely)
Remove `startupView`, `startupHeader`, `commandChips`, the figlet
`zeroLogoLines`, gap math, and the `showSplash` field/branch. The app boots
into the chat surface; when the transcript is empty, the stream area shows,
vertically centered:
- a 5–6 line block-art `0` in accent (derive the glyph from the `O` columns
  of the old figlet constant before deleting it),
- tagline (muted): `a std-lib-first coding agent · bring your own key · no lock-in`,
- hint (faint): `running zero against <provider/model>` with the model in ink,
- three suggestion chips, one per line: `❯` (accent bold) + suggestion (ink)
  inside a `line`-bordered rounded box; pressing `1`–`3` while the composer
  is empty inserts that suggestion. Define suggestions as a string slice in
  the tui package (e.g. add a --version flag / explain internal/agent/loop.go /
  fix the failing test in internal/tools).

### Stream blocks (restyle `transcript.go` / `rendering.go` rows)
- **user** (`rowUser`): `❯ ` accent bold + ink text. Blank line above turns.
- **assistant interim** (`rowAssistant` while streaming): muted text, wrapped
  to `min(width-4, 74)` columns, trailing `▌` cursor in accent while the
  turn is pending (drives off existing pending state — no new ticker).
- **final answer** (last assistant row of a turn): a `│` accent rail gutter
  on every wrapped line + ink text, same 74-col measure. If distinguishing
  interim vs final is not already possible from row data, mark the row at
  append time — do not re-parse text.
- **done line** (end of a turn): `●` green (red on error) + faint
  `done · N tools · Xs` with faintest `·` separators, derived from data the
  model already has (tool rows this turn + existing usage/timing if present;
  omit segments that don't exist rather than inventing counters).
- **system / error notes** (`rowSystem`/`rowError` equivalents): one-line
  bordered note — system: faint on panel with `line` border; denial/error:
  red text, red-tinted border. Keep content unchanged.

### Tool cards (replaces `titledCard`/tool rows for `rowToolCall`+`rowToolResult`)
Rounded border card on panel bg. Head row on panel2 with a bottom rule:
status glyph + tool name (ink bold) + target (muted, middle-truncated) +
one-line arg via the existing `argHint` (faintest, right side) + optional
`auto` tag (amber text in amber-bordered mini box) when the call was
auto-approved by mode or a stored grant. Status glyph: running =
`bubbles/spinner` MiniDot in accent · ok = `✓` green · error = `✗` red.
Border tint follows state: line → accent-ish `#5a6b2e` while running →
red-mix on error (define both as theme tokens). Bodies by result shape,
reusing the existing detection where present:
- **diff** (`looksLikeDiff`): head row = path (ink) + `NEW FILE` tag (green
  on addBg) when applicable + right-aligned `+N` green / `−N` red counts;
  body lines = 4-col right-aligned line number (faintest) + sign col
  (green/red) + text — added rows on addBg in addInk, deleted on delBg in
  delInk, context muted. Keep the 16-line cap + `… N more lines` footer.
- **read**: same gutter, muted text, head shows path + `Lstart–Lend` (faint).
- **bash**: cmd row `❯` accent bold + command (ink) above a rule; output
  muted (stderr-ish in delInk), indented 2; footer `exit 0` green /
  `exit N` red when an exit code is in the result.
- **grep**: rows `file:line` (blue) + text (muted, truncated); footer faint
  `N matches`.
- Anything else: current generic output rendering, restyled to muted on panel.

### Permission card (restyle the existing `pendingPermission` prompt — keys unchanged)
Card with amber-mixed border on permBg. Top row: `PERMISSION` badge (onAccent
on amber, bold) + `risk: <low|medium|high>` (amber) from the request's
existing risk data. Body: `<tool>` (amber bold) + target (ink) + reason
(muted). Action row of bordered key-chips: `[a] allow once` (onAccent on
accent), `[y] always` (ink, accent border), `[d] deny` (ink, red on hover is
web-only — just red-bordered), `[esc]` hint faint. After resolution collapse
to one faint line: `allowed once · <tool>` green / `always · <tool>` green /
`denied · <tool>` red. Ask-user and spec-review prompts get the same card
language with `line` borders. Do NOT alter `handlePermissionKey`,
`nextPermissionMode`, or grant-store semantics.

### Composer + status line
Composer: `❯` accent bold + the existing `bubbles/textinput`, borderless
(remove the bordered input block), placeholder `describe a task for zero…`,
switching to `running… ctrl+c to interrupt` while pending; right-aligned
faint hint `run ↵` (idle, only when input is non-empty) / `esc stop`
(pending). Rules above composer and above status line in `line`.
Status line groups separated by ` │ ` (line color): `●` accent + provider ·
model id · flexible gap · `<X.XK> tok` (existing usage segment data,
keep cost when priced) · `interactive` · permission mode (reuse
`modeLabel()`: `⏵⏵ auto-approve` green / `ask` amber / `unsafe` red) ·
optional green `always: <n> grants` when the sandbox grant store exposes a
cheap count (omit otherwise — do not add plumbing).

### Overlays (restyle in place, identical key handling)
- Autocomplete: rows on panel, selected row on selBg with accent `❯` marker,
  name ink / desc faint.
- Picker (`/model`, `/provider`, etc.): bordered panel; rows = provider dot
  (accent remote, blue local) + id (ink) + right meta `ctx · KEY_ENV`
  (faint/faintest) when the registry/provider catalog already exposes them;
  selected row selBg. Title row + `↑/↓ · ⏎ · esc` hints faint.
- Sessions: `/resume` (alias `/sessions`) keeps its existing flow; restyle
  its list as stacked cards — id (accent) + age (faint) top row, title (ink),
  meta line (faint with faintest dots). No new persistence behavior.

### Explicitly OUT of scope
The prototype's `text/json` toggle visualizes the headless
`--output-format stream-json` CLI mode (see docs/STREAM_JSON_PROTOCOL.md) —
not a TUI feature. The TweaksPanel in the HTML is a design harness — ignore
it. No mouse support, no model dropdown (keyboard picker is the translation).

---

## Adaptive requirements (treat as acceptance criteria)
- Width tiers, re-evaluated on every `WindowSizeMsg`:
  **≥100** full spec · **80–99** drop tool-arg column, header ctx, status
  `interactive` group · **60–79** drop diff/read line-number gutters, badge
  renders as `0` without padding, status keeps provider+tokens+mode only ·
  **<58** (current `minStartupWidth`) single-segment header, cards lose
  side borders (keep top/bottom rules), composer + mode line only.
- Text measure: say/final wrap at `min(width-4, 74)`; paths middle-truncate
  (`internal/…/root.go`); never emit a styled line wider than the terminal
  (extend the existing `fitStyledLine`/`truncateRunes` helpers — reuse, don't
  fork).
- Height: when the terminal is shorter than the frame, transcript trims from
  the top (existing inline behavior) — verify no overlap of composer/status.
- Color: nothing may rely on truecolor to be legible — check every pairing
  at 256 colors mentally; the four tint tokens must remain darker than ink.
- Tests for the tier logic: table-driven across widths {58, 70, 80, 100, 120}
  asserting which segments render, using the existing ANSI-stripping helpers.

---

## Stages

### Stage 0 — recon + delete the `zeroline` design system  *(tag: `tui-lime-s0`)*
Recon first: `grep -rni zeroline --include='*.go' .` and read
`internal/tui/options.go`, `internal/cli/app.go`. Then remove completely:
`internal/zeroline/` (whole package + tests), `internal/tui/zeroline_view.go`
(+ test), `internal/cli/zeroline.go`, the `case "zeroline":` dispatch and the
zeroline help-text line in `internal/cli/app.go`, the skin parameter on
`runInteractiveTUIWithSkin` (rename to `runInteractiveTUI`), the `Skin`,
`ThemeVariant`, `ThemeDark` fields in `tui.Options`, the model's `skin`
field and every `m.skin == "zeroline"` branch (View dispatch, `/theme`
special-case, update paths, `picker.go`, `image_attach.go`). Update or
delete affected tests; the zeroline grep must return zero hits. Gate, tag.

### Stage 1 — Lime palette on the existing layout  *(tag: `tui-lime-s1`)*
Rewrite `theme.go` to the token table. Restyle every current render site
(header, transcript rows, titledCard/diffCard, overlays, status line,
prompts) to the new tokens WITHOUT changing structure yet — this isolates
the string-assertion churn in `model_test.go` / `session_test.go` /
`command_polish_test.go` from the layout work. Prefer asserting plain
content via the ANSI-stripping helpers over styled bytes. Gate, tag.

### Stage 2 — chat-surface components  *(tag: `tui-lime-s2`)*
Title bar + rule, empty state replacing the splash (splash code deleted),
stream blocks (user/interim/final/done/notes), tool cards with all four
result bodies, MiniDot spinner, composer + status line redesign. Add
rendering tests per block type with synthetic rows. Gate, tag.

### Stage 3 — interactive surfaces  *(tag: `tui-lime-s3`)*
Permission card + resolved collapse + auto tag, ask-user and spec-review
cards, autocomplete/picker restyle with model-row metadata, `/resume`
sessions card list, suggestion-chip `1–3` insertion. Key handling and
semantics byte-identical to before — assert that in tests where they
already exist. Gate, tag.

### Stage 4 — adaptive polish + docs  *(tag: `tui-lime-s4`)*
Implement + test the width tiers, sweep remaining render sites (spec mode,
command center, doctor output if styled) for stray old-palette styles,
update `internal/cli/app.go` help text and README's TUI section, manual run
`go run ./cmd/zero` at 58/80/100/120 cols (no wrapped rules, no orphan ANSI,
prompts render correctly). Full gate + `go run ./cmd/zero-release smoke`.
Gate, tag.

---

## Hard constraints
- No new module dependencies; charmbracelet stack only. No mouse capture.
- Zero behavior change to permissions, grants, ask-user, spec review,
  sessions, slash commands, autocomplete, image attach, compaction, rewind.
- All colors in `theme.go`; renderers consume named styles only.
- Reuse existing helpers (`fitStyledLine`, `truncateRunes`, `argHint`,
  `shortenPath`, header candidates) instead of writing parallel ones.
- Comment style matches the repo: explain WHY at non-obvious decisions.

## Definition of done
One design in the tree (no zeroline references, no splash, no cyan tokens),
the Lime chat surface matching this spec at ≥100 cols and degrading per the
width tiers below that, every pre-existing flow keyboard-reachable and
visually consistent, `go test ./...` + release build + smoke fully green.
