---
title: 2. Choose features
weight: 2
---

Choose what to keep before editing application code. In the
[next step]({{% relref "docs/getting-started/cut" %}}), you'll remove unwanted
features from the source. If you're unsure, keep the feature; you can edit or
remove its code later.

## Choose a service

| You want | Cut |
|---|---|
| CLI only | `service` |
| A background worker | `service.https` |
| A worker with an HTTPS dashboard | nothing |

The service runs as a Linux user systemd unit or a Windows scheduled task.
Replace `runWorker` in `internal/app/commands/worker.go` with your background
work. The dashboard adds local HTTPS, users, sessions, and permissions. Tuned
for small user counts.

## Choose update support

| You want | Cut |
|---|---|
| Users update by rerunning the installer | `update` |
| Also check for releases and show notices | `update.apply` |
| Also apply updates from the CLI or dashboard | `update.apply.auto` |
| Also support unattended updates | nothing; keep `service` |

Cutting a feature removes its dependents. Cutting `service`, for example, also
removes `service.https` and `update.apply.auto`. You can preview these cuts
before removal.

Unattended updates are disabled by default, even when the feature is retained.
To enable them by default in your app, after finalizing add `AutomaticUpdates: true,`
to the `Configuration` returned by `DefaultConfig` in `internal/types/types.go`.
The field is currently omitted, so it defaults to `false`. This affects new
installations; existing installations keep their saved preference.

Users enable them with `<APP> update --automatic=true`. See
[update settings]({{% relref "docs/getting-started/operate" %}}#update) for the
installation preferences.

Continue to [Cut and rename]({{% relref "docs/getting-started/cut" %}}) with the
features you want to remove.
