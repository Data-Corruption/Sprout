---
title: 6. Install and operate
weight: 6
---

Use this page as a starting point for your application's user docs. Replace
`<APP>` and `<RELEASE_URL>`, remove sections for features you cut, and add your
support link.

## Install

Linux or WSL (run inside the Linux environment):

```sh
curl -fsSL <RELEASE_URL>install.sh | sh
```

Native Windows 11 (PowerShell):

```powershell
irm <RELEASE_URL>install.ps1 | iex
```

Install as your normal user; no elevation is needed. Releases support Linux,
WSL, and native Windows 11 on `amd64` and `arm64`.

| Platform | Binary | Data | Service |
|---|---|---|---|
| Linux | `~/.local/bin/<APP>` | `~/.<APP>` | User systemd unit |
| Windows | `%LOCALAPPDATA%\Programs` | `%LOCALAPPDATA%\<APP>` | Scheduled task based |

On Linux, service setup requires a working `systemd --user` version 246 or newer.
Without it, the installer installs the binary and reports that service setup was
skipped. Run `<APP> service run` manually on those hosts. This also applies to
WSL. See the [Linux distro matrix]({{% relref "docs/architecture" %}}#linux-distro-matrix)
for tested and considered distributions. macOS and BSD are not supported.

The Linux installer checks for required shell tools and installs a pinned Cosign
if needed. Follow any PATH or missing-tool instructions it prints.

A first install starts the retained service. Reinstallation restarts it only if
it was running. If an install or update fails during migration, rerun the
installer to recover; normal app commands cannot start until recovery completes.

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

On Linux, use `systemctl --user` for additional operations such as `enable`,
`disable`, and `reset-failed`. Windows stop and restart commands **wait for graceful
shutdown** before falling back to Task Scheduler termination.
<!-- For real though, what the hell Microsoft -->

For debugging or hosts without user systemd, run the service in the foreground:

```sh
<APP> service run
```

With the dashboard retained, check its health endpoint:

```sh
curl --insecure --fail https://127.0.0.1:8484/healthz
```

It returns `ok`. Use `--insecure` only for the application's default self-signed
certificate.

## Configure without the dashboard

```sh
<APP> config show
<APP> config set --log info
<APP> config set --port 9443
<APP> config set --ui-bind 127.0.0.1:9443
<APP> config set --proxy-bind 127.0.0.1:9080
<APP> config set --proxy-bind ""
```

Use `config show` to inspect user-editable settings. After changing listener
binds or ports, restart the service.

The root `--log` flag is a one-run override. For manual foreground service
runs, `service run --port` overrides the dashboard port for that run.
`config set` persists changes.

## First dashboard login

```sh
<APP> users add --username admin --perms "admin"
```

Enter a password at the prompt; it does not echo. Then open `https://localhost:8484`, or whichever port the application was built
with. Fresh production and development configurations bind to loopback. To
allow LAN clients, explicitly persist a wildcard or interface bind and restart:

```sh
<APP> config set --ui-bind :8484
<APP> service restart
```

Accept the browser warning for the locally generated certificate. LAN access
also requires the host firewall to allow the dashboard port.

To limit dashboard access, use `settings` for
configuration writes, `server.control` for stop, restart, and update actions, or
`admin` for all permissions. Combine names with spaces; prefix a name with `!`
to exclude it:

```sh
<APP> users add --username operator --perms "settings"
<APP> users add --username maintainer --perms "admin !server.control"
<APP> users list
<APP> users remove --username operator
```

Removing a credential also revokes its active sessions.

### Use a reverse proxy

This example uses Caddy on the same Linux host as the app. Install it using
[Caddy's package instructions](https://caddyserver.com/docs/install), choosing a
package with the `caddy` systemd service.

Point your hostname's DNS records at this host and allow inbound TCP ports 80
and 443 through the firewall and any router forwarding. Caddy uses those to
serve HTTPS and obtain certificates. For a host that must stay private, see
[Caddy's DNS challenge setup](https://caddyserver.com/docs/automatic-https#dns-challenge).

Enable the app's loopback-only HTTP listener:

```sh
<APP> config set --proxy-bind 127.0.0.1:8485
<APP> service restart
```

Edit `/etc/caddy/Caddyfile`:

```sh
sudoedit /etc/caddy/Caddyfile
```

Add this site block, replacing `app.example.com` with your hostname. Keep any
existing site blocks you still use:

```text
app.example.com {
    reverse_proxy 127.0.0.1:8485
}
```

Validate the config, enable Caddy at boot, and load the changes:

```sh
sudo caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
sudo systemctl enable --now caddy
sudo systemctl reload caddy
systemctl status caddy
```

Open `https://app.example.com` and log in with your app credential. Caddy obtains
and renews the certificate automatically. After later edits, validate and reload
again. If startup fails, check `journalctl -u caddy --no-pager`.
See [Caddy's service guide](https://caddyserver.com/docs/running#using-the-service)
for other service setups.

The proxy listener remains loopback-only; the direct dashboard listener still
uses its own HTTPS certificate.

## Update

```sh
<APP> update
```

The command checks for a release. With `update.apply` retained, it asks before
installing. To apply from a script, pass `--yes`:

```sh
<APP> update --yes
```

With only discovery retained, rerun the installer to apply the release. If all
update features were removed, rerun the installer to check and update.

### Update preferences

```sh
<APP> update --notify=false      # hide notices
<APP> update --background=false  # stop periodic checks and unattended updates
<APP> update --automatic=true    # enable unattended updates; requires service
<APP> update --automatic=false   # require a manual update request
```

Notices and background checks default to enabled. Unattended updates default to
disabled and require `update.apply.auto` plus the service. Enabling automatic
updates also enables background checks unless `--background` is supplied in the
same command. Hiding notices does not stop updates.

An explicit `<APP> update` checks regardless of the background preference. The
service picks up preference changes within a minute; an update already launched
continues to completion.

Updates use the release source saved during installation. For a private or
approved host, see [Run a mirror]({{% relref "docs/getting-started/mirror" %}}).

## Uninstall

```sh
<APP> uninstall
```

This removes the service, application data, binary, and applicable PATH entry.
It needs no elevation.

The storage root retains `control/`, `maintenance/`, and `logs/` for recovery.
If the binary is missing or will not start, use the cached installer:

```sh
~/.<APP>/maintenance/install.sh --uninstall
```

On Windows, run `%LOCALAPPDATA%\<App>\maintenance\install.ps1 -Uninstall`.
Either is safe to repeat. Delete the storage root by hand if you want the
machine fully clean.
