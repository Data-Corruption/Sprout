# Changelog

<!--
Replace with your changelog entries. You can start at any version that isn't already tagged.
Remove the leading "//" from the example entries. It's to keep the CI from thinking they're real.

// ## [v1.0.1] - 2025-12-06

Example update.

// ## [v1.0.0] - 2025-11-24

Minimal starter for Go CLI apps with an optional webserver daemon, changelog‑driven Forgejo Actions CI/CD, and self‑updating installs.

Added
- CLI scaffold using urfave/cli v3 with common flags and subcommands.
- Service subcommand running an HTTP server (default :8383); installer provisions a systemd service.
- Shared atomic data/config directory via LMDB, safely used by both CLI and service.
- Intuitive migration system for data/config.
- Structured, rotatable logging via stdx/xlog under the per-user data path.
- Changelog-driven release automation; daily lightweight version checks and an update command with opt-out notifications.
- Cross-platform installers:
  - Linux installer with optional version pinning.
  - Windows PowerShell (WSL) installer.
- Build scripts for reproducible, versioned artifacts.
- Apache-2.0 license and template documentation.
- Build time variable injection via LDFLAGS with verification tests.
- Tests for most of the important parts of the codebase (updating, migrations, etc).

Notes
- Local builds use a placeholder version (vX.X.X) and skips update logic.
- Project structure using standard Go layout (`cmd`, `internal`, `pkg`).

-->