# OAuth logins & using ChatGPT / Claude subscriptions

Zero supports two distinct things people mean by "log in with OAuth":

1. **OAuth login for a provider/gateway** that issues a *standard* bearer token —
   fully built in (`zero auth login …`). Zero attaches the token to model calls
   automatically.
2. **Using a ChatGPT or Claude *subscription*** (Plus / Pro / Max) instead of a
   pay-per-token API key — only possible through a **local proxy**, for the
   reasons documented below. Zero ships a convenience preset and this recipe.

---

## 1. OAuth login for a provider or gateway (built in)

For any OAuth 2.0 / OIDC provider that returns a normal access token usable as
`Authorization: Bearer …` on its API, configure it with `ZERO_OAUTH_<NAME>_*`
env vars and log in:

```sh
export ZERO_OAUTH_ACME_CLIENT_ID=…
export ZERO_OAUTH_ACME_AUTHORIZE_URL=https://acme.example/oauth/authorize
export ZERO_OAUTH_ACME_TOKEN_URL=https://acme.example/oauth/token
export ZERO_OAUTH_ACME_SCOPES="openid profile"
zero auth login acme            # browser (loopback); --device for headless
zero auth status
```

When a login exists for a provider, the **OpenAI and Anthropic** providers send
`Authorization: Bearer <fresh-token>` (auto-refreshed; one refresh-and-retry on a
`401`) instead of the API key. With no login they use the API key exactly as
before. Tokens are stored 0600 (or the OS keyring with
`ZERO_OAUTH_STORAGE=keyring`) and never logged. See `zero auth --help`.

### In the setup wizard (`/provider`)

Running `/provider` opens a **"How do you want to connect?"** chooser:

```
❯ Sign in with OAuth                 One-click browser login (OpenRouter, xAI)
  Paste an API key / browse providers  Any of 20+ providers, local, or a proxy
```

Pick **Sign in with OAuth** → the list of providers that do real OAuth → choose one:

```
❯ OpenRouter    browser sign-in · creates a key
  xAI (Grok)    browser or device code
```

- **OpenRouter / xAI** are real OAuth: your browser opens to approve → done (no
  key to paste). OpenRouter mints a key; xAI stores a refreshable token. The same
  chooser appears in first-run onboarding.
- **Device code (headless / SSH):** for a provider that supports it (xAI), press
  **d** on the list to get a code to enter on another device instead of opening a
  browser. On an SSH session or headless Linux box (no `DISPLAY`) device code is
  used automatically; set `ZERO_OAUTH_DEVICE=1` to force it anywhere. The CLI
  equivalent is `zero auth login xai --device`.
- **ChatGPT / Claude are intentionally not in this list** — they can't do in-app
  OAuth (see §2). Use *Paste an API key / browse providers* + a local proxy
  (`chatgpt-proxy` / `custom-anthropic-compatible`), as described below.

### Built-in OAuth providers (no env needed)

Two providers ship a working browser login out of the box:

- **OpenRouter** — `zero auth openrouter` opens a browser, you approve, and it
  **mints an OpenRouter API key** (public PKCE flow, no client_id). In the
  interactive setup wizard, pick **OpenRouter** and press **ctrl+o** at the key
  step to do the same inline ("Log in with OAuth"). The minted key is saved to the
  provider profile and used normally.
- **xAI (Grok)** — `zero auth login xai` (browser, or `--device` for headless).
  The token is used directly on `api.x.ai/v1`; configure an `xai` provider profile
  and it's picked up automatically. Requires a SuperGrok / X Premium+ subscription;
  the client_id is an undocumented public Grok-CLI client (override via
  `ZERO_OAUTH_XAI_*` if it changes).

Any field of a preset is overridable via `ZERO_OAUTH_<NAME>_*`. For a fully custom
OAuth/OIDC provider, set those env vars (see `zero auth --help`) and
`zero auth login <name>`.

---

## 2. ChatGPT / Claude subscriptions — why a proxy is required

We researched this carefully. As of mid-2026, a **subscription** OAuth token does
**not** work as a drop-in bearer against the standard APIs:

- **OpenAI (ChatGPT):** a "Sign in with ChatGPT" token only works against
  ChatGPT's own backend (`chatgpt.com/backend-api/codex/responses`, the Responses
  API), **not** `api.openai.com`. That backend is **Cloudflare bot-protected** —
  non-browser / headless clients get `cf-mitigated: challenge` → `403`. It also
  requires mimicking the official Codex client (originator + account-id header).
- **Anthropic (Claude):** the Messages API **rejects** subscription OAuth tokens
  for third-party use unless the request spoofs the Claude Code identity
  (`anthropic-beta: oauth-2025-04-20`, `claude-cli` UA, and a verbatim
  *"You are Claude Code…"* system prompt) — and **even then** tool-using requests
  on Max plans are routed to a disabled billing lane and `400`. Anthropic's Feb
  2026 policy **explicitly prohibits** subscription-token use outside Claude
  Code / claude.ai and has **actively enforced** it (account bans), and the
  request to allow it (claude-code #37205) was closed *"not planned."*

So Zero does **not** call those backends directly or spoof those clients — that
would be fragile, account-risky, and (for Anthropic) against the vendor's terms.
The robust, supported pattern is a **local proxy** that holds your subscription
session and exposes a clean OpenAI- or Anthropic-compatible endpoint on
`127.0.0.1`. The proxy absorbs the Cloudflare / client-spoofing surface; Zero
just points at it.

### ChatGPT via a local proxy

Run a local ChatGPT OAuth proxy that exposes an OpenAI-compatible endpoint (these
typically listen on `127.0.0.1:10531/v1`). Then use the built-in **`chatgpt-proxy`**
preset (no API key — the proxy authenticates):

```jsonc
// ~/.config/zero/config.json (or ./.zero/config.json)
{
  "activeProvider": "chatgpt",
  "providers": [
    {
      "name": "chatgpt",
      "catalogID": "chatgpt-proxy",     // OpenAI-compatible, local, no key
      "baseURL": "http://localhost:10531/v1", // override for your proxy's port
      "model": "gpt-5"                   // whatever model your proxy serves
    }
  ]
}
```

```sh
zero exec --prompt "say hi"   # routes through the proxy → your ChatGPT plan
```

### Claude via a local proxy

There is no single canonical Claude OAuth-proxy port, so use the generic
**`custom-anthropic-compatible`** entry pointed at your proxy's Anthropic-compatible
endpoint:

```jsonc
{
  "activeProvider": "claude",
  "providers": [
    {
      "name": "claude",
      "catalogID": "custom-anthropic-compatible",
      "baseURL": "http://localhost:<port>",  // your Claude proxy
      "apiKey": "unused-by-proxy",
      "model": "claude-sonnet-4.5"
    }
  ]
}
```

---

## 3. Supported alternatives (no proxy)

- **API key (recommended, simplest):** set `OPENAI_API_KEY` / `ANTHROPIC_API_KEY`
  (or per-profile `apiKey`) and use the `openai` / `anthropic` catalog providers.
  Bills as API usage.
- **Anthropic subscription automation, sanctioned:** spawn the real `claude` CLI
  (e.g. `claude -p …`) as a subprocess — the only path Anthropic recognizes as a
  first-class subscription session.

---

## Notes

- The `chatgpt-proxy` base URL / port and model are defaults you override for your
  setup; they are not an endorsement of any specific proxy implementation.
- Subscription-via-proxy depends on third-party tools and undocumented vendor
  backends; it can break without notice. The API-key path is the stable one.
