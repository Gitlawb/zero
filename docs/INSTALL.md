# Installing Zero

Zero is distributed as:

- an npm package, `@gitlawb/zero`
- release archives on GitHub Releases
- source builds with Go 1.25+

The npm package and install scripts download a platform-specific release archive.
They require a published GitHub Release for the requested version.

## npm

```bash
npm install -g @gitlawb/zero
zero
```

The package supports Linux, macOS, and Windows on x64 and arm64. It installs the
`zero` command and downloads the matching release binary during `postinstall`.

Requirements:

- Node.js 18+
- network access to npm and GitHub Releases

## Linux And macOS Script

Install the latest release:

```bash
curl -fsSL https://raw.githubusercontent.com/Gitlawb/zero/main/scripts/install.sh | bash
```

From a checkout:

```bash
scripts/install.sh
```

Install a specific version:

```bash
ZERO_VERSION=0.1.0 scripts/install.sh
scripts/install.sh --version 0.1.0
```

Install somewhere else:

```bash
ZERO_INSTALL_DIR="$HOME/bin" scripts/install.sh
scripts/install.sh --install-dir "$HOME/bin"
```

Defaults:

- Repository: `Gitlawb/zero`
- Version: latest GitHub release
- Install path: `~/.local/bin/zero`

Requirements: Bash, `curl` or `wget`, `tar`, and `shasum` or `sha256sum`.

## Windows PowerShell Script

Install the latest release:

```powershell
irm https://raw.githubusercontent.com/Gitlawb/zero/main/scripts/install.ps1 | iex
```

From a checkout:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/install.ps1
```

Install a specific version:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/install.ps1 -Version 0.1.0
```

Install somewhere else:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/install.ps1 -InstallDir "$env:USERPROFILE\bin"
```

Defaults:

- Repository: `Gitlawb/zero`
- Version: latest GitHub release
- Install path: `%LOCALAPPDATA%\zero\bin\zero.exe`

## From Source

```bash
git clone https://github.com/Gitlawb/zero.git
cd zero
go run ./cmd/zero
```

Build a local binary:

```bash
go build -o zero ./cmd/zero
```

Source builds require Go 1.25+.

## Release Archive Format

Release archives are named:

- `zero-v<version>-linux-<arch>.tar.gz`
- `zero-v<version>-macos-<arch>.tar.gz`
- `zero-v<version>-windows-<arch>.zip`

Supported targets:

- `linux-x64`
- `linux-arm64`
- `macos-x64`
- `macos-arm64`
- `windows-x64`
- `windows-arm64`

Each archive must have a matching `.sha256` file. The install scripts download
both files, verify the checksum, and then copy the binary into the install
directory.

## Updating

Check for a newer release:

```bash
zero update --check
```

Then reinstall with npm or rerun the install script for the version you want.
