---
title: 6. Install and operate
weight: 6
---

This step kinda sorta isn't for you. It's a loose example for end-user docs.
Cut this down, reword it how you want, and point your end-users at it.

Replace `<APP>` and `<RELEASE_URL>` with your values, delete the sections for
features you cut, add your support link, and publish the result as your
application's install documentation.

## Install

Linux:

```sh
curl -fsSL <RELEASE_URL>install.sh | sh
```

Windows 11:

```powershell
irm <RELEASE_URL>install.ps1 | iex
```

Both installers are per-user and need no elevation. They read the promoted
version once, verify the release's signed checksums, verify the selected
binary's SHA-256, publish a transitional phase in `state.json`, stop matching
processes, take the exclusive lifecycle lock, replace installed state, and run
migrations. A first install registers and starts the service; a later one
restarts it only if it had been running, so a deliberately stopped service
stays stopped.

Failures before migration starts can restore the prior binary, release source,
and service definition. Invoking migration is the point of no return. If
migration fails or is interrupted, the new installed state and the transitional
phase remain, normal startup fails closed, and rerunning the installer is the
recovery path. Any migration that changes files or external systems should
therefore be idempotent.

If your application needs downgrades, add a coordinated pre-migration database
snapshot and explicit reversal for non-database side effects. Restoring only an
old binary is not sufficient.

On Linux the binary goes in `~/.local/bin` and data in `~/.<APP>`, with the
service as a `systemd --user` unit. On Windows the binary goes under
`%LOCALAPPDATA%\Programs`, data under `%LOCALAPPDATA%`, and the service is a
per-user scheduled task that starts at login.

Releases target Linux and Windows on `amd64` and `arm64`. macOS and BSD are not
supported by this installer model.

Linux binaries are fully static, so distro choice mostly does not matter. What
does matter is whether `systemd --user` works: without it — on Alpine, Void, or
some WSL setups — the binary still installs and the service registration is
skipped rather than failing the install.

The installer needs the usual shell tools:
`curl gzip mktemp install sha256sum sed awk flock stat od date sync readlink`.
It checks for `ss` or `netstat` to warn about an occupied dashboard port and
says so when neither exists, without failing the install. A stock
system has them, including busybox userlands. It does *not* need `cosign`: if
that is missing, a pinned and SHA-256-verified copy is installed to
`~/.local/bin` first.

{{% details title="Platform support" closed="true" %}}

| Family | Distros | Service | Notes |
|---|---|---|---|
| Debian | Debian, MX Linux, Raspberry Pi OS | systemd --user | MX Linux defaults to sysvinit on some ISOs and degrades to binary-only there; its "advanced hardware support" ISO uses systemd. |
| Ubuntu | Ubuntu, Mint, Pop!_OS, Zorin | systemd --user | Stock images have everything. |
| Arch | Arch, CachyOS, Manjaro, Omarchy | systemd --user | Stock images have everything. |
| Arch (immutable-ish) | SteamOS | systemd --user | Read-only root, but the install is entirely user-level, so it should work untouched. Desktop mode only. |
| Fedora | Fedora, Rocky, Alma, RHEL | systemd --user | `~/.local/bin` is already on PATH via the stock profile, and the installer notices and skips dotfile edits. |
| Fedora (ostree) | Silverblue, Kinoite, Bazzite, Bluefin, Aurora | systemd --user | Image-based root. User-level install works untouched; missing CLI tools point at Homebrew or distrobox rather than `dnf`. |
| openSUSE | Tumbleweed, Leap | systemd --user | Stock images have everything. |
| openSUSE (immutable) | MicroOS, Aeon, Kalpa | systemd --user | Transactional root, detected via `transactional-update`. Same story as ostree. |
| Alpine | Alpine | binary only | OpenRC, no systemd. Run it with `<APP> service run`, or write an OpenRC user service. |
| Void | Void | binary only | runit. Same degrade as Alpine. |
| NixOS | NixOS | systemd --user | The binary is pure static, should work fine albeit it's un-Nixy (not a flake or module). |
| Windows | Windows 11 | scheduled task | `install.ps1`, fully per-user, no admin. |

Gentoo, Devuan, Artix, and other niche setups are untested but should follow
their family's row: systemd means a full install, otherwise binary-only.

The installer never hard-fails on a service problem. It installs the binary and
tells you what it skipped:

- no `systemctl`, a broken `systemd --user`, or systemd older than 246 skips
  service setup;
