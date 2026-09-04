# Working on this codebase

Orientation for coding agents and new contributors. Read this before touching
anything; read the linked docs before touching the things they cover.

## What this is

A per-user Go CLI application that may also run a background worker, a local
HTTPS dashboard, and signed self-updates. Every CLI command (the service
included) is its own process built around one `App`; processes share SQLite in
WAL mode, rotating logs, and a small lock/state protocol in the control
directory. Installers on Linux (`install.sh`, POSIX sh) and Windows
(`install.ps1`) own install, update, and uninstall as transactions.

If `scripts/cut` exists in this tree, this is the upstream Sprout template. If
it doesn't, this is a finalized fork: the project is ordinary Go and shell that
belongs to whoever forked it, and the "Upstream template only" section below
does not apply.

## Where things live

| Path | Owns |
|------|------|
| `cmd/main.go` | Process entry, global flags, signal handling, final error print |
| `internal/app` | `App` composition root, cleanup stack, update checks, maintenance job admission |
| `internal/app/commands` | CLI commands, service coordinator, `runWorker` (the worker customization point) |
| `internal/maintenance` | Lifecycle guard, durable `state.json`, detached installer launch, service singleton lock |
| `internal/layout` | Every filesystem path and its permission policy; nothing else resolves paths |
| `internal/platform/database` | SQLite open/pool, ordered migrations, focused accessors per table |
| `internal/platform/http` | Listeners, router, auth middleware, sessions, CSRF/Origin guard, settings handlers |
| `internal/platform/secrets` | TLS material generation and storage |
| `internal/platform/release` | Reads the root `version` pointer from a release host |
| `internal/types` | Configuration shape and the permission bitmask |
| `internal/ui` | Embedded templates, vanilla JS modules, Tailwind/DaisyUI source |
| `internal/build` | Values baked in at build time |
| `pkg/` | Small reusable packages: locks, rotating logs, HTTP helpers, crypto, prompts, sd_notify |
| `scripts/build.sh`, `scripts/build/` | Editable project values, local builds, remote publication |
| `scripts/vendor.sh` | Pinned versions and SHA-256s for every third-party tool; the only fetcher |
| `scripts/install.sh`, `scripts/install.ps1` | The installers; templated by `build.sh` |
| `scripts/test.sh`, `scripts/test-*` | Test entrypoints and harnesses |
| `internal/cut`, `cmd/cut`, `cmd/cutmatrix` | Upstream only: the fence cutter and the 18-variant matrix; deleted by finalize |
| `docs/content/docs/` | Reader-facing documentation (Hugo site) |
| `docs/MAINTENANCE.md` | The install/update/uninstall protocol, precisely |

## Documents of record

- `docs/content/docs/architecture.md`: how the processes, database, service,
  dashboard, updates, and releases fit together. Start here.
- `docs/MAINTENANCE.md`: paths, state machine, locks, drain order, detached
  jobs. Read it before changing anything in `internal/maintenance`,
  `internal/layout`, or either installer.
- `docs/content/docs/getting-started/release.md`: publication ordering,
  resume, retention, signing identity.

When a document and the code disagree, the code is right and the document is
stale. Fix the document in the same change.

## Rules that are not obvious from the code

**Fail closed.** Unknown state, unreadable state, a permission that's too
loose, a symlink where a file should be, an installer pair that won't verify:
reject and say why. Never repair silently; a wrong mode is evidence.

**The installer is the only thing that mutates an installation.** The Go
binary admits a verified installer as a detached job and gets out of the way.
Do not add code that replaces the binary, edits the unit or scheduled task, or
runs migrations outside the `--migrate` path the installer authorizes with a
nonce.

**Invoking migration is the point of no return.** Before it, the installer can
roll back. After it, failure keeps the transitional state and the operator
reruns the installer. Do not add code that pretends a downgrade happened.

**Migrations are ordered functions in
`internal/platform/database/migration.go`.** Database steps and the
`user_version` bump commit together. Non-database side effects in a step must
be idempotent because an interrupted step can run again.

**Ordinary processes hold the shared lifecycle lock and publish a PID marker;
installers take the exclusive side.** Every process also watches `state.json`
and cancels its context when the phase leaves `ready` or the version or epoch
changes. Anything that blocks must return promptly on context cancellation.

**Windows has no graceful task stop.** Controllers write an expiring
`service.stop` lease; the service polls it. A controller retires only the lease
it wrote (`ReleaseServiceStopRequest`); start paths clear unconditionally.
`schtasks /End` is the fallback after a timeout, never the first move.

**Sessions have a fixed lifetime.** Cookie `MaxAge` and the database row both
expire 30 minutes after login. There is no sliding renewal on purpose.

**Every process keeps a modest fixed SQLite pool (4).** The Wasm driver gives
each connection its own memory sandbox and SQLite serializes writers anyway.
`go.mod` pins `ncruces/go-sqlite3` to a floor that fixes a Windows WAL
corruption bug; do not lower it.

