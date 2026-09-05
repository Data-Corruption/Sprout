# Application Maintenance Lifecycle

Install, update, and uninstall are installer-owned transactions. The Go
application participates by holding a lifecycle lease while it runs, recording
an instance marker, checking for updates, and admitting a verified installer as
a detached maintenance job. It does not modify its own installation.

There is intentionally no compatibility migration for older layouts. Sprout is
a starter template, and the layout below is the only supported layout.

## Paths

The storage root is `~/.<app>` on Linux and
`%LOCALAPPDATA%\<AppName>` on Windows. Development builds use a separate
`-dev` root. XDG runtime directories and `/run/user` are not used.

```text
<storage>/
  data/
    db/
    secrets/
    tmp/
    <app>.env
  control/
    state.json
    operation.lock
    lifecycle.lock
    service.lock
    service.stop
    instances/
  maintenance/
    install.sh | install.ps1
    install.sh.cosign.bundle | install.ps1.cosign.bundle
    release-url
    jobs/
      <id>/
        run.sh | run.ps1
        pid
        ... downloaded installer and bundle
  logs/
    maintenance.log
    ... application logs
```

The Linux binary remains at `~/.local/bin/<app>` and its optional user unit at
`~/.config/systemd/user/<app>.service`. On Windows, the binary is under
`%LOCALAPPDATA%\Programs\<AppName>` and the optional service is a user Scheduled
Task. Managed Cosign is shared rather than application-owned.

All application path policy lives in `internal/layout`. `internal/app` receives
one `layout.Layout` and does not independently resolve storage or temporary
paths.

## Durable state

`control/state.json` is atomically replaced and contains:

```json
{
  "phase": "ready",
  "version": "v1.2.3",
  "targetVersion": "",
  "nonce": "",
  "changedAt": "2026-01-02T03:04:05Z",
  "installationEpoch": "..."
}
```

The supported transitions are:

```text
absent or uninstalled -> installing -> ready
ready                -> updating   -> ready
ready                -> uninstalling -> uninstalled
```

The file is one compact line. The Linux installer reads it with `sed`, not a
JSON parser, so every value must stay a plain string with no embedded quotes,
escapes, or nesting, and every key must be written as `"key":"value"` with no
whitespace. Go writes it through `encoding/json`, which produces the same form.
Both writers own the shape; nothing else may add fields the installers do not
expect.

`installationEpoch` identifies one installation lifetime. It is stable across
updates and is replaced only when an uninstalled application is installed
again. A detached update captures both the epoch and source version; a detached
uninstall captures the epoch. Its controller proceeds only when state is still
`ready` and those expectations match. This prevents an old queued job from
changing a newer installation.

`nonce` exists only in `installing` and `updating`. The script generates it,
publishes it with the transition, and supplies it to the `--migrate` process in
`APP_MAINTENANCE_NONCE`. The maintenance guard accepts a migrator only when the
phase, target version, and nonce all match. Ordinary processes can start only in
`ready` with the exact running version.

An unsafe failure after migration begins remains in its transitional state so a
normal binary cannot run against an uncertain schema. Rerunning the installer
is the recovery path. Uninstall is forward-only after `uninstalling` is
published and is safe to repeat. A successful transitional recovery starts an
enabled registered service; a disabled service remains disabled. This restores
the common pre-failure running state without adding another durable intent.

## Locks and process draining

`operation.lock` is exclusive for the entire script controller. It serializes
install, update, recovery, and uninstall transactions.

`lifecycle.lock` is shared by every ordinary application process and exclusive
while a script changes application data, the binary, or service registration.
A normal process checks `ready` and its version, acquires the shared lock,
creates `control/instances/<pid>`, and checks state again. Publishing the
marker before the second check closes the startup/drain race; a failed check
immediately removes it. The installer-owned migrator does not acquire a shared
lease because its controller already owns lifecycle exclusivity.

A controller performs these steps in order:

