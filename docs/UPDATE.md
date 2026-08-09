# Update Flow

`zero update --check` checks the latest GitHub release and compares it with the
local CLI version.

```bash
zero update --check
zero update --check --json
zero update --check --repo Gitlawb/zero
zero update --check --target windows-x64
```

The command is intentionally check-only:

- It does not replace the running binary.
- It exits with code `0` when the check succeeds, even when an update is
  available.
- It exits with code `1` when the release check cannot be completed.
- `--json` prints the same result in a machine-readable format for scripts and
  CI.

Useful flags:

| Flag | Purpose |
|---|---|
| `--repo <owner/repo>` | Check another GitHub repository. |
| `--endpoint <url|owner/repo>` | Check a specific release API URL or repository slug. |
| `--timeout <duration>` | Override the default release check timeout. |
| `--target <platform-arch>` | Validate release metadata for another supported target. |

Supported targets are `linux-x64`, `linux-arm64`, `macos-x64`, `macos-arm64`,
`windows-x64`, and `windows-arm64`. Without `--target`, Zero checks the current
platform.

Endpoint resolution order:

1. `--endpoint`
2. `ZERO_UPDATE_RELEASE_URL`
3. `--repo`
4. `https://api.github.com/repos/Gitlawb/zero/releases/latest`

Installer scripts download the matching release asset for the local platform and
verify its `.sha256` file. If Zero is already installed, run `zero update --check`
before reinstalling.

## Authentication

Unauthenticated GitHub API requests are subject to strict
[rate limits](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api).
If you see `403 Forbidden` during update checks, set a GitHub personal access token:

| Environment variable | Role |
|---|---|
| `ZERO_GITHUB_TOKEN` | Used for update checks (takes precedence) |
| `GITHUB_TOKEN` | Fallback when `ZERO_GITHUB_TOKEN` is not set |

Tokens are **only** sent to `https://api.github.com`. Custom endpoints (set via
`--endpoint` or `ZERO_UPDATE_RELEASE_URL`) and plain HTTP URLs never receive
credentials, so you can safely point Zero at a private mirror without leaking
your token.
