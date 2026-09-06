---
title: Architecture
weight: 2
aliases:
  - /docs/philosophy/
---

Sprout is a Go application template with per-user installation, shared SQLite
state, an optional service and dashboard, and signed updates. Each CLI command
runs in its own process. The service is one of those commands.

## Design choices

A project created from Sprout owns its source and can diverge immediately.
There is no Sprout runtime dependency or requirement to keep up with upstream.
The template provides a working starting point for applications whose needs
will differ over time.

The code favors the standard library, explicit subsystem boundaries, and a
small set of dependencies. Linux and Windows share an installation protocol;
platform-specific code handles paths, locks, and service management. Keeping
that scope limited makes it practical to test the full lifecycle.

## Processes

```mermaid
flowchart LR
  subgraph cli ["CLI process"]
    cmd["command"] --> app1["App"]
  end
  subgraph service ["Service process"]
    router["HTTPS router"] --> app2["App"]
    worker["runWorker"] --> app2
  end
  browser["Browser"] -->|HTTPS| router
  app1 --> db[("SQLite in WAL mode")]
  app2 --> db
```

`cmd/main.go` creates an `App` and registers the CLI commands. Normal startup
resolves the storage layout, checks lifecycle state, takes a shared lifecycle
lock, and records a PID marker. It then starts rotating logs, opens SQLite,
checks the schema version, and loads configuration.

Commands receive the `App` containing these resources. Cleanup runs in reverse
order. Errors return to `main`, where they are logged and printed with a
nonzero exit status.

## SQLite

The database lives under `data/db/` in the per-user storage root:
`~/.<APP>` on Linux and `%LOCALAPPDATA%\<APP>` on Windows. Each process uses
`database/sql` with `ncruces/go-sqlite3`, which runs SQLite without cgo by
doing a three step machine-translation (C -> WASM -> Go).

