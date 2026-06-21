# Zero Install Scripts

> **Status: pre-release.** No GitHub release has been published yet, so the install scripts below
> (which download release assets from `Gitlawb/zero`'s Releases) will fail with a 404 until the first
> release exists. Until then, build from source: `go run ./cmd/zero` or `go build -o zero ./cmd/zero`
> (requires Go 1.25+). The instructions below describe how the scripts work once releases are published.

Zero release archives are published as:

- `zero-v<version>-linux-<arch>.tar.gz`
- `zero-v<version>-macos-<arch>.tar.gz`
- `zero-v<version>-windows-<arch>.zip`

Each archive must have a matching `.sha256` file. The install scripts download both files and verify the checksum before copying the binary.

Maintainers can verify the release directory before upload:

```bash
go run ./cmd/zero-release package
go run ./cmd/zero-release verify
```

`verify:release` requires every archive in `dist/release` to have a same-directory `.sha256` file whose contents are:

```text
<sha256>  <archive-name>
```

## Linux And macOS

From a checkout:

```bash
scripts/install.sh
```

Install a specific version:

```bash
ZERO_VERSION=0.1.0 scripts/install.sh
```

Install to a custom directory:

```bash
ZERO_INSTALL_DIR="$HOME/bin" scripts/install.sh
```

Install from a fork or internal repository:

```bash
scripts/install.sh --repo owner/repo
```

Defaults:

- Repository: `Gitlawb/zero`
- Version: latest GitHub release
- Install path: `~/.local/bin/zero`

Requirements: `curl` or `wget`, `tar`, and `shasum` or `sha256sum`.

## Windows

From a checkout:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/install.ps1
```

Install a specific version:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/install.ps1 -Version 0.1.0
```

Install to a custom directory:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/install.ps1 -InstallDir "$env:USERPROFILE\bin"
```

Install from a fork or internal repository:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/install.ps1 -Repository owner/repo
```

Defaults:

- Repository: `Gitlawb/zero`
- Version: latest GitHub release
- Install path: `%LOCALAPPDATA%\zero\bin\zero.exe`

## Updating

Check whether a newer release exists:

```bash
zero update --check
```

For M2, updates are check-only. Re-run the installer to replace the local binary after reviewing the release.
