---
title: 3. Cut and rename
weight: 3
---

Run this once on your `setup` branch, before editing application code or deploying.
Finalization removes the cutter itself.

## Preview

Pass your new module path and all the features you want to remove:

```sh
./scripts/cut --module github.com/YOU/YOUR_APP service.https update
```

Review the listed deletions and import changes. Preview changes no files and
needs no network access.

## Finalize

Apply the same plan with `--finalize`:

```sh
./scripts/cut --finalize --module github.com/YOU/YOUR_APP service.https update
```

To keep every feature, omit the feature names:

```sh
./scripts/cut --finalize --module github.com/YOU/YOUR_APP
```

Finalization removes the selected code and setup tooling, renames imports, runs
`goimports`, and runs `go mod tidy`. It may need network access to fetch tools
or modules. If tidy reports a warning, resolve it and run `go mod tidy` again
before continuing.

## Set project values

Edit the project block near the top of `scripts/build.sh`:

```sh
APP_NAME="your-app"
RELEASE_URL="https://cd.example.com/"
CONTACT_URL="https://github.com/YOU/YOUR_APP"
DEFAULT_LOG_LEVEL="warn"
```

`APP_NAME` determines the installed binary name and storage directory.
`RELEASE_URL` must end in `/`; it can remain a placeholder until
[release setup]({{% relref "docs/getting-started/release" %}}).
`DEFAULT_LOG_LEVEL` applies to new installations.

If you kept the service, set `SERVICE_DESC`. If you kept the dashboard, set
`SERVICE_DEFAULT_PORT` (default `8484`). You can also replace the remaining
Sprout branding in the README and UI.

## Verify and commit

```sh
./scripts/test.sh
./scripts/build.sh
```

Fix any errors before continuing. For an upstream issue, include the failing
command, its output, your OS, architecture, and tool versions.

Review the diff, then commit the setup:

```sh
git add -A
git commit -m "Set up project from Sprout"
```
