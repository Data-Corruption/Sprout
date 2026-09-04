---
title: 3. Cut and rename
weight: 3
---

{{< callout type="important" >}}
This manual flow is complete and supported. Run it once, before changing
application code or deploying anything.
{{< /callout >}}

Three transformations, in this order: delete the features you don't want, put
your name on what is left, and remove the machinery that did the deleting. Do
them on the `setup` branch from [step 1]({{% relref "docs/getting-started/create" %}}),
before you write any application code.

## Preview the cut

Pass every feature you decided against and the module path for your project in
one command:

```sh
./scripts/cut --module github.com/YOU/YOUR_APP service.https update
```

By default it gives a preview and changes nothing. It prints the files it would delete
outright, the fenced blocks it would remove, the markers it would strip, and the
Go imports it would rename, after validating the marker structure and every
behavior-bearing module reference in the repository. The preview includes the
template-only cutter and matrix machinery, which never belongs in the finished
application. If anything listed surprises you, there ya go.

The preview is not quite byte-for-byte. `--finalize` additionally runs
`goimports` over the rewritten Go files, so imports left unreferenced by removed
blocks disappear then rather than here. That is also why finalizing may need
network access on a machine that has neither `goimports` on `PATH` nor a copy in
`tools/`; a `goimports` on `PATH` is reused only when its embedded module version
matches the pin. Previewing never needs the tool or network.

## Make it

```sh
./scripts/cut --finalize --module github.com/YOU/YOUR_APP service.https update
```

`--finalize` applies the previewed plan. It removes all ownership
comments and all template-only tooling. Keeping all six application features is
a valid choice and still should be done, both to rename the module and to
finish the template:

```sh
./scripts/cut --finalize --module github.com/YOU/YOUR_APP
```

The cutter validates before it touches anything, deletes whole feature-owned
files and fenced blocks, preserves file modes and line endings, formats changed
Go, and writes through temporary files. Retained files are rewritten before the
cutter removes itself, then `goimports` prunes the imports those removals left
unused and the cutter re-walks the tree to confirm no marker survived and every
planned deletion happened. Every deletion is closed: nothing uncomments a
fallback implementation, adds a missing import, or applies a hidden dependency
rule.

And that's it, once the markers are gone there is no supported way to
make another cut.

{{% details title="What the markers looked like" closed="true" %}}

You will see these in the source until you finalize, and in a diff afterwards.
A block marker means "this code belongs to this feature," and a file marker
means the whole file does. The reserved `template` owner marks cutter and matrix
machinery that every finalization removes:

```go
// --- BEGIN update.self ---
// optional code
// --- END update.self ---

// --- FILE service.https ---

// --- FILE template ---
```

Equivalent forms exist for shell/YAML and HTML. Code that needs two features
uses nested fences, so cutting either one removes it. That nesting is why the
cutter needs no dependency logic.

{{% /details %}}

## Set the project values

The project block at the top of `scripts/build.sh` contains the values intended
for normal setup:

```sh
APP_NAME="your-app"
RELEASE_URL="https://cd.example.com/"
CONTACT_URL="https://github.com/YOU/YOUR_APP"
DEFAULT_LOG_LEVEL="warn"
```

`APP_NAME` is the binary file name and the storage root (`~/.<APP_NAME>` on
Linux, `%LOCALAPPDATA%\<APP_NAME>` on Windows), so it is the one your users
actually see. `RELEASE_URL` must end in `/` and can
stay a placeholder until [step 5]({{% relref "docs/getting-started/release" %}}).
`CONTACT_URL` is your repository or landing page and is available for the
application's User-Agent (currently unused). `DEFAULT_LOG_LEVEL` controls a
fresh install.

If you kept the service, set `SERVICE_DESC` to something recognizable in a
service listing. If you kept the dashboard, change `SERVICE_DEFAULT_PORT` if
`8484` is not yours. Anything outside the project block is not meant to be
modified by standard use.

Everything else in the repository that says "sprout" is basically cosmetic,
and you can change them if you want.

## Verify

```sh
./scripts/test.sh
./scripts/build.sh
```

The first runs `go test -race ./...` against the tree you just created. The
second builds a development binary and then checks the values that were baked
into it, comparing the binary's own `--build-vars` output against `APP_NAME`,
the version, and the rest. The package path is derived rather than copied, and
this check still turns any malformed `-ldflags` value into an immediate
`name mismatch` failure.

Do not continue past an error here. All cut cases are tested upstream so it
should work. If you happen to find an issue with Sprout, open an issue and
gimme as much detail as possible. OS, arch, build tool versions, etc.

## Commit the setup

Finalization already removed the cutter, its eighteen-tree upstream matrix, and
the focused installer variants. Commit the finished tree as one reviewable
change:

```sh
git add -A && git commit -m "Set up project from Sprout"
```

You now own a normal Go repository.
