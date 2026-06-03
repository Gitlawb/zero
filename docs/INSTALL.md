# Zero Install Scripts

Zero release archives are published as:

- `zero-v<version>-linux-<arch>.tar.gz`
- `zero-v<version>-macos-<arch>.tar.gz`
- `zero-v<version>-windows-<arch>.zip`

Each archive must have a matching `.sha256` file. The install scripts download both files and verify the checksum before copying the binary.

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