1. Acquire the operation lock and validate any detached-job expectations.
2. Atomically publish the transitional state.
3. Stop the registered service and drain marked processes.
4. Acquire the exclusive lifecycle lock.
5. Mutate the installation and run an authorized migration when applicable.
6. Atomically publish the terminal state and release both locks.

Every normal process on both platforms watches the atomic state and cancels its
application context when the phase leaves `ready`, its version changes, or its
epoch changes. Because the transitional state is published before anything is
stopped, this is the first drain signal a process receives. On Linux, draining
additionally revalidates that each marked PID is running the installed
executable before sending `SIGTERM`, which reaches the same cancellation; on
Windows the marker-drain step relies on the state watcher. Stopping the
registered Scheduled Task additionally writes the service-stop lease before
using Task Scheduler's forced fallback. Commands that block should return
promptly when their context is cancelled.

After 15 seconds, each script revalidates executable and PID identity and
forcibly stops only the remaining application processes. Stale, malformed, and
unrelated PID markers are removed without signaling unrelated processes.

The root command owns one interrupt/`SIGTERM` context. Service components (the
worker, HTTP server, automatic update checker, and Windows service-stop
watcher) are joined before service shutdown completes. A second process signal
uses the operating system's default behavior so a non-cooperative command can
still be terminated. On Windows, Go's runtime installs `SetConsoleCtrlHandler`.
When Windows delivers them to the console process, `CTRL_C_EVENT` and
`CTRL_BREAK_EVENT` become an interrupt, while `CTRL_CLOSE_EVENT`,
`CTRL_LOGOFF_EVENT`, and `CTRL_SHUTDOWN_EVENT` become `SIGTERM`. The last three
provide only Windows' bounded opportunity to clean up. Scheduled Task control
does not rely on console delivery: maintenance transitions use the state
watcher, and explicit task stops use the service-stop lease.

## Installer and detached jobs

The installer entry points are:

```text
install.sh [--update | --uninstall]
install.ps1 [-Update | -Uninstall]
```

No flag means install, reinstall, or recovery. Install and update verify both
cache inputs before publishing them with atomic file replacements; the bundle
is replaced before the installer, which is the cache commit point. An
interrupted mismatched pair fails verification rather than executing. Update is
remote-only.

The Go `update` and `uninstall` commands admit a detached job and exit. A short,
independent admission timeout is used so the job is not cancelled by the drain
it initiates. Successful admission is not successful completion: the durable
state and `logs/maintenance.log` are authoritative afterward.

An admission timeout can be ambiguous if the platform accepted the job just
before its registrar was stopped. In that case the private runner is retained
so an accepted job remains runnable; state expectations make a user retry safe.

The detached worker downloads and verifies the current remote installer before
running it. Uninstall alone may fall back to the verified cache when remote
download or verification fails before execution. Once any installer starts,
the worker never starts a second transaction as fallback. Direct invocation of
the cached installer with its uninstall flag omits job expectations and is the
recovery path when the binary cannot initialize.

Linux uses `systemd-run --user` only in a service-capable build when the caller
is itself managed by systemd (`NOTIFY_SOCKET` is present). Otherwise it uses a
detached `setsid` process. A managed `systemd-run` admission failure is reported
without falling back. Windows uses a hidden user Scheduled Task; it does not
open a terminal window.

Each admitted job owns `maintenance/jobs/<id>/`. The runner script removes it
on exit (`trap ... EXIT` on Linux, `finally` on Windows); Go removes it when
admission fails before the job could have started, and when the admitting
process proves the runner is dead (below). Nothing else reaps job directories.

Directory existence is the runner's completion signal, but only the process
that admitted a job watches it, and a runner killed without reaching its trap
(`SIGKILL`, power loss, `schtasks /End`) would otherwise leave that process
believing maintenance is in progress until it restarted. So the runner's first
action after arming cleanup is to write `<id>/pid`: its PID on Linux; its PID
and process creation FILETIME on Windows. `maintenance.ProbeJob` then answers
three ways. Directory gone: the job completed. Runner alive: still running.
Directory present and runner provably dead: orphaned, and the admitting process
removes the directory and treats the job like any other pre-commit failure.

