---
title: 2. Choose features
weight: 2
---

Sprout's optional features are source code, not runtime switches. There is no
config file that turns the dashboard off. There is a fenced dashboard, and you
can take an axe to it. The opposite of code gen. Code *de*generation?

This page tells you about the features, then in
[step 3]({{% relref "docs/getting-started/cut" %}})
you cut the ones you don't want.

## What's on the menu

```text
update                         release discovery and notices
└── update.apply               apply an update on request
    └── update.apply.auto      apply unattended; also requires service

service
└── service.https
```

These are cumulative capabilities: application of updates needs discovery,
and unattended application needs both manual application and a running service.
The cutter knows these dependencies. Cutting a prerequisite also removes its
dependents; the preview explains each removal. Cutting `service`, for example,
also cuts `service.https` and `update.apply.auto`.

There are eleven valid source outputs. Three nonautomatic update levels combine
with all three service shapes; automatic updates combine with the two shapes
that retain the service. Upstream CI tests every one.

## Pick a service shape

| You want | Cut |
|---|---|
| CLI only, no background process | `service` |
| A headless background worker | `service.https` |
| A worker plus an HTTPS dashboard | nothing |

Base `service` is a background worker plus the CLI commands that manage it as a
`systemd --user` unit or a Windows scheduled task. It includes a small SQLite
IPC example: `app hash hello` inserts a request, the worker computes SHA-256,
the CLI prints the answer. That example exists to show the wiring, delete or
replace it with your real app service.

`service.https` adds the dashboard on top: router, handlers, templates,
credentials, sessions, permissions, TLS material, and both listeners. It runs
as a sibling of the worker inside the same process and shares its cancellation
context.

The worker itself is an ordinary function, not a component framework:

```go
func runWorker(ctx context.Context, app *app.App) error
```

If your application is a bot, a scheduler, a queue consumer, or a local
protocol server, that function is where it goes.

## Pick an update capability

| You want | Cut |
|---|---|
| Updates handled outside the application | `update` |
| Discovery and notices; users apply updates by rerunning the installer | `update.apply` |
| CLI confirmation and dashboard action to apply updates | `update.apply.auto` |
| Also support unattended updates from the service | nothing; retain `service` |

Base `update` owns explicit checks, cached release information, CLI/dashboard
notices, and roughly daily background checks. CLI processes check opportunistically
while a command runs; the service can keep checking while the application is idle.
A short CLI command may finish before its check does. Shutdown cancels and joins
the check, and CLI background checks never install anything.

`update.apply` adds `<app> update` confirmation, `--yes` for noninteractive
application, and the dashboard update action when HTTPS is retained. The binary
admits a detached, verified installer; the installer still owns all changes to
the installation.

`update.apply.auto` adds unattended application and retries in the service.
Keeping this source capability does not opt an installation into unattended
updates: enable that preference explicitly with `<app> update --automatic=true`.

### Installation preferences

These settings control retained capabilities; they cannot restore cut code.

```sh
<app> update --notify=false       # hide CLI and dashboard notices
<app> update --background=false   # stop background checks and automatic application
<app> update --automatic=true    # enable unattended application (requires service)
<app> update --automatic=false   # keep checking, require a manual update request
```

Notices and background checks default to enabled; unattended application defaults
to disabled. Enabling automatic application also enables background checks unless
`--background` is explicitly supplied in the same command. Hiding notices does
not disable checking or automatic application. Explicit `<app> update` checks
regardless of the background preference. The service rereads preferences within
one minute; a maintenance job already admitted continues to completion.

Cutting all update features does not cut updating. Rerunning the installer
still replaces the binary safely, coordinates running processes, and migrates.
Failures before migration starts can restore the previous installed state;
after migration is invoked, rerunning the installer is the recovery path.

### Choose where releases come from

The installer persists its effective release URL, including an `APP_RELEASE_URL`
mirror override. All retained update capabilities use that source and continue
to verify against the application's original signing identity. A mirror can
therefore stage and approve releases before advancing its own `version` pointer;
its users only discover releases that it has promoted. See
[End-user mirror]({{% relref "docs/getting-started/mirror" %}}).

{{< callout type="information" >}}
On Linux, a detached update launched from inside the systemd-managed service
is admitted through `systemd-run --user`, so stopping the unit cannot kill the
updater. From a CLI process or a manually run `service run` it detaches as its
own session instead. Hosts without `systemd --user` get the binary-only install
and still update through either of those paths.
{{< /callout >}}

## Decide once

Feature cuts are supported as the first thing you do to a fresh tree, before
application edits and before any deployment. Git can undo a bad first cut, but
it can't prove that ownership markers are still accurate after your fork has
diverged, and removing state from an application that is already
installed somewhere is a migration problem.

So pick everything now, in one list. If you are unsure about a feature, keeping
it is the cheaper mistake: deleting ordinary code later is a normal afternoon,
while reintroducing a cut feature means going back to upstream and merging by
hand.