The driver must remain at `v0.35.3` or newer. Earlier versions have a
[Windows multi-process WAL corruption bug](https://github.com/ncruces/go-sqlite3/issues/404).
The fix requires Windows 10 1803 or Server 2019 or newer; Sprout targets Windows 11.

WAL allows concurrent readers while SQLite serializes writes. A busy timeout
and eager write transactions allow brief contention to wait. Each process has
a pool of four connections; every Wasm connection owns a memory sandbox, and
more connections do not add concurrent writers.

The initial schema contains JSON configuration with transactional `View` and
`Update` accessors, dashboard sessions when HTTPS is retained, and hash requests
when the service is retained. The hash command demonstrates IPC: the CLI inserts
a request, the worker writes the SHA-256 result, and the CLI reads it back.

### Migrations

Migrations are ordered functions in `internal/platform/database/migration.go`.
Each database step and its `PRAGMA user_version` advance commit together.
New installations run all steps from version zero; upgrades run the remaining
steps. Non-database side effects must be idempotent because SQLite cannot roll
back a file write or external API call.

Normal processes reject a schema mismatch. The installer authorizes migrations
in production. An isolated dev build can initialize a version-zero database.

## Installation lifecycle

Processes share `control/lifecycle.lock` and record PID markers under
`control/instances/`. Before changing an installation, the installer atomically
publishes a transitional phase in `control/state.json`: `installing`, `updating`,
or `uninstalling`, with the target version and a fresh migration nonce.

Every running process watches that state and cancels its context when the phase
leaves `ready`, or the version or installation epoch changes. New processes
refuse to start during a transition. The installer stops matching processes,
takes the exclusive lifecycle lock, replaces installed state, and invokes the
new binary's migration path. Migration requires the matching phase, version,
and nonce. Success publishes `ready` while the exclusive lock is still held.

SQLite coordinates ordinary data access. The lifecycle lock keeps old processes
out of the database during binary replacement and migration. PID shutdown checks
also match the executable path, so a reused PID does not cause an unrelated
process to be stopped. After taking the exclusive lock, the installer removes
stale markers.

**Invoking migration is the point of no return.** Before it, failure can restore
the previous binary, release source, and service definition. Once migration may
have changed state, failure leaves the installation transitional. Rerunning the
installer recovers it. Downgrade support would require a coordinated database
snapshot and reversal of external side effects.

Any additional process that opens this database must join the lifecycle
protocol: state check and watcher, shared lock, and PID marker. The installer's
shutdown logic must also recognize its executable. See
`internal/maintenance/guard.go` and `docs/MAINTENANCE.md` before extending it.

All coordination files live in the storage root; there is no separate runtime
directory. On Linux, application-owned directories require user ownership and
mode `0700`. Unsafe permissions and symlinks are rejected. Uninstall retains
`control/`, `maintenance/`, and `logs/` for recovery.

## Service

The service starts `runWorker(ctx, app)` and any retained dashboard listeners
under one cancellation context. It reports readiness after all components are
ready. If one fails, it cancels the others, waits for cleanup, and joins their
errors.

Linux uses a per-user systemd unit; Windows uses a per-user scheduled task.
Both invoke `service run`. A process-lifetime lock on `control/service.lock`
keeps the service a singleton, including when started manually. The example
queue relies on this single-consumer guarantee.

### Windows shutdown

Task Scheduler has no graceful stop operation. Controllers write an expiring
`control/service.stop` lease, which the service checks every 250 ms. The
coordinator cancels the worker and listeners and waits for them to finish.
The controller waits for Task Scheduler to report the stopped state, using
`schtasks /End` only after a timeout.

Restart waits for shutdown before clearing the lease and starting the task;
otherwise Task Scheduler's `IgnoreNew` policy can discard the start. Start also
clears stale leases. A controller retires only its own stop request. Install,
update, uninstall, and dashboard controls use the same protocol.

## Dashboard

The HTTPS feature owns routing, handlers, templates, assets, credentials,
sessions, permissions, certificates, and listeners. The main listener always
uses HTTPS. An optional plain HTTP listener accepts only loopback binds for a
local reverse proxy. Both serve the same router.

The router consults `X-Forwarded-Proto` only for requests arriving without TLS.
Its trust therefore depends on the plain listener remaining loopback-only.
Adding a non-loopback plain listener requires explicit proxy trust handling.

On first run, Sprout generates an ECDSA P-256 certificate with SANs for localhost,
loopback, the hostname, and detected interface addresses. The pair is reused
across restarts. On Linux, its directory is `0700` and key is `0600`; loosened
permissions cause startup to fail. Deleting the pair allows regeneration.

Credentials are created through the CLI. Usernames are normalized and unique;
passwords use Argon2id. SQLite sessions survive restarts and are revoked when
their credential is removed. A session stores the SHA-256 of a random 256-bit
cookie token, the username, and permissions. Sessions expire 30 minutes after
login without renewal. Cookies have the same lifetime and use HttpOnly,
SameSite=Strict, and Secure outside dev builds.

Login performs one password comparison, including a dummy comparison for unknown
users. Each router has a login limiter and bounded password-verification slots.
Static assets and login are public; settings and controls require a session.
Production writes require a matching `Origin`, plus a JSON content type on JSON
endpoints. Security headers wrap every route.

The starter permissions are `settings`, `server.control`, and `admin`. Reads
require a session; writes check the relevant permission. Application-specific
permissions belong in `internal/types/perms.go`. Extend this local dashboard
baseline to match your application's exposure and data.

Source assets and templates live under `internal/ui`. The build uses Tailwind,
DaisyUI, and esbuild, hashes the outputs, and embeds them into the binary.
There is no npm project. New configurations bind to loopback; LAN access requires
an explicit bind change. Dev builds bypass auth and use isolated `-dev` storage.

## Updates

Feature selection controls update capabilities:

```text
update                         discovery and notices
└── update.apply               user-requested application
    └── update.apply.auto      unattended application; also needs service

service                        background worker
└── service.https              dashboard
```

Cutting a prerequisite also cuts its dependents. All feature combinations still
support updates by rerunning the installer. Notifications and background checks
default to enabled; unattended application requires an explicit installation
preference and a running service. Ordinary CLI commands never install updates
in the background.

Periodic checks share an expiring database lease across processes. Success
commits the result and releases the lease. Failure leaves it until expiry to
avoid rapid replacement checks. Explicit user checks bypass that lease.
Availability is derived from the running version, release source, and cached
check result; installer admission has separate job state.

### Detached installation

An application-driven update downloads the installer, verifies its cosign
identity, and launches a detached job. The installer then owns shutdown,
replacement, migration, recovery, and restart. On Linux, a caller in the
systemd-managed service uses `systemd-run --user`, so stopping the service does
not stop its updater. CLI and manually run service callers detach into their
own session. Cosign is resolved from `~/.local/bin/cosign` first, then `PATH`.

Each job runs under `maintenance/jobs/<id>/` and removes its directory on exit.
The admitting process uses that cleanup to detect a failure before the installer
drained it. The runner records its process identity so a directory left by a
provably dead runner can also be reaped and retried. Platform identity checks
are specified in `docs/MAINTENANCE.md`.

### Release sources and mirrors

The installer persists its effective source in `maintenance/release-url`,
including an `APP_RELEASE_URL` override. That metadata participates in rollback.
Checks and detached jobs use the saved source; a detached updater passes it to
the next installer. Missing or invalid metadata prevents updating rather than
falling back to the public host.

Signatures cover artifact bytes and the original workflow identity, so unchanged
artifacts verify from a mirror. Its `version` pointer decides which release users
can discover. Changing the source does not change the trusted signer.
[Run a mirror]({{% relref "docs/getting-started/mirror" %}}) covers staging and
promotion.

A modified installer needs a new signature and explicit trust in its signer.
Its binary checks may still trust the original signer, but application-driven
updates reject installers signed only by the mirror operator. Those installers
must be verified and run separately.

## Releases

`scripts/build.sh` owns project values and the build phase order. Modules under
`scripts/build/` handle local artifacts and publication. GitHub Actions selects
the candidate version from `CHANGELOG.md` on pushes to `main`.

Objects are relative to `RELEASE_URL`. A URL path becomes the publication prefix
inside the bucket, including `.state/` and `.staging/`; sibling prefixes are left
alone.

```text
install.sh
install.sh.cosign.bundle
install.ps1
install.ps1.cosign.bundle
version
releases/
  v1.0.0/
    version
    linux-amd64.gz
    linux-arm64.gz
    windows-amd64.exe.gz
    windows-arm64.exe.gz
    checksums.txt
    checksums.txt.cosign.bundle
```

Publication proceeds in this order:

1. Build and sign a candidate, or reuse an already verified one.
2. Upload the immutable `releases/<version>/` prefix and verify its remote bytes.
3. Render root installers. Test changed installers against the candidate and
   any current release, then sign and publish them.
4. Replace the root `version` pointer, making the release public.
5. Record promotion, push the Git tag, and apply retention.

Each installer bundle is published before its script; the script is the pair's
commit point. A temporary mismatch fails verification. Verified staging allows
an interrupted pair replacement to resume. Unchanged installers are left alone.

Installers read the root pointer once and use that release for their entire run.
A fresh runner can inspect signed remote state and resume publication. It rejects
invalid complete prefixes, incomplete promoted releases, unexplained installer
pairs, backward promotion, and tags pointing to another commit.

The publisher keeps the two newest promoted releases and any older release
until at least 24 hours after its promotion. More than two may therefore remain
in the bucket. Mirror operators maintain their own retention schedule.

[Publish a release]({{% relref "docs/getting-started/release" %}}) covers setup,
verification, and recovery procedures.

### Build tools and signing

Third-party tool versions live in `scripts/vendor.sh`. Downloaded executables
and assets are verified against SHA-256 pins on every use. Esbuild and goimports
are installed through Go modules and the checksum database. Local builds may use
Tailwind from `PATH` for Nix environments; CI uses the pinned download.

GitHub Actions are pinned to commit SHAs. The release job alone requests
`contents: write` for tags and `id-token: write` for keyless cosign signing.
The signing identity includes the repository and
`.github/workflows/release.yml@refs/heads/main`; changing that identity breaks
verification for existing installations.

## Template finalization

Optional source is fenced with feature ownership markers. Nested blocks require
both owners; whole-feature prerequisites live in `internal/cut/features.go`.
The reserved `template` owner marks tooling removed by every finalization.

```go
// --- BEGIN update.apply ---
// optional code
// --- END update.apply ---

// --- FILE service.https ---
```

Equivalent marker forms exist for shell, YAML, HTML, and CSS. Preview validates
markers and module references without changing files. Finalization deletes
selected files and blocks, renames module references, strips markers, runs
`goimports` and `go mod tidy`, and checks the resulting tree. File modes and line
endings are preserved. The finalized application contains no cutter.

## Testing

Tests use Go's standard `testing` package and live beside the code. Database
tests open real SQLite in temporary directories. Auth tests use real password
comparisons and sessions. Subprocess tests exercise database initialization,
TLS generation, shared log rotation, and lifecycle lock exclusion across
independent processes.

The upstream cut matrix tests all eleven valid feature combinations on Linux
and Windows. It finalizes temporary copies with replacement module paths and
checks compilation, tests, and surviving shell scripts. The cutter and matrix
are removed from finalized projects.

Linux lifecycle tests build a production binary and install it in unprivileged
Incus system containers. Coverage includes Debian, Ubuntu, Fedora, Arch, Alpine,
openSUSE, Void, and Rocky. Systemd cases run real user managers; Alpine and Void
exercise binary-only installation. Probes cover service state, the hash worker,
HTTPS health, restart, reinstall, migration failure, and recovery. Containers
share the host kernel.

Per-case logs remain under `out/lifecycle-e2e-logs/<run>/`. `KEEP_FAILED=true`
retains failed containers and backing directories for inspection. Windows CI
runs the native Go suite and a PowerShell installer harness covering upgrades,
process shutdown, stale markers, and version pinning.

Release tests use an rclone local backend and a cosign stand-in. They exercise
interruption, resume, promotion, installer pair recovery, immutable tags, and
retention without a real bucket. Lifecycle fixtures skip signature verification
with `APP_SKIP_VERIFY=true`; binary SHA-256 checks still run.

See [Build and run]({{% relref "docs/getting-started/build" %}}#test-your-changes)
for test commands and Incus setup.

### CI jobs

Every workflow starts with a gate requiring `CI_ENABLED=true`. Validation runs
for pull requests targeting `main` and pushes to `main`:

- `cut-matrix`: all feature combinations, upstream only.
- `lifecycle-e2e`: Linux Go tests, shell lint, release tests, and a distro subset.
- `windows-test`: native Go tests, representative cuts, PowerShell parsing,
  and the installer harness.
- `release`: push-only publication after validation; also provisions its own
  Incus daemon for candidate installer checks.

## Code map

| Path | Responsibility |
|---|---|
| `cmd/main.go` | Process entry, flags, and final error handling |
| `internal/app` | Resource initialization, cleanup, update checks, job admission |
| `internal/app/commands` | CLI commands, service coordination, worker |
| `internal/maintenance` | Lifecycle state, locks, detached installers |
| `internal/layout` | Filesystem paths and permissions |
| `internal/platform/database` | SQLite, migrations, accessors |
| `internal/platform/http` | Listeners, routing, authentication, handlers |
| `internal/platform/secrets` | TLS generation and storage |
| `internal/ui` | Templates and frontend source |
| `internal/build` | Values compiled into the binary |
| `pkg` | Shared utilities: locks, logs, HTTP, crypto, prompts, systemd notification |
| `scripts/build.sh`, `scripts/build/` | Local builds and release publication |
| `scripts/vendor.sh` | Pinned tools and downloads |
| `scripts/test.sh`, `scripts/test-*` | Test entrypoints and harnesses |
| `scripts/install.sh`, `scripts/install.ps1` | Installation, update, uninstall, recovery |