"Provably dead" is deliberately strict about PID reuse. Linux requires
`kill(pid, 0)` to succeed and `/proc/<pid>/cmdline` to name `run.sh` inside
the job directory, which a recycled PID cannot satisfy; a zombie has an empty
cmdline and counts as dead. Windows opens the PID with
`PROCESS_QUERY_LIMITED_INFORMATION` (granted for any same-user process without
elevation) and requires both the recorded creation time and `STILL_ACTIVE`. A
missing or partial identity file is "starting" for one minute after admission
and "never ran" after that, which also resolves an ambiguous admission timeout
whose job never actually started. Evidence that cannot be read is an error, not
a guess: the admitting process keeps waiting and logs it.

The probe tracks the runner, not the installer it spawned. If only the runner
is killed while its installer is mid-transaction, the admitting process reaps
the directory and may attempt a retry; that retry is refused by `state.json`
or the exclusive lifecycle lock, and the running installer is unaffected by
the unlink. Other processes, and every process after a restart, ignore
orphaned directories; fail-closed behavior comes from `state.json`, never from
the job directory.

## Release source

When update support is retained, each installer persists its effective release
URL in `maintenance/release-url`, including `APP_RELEASE_URL` mirror overrides.
The write participates in the installation transaction and pre-migration rollback.
Checks and detached jobs read this file; detached jobs also pass the source to
the installer so an update stays on the same mirror. Missing or invalid metadata
prevents updates without falling back to a public host. The original baked
signing identity is enforced independently of the download location.

## Update checks

Application initialization does not start an automatic updater. Root command
setup may start one cancellable, check-if-due operation. Periodic one-shot and
service checks share the database lease; manual checks intentionally bypass it.
The one-shot may persist the checked source, latest version, and timestamp but
never launches maintenance. Availability is derived against the running version
and current source; admission does not clear it and failure does not restore it. It
is cancelled and joined before the database closes.

Only the service's blocking update-check component may admit an automatic
update. It first consumes fresh availability persisted by another process, then
runs the periodic loop. All in-process update checks share one mutex; the
database update lease remains the cross-process guard. Background checking and
notice display have separate preferences, both enabled by default. Automatic
application requires retained `update.apply.auto` code, a running service, and an
explicitly enabled `AutomaticUpdates` preference (disabled by default). Disabling
background checking also prevents automatic application. Preferences are reread
within one minute; already admitted jobs continue independently.

## Uninstall and recovery

Uninstall removes application data, the binary, and application service
registration. Windows also removes only the application's binary directory
entry from the user `PATH`, then removes that directory. Linux leaves the
generic marker-delimited `~/.local/bin` profile bootstrap in place because it is
shared and idempotent.

The following are retained:

- `control/`, including terminal state and locks
- `maintenance/`, including the verified cached installer and signature bundle
- `logs/`, including `maintenance.log`
- shared managed Cosign

Terminal state is `uninstalled`. There is no purge mode. To recover when the
normal binary is missing or initialization fails, run the retained installer
directly:

```sh
~/.<app>/maintenance/install.sh --uninstall
```

On Windows, run `%LOCALAPPDATA%\<AppName>\maintenance\install.ps1 -Uninstall`.
The command is idempotent and works without detached-job expectations.

## What changed

Earlier drafts spread paths across XDG directories with a separate runtime
directory, kept a "durable intent" file beside the state, and called the
process-side check a "migration guard". All of that collapsed into the layout
above: one storage root, `control/state.json` as the only durable lifecycle
record, lifecycle coordination in `internal/maintenance`, and install, update,
and uninstall as one script-owned transaction model with detached admission and
retained recovery artifacts.
