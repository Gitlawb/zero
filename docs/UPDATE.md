# Update Flow

`zero update --check` checks the latest GitHub release and compares it with the
local CLI version. `zero update --apply` (or its shorthand, `zero upgrade`)
downloads, verifies, and installs it.

```bash
zero update --check
zero update --check --json
zero update --check --repo Gitlawb/zero
zero update --check --target windows-x64

zero upgrade
zero update --apply
zero update --apply --json
```

`--check` and `--apply` are mutually exclusive. `zero update` requires one of
them explicitly; `zero upgrade` is `zero update` with `--apply` implied.

`--check` is check-only:

- It does not replace the running binary.
- It exits with code `0` when the check succeeds, even when an update is
  available.
- It exits with code `1` when the release check cannot be completed.
- `--json` prints the same result in a machine-readable format for scripts and
  CI.

`--apply` installs the update in place:

- npm installs delegate to `npm install -g @gitlawb/zero@latest`.
- Standalone installs download the release archive, verify its checksum,
  extract it, and atomically replace the running binary plus any installed
  optional sandbox helpers.
- On Windows, the running executable is renamed aside and cleaned up on a
  later invocation, since it can't be overwritten while running.
- `--target` cannot be combined with `--apply`; it only applies to `--check`,
  since applying always installs onto the current machine.
- `--json` prints the same result in a machine-readable format.

Useful flags:

| Flag | Purpose |
|---|---|
| `--repo <owner/repo>` | Check another GitHub repository. |
| `--endpoint <url\|owner/repo>` | Check a specific release API URL or repository slug. |
| `--timeout <duration>` | Override the default release check timeout. |
| `--target <platform-arch>` | Validate release metadata for another supported target (`--check` only). |

Supported targets are `linux-x64`, `linux-arm64`, `macos-x64`, `macos-arm64`,
`windows-x64`, and `windows-arm64`. Without `--target`, Zero checks the current
platform.

Endpoint resolution order:

1. `--endpoint`
2. `ZERO_UPDATE_RELEASE_URL`
3. `--repo`
4. `https://api.github.com/repos/Gitlawb/zero/releases/latest`

Installer scripts download the matching release asset for the local platform and
verify its `.sha256` file. If Zero is already installed, run `zero upgrade`
instead of reinstalling.