- `~/.local/bin` already on PATH means no dotfile edits;
- an unwritable rc file is a warning, not a failure;
- missing CLI tools on an immutable root suggest `brew install ...` or
  distrobox instead of the host package manager.

On Windows the scheduled task runs headless (S4U) when the account has the
batch-logon right and falls back to an interactive-logon task when it does not.
Since a per-user install cannot add firewall rules, Windows Firewall will prompt
or silently block inbound LAN access to the dashboard; localhost needs nothing.

{{% /details %}}

{{% details title="Optionally, verify the installer first" closed="true" %}}

The one-liners verify the application artifacts, but a shell pipe executes the
installer before you get a chance to look at it. For complete chain verification,
verify the installer against the release workflow identity first.

Linux:

If Cosign is not installed:

```sh
case "$(uname -m)" in
  x86_64)  cosign_arch=amd64 ;;
  aarch64) cosign_arch=arm64 ;;
  *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

cosign_tmp="$(mktemp)"
trap 'rm -f "$cosign_tmp"' EXIT
curl -fsSLo "$cosign_tmp" \
  "https://github.com/sigstore/cosign/releases/latest/download/cosign-linux-${cosign_arch}"
sudo install -m 0755 "$cosign_tmp" /usr/local/bin/cosign
cosign version
```

then

```sh
curl -fsSLO <RELEASE_URL>install.sh
curl -fsSLO <RELEASE_URL>install.sh.cosign.bundle
cosign verify-blob \
  --certificate-identity "https://github.com/OWNER/REPO/.github/workflows/release.yml@refs/heads/main" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  --bundle install.sh.cosign.bundle install.sh
sh install.sh
```

Windows:

If Cosign is not installed, use WinGet (included with Windows 11):

```powershell
winget install --id Sigstore.Cosign --exact --source winget
cosign version
```

then

```powershell
irm <RELEASE_URL>install.ps1 -OutFile install.ps1
irm <RELEASE_URL>install.ps1.cosign.bundle -OutFile install.ps1.cosign.bundle
cosign verify-blob `
  --certificate-identity "https://github.com/OWNER/REPO/.github/workflows/release.yml@refs/heads/main" `
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" `
  --bundle install.ps1.cosign.bundle install.ps1
powershell -ExecutionPolicy Bypass -File install.ps1
```

{{% /details %}}

## Control the service

```sh
<APP> service status
<APP> service start
<APP> service restart
<APP> service stop
```

These use the platform's service manager with a deadline and return its
failures. On Linux they drive the per-user systemd unit, and native
`systemctl --user` remains the right tool for anything without an application
wrapper: `enable`, `disable`, `reset-failed`, and reading the journal.

The foreground service holds a process-lifetime operating-system lock. Starting
a second managed or manual instance for the same installation fails immediately
instead of creating two workers.

{{% details title="Why Windows needs an extra step to stop" closed="true" %}}

A per-user scheduled task is not a Windows service, and Task Scheduler has no
graceful stop — it kills the process. So `service stop` writes a short-lived
expiry to:

```text
%LOCALAPPDATA%\<App>\control\service.stop
```

The service coordinator watches that file independently of whatever your worker
is doing. While the lease is active it cancels the shared context, waits for the
worker and dashboard listeners to drain, then runs normal cleanup: close the
database, flush logs, remove the PID marker, release the lifecycle lock. The
controller polls Task Scheduler until the task confirms it stopped, and only
falls back to `schtasks /End` if the deadline passes. An unknown task state is
never accepted as success.

`service restart` performs that whole stop sequence before clearing the lease
and starting the task, then waits for `Running`. Without the wait, Task
Scheduler's `IgnoreNew` policy will quietly discard a start request while the
old instance is still exiting. `service start` also clears stale or expired
lease state.

The installer, updater, uninstaller, and the dashboard's controls all use this
same protocol. It is only a per-user file, with no schema or version coupling,
so an older binary simply ignores it and reaches the bounded hard stop instead.
Reserve a direct `Stop-ScheduledTask` or `schtasks /End` for emergencies; it
skips every graceful step above.

{{% /details %}}

There is also a foreground entry point, which is what the managed service
actually runs:

```sh
<APP> service run
```

Use it for debugging, or as the way to run the application on a Linux system
without user systemd. It owns process signals, starts the worker and the
dashboard together, and waits for both to shut down.

The example worker is just a little hash function:

```sh
<APP> hash hello
```

For a constant-cost liveness probe, the dashboard exposes an unauthenticated
`GET /healthz`. It returns only `ok` as plain text—no build or configuration
details:

```sh
curl --insecure --fail https://127.0.0.1:8484/healthz
```

`--insecure` is only for the application's default self-signed certificate.

## Configure without the dashboard

```sh
<APP> config show
<APP> config set --log info
<APP> config set --port 9443
<APP> config set --ui-bind 127.0.0.1:9443
<APP> config set --proxy-bind 127.0.0.1:9080
<APP> config set --proxy-bind ""
```

`config show` exposes only safe, user-editable values — never credential hashes
or internal update state. The point of having these on the CLI is that a bad
listener setting cannot lock you out of fixing that same listener setting.

The root `--log` flag is a one-run override. For manual foreground service
runs, `service run --port` overrides the dashboard port for that run.
`config set` persists changes.

## First dashboard login

```sh
<APP> users add --username admin --perms "admin"
```

Then open `https://localhost:8484`, or whichever port the application was built
with. Fresh production and development configurations bind to loopback. To
allow LAN clients, explicitly persist a wildcard or interface bind and restart:

