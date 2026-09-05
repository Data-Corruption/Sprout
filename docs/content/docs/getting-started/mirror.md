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
set -eu
BASE=https://cd.example.com/   # the official release URL
version=$(curl -fsSL "${BASE}version")
# Validate the version before using it as a path; this recipe uses stable releases.
printf '%s\n' "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' || exit 1

mkdir -p "releases/$version"
for f in version linux-amd64.gz linux-arm64.gz windows-amd64.exe.gz \
         windows-arm64.exe.gz checksums.txt checksums.txt.cosign.bundle; do
  curl -fsSLo "releases/$version/$f" "${BASE}releases/$version/$f"
done
for f in install.sh install.sh.cosign.bundle install.ps1 install.ps1.cosign.bundle; do
  curl -fsSLO "$BASE$f"
done
printf '%s\n' "$version" > version

# This is a local staging directory, not the live mirror.
# Verify, test, and approve before publishing; publish the root version last.
```

This example downloads a stable release into a local staging directory. For
prerelease versions, validate the full semantic version before using it as a
path. A download is not an approval or a signature check. Before publishing:

1. Verify `checksums.txt.cosign.bundle` against the original workflow identity,
   then match every release artifact against the signed checksums. Verify both
   root installer bundles against that same identity. Root installers can change
   during a download: if a pair does not verify, fetch the pair again and stop if
   it still fails.
2. Scan and test the staged release and installers using your approval process.
3. Upload the complete immutable `releases/<version>/` prefix. Verify the hosted
   copy before making it discoverable. Never replace a version with different bytes.
4. Publish the verified root installer pairs. Publish each bundle before its
   installer, which is the pair's commit point, as described in
   [Publish a release]({{% relref "docs/getting-started/release" %}}). A reader
   encountering the temporary mismatch fails verification safely; it can retry.
   If publication stops between the two objects, finish publishing that exact
   verified pair before promoting the release. Leave unchanged pairs alone.
5. Replace the mirror's root `version` pointer **last**. Serve it without stale
   caching; bypass edge caching or preserve `Cache-Control: no-store` end to end.

Installers are executable maintenance controllers, so approval covers their bytes
as well as the release artifacts. They are independent of application versions:
replacing a root installer makes it available to users even before the version
pointer moves. Keep the old verified pairs until publication has completed.

### Retain releases that installations may still be using

Always keep the current and previous promoted releases. Retire an older prefix
only after at least 24 hours have passed since it stopped being advertised by the
mirror's root pointer. Record that time when advancing the pointer; the upload
time is not a substitute. A version could have been current for months.

This gives installations already pinned to an old prefix time to finish. Extend
the grace period if your environment allows installation runs to remain paused
longer. Keeping all approved releases indefinitely is also fine and can provide
an approval history. Mirror retention is independent of upstream retention;
copy everything you need before upstream removes it.

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

## Updates stay on the mirror

When update support is retained, the installer writes the effective source URL
into `maintenance/release-url`, including a mirror supplied through
`APP_RELEASE_URL`. This is installer-owned installation metadata, not a baked
application default or a database preference. It participates in the same
rollback transaction as the binary. A later explicit installation can change
it; callers must repeat their intended `APP_RELEASE_URL` override when rerunning
an installer directly.

Release checks, manual application, and unattended application all use this
persisted source. A detached updater passes the same URL to its installer, so
an update preserves the mirror even though the official installer has a public
default baked in. Missing or invalid source metadata prevents updating; the
application never falls back to the public host. Cached discovery information
from a different source is not used for notices or automatic application.

The mirror's `version` pointer is its approval gate. Publishing upstream does
not change what these installations see. Once you copy, test, and promote a
release on the mirror, installations can discover and apply it using whichever
[update capabilities]({{% relref "docs/getting-started/features" %}}) their build
retains. Unattended application also requires the service and an enabled
`<app> update --automatic=true` preference. Organizations that control the exact
installation time can leave that preference disabled and initiate each update
manually.

The URL selects a source; it does not replace the signing identity. Every
application-driven update still verifies the downloaded installer against the
original baked identity before execution, and the installer verifies release
artifacts against that identity too. A mirror can select which authentic
releases it offers or stop serving them; it cannot make a modified installer
pass those checks.

## If you really must modify the installer

Discouraged, but the signature model degrades honestly instead of silently:

- your modified script no longer verifies against the official identity, so
  re-sign it with your own cosign identity and tell your users to verify against
  *you*;
- the artifact verification inside the script still chains to the official
  identity, so your users end up trusting two identities: yours for the script,
  the vendor's for the binaries;
- application-driven updates still require installers signed by the original
  identity. A modified installer signed only by your organization fails that
  check; apply it explicitly after your own verification instead. Unmodified
  official installers served from your host continue to work normally.
