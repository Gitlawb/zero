# Zero Update Flow

`zero update --check` checks the latest GitHub release for `Gitlawb/zero` and compares it with the local CLI version.

```bash
zero update --check
zero update --check --json
zero update --check --repo Gitlawb/zero
zero update --check --endpoint https://api.github.com/repos/Gitlawb/zero/releases/latest
zero update --check --timeout 3s
zero update --check --target windows-x64
```

For M2 this command is intentionally check-only:

- It does not replace the running binary.
- It exits with code `0` when the check succeeds, even when an update is available.
- It exits with code `1` when the release check cannot be completed.
- `--json` prints the same result in a machine-readable format for scripts and CI.
- `--repo <owner/repo>` checks a different GitHub repository when no endpoint is provided.
- `--endpoint <url|owner/repo>` checks a specific release API URL or repository slug.
- `--timeout <duration>` overrides the default release check timeout.
- `--target <platform-arch>` validates release metadata for another supported target from the current machine.
- Release checks time out after 5 seconds by default.
- It validates that the latest release includes the expected archive and matching `.sha256` asset for the selected platform target.

Supported targets are `linux-x64`, `linux-arm64`, `macos-x64`, `macos-arm64`, `windows-x64`, and `windows-arm64`. Without `--target`, Zero checks the current platform.

The release endpoint resolves in this order:

- `--endpoint` from the CLI, or `Options.Endpoint` when calling `update.Check` from code.
- `ZERO_UPDATE_RELEASE_URL` from the environment.
- `--repo` from the CLI, or `Options.Repository` when calling `update.Check` from code.
- `https://api.github.com/repos/Gitlawb/zero/releases/latest`.

`--endpoint`, `Options.Endpoint`, and `ZERO_UPDATE_RELEASE_URL` may be a full URL or an `owner/repo` slug.

Installer scripts download the matching release asset for the local platform and verify its `.sha256` file. If Zero is already installed, use this command before re-running the installer.
