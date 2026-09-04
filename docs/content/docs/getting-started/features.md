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
update
├── update.self
├── update.notifications
└── update.auto

service
└── service.https
```

It's a short list. They combine into eighteen possible source outputs, six
update policies times three service shapes. Upstream CI runs
`go test -race ./...` against every one of them, so whatever you pick is a
combination already tested.

Cutting a parent cuts its children. Cutting a child leaves its parent and
siblings. Multiple names are a union.

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

## Pick an update policy

| You want | Cut |
|---|---|
| No update code at all. User updates by rerunning the install command | `update` |
| `<app> update` and it only checks. User does the update by rerunning the install command | `update.self update.notifications` |
| `<app> update` offers to do the update if a new version is present. If the dashboard is present a button to update remotely is also included. | `update.notifications` |
| Daily checks plus CLI and dashboard notifications. User does the update by rerunning the install command | `update.self` |
| Daily checks, self-update offer, dashboard button, and notifications | `update.auto` |
| All of it, including unattended updates that happen during the daily check. | nothing |

Base `update` is just the ability to check releases. `update.self` lets the running
binary launch the verified installer against itself. `update.notifications`
adds persisted notification state, the roughly daily checker, and the notices
that surface in the CLI and dashboard. `update.auto` is only the detached
launch that the periodic checker performs when it finds something newer.

Cutting every update feature doesn't cut updating. Re-running the original
install command always replaces the binary safely, coordinates running
processes, and migrates. Failures before migration starts can restore the
previous installed state; once migration is invoked, rerunning the installer is
the recovery path. What `update.self` removes is the application's ability to
*start* that itself, which is the point when updates have to pass through an
administrator, an approved mirror, or a deployment system rather than being
initiated by the app.

`update.auto` is a sibling name for easy selection, but its code is nested
inside both prerequisites, so cutting self-update or notifications removes
automatic updates too. There is no second scheduler hiding in the shrubbery.
The ergonomics of this could be a little better but the idea should be clear
enough and you only need to do this once.

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