**Rotating-log failures are sticky only when the file is.** `pkg/xlog/rlog`
disables itself after an I/O error on the log file; a lock-acquisition timeout
or a prune failure returns an error for that write and the next flush retries
with the buffer intact. Keep that split when touching the writer.

**Dependencies must earn their keep.** Standard library first; `golang.org/x/`
is fine. A new third-party module needs to solve a non-trivial problem cleanly
without dragging a tree behind it.

**Test files end in `_test.go`, nothing else.** `cmd/hygiene_test.go` fails on
`*_test_*.go` names and on `testing` reaching the shipped binary. That bug
happened once for real.

**Line endings.** `.gitattributes` forces LF for `*.go` and `*.sh`. Some
PowerShell and a few other files are CRLF; do not "fix" them wholesale.

**Windows code compiles only under `GOOS=windows`.** After touching a
`_windows.go` file, run `GOOS=windows go vet ./...` and
`GOOS=windows go test -c -o /dev/null ./<pkg>` to catch build breaks; the
tests themselves run in CI on a Windows runner.

## Build and test

```sh
./scripts/test.sh              # go test -race ./... with embed placeholders; run constantly
./scripts/test.sh -lint        # pinned shellcheck over the shell scripts; run after touching them
./scripts/build.sh             # dev binary: isolated -dev storage, debug logs, auth bypass, no updates
./scripts/build.sh --prod      # production-mode binary for this architecture
./scripts/build.sh --prod-all  # all release binaries
gofmt -l ./cmd ./internal ./pkg && go vet ./... && GOOS=windows go vet ./...
```

Slower harnesses (Linux only): `./scripts/test.sh -release` runs the
publication state machine against a local rclone backend; `-e2e` runs the
install/update/uninstall lifecycle across distros in Incus containers, with
per-case logs under `out/lifecycle-e2e-logs/<run>/`. See `architecture.md` for
what each covers.

Third-party tools (Tailwind, esbuild, cosign, rclone, shellcheck, goimports,
Hugo) are pinned by version and SHA-256 in `scripts/vendor.sh` and fetched into the
gitignored `tools/`. Never depend on `tools/` contents directly.

Generated and ignored: `internal/ui/assets/{css/output.css,js/output.js,manifest.json}`,
`out/`, `tools/`, `docs/out/`. Edit sources under `internal/ui/assets/css/input.css`
and `internal/ui/assets/js/src/`.

## Where to add things

- CLI command: constructor in `internal/app/commands`, registered in
  `commands.go`. A test scans the AST and fails if you forget to register it.
- Worker: replace the body of `runWorker` in `internal/app/commands/worker.go`.
  If you remove the hash example, `scripts/test-lifecycle-e2e.sh` already has
  `TEST_EXAMPLE_HASH=false` in forks.
- Durable state: a migration step, then accessors beside the owning subsystem.
- Dashboard route: a handler package under `internal/platform/http/router`,
  mounted in `router.go`, with an explicit permission on writes.
- Permission: `internal/types/perms.go`.
- Project values (name, release URL, contact, ports): the block at the top of
  `scripts/build.sh`.

## Style

Explicit errors wrapped with `%w`, `errors.Join` for cleanup, `sync.Once` for
idempotent close, context cancellation as the cooperative stop everywhere
(doesn't need handling *everywhere*, e.g. database txns. Just try not to block
forever). Comments explain why an ordering or check exists, not what the next
line does. Tests use only the standard `testing` package, open real SQLite in
`t.TempDir()`, and spawn real subprocesses for cross-process claims. Keep it
that way.

## Upstream template only

Skip this section in a finalized fork.

Optional features are source code fenced with ownership markers, not runtime
flags:

```text
// --- BEGIN update.self ---   ...   // --- END update.self ---
// --- FILE service.https ---        (whole file)
// --- FILE template ---             (deleted by every finalize)
```

Four comment styles are recognized (`//`, `#`, `<!-- -->`, `/* */`). Owners:
`update`, `update.self`, `update.notifications`, `update.auto`, `service`,
`service.https`, plus reserved `template`. Nested fences express "needs both";
cutting a parent cuts its dotted children. Markdown is deliberately not a
marker candidate.

- New optional code must live inside the correct fence or `FILE` owner, and
  must leave every one of the 18 cut variants compiling and passing.
  `./scripts/test.sh -cut` runs them all.
- Do not add compatibility shims, legacy decoding, or deprecation paths.
  Upstream never publishes releases; forks freeze their own invariants at
  their first release. Change the initial migration, layouts, and protocols in
  place.
- Two things a fork cannot change after shipping: the artifact layout and the
  cosign signing identity, which is derived from the path
  `.github/workflows/release.yml`. Never rename or move that file.
- `scratch-pad.md`, when present, is gitignored upstream design rationale.
  Trust the code over it.
