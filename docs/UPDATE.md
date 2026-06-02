# Zero Update Flow

`zero update --check` checks the latest GitHub release for `Gitlawb/zero` and compares it with the local CLI version.

For M2 this command is intentionally check-only:

- It does not replace the running binary.
- It exits with code `0` when the check succeeds, even when an update is available.
- It exits with code `1` when the release check cannot be completed.
- `--json` prints the same result in a machine-readable format for scripts and CI.

Installer scripts should use this command before downloading the matching release asset for the local platform.
