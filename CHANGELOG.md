# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
aims to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html) once the first release is
tagged. Until then, source builds report the version `dev`.

## [0.3.0](https://github.com/Gitlawb/zero/compare/v0.2.0...v0.3.0) (2026-07-09)


### Features

* gate project-scoped hooks, plugins, and MCP servers behind workspace trust ([#529](https://github.com/Gitlawb/zero/issues/529)) ([a880ce8](https://github.com/Gitlawb/zero/commit/a880ce80a6ec72da511fb9bdf6dd69291c72a64b))
* **modelregistry:** infer reasoning efforts for Hunyuan and vendor-prefixed model ids ([#599](https://github.com/Gitlawb/zero/issues/599)) ([92a92ce](https://github.com/Gitlawb/zero/commit/92a92ceb29dc63f5216ab140ef9a6dd9afe17df8))
* **providers:** OAuth login profiles and list-first /provider manager ([#560](https://github.com/Gitlawb/zero/issues/560)) ([1655056](https://github.com/Gitlawb/zero/commit/16550569c1be615cfaf244dab25909ec37f6dee6))
* **tui:** remember recent provider+model selections in /model picker ([#568](https://github.com/Gitlawb/zero/issues/568)) ([d0c4e62](https://github.com/Gitlawb/zero/commit/d0c4e62cd429a0614c15296934c740a08bc0e07b))
* **tui:** show CLI version on the startup home screen ([#538](https://github.com/Gitlawb/zero/issues/538)) ([fd69233](https://github.com/Gitlawb/zero/commit/fd69233e334f1823a06b5794085a9255b3abdfa8))
* voice dictation (speech-to-text) ([#557](https://github.com/Gitlawb/zero/issues/557)) ([87158a1](https://github.com/Gitlawb/zero/commit/87158a1c90b4f91fc5f2bb8178ebaf46d7654680))


### Bug Fixes

* address bugs found in a multi-agent codebase audit ([#481](https://github.com/Gitlawb/zero/issues/481)) ([008bc9b](https://github.com/Gitlawb/zero/commit/008bc9b3f3ba13c7d4822b9559b020f381ff555b))
* **agent:** keep tools exposed for max-turn finalization ([#533](https://github.com/Gitlawb/zero/issues/533)) ([3f0503b](https://github.com/Gitlawb/zero/commit/3f0503bc2312ae29d5ade784d8824dc9a3524958))
* **auth:** persist OpenRouter API key after login ([#595](https://github.com/Gitlawb/zero/issues/595)) ([2a062aa](https://github.com/Gitlawb/zero/commit/2a062aa4014c6cd5e20a57dad4a685f88966f109))
* bump Go to 1.26.5 for crypto/tls fix (GO-2026-5856) ([#607](https://github.com/Gitlawb/zero/issues/607)) ([a7cfb99](https://github.com/Gitlawb/zero/commit/a7cfb99fed7b88ebc09a2f251cb82864d3c2cade))
* gitignore Windows sandbox helpers and npm version marker ([#578](https://github.com/Gitlawb/zero/issues/578)) ([25653f6](https://github.com/Gitlawb/zero/commit/25653f686c016f95b81ceb7ff5d5452d37c4d4f3))
* **mcp:** silence startup warning for unconfigured default servers ([#563](https://github.com/Gitlawb/zero/issues/563)) ([302f58b](https://github.com/Gitlawb/zero/commit/302f58bb5f2a03ec7230354ed4747e4e55c16c50))
* **mcp:** skip RFC 8414 discovery when OAuth endpoints are preconfigured ([#586](https://github.com/Gitlawb/zero/issues/586)) ([8a52d98](https://github.com/Gitlawb/zero/commit/8a52d98cad7cd0086dee9aede4ce477e432bd385))
* **modelregistry:** reject oversized models.dev cache responses ([#602](https://github.com/Gitlawb/zero/issues/602)) ([66a6396](https://github.com/Gitlawb/zero/commit/66a63964149fb2f07e646e5f1987627c5cd9ac28))
* **provider:** stop dropping custom no-auth providers on restart ([#558](https://github.com/Gitlawb/zero/issues/558)) ([ba99fa8](https://github.com/Gitlawb/zero/commit/ba99fa8d487fb28f2700e0ff10b2a25c75303cf7))
* **sandbox:** Windows-appropriate suggestions when blocking interactive commands ([#414](https://github.com/Gitlawb/zero/issues/414)) ([ba4c007](https://github.com/Gitlawb/zero/commit/ba4c00755dc1c31b3dcca18e50b20f62c6bf5d1f))
* **tools:** block MSYS and WSL shells under the Windows sandbox ([#587](https://github.com/Gitlawb/zero/issues/587)) ([0666818](https://github.com/Gitlawb/zero/commit/066681855b80e3baf5d07d7397610b25f724e353))
* **tools:** platform-specific pager suggestions, quote/caret-safe cd detection ([#543](https://github.com/Gitlawb/zero/issues/543)) ([8b248f4](https://github.com/Gitlawb/zero/commit/8b248f4e1198dc86ab332a697fba4cf520823cbd))
* **tools:** Windows cmd.exe quoting guidance and clipboard escaping fix ([#468](https://github.com/Gitlawb/zero/issues/468)) ([f10ed0c](https://github.com/Gitlawb/zero/commit/f10ed0c893ce6de08923f143d681ba96f0fcfe3a))
* **tui:** bypass toggleSidebar and toggleMouse global shortcuts when composer is non-empty ([#576](https://github.com/Gitlawb/zero/issues/576)) ([c7346fb](https://github.com/Gitlawb/zero/commit/c7346fbcf01fe7e70ed2fcfccbf9965e5985727b))
* **tui:** paste protection for Termux char-by-char paste ([#573](https://github.com/Gitlawb/zero/issues/573)) ([8e9149f](https://github.com/Gitlawb/zero/commit/8e9149f1a296bebebf95cae9dd2a7c5156c9dbb6))
* **tui:** update picker_test for switchProviderModel 4-value return ([#589](https://github.com/Gitlawb/zero/issues/589)) ([8f15650](https://github.com/Gitlawb/zero/commit/8f156506dc92449bd24caa14e36f28477ba00fff))
* **update:** clearer error on unsupported release platform (android/termux) ([#603](https://github.com/Gitlawb/zero/issues/603)) ([1fc9b2d](https://github.com/Gitlawb/zero/commit/1fc9b2d25c79e089f34cb7b5b6a7f7c7b8233123))
* **update:** support safe symlink extraction during updates ([#575](https://github.com/Gitlawb/zero/issues/575)) ([ce9cb91](https://github.com/Gitlawb/zero/commit/ce9cb912958a3d9ae0b052bfebb849c28e0e719b))
* warn about untracked scratch files left behind after a run ([#571](https://github.com/Gitlawb/zero/issues/571)) ([062328b](https://github.com/Gitlawb/zero/commit/062328b632a2d27353a6d47c522bbd22d7282539))


### Performance Improvements

* **grep:** stop content scan after head limit ([#601](https://github.com/Gitlawb/zero/issues/601)) ([8a05e64](https://github.com/Gitlawb/zero/commit/8a05e6486f7a63281b869064db03dfc5531e6a04))

## [0.2.0](https://github.com/Gitlawb/zero/compare/v0.1.0...v0.2.0) (2026-07-06)


### Features

* add --auto flag for LLM-generated commit messages ([#423](https://github.com/Gitlawb/zero/issues/423)) ([b0abde7](https://github.com/Gitlawb/zero/commit/b0abde7d0697e808480cd59d69a6f4d0c6320475))
* add zero changes push and pr subcommands, and extra repo-info metrics ([#391](https://github.com/Gitlawb/zero/issues/391)) ([2312abe](https://github.com/Gitlawb/zero/commit/2312abe5ddd95f4c6ef373cfb61cc03092f48cdd))
* agent quality, caching, retry, and tooling upgrades ([#506](https://github.com/Gitlawb/zero/issues/506)) ([3c81fea](https://github.com/Gitlawb/zero/commit/3c81fea22873ee3df7fc97b10cb4f77792706c4b))
* **agent:** curb over-engineering the solution in the editing discipline ([#517](https://github.com/Gitlawb/zero/issues/517)) ([f4c998a](https://github.com/Gitlawb/zero/commit/f4c998ac30c4f07ff313a2d706791e857293be49)), closes [#516](https://github.com/Gitlawb/zero/issues/516)
* **agent:** inject per-user config.UserConfigDir()/zero/ZERO.md guidelines into system prompt ([#475](https://github.com/Gitlawb/zero/issues/475)) ([7b10aab](https://github.com/Gitlawb/zero/commit/7b10aab74bf14a01166b2cea22deab79bba9850b))
* **openai:** forward prompt_cache_key for server-side prefix cache routing ([#515](https://github.com/Gitlawb/zero/issues/515)) ([87e7e69](https://github.com/Gitlawb/zero/commit/87e7e69afd18b5539579856f3a61c6a95bc445ae))
* **providers:** add `zero providers models` to discover a provider's models ([#386](https://github.com/Gitlawb/zero/issues/386)) ([0bc8074](https://github.com/Gitlawb/zero/commit/0bc8074c97b0310e4a9d70c3f967003ee5e8a59f))
* **providers:** add KiloCode and OpenCode provider support ([#388](https://github.com/Gitlawb/zero/issues/388)) ([b1ccb6d](https://github.com/Gitlawb/zero/commit/b1ccb6d9c1875377f5e5ea81a1304edd1e41ab4f))
* **providers:** add Meituan LongCat catalog preset ([#424](https://github.com/Gitlawb/zero/issues/424)) ([b4275e3](https://github.com/Gitlawb/zero/commit/b4275e350472b2490212bf814709819d354c1216))
* **providers:** split minimax zai into intl cn ([#398](https://github.com/Gitlawb/zero/issues/398)) ([aaad4d2](https://github.com/Gitlawb/zero/commit/aaad4d271270f41af837b6f3b60ae80beba0c645))
* require manual approval before npm publish + drop release-as pin ([#369](https://github.com/Gitlawb/zero/issues/369)) ([bd89a1f](https://github.com/Gitlawb/zero/commit/bd89a1f451643c1b65ec803070abc7b116631ebe))
* **sandbox:** unelevated Windows fallback tier instead of prompts-only degrade ([#427](https://github.com/Gitlawb/zero/issues/427)) ([b9ddd6f](https://github.com/Gitlawb/zero/commit/b9ddd6f42138312a1fee8d8bb67c46c8eb1dea2f))
* support shift enter for composer newlines ([#462](https://github.com/Gitlawb/zero/issues/462)) ([daf65e0](https://github.com/Gitlawb/zero/commit/daf65e0af9a040314d4ab337b0ad59c55416b7bc))
* **tui:** /loop — repeat a prompt or command on an interval or self-paced ([#502](https://github.com/Gitlawb/zero/issues/502)) ([387fe67](https://github.com/Gitlawb/zero/commit/387fe67ee7cd81317c9c969f5906a4437080fea3))
* **tui:** add search/filter to provider picker in setup wizard ([#400](https://github.com/Gitlawb/zero/issues/400)) ([2fcea71](https://github.com/Gitlawb/zero/commit/2fcea71778d23e050c93409c471aef45b68c1621))
* **update:** add zero upgrade command to apply self-updates ([#461](https://github.com/Gitlawb/zero/issues/461)) ([5f36349](https://github.com/Gitlawb/zero/commit/5f36349c1884e81fa9bc66bb5fe813b627e897b7))


### Bug Fixes

* **action:** keep provider key scoped to zero step ([#448](https://github.com/Gitlawb/zero/issues/448)) ([407a927](https://github.com/Gitlawb/zero/commit/407a92739ff508cba32d2c12b3f36f0efcdd54c3))
* add android platform support for Termux npm install ([#455](https://github.com/Gitlawb/zero/issues/455)) ([9bd93c6](https://github.com/Gitlawb/zero/commit/9bd93c62f8d57fb74057284aa66a1b6e1429dcdd)), closes [#449](https://github.com/Gitlawb/zero/issues/449)
* **agent:** reject a malformed additional_permissions payload before prompting ([#453](https://github.com/Gitlawb/zero/issues/453)) ([e4f760e](https://github.com/Gitlawb/zero/commit/e4f760ee8bd57299cd2fcb37e8e23130037c2607))
* allow non-TLS connections to private-network provider endpoints ([#444](https://github.com/Gitlawb/zero/issues/444)) ([1d86384](https://github.com/Gitlawb/zero/commit/1d8638466ca31517eb9db2b9353d3dce1cbeeabc))
* **auth:** route zero auth login chatgpt to the dedicated ChatGPT flow ([#443](https://github.com/Gitlawb/zero/issues/443)) ([305a62c](https://github.com/Gitlawb/zero/commit/305a62c954ca6cec00bc58d5398f933415156aff))
* **config:** fall back to a usable saved provider instead of forcing full re-onboarding ([#410](https://github.com/Gitlawb/zero/issues/410)) ([c60ad87](https://github.com/Gitlawb/zero/commit/c60ad8729f79bb841114d352ee2d2fe29d5d0e41))
* **config:** let a gateway ANTHROPIC_BASE_URL resolve as anthropic-compatible ([#497](https://github.com/Gitlawb/zero/issues/497)) ([30dd7c3](https://github.com/Gitlawb/zero/commit/30dd7c3112ad22d42fa12b5addd4e38f4beda42a)), closes [#479](https://github.com/Gitlawb/zero/issues/479)
* **config:** unbrick first-run setup — default google/anthropic models, enter setup on fixable config errors ([#385](https://github.com/Gitlawb/zero/issues/385)) ([72eed06](https://github.com/Gitlawb/zero/commit/72eed06b4f94c43d75d31fe54a58d2f566de059e))
* **config:** use ~/.config on macOS and enter setup when no provider ([#371](https://github.com/Gitlawb/zero/issues/371)) ([#372](https://github.com/Gitlawb/zero/issues/372)) ([027a8f2](https://github.com/Gitlawb/zero/commit/027a8f2768b17b89f5c8270887f156e2ccda69ea))
* **docs:** rename AGENTS.MD &gt; AGENTS.md ([#438](https://github.com/Gitlawb/zero/issues/438)) ([4266baf](https://github.com/Gitlawb/zero/commit/4266baf222df583ed2078b776687f12d496475b5))
* **gemini:** strip unsupported JSON Schema fields from tool declarations ([#374](https://github.com/Gitlawb/zero/issues/374)) ([39e7100](https://github.com/Gitlawb/zero/commit/39e7100674150144a1152e3110c64c7cf0321d64)), closes [#373](https://github.com/Gitlawb/zero/issues/373)
* **install:** persist install dir to user PATH on Windows ([#407](https://github.com/Gitlawb/zero/issues/407)) ([bdb1b0e](https://github.com/Gitlawb/zero/commit/bdb1b0ecd15859b1712a6037d296dace7f9c3c3f))
* **mcp:** block cross-origin credential redirects ([#396](https://github.com/Gitlawb/zero/issues/396)) ([f915f70](https://github.com/Gitlawb/zero/commit/f915f70e5a3096e2419fa8d961a0f84a626fa4a9))
* **oauth:** treat Windows ERROR_ACCESS_DENIED as lock contention in createSecretFile ([#445](https://github.com/Gitlawb/zero/issues/445)) ([d05e914](https://github.com/Gitlawb/zero/commit/d05e9148a7f79f67d1d3c31fca2775f21fbd331e))
* **openai:** handle Ollama reasoning stream deltas ([#486](https://github.com/Gitlawb/zero/issues/486)) ([f6c0606](https://github.com/Gitlawb/zero/commit/f6c060631e18e082dda24cc4dc0903c31c2120d6))
* preserve conversation context in exec prompts ([#460](https://github.com/Gitlawb/zero/issues/460)) ([949ee43](https://github.com/Gitlawb/zero/commit/949ee43f71e5cb7fab4695c5cb7b442fe4ecfbf7))
* **provider-wizard:** allow multiple custom OpenAI-compatible providers ([#403](https://github.com/Gitlawb/zero/issues/403)) ([3fbbd28](https://github.com/Gitlawb/zero/commit/3fbbd28e4c586822cc4312c86232d94befe56e87))
* **sandbox:** fix nested pipe creation under the Windows restricted token ([#456](https://github.com/Gitlawb/zero/issues/456)) ([563a6db](https://github.com/Gitlawb/zero/commit/563a6dbe91e65d5daeefd7626e8a77e30a6d8fb2))
* **sandbox:** gate /tmp test assertions on GOOS, not path existence ([#426](https://github.com/Gitlawb/zero/issues/426)) ([f653dca](https://github.com/Gitlawb/zero/commit/f653dcac363fb69ad7be5b35e6e0fa6d2bce476d))
* **sandbox:** self-heal a corrupt unelevated setup marker ([#437](https://github.com/Gitlawb/zero/issues/437)) ([8d0c5fe](https://github.com/Gitlawb/zero/commit/8d0c5feccb8bdbfb015df0508aa6e3bcbd1fd0e8))
* **specialist:** cap max specialist nesting depth ([#491](https://github.com/Gitlawb/zero/issues/491)) ([177442c](https://github.com/Gitlawb/zero/commit/177442cfe4015bd8df04cc9894f98b468ee796d4))
* Termux/Android support — PRoot scroll, SIGSYS sandbox, build docs ([#509](https://github.com/Gitlawb/zero/issues/509)) ([0f69d99](https://github.com/Gitlawb/zero/commit/0f69d995e9b586b774f66c066b21abab5e03024a))
* **tools:** Block MSYS coreutils under Windows sandbox ([#476](https://github.com/Gitlawb/zero/issues/476)) ([81aad58](https://github.com/Gitlawb/zero/commit/81aad58d97839e51c068e2f08907618991fdc3fb))
* **tools:** CRLF line ending mismatch in edit_file tool on Windows ([#378](https://github.com/Gitlawb/zero/issues/378)) ([33dc7ae](https://github.com/Gitlawb/zero/commit/33dc7ae2cc82c5389675531e1416856dae7151ce))
* **tools:** fix cmd.exe /S/C corrupting commands with embedded quotes ([#465](https://github.com/Gitlawb/zero/issues/465)) ([190241b](https://github.com/Gitlawb/zero/commit/190241bd593f43211b766e0b13c8e89802d4bb37))
* **tools:** flag piped POSIX utilities before running on Windows ([#412](https://github.com/Gitlawb/zero/issues/412)) ([5658a36](https://github.com/Gitlawb/zero/commit/5658a366274fc59a9d5336b06a21019c9c25cbf1))
* **tools:** make grep and glob respect run cancellation ([#464](https://github.com/Gitlawb/zero/issues/464)) ([ba6c026](https://github.com/Gitlawb/zero/commit/ba6c0264697b7d7ed479f6e782fba9700a481e3d))
* **tools:** require permission before web_search requests ([#382](https://github.com/Gitlawb/zero/issues/382)) ([960db96](https://github.com/Gitlawb/zero/commit/960db9660e4e31dc588fe8f7d6f116ff5e225566))
* **tui:** compose help overlay through the viewport overlay pipeline ([#421](https://github.com/Gitlawb/zero/issues/421)) ([5b2b4de](https://github.com/Gitlawb/zero/commit/5b2b4dea1aaf9e0f68baa25e97e83296fb17b1a2))
* **tui:** keep the profile name on /model switch so the stored key resolves ([#441](https://github.com/Gitlawb/zero/issues/441)) ([9134148](https://github.com/Gitlawb/zero/commit/9134148f4df3e4e556fba6c2f8babfdf6fcfeee1)), closes [#440](https://github.com/Gitlawb/zero/issues/440)
* **tui:** resolve every permission request so the agent can't deadlock ([#397](https://github.com/Gitlawb/zero/issues/397)) ([952788f](https://github.com/Gitlawb/zero/commit/952788f72d32957659fe004521fcc8372b9ba9b4))
* **tui:** show an M suffix for million-scale token counts ([#457](https://github.com/Gitlawb/zero/issues/457)) ([0562e3b](https://github.com/Gitlawb/zero/commit/0562e3bef7df2328610a48a1e81632a8da4aec64))
* **tui:** title /model rows by model name, not the catalog description ([#395](https://github.com/Gitlawb/zero/issues/395)) ([cdf9d83](https://github.com/Gitlawb/zero/commit/cdf9d839ae57a729f292f36f7c5b0c67b41b288d))


### Performance Improvements

* cache TUI model registry ([#496](https://github.com/Gitlawb/zero/issues/496)) ([e7d88b4](https://github.com/Gitlawb/zero/commit/e7d88b4b518049733da25a8447c00144bd1da518))
* universal tool-output ceiling with spill + async post-edit diagnostics ([#518](https://github.com/Gitlawb/zero/issues/518)) ([95ccd5b](https://github.com/Gitlawb/zero/commit/95ccd5bc327f6fb464ff0239f7229de789f578dc))

## 0.1.0 (2026-07-02)


### Features

* publish zero to npm via release-please ([#367](https://github.com/Gitlawb/zero/issues/367)) ([8eccc26](https://github.com/Gitlawb/zero/commit/8eccc2669887bc38d35bc16a315c888e4d9ec43a))
* **tui:** FILES sidebar panel with click-to-select and file drill-in ([#365](https://github.com/Gitlawb/zero/issues/365)) ([142c548](https://github.com/Gitlawb/zero/commit/142c548c89a8652ce300e64ddf1228ee36df7606))


### Bug Fixes

* **auth:** propagate credentials to every provider-build surface and pin children to the live provider ([#366](https://github.com/Gitlawb/zero/issues/366)) ([6e0a665](https://github.com/Gitlawb/zero/commit/6e0a665118fe0e09c4b07d482dd18f86045acd2b))

## [Unreleased]

### Added
- `SECURITY.md` with a private vulnerability-reporting path, `CODE_OF_CONDUCT.md`, this changelog, and
  GitHub issue/PR templates.
- Interactive `/theme` picker: bare `/theme` opens a popup that live-previews each palette as you move
  and applies on select (Esc reverts).
- Ten built-in color themes alongside the `dark`/`light` built-ins — `dracula`, `nord`, `gruvbox`,
  `tokyo-night`, `catppuccin`, `one-dark`, `solarized-dark`, `rose-pine`, `everforest`, and
  `solarized-light` — selectable via `/theme <name>`, `--theme <name>`, or `ZERO_THEME`. Every palette
  is contrast-audited to WCAG AA. The built-in light theme was reworked for legibility.
- `--theme <name>` flag for the TUI, accepting `auto` or any registered theme (previously only the
  `ZERO_THEME` env var existed).
- "Accessibility / Appearance" section in the README documenting `NO_COLOR`, `ZERO_THEME`, `/theme`,
  and `ZERO_NO_FADE`.

### Changed
- Provider connectivity health checks now allow loopback hosts for explicitly user-configured local
  providers (Ollama / LM Studio), so the keyless local-model path verifies instead of failing with
  "localhost hosts are blocked". The SSRF guard for fetched/remote URLs is unchanged.
- Auth (401/403) errors now show a curated, actionable message pointing at `zero auth` / setup; the
  raw upstream body is shown only under a verbose/debug flag.
- No-provider / missing-key errors now point at `zero setup` and `zero auth`, and distinguish a
  missing key from a rejected key.
- `zero doctor` no longer reports "Overall: pass" when no provider credential is configured, and
  formats the missing-language-server list for humans (no raw Go `map[...]`).
- Raised the `faint`/`faintest` theme tokens (and the light-theme accent) to meet WCAG AA contrast for
  the content they carry.
- `NO_COLOR` is now honored for any non-empty value, per the no-color.org spec.

### Removed
- The inert `/input-style` slash command (it had no backend).

### Fixed
- README/`go.mod` Go-version mismatch and other stale public-release docs claims.
