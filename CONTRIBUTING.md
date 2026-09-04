# Contributing

Thanks for looking. This file is short on purpose; `AGENTS.md` is the real
orientation for anyone changing code, human or otherwise, and the docs under
`docs/content/docs/` go into more detail. Read those first. This file is for
the upstream Sprout template. The generator will remove it.

## Before you start

Open an issue before a large change. Sprout is a working application template,
not a framework, and it keeps a deliberately small scope believe it or not.
Things it will not add, so you don't spend a weekend on them:

- an upstream patch or migration system for clones / forks/ template users;
- a generic service, worker, or plugin framework;
- runtime feature toggles standing in for the source fences;
- new third-party modules that don't solve a non-trivial problem cleanly.

Bug reports need the OS and distro, architecture, Go version, the output of
`<binary> --build-vars`, and the exact command and error. Installer problems
should include `logs/maintenance.log` from the storage directory the installer
names in its output.

## Development

Linux or WSL on `amd64`/`arm64`, the Go version in `go.mod`, Bash, `curl`,
and `gcc` (only because `go test -race` needs it).

```sh
./scripts/test.sh            # go test -race ./...; run liberally
./scripts/test.sh -lint      # pinned shellcheck; after touching any .sh
gofmt -l ./cmd ./internal ./pkg && go vet ./... && GOOS=windows go vet ./...
```

If you touched the installers or the release scripts, also run
`./scripts/test.sh -release` (local fake remote, seconds) and, if you can,
`./scripts/test.sh -e2e` (needs Incus; see the release docs). If you touched a
`_windows.go` file, `GOOS=windows go test -c -o /dev/null ./<pkg>` catches
build breaks locally; the tests themselves run in CI on Windows.

## Pull requests

- One concern per PR. Small is good.
- Explain why in the description, not what; the diff shows what.
- Docs live next to the code they describe. If your change makes
  `docs/content/docs/**`, `docs/MAINTENANCE.md`, or `AGENTS.md` wrong, fix them
  in the same PR.
- Tests use only the standard `testing` package, open real SQLite in
  `t.TempDir()`, and spawn real subprocesses for cross-process claims. Test
  files end in `_test.go`; a hygiene test fails on anything else.
- Fail closed. Unknown state, a permission that is too loose, a symlink where a
  file should be: reject with a clear error. Never repair silently.
- `.gitattributes` forces LF for `*.go` and `*.sh`. A few other files are
  deliberately CRLF; leave them.

AI-assisted contributions are ok.. but you still need to understand every line
submitted and be able to explain it in review; PRs that read as unreviewed
model output will be closed. That and it needs to meet basic quality standards.
Yes this is subjective, if you think of a better rule here, fork the project
and try using it. Diversity in how we treat this stuff is unironically good.

## Security

Do not open a public issue for a vulnerability. Use GitHub's private
vulnerability reporting on this repository (Security tab, "Report a
vulnerability"). Include the version, platform, and a reproduction if you have
one. You'll get a reply; fixes ship as a normal forward release.

## License

MIT, see `LICENSE.md`. By contributing you agree your contribution is licensed
the same way.

## Other template only stuff

- Optional features are source fences (`// --- BEGIN update.self ---` and
  friends), not runtime flags. New optional code goes inside the correct fence
  or `FILE` owner and must leave every one of the 18 cut variants compiling
  and passing: `./scripts/test.sh -cut`.
- No compatibility shims or deprecation paths. Upstream never publishes
  releases; forks freeze their own invariants at their first release. Change
  the initial migration, layouts, and protocols in place.
- `Markdown` cannot carry cut markers by design, so anything you add to a
  `.md` file that only makes sense upstream should be written the way this
  file and `AGENTS.md` are: applicable in a fork, or clearly labelled as not.
- The docs site under `docs/` builds with the pinned Hugo from
  `./scripts/vendor.sh hugo`; `hugo --panicOnWarning` must pass. Deployment is
  upstream-only and lives in `docs/DEPLOYMENT.md`.
- CI is gated on the `CI_ENABLED` repository variable so fresh template copies
  stay inert. If your PR's checks show only the gate job, that is the
  variable, not your change.
