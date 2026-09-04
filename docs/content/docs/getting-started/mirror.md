---
title: 7. End-user mirror
weight: 7
---

Most applications never need this page. It exists for the situation where
somebody else decides which version your users are allowed to run: compliance
review, vulnerability scanning, an internal approval gate, or a network that
does not reach your release host.

The whole thing works because of one property:

{{< callout type="information" >}}
Cosign signatures cover bytes and signer identity. Nothing about the download
location is signed. A byte-for-byte copy of the official artifacts verifies
identically from any host on earth.
{{< /callout >}}

So a mirror is not a special build, a re-sign, or a fork. It is a copy of static
files on any host that can serve them, and the verification your users perform
still chains back to your CI identity rather than to the mirror operator.

## What gets copied

Two independent things live at the release root: the generic installers, which
change only when their own bytes change, and a `version` pointer naming the
promoted release. Everything else lives under an immutable
`releases/<version>/` prefix. [Publish a release]({{% relref "docs/getting-started/release" %}})
has the full layout; a mirror needs the four root installer objects, the root
`version`, and every prefix it wants to keep serving.

## Operate one

Copy unmodified. Upload the immutable prefix first and replace the mirror's root
`version` last, exactly like the real publisher does, so a user who starts an
install during your sync still finishes against a complete release:

```sh
BASE=https://cd.example.com/   # the official release URL
version=$(curl -fsSL "${BASE}version")
case "$version" in v[0-9]*.[0-9]*.[0-9]*) ;; *) exit 1 ;; esac

mkdir -p "releases/$version"
for f in version linux-amd64.gz linux-arm64.gz windows-amd64.exe.gz \
         windows-arm64.exe.gz checksums.txt checksums.txt.cosign.bundle; do
  curl -fsSLo "releases/$version/$f" "${BASE}releases/$version/$f"
done
for f in install.sh install.sh.cosign.bundle install.ps1 install.ps1.cosign.bundle; do
  curl -fsSLO "$BASE$f"
done
printf '%s\n' "$version" > version

# upload releases/$version, then the root installer pairs, then root version
```

Scan, test, and approve however your organization requires before publishing.
Then do not edit anything you publish. Editing any file breaks its signature by
design, the artifacts chain to the official CI identity, and that chain is the
entire point of mirroring rather than rebuilding.

Retention is your call and it is a different call than the release host's. The
official host keeps the two newest promoted versions and will not remove an
older third until it is at least 24 hours old, because its only job is to let
in-flight installs finish. A mirror can keep everything forever, and often
should: it is the record of which versions your users were approved to run.
Whatever you do, keep the previously promoted prefix around while installs
pinned to it may still be running.

## Install from one

Users run the official installer and point it somewhere else with an
environment variable.

Linux:

```sh
curl -fsSLO https://mirror.example.com/install.sh
curl -fsSLO https://mirror.example.com/install.sh.cosign.bundle
cosign verify-blob \
  --certificate-identity "https://github.com/OWNER/REPO/.github/workflows/release.yml@refs/heads/main" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  --bundle install.sh.cosign.bundle install.sh
APP_RELEASE_URL=https://mirror.example.com/ sh install.sh
```

Windows:

```powershell
irm "https://mirror.example.com/install.ps1" -OutFile install.ps1
irm "https://mirror.example.com/install.ps1.cosign.bundle" -OutFile install.ps1.cosign.bundle
cosign verify-blob `
  --certificate-identity "https://github.com/OWNER/REPO/.github/workflows/release.yml@refs/heads/main" `
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" `
  --bundle install.ps1.cosign.bundle install.ps1
$env:APP_RELEASE_URL = "https://mirror.example.com/"
powershell -ExecutionPolicy Bypass -File install.ps1
$env:APP_RELEASE_URL = $null
```

Verify against the *original* release workflow identity, not the mirror's. From
there the run is identical to an official install. The installer reads the
mirror's root `version` once, pins it, downloads `checksums.txt` and its bundle,
verifies them with cosign, and SHA-256-matches both the prefix version file and
the binary it selected.

## Mirror installs never auto-update

This is deliberate, and overrides if you left `update.auto` code in the source.

The installers write `maintenance/release-url` under the storage root, the
source that update checks and self-update read, but only when the effective URL
matches the official one baked into them at build time. Passing `APP_RELEASE_URL` means that file is never
written. Periodic checks stay off, and an explicit `update` gives source-neutral
guidance to repeat the original installation or follow administrator
instructions. It never falls back to a public host.

Two concrete reasons:

- self-update executes the release host's installer, so pointing it at a
  third-party host would hand that operator code execution on every update;
- mirrors exist to pin approved versions, and auto-update is the act of
  unpinning them.

So updating a mirror install is a two-party operation, which is the shape you
wanted when you built a mirror: you copy and approve the new release set, your
users re-run the same install command. As a backstop, even a forced update from
a mirror fails safe. The app cosign-verifies the installer it downloads against
the official identity before executing it, so a modified script never runs.

## If you really must modify the installer

Discouraged, but the signature model degrades honestly instead of silently:

- your modified script no longer verifies against the official identity, so
  re-sign it with your own cosign identity and tell your users to verify against
  *you*;
- the artifact verification inside the script still chains to the official
  identity, so your users end up trusting two identities: yours for the script,
  the vendor's for the binaries;
- do not force-write the `release-url` file from a modified script. Self-update
  from your host would fail cosign verification at update time anyway, so all
  you would accomplish is a confusing failure later instead of a clear absence
  now.
