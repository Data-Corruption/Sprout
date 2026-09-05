---
title: 5. Publish a release
weight: 5
---

Releasing is changelog-driven. Than mean you add a new entry to the root
`CHANGELOG.md` and when it gets pushed to main, a GitHub Actions workflow
runs that detects it and builds, tests, releases. If the run gets
interrupted you can resume by a rerunning that run via a button in GitHub
or via their CLI tool `gh run rerun RUN_ID --failed`.

This assumes GitHub Actions and a Cloudflare R2 bucket. `rclone` does the
actual object operations, so pointing it at a different S3-compatible
destination is fairly trivial and plays nice with the way Sprout uploads.
It's not a simple config value you change, you'll need to edit stuff but
yeah, small edits, not a big rewrite.

## Set up the artifact host

In Cloudflare:

1. create an R2 bucket;
2. attach the public custom domain you want to use;
3. create an R2 API token with object read/write access to that bucket;
4. bypass edge caching for the release hostname, or preserve the
   `Cache-Control: no-store` metadata the script sets, end to end.

{{% details title="Where those things are in the Cloudflare dashboard" closed="true" %}}

Assuming an account and a domain already exist:

**Storage & databases → R2 object storage → Overview → Create bucket.** Name it
something like `your-app-cd`, region `Auto`, storage class `Standard`. Then in
**Bucket settings → Custom Domains → Add**, attach `cd.yourdomain.com` or any
subdomain you want.

**Account home → your domain → Rules → Overview → Create rule → Cache Rule.**
Match on hostname equals `cd.yourdomain.com`, action `Bypass cache`. Skipping
this is how you end up serving a stale `version` pointer to half the planet
after a release.

**R2 object storage → Overview → Manage API tokens:** it is on the right under
Account Details and easy to miss. Create a *user* API token with
`Object Read & Write`, then collect four values: the access key ID and secret
access key it shows you once, the account ID from Account Details, and the
bucket name.

Those four go into **Repo settings → Secrets and variables → Actions** as the
secrets listed below.

{{% /details %}}

Point `RELEASE_URL` at that domain in `scripts/build.sh`. It must end in `/`.
It gets baked into the installers as their default. Each installation persists
its effective source for later checks and updates, including an `APP_RELEASE_URL`
override pointing at an approved mirror.

In the GitHub repository, add these Actions secrets:

```text
R2_ACCESS_KEY_ID
R2_SECRET_ACCESS_KEY
R2_ACCOUNT_ID
R2_BUCKET
```

Then add the Actions **repository variable** `CI_ENABLED` set to `true`. Fresh
copies of the template leave it unset, so both workflows run only a small gate
job that reports CI is disabled. No checkout, tool download, build, test, or
deployment starts until you opt in. Once enabled, publication remains eligible
only for pushes to `main`.

{{< callout type="warning" >}}
Do not rename or move `.github/workflows/release.yml`. Its exact path on `main`
is part of the cosign certificate identity that every installer verifies, so
moving it invalidates signatures your users have already trusted. The file's
*contents* are free to change; only its path is frozen.
{{< /callout >}}

The release job is the only one that receives write access to repository
contents, for the tag, and an OIDC token, for keyless signing. Every other
permission in the workflow is read-only or empty, and every action it uses is
pinned to a commit SHA.

Build tools are pinned the same way, and every pin lives in one file:
`scripts/vendor.sh`. Esbuild and goimports are installed from source through
`go install`, which the Go checksum database covers, while Tailwind, DaisyUI,
cosign, rclone, shellcheck, and Hugo are downloaded to a gitignored `tools/`
directory and re-verified against SHA-256 pins on every use. Editing a version there without
its matching `*_SHA*` value makes the build refuse to start, so a version bump
cannot quietly turn integrity checking off. CI signs with the cosign that
`vendor.sh` fetches, which is the same version and hash `install.sh` bootstraps
for itself. Run `./scripts/vendor.sh` to fetch every tool supported on the
current host up front, or
`./scripts/vendor.sh --refetch` to ignore both the cache and `PATH`. `VERBOSE`
is the only optional repository variable left.

## Try production locally first

```sh
./scripts/build.sh --prod-all
```

Cross-compiles all four release binaries and verifies the values baked into
them. Hell of a lot easier debugging most errors here than in CI.

## Publish

The newest real heading in `CHANGELOG.md` is the candidate version:

```md
## [v1.0.0] - 2026-08-10

My name jeff.
```

Commit the complete release and push it to `main`. CI runs the tests, then:

1. inspects the current bucket and Git tags;
2. builds four binaries, or reuses an already verified candidate;
3. signs the checksums and uploads `releases/<version>/`;
4. downloads and verifies what it just uploaded;
5. deterministically renders both root installers;
6. signs and replaces an installer only if its bytes actually changed;
7. moves the root `version` pointer;
8. records the promotion, pushes the Git tag, then applies retention.

The bucket ends up shaped like this:

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

