---
title: 4. Build and run
weight: 4
---

Builds and tests have separate entrypoints:

```sh
./scripts/build.sh             # fast dev build for this architecture
./scripts/build.sh --prod      # tests plus a production binary for this arch
./scripts/build.sh --prod-all  # tests plus all four release binaries
./scripts/test.sh              # race-enabled Go tests, no binary
./scripts/test.sh -lint        # pinned shellcheck over the shell scripts
```

Run it on Linux or WSL on `amd64` or `arm64`. Local and `--prod` builds land in
`out/linux-<arch>`; `--prod-all` also cross-compiles both Windows targets.

The first form is the common one. It skips tests,
isolates its storage root under a `-dev` suffix, forces debug
logging, disables all update behavior, and bypasses dashboard authentication.
This dev build is never shipped, just used for development / testing.

## Run it

```sh
./scripts/build.sh
```

If you kept the service, start it in one terminal:

```sh
./out/linux-$(go env GOARCH) service run
```

and poke it from another:

```sh
./out/linux-$(go env GOARCH) hash hello
```

You should get `hello from service, here is your SHA-256: <hash>` back. The
round trip is the whole point of the example: the CLI process wrote a request
into SQLite, the completely separate service process picked it up and answered,
and the CLI read the answer back. No socket, no port, no message broker, no
daemon protocol to version. If nothing comes back, the CLI will point you at
`service status` instead of hanging.

With `service.https` retained, open `https://localhost:8484`. The certificate
is locally generated and self-signed, so the browser warning is expected and
the dev build lets you straight in without a password.

If you cut the service, `./out/linux-$(go env GOARCH) config show` is a quick
way to check that the database and configuration are working.

## Check a production build

```sh
./scripts/build.sh --prod
```

This runs the tests, builds assets, compiles a production binary for the
current host, and verifies the values baked into it. Production means real
paths instead of `-dev` ones, normal log levels, update behavior enabled, and
dashboard auth turned on.

Which means with `service.https` you now need a credential before you can log
in:

```sh
./out/linux-$(go env GOARCH) users add --username admin --perms "admin"
./out/linux-$(go env GOARCH) service run
```

The password is always read from a prompt that does not echo.

{{% details title="How dashboard permissions work" closed="true" %}}

Each user carries a permission bitmask:

- `settings` allows changes to the log level, UI bind, and proxy bind.
- `server.control` allows stop, restart, and self-update actions.
- `admin` grants every permission and is the default for `users add`.

Pass space-separated names to combine permissions. Prefix a name with `!` to
remove it:

```sh
./out/linux-$(go env GOARCH) users add --username operator --perms "settings"
./out/linux-$(go env GOARCH) users add --username maintainer --perms "admin !server.control"
./out/linux-$(go env GOARCH) users list
./out/linux-$(go env GOARCH) users remove --username operator
```

Any logged-in user can view the settings page; permissions gate its write
actions. Add app-specific bits beside the starter set in
`internal/types/perms.go` as your routes grow.

{{% /details %}}

Local builds of either kind report their version as `v0.0.0-dev`, a valid
SemVer prerelease, and bake in `DevMode`. When you need something to behave
differently outside production, branch on `BuildInfo().DevMode` (or
`App.DevMode`) rather than sniffing the version string.

## Pimp your ride

At this point you have local builds working and can start making your app.
Everything from here is your application, and the useful entry points are:

- **Add a CLI command:** write a constructor in `internal/app/commands` and
  register it in `commands.go`.
- **Replace the background job:** `runWorker` in
  `internal/app/commands/worker.go`. You can delete the hash request code while
  you're in there.
- **Store something durable:** add a migration step in
  `internal/platform/database/migration.go`, then put ordinary SQL accessors
  beside the subsystem that owns them. If you are pre-release feel free to just
  edit the initial step.
- **Add a dashboard page or API:** a focused handler package under
  `internal/platform/http/router`, mounted in `router.go`, with an explicit
  permission required for writes. Permissions are a bitmask in
  `internal/types/perms.go` - the starter set is `settings`, `server.control`,
  and `admin`, and your own bits can go beside them if you want.
- **Change the frontend:** templates and source assets under `internal/ui`.
  The build script rebuilds, hashes, and embeds them; there is no npm project
  to adopt and raise. New static files go anywhere under
  `internal/ui/assets/` and are reachable by their cache-busted path, either
  `{{ assetPath "img/logo.png" }}` in a template or
  `a.UI.Assets["img/logo.png"].URLPath` in a handler.

{{< callout type="warning" >}}
Some HTML formatters do not understand Go template syntax and will happily
insert spaces inside `{{ }}` expressions, which breaks them silently. That is
why this repository disables format-on-save for HTML in `.vscode/settings.json`.
{{< /callout >}}

[How Sprout Fits Together]({{% relref "docs/architecture" %}}) has the rest of
the map, including why the lifecycle lock exists and what the `App` container
is actually for. It's there if you need to dig deeper.
