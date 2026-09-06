---
title: 4. Build and run
weight: 4
---

Run these commands on Linux or WSL, on `amd64` or `arm64`.

## Run a dev build

```sh
./scripts/build.sh
```

The binary is written to `out/linux-<arch>`. Dev builds use separate `-dev`
storage, debug logging, and no dashboard authentication or update checks.

If you kept the service, start it:

```sh
./out/linux-$(go env GOARCH) service run
```

In another terminal, try the example command:

```sh
./out/linux-$(go env GOARCH) hash hello
```

You should receive `hello from service, here is your SHA-256: <hash>`.
With the dashboard retained, open `https://localhost:8484`. Accept the warning
for the local self-signed certificate; a dev build needs no login.

For a CLI-only build, check the configuration instead:

```sh
./out/linux-$(go env GOARCH) config show
```

## Make it your app

| Change | Start here |
|---|---|
| Add a command | Add a constructor under `internal/app/commands` and register it in `commands.go`. |
| Replace the example worker | Edit `runWorker` in `internal/app/commands/worker.go`. |
| Add durable state | Add a migration in `internal/platform/database/migration.go` and SQL accessors beside the owning subsystem. Before your first release, you can edit the initial migration. |
| Add a dashboard route | Add a handler under `internal/platform/http/router`, mount it in `router.go`, and require a permission for writes. Define permissions in `internal/types/perms.go`. |
| Change the frontend | Edit templates and source assets under `internal/ui`. The build bundles and embeds them. |

Add static files under `internal/ui/assets/`. Refer to their versioned paths
with `{{ assetPath "img/logo.png" }}` in a template or
`a.UI.Assets["img/logo.png"].URLPath` in a handler.

Keep HTML format-on-save disabled in `.vscode/settings.json` unless your formatter
supports Go templates; some formatters break expressions inside `{{ }}`.

[Architecture]({{% relref "docs/architecture" %}}) covers process lifecycle,
migrations, and subsystem boundaries.

## Test your changes

```sh
./scripts/test.sh        # Go tests with the race detector
./scripts/test.sh -lint  # ShellCheck; run after editing shell scripts
```

## Check production builds

```sh
./scripts/build.sh --prod      # current Linux architecture
./scripts/build.sh --prod-all  # Linux and Windows, amd64 and arm64
```

Both run tests and verify the values compiled into the binaries. Production
builds use normal installation paths and enable authentication and retained
update features. Install a published release to test the full installation
lifecycle; see [Install and operate]({{% relref "docs/getting-started/operate" %}}).

All local builds report `v0.0.0-dev`. In code, use `BuildInfo().DevMode` or
`App.DevMode` to distinguish a dev build from production mode.

## Test installer or release changes

Run these when changing the installation or publication machinery:

```sh
./scripts/test.sh -release  # local fake remotes; does not touch your bucket
./scripts/test.sh -e2e      # installation lifecycle; requires Incus
```

{{% details title="One-time Incus setup" closed="true" %}}

Incus runs full Linux userspaces as unprivileged system containers, including
their real init and user service manager. On Ubuntu 24.04:

```sh
sudo apt-get update
sudo apt-get install -y incus
sudo systemctl enable --now incus.socket
sudo incus admin init --minimal
sudo usermod -aG incus-admin "$USER"
```

Reopen your login session after changing groups. Membership in `incus-admin`
grants full control of the local daemon and is effectively root access; add
only trusted users. See the
[Incus installation guide](https://linuxcontainers.org/incus/docs/main/installing/)
for other host distributions.

Ubuntu 24.04's native package is Incus 6.0 LTS, which supports its Linux 5.4
baseline and WSL's 6.6 kernel. Current Incus 7.x releases require Linux 6.12 or
newer, so do not replace the native LTS package on an older host. Under WSL,
enable systemd and restart WSL before installing Incus, and keep the checkout
on the Linux filesystem (for example, under `/home`) rather than `/mnt/c`. If
Docker's forwarding rules block guest package downloads, apply the targeted
`incusbr0` rules from the official
[firewall guide](https://linuxcontainers.org/incus/docs/main/howto/network_bridge_firewalld/);
the harness does not silently rewrite a developer machine's firewall.

Sanity check the daemon and one system container:

```sh
incus info
incus launch images:alpine/3.24 sprout-incus-smoke
incus exec sprout-incus-smoke -- true
incus delete sprout-incus-smoke --force
```

{{% /details %}}

For coverage and upstream-only tests, see
[Architecture]({{% relref "docs/architecture" %}}#testing).

When you're ready to ship, continue to
[Publish a release]({{% relref "docs/getting-started/release" %}}).