Installers read the root `version` once and stay pinned to that immutable
prefix for their whole run, which is why an install that started before a
release can finish after it. A release is public before its Git tag exists,
never the other way around. [How Sprout Fits Together]({{% relref "docs/architecture" %}}#releases-move-forward-in-durable-steps)
explains why that ordering is what it is.

## If it fails, retry

Aside from tests, e.g an ordinary runner, network, or upload interruption,
re-run the failed jobs on the original workflow run:

```sh
gh run rerun RUN_ID --failed
```

Do NOT:
- reuse the version with different source
- create the tag by hand, or push a dummy commit to trigger a fresh run
- add another changelog entry (i mean this one is fine, nothing will break but avoid it)

The new runner inspects the signed remote state and continues from the last thing it
can prove finished: a partial prefix is uploaded again, a complete verified one
is reused without rebuilding, an already-moved pointer is not moved twice, a
missing tag is pushed at the commit recorded during promotion.

The publisher refuses to overwrite a complete-but-invalid prefix, move the root
pointer backward, replace an installer pair it cannot explain, or force a tag
onto a different commit. Retry once if the failure might be transient. If the
same integrity error comes back, leave the bucket and the logs alone while you
work out why. Fail-closed automation is being annoying on purpose.

{{% details title="When the same integrity error keeps coming back" closed="true" %}}

Read the exact CI error and inspect the bucket before changing anything. Each of
these means something specific:

**A complete but invalid release prefix that was never promoted.** First prove
it: no root pointer naming it, no promotion marker, no Git tag. Then delete the
entire `releases/<version>/` prefix and rerun the same workflow.

**An incomplete or invalid prefix that *was* promoted or tagged.** Restore the
exact original signed objects from trusted storage. Never build different bytes
under a version somebody may already be running. If they cannot be restored,
treat it as a release incident and publish a new forward version.

**An invalid root installer pair with no matching verified staging.** Look at
the script and bundle before touching them. Either deliberately restore a
known-good pair, or remove both objects and rerun after reviewing the candidate
that will replace them.

**A Git tag already on a different commit.** Do not let automation force-move
it. Published tags stay immutable; correct the release with a new version
instead.

**An invalid promotion marker.** Restore or reconstruct it only from trusted
release records, its commit decides the tag target and its timestamp decides
retention.

When in doubt, preserve the remote objects and the logs while you investigate.
The automatic path is deliberately less convenient than guessing about release
integrity.

{{% /details %}}

## Retention and installers

The publisher always keeps the two newest promoted releases. An older one is
deleted only once it is also at least 24 hours old, which gives installer runs
that already pinned it *plenty* of time to finish.

Root installers are independent of application versions. A new release does not
re-sign or re-upload them unless their rendered bytes changed, and when they do
change, CI exercises the new installer against both the current and the staged
release before promoting anything. Keep that in mind if you plan on editing
the installers.

## Testing the machinery

Normal application work probably never needs this. But if you ever modify the
release / install systems you can test them via:

```sh
./scripts/test.sh -release   # local fake remotes, does not touch your bucket
./scripts/test.sh -e2e       # needs a local Incus daemon
```

{{% details title="One-time Incus setup" closed="true" %}}

Incus runs full Linux userspaces as unprivileged system containers, including
their real init and user service manager. On Ubuntu 24.04:

```sh
sudo apt-get update
sudo apt-get install -y incus
sudo systemctl enable --now incus.socket
sudo incus admin init --minimal
sudo usermod -aG incus-admin "$USER"
```

Reopen your login session after changing groups. Membership in `incus-admin`
grants full control of the local daemon and is effectively root access; add
only trusted users. See the
[Incus installation guide](https://linuxcontainers.org/incus/docs/main/installing/)
for other host distributions.

Ubuntu 24.04's native package is Incus 6.0 LTS, which supports its Linux 5.4
baseline and WSL's 6.6 kernel. Current Incus 7.x releases require Linux 6.12 or
newer, so do not replace the native LTS package on an older host. Under WSL,
enable systemd and restart WSL before installing Incus, and keep the checkout
on the Linux filesystem (for example, under `/home`) rather than `/mnt/c`. If
Docker's forwarding rules block guest package downloads, apply the targeted
`incusbr0` rules from the official
[firewall guide](https://linuxcontainers.org/incus/docs/main/howto/network_bridge_firewalld/);
the harness does not silently rewrite a developer machine's firewall.

Sanity check the daemon and one system container:

```sh
incus info
incus launch images:alpine/3.24 sprout-incus-smoke
incus exec sprout-incus-smoke -- true
incus delete sprout-incus-smoke --force
```

{{% /details %}}

[How Sprout Fits Together]({{% relref "docs/architecture" %}}#testing-goes-after-what-actually-breaks)
explains what these tests actually cover, and what the four CI jobs run.

Once a second release exists, test it. If you kept the default update features
run `<APP> update` and decline the confirmation (the app will then know a new
update is available). Open / refresh the dashboard and a notice should
appear. If you kept the `update.apply` you can click **Update** and the app
should update / page refresh in a few seconds. Before it runs anything, the
application re-downloads `install.sh` and cosign-verifies it against the
identity baked in at build time, so a tampered or mirror-modified script aborts
the update instead of executing.