```sh
<APP> config set --ui-bind :8484
<APP> service restart
```

Or edit the default.

Existing persisted binds are left alone. The primary listener always uses a
locally generated certificate, so a browser warning on first contact is
expected. LAN access also depends on the host firewall.

Credentials can be scoped:

```sh
<APP> users add --username operator --perms "settings"
<APP> users add --username maintainer --perms "admin !server.control"
<APP> users list
<APP> users remove --username operator
```

Removing a credential also revokes its active sessions, which live in SQLite
and therefore survive restarts and can be revoked from another process.

### Put a real certificate in front

Enable the loopback-only HTTP listener and point a TLS-terminating reverse
proxy at it:

```sh
<APP> config set --proxy-bind 127.0.0.1:8485
<APP> service restart
```

With Caddy, that is the entire configuration:

```text
app.example.com {
    reverse_proxy 127.0.0.1:8485
}
```

A non-loopback proxy bind is rejected. The direct dashboard listener stays
HTTPS regardless.

## Update

```sh
<APP> update
```

A fresh check runs immediately. If the application is current it says so and
exits successfully. If a release exists, it asks before launching the verified
installer. Scripts have to opt in:

```sh
<APP> update --yes
```

Non-interactive input without `--yes` fails clearly rather than waiting forever
for an answer nobody is there to give. Declining is a successful no-op.

When notifications are retained, this toggles them:

```sh
<APP> update --notify
```

Periodic checks share an expiring database lease across all CLI and service
processes, so only one process contacts the release host. A successful result
and lease release commit together; a failed request leaves the lease to expire
instead of triggering a retry storm. An explicit `<APP> update` check bypasses
that lease.

Official installers persist their release source on the machine. Mirror and
managed installs deliberately do not, so update checking stays off and the
command tells the user to repeat their original installation or follow their
administrator's instructions. It never quietly falls back to a public host.

On Linux the update runs as a detached job. When the caller is itself the
systemd-managed service (`NOTIFY_SOCKET` is set), the job is admitted through
`systemd-run --user` so stopping the service unit cannot take the updater down
with it; a failure to admit it there is reported rather than worked around.
From a CLI process or a manually run `service run`, the job detaches as its own
session instead, so hosts without `systemd --user` update normally. Linux
self-update resolves Cosign from the installer-managed `~/.local/bin/cosign`
first and then `PATH`, so it still works when a service has a smaller
environment than an interactive shell.

If your users install through an approved mirror instead of your release host,
their updates work differently on purpose:
[run an approved mirror]({{% relref "docs/getting-started/mirror" %}}) covers
both sides of that.

## Uninstall

```sh
<APP> uninstall
```

Stops and removes the service, removes application data, cleans up the PATH
entry where applicable, and deletes the binary. Everything the installer
created belongs to that user, so this needs no elevation either.

Three directories under the storage root are kept on purpose: `control/` with
the terminal `uninstalled` state and locks, `maintenance/` with the verified
cached installer and its signature bundle, and `logs/` with
`maintenance.log`. They are what lets you recover when the binary itself is
missing or will not start:

```sh
~/.<APP>/maintenance/install.sh --uninstall
```

On Windows, run `%LOCALAPPDATA%\<App>\maintenance\install.ps1 -Uninstall`.
Either is safe to repeat. Delete the storage root by hand if you want the
machine fully clean.
