# ChuckMayo Fork Distribution

This fork publishes pinned CLI releases for macOS and Linux at
<https://github.com/ChuckMayo/beads/releases>. The fork keeps Beads focused on
issue tracking: generated Beads instructions never grant or revoke source Git
permissions.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/ChuckMayo/beads/v1.2.2-chuck.1/scripts/install-chuck-fork.sh | bash
```

The installer defaults to `v1.2.2-chuck.1`, verifies the release checksum, and
installs `bd` to `~/.local/bin`. Override either value when needed:

```bash
BD_FORK_VERSION=v1.2.2-chuck.1 BD_INSTALL_DIR=/custom/bin bash install-chuck-fork.sh
```

Ensure the install directory appears before Homebrew or another system-level
`bd` on `PATH`, then confirm the selected binary:

```bash
command -v bd
bd version
```

## Update a machine

Change `BD_FORK_VERSION` to a tested fork release and rerun the installer. Pin
the same version in automation and container builds so developer machines and
CI do not silently drift onto different Beads behavior.
