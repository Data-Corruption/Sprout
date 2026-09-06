---
title: Run a mirror
weight: 7
---

Use a mirror to review releases before making them available to users, or to
serve installations that cannot reach the public release host. Copy the official
artifacts unchanged so they retain their signatures.

## Stage a release

In an empty staging directory, download a release and the root installers:

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

This example accepts stable versions. For prereleases, validate the full semantic
version before using it as a path.

## Verify and publish

Before publishing:

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
   [release publication]({{% relref "docs/architecture" %}}#releases). A reader
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

## Install from the mirror

Install Cosign using the [installer verification instructions]({{% relref "docs/getting-started/operate" %}}#install),
then substitute your mirror URL and the original repository identity below.

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

Continue only if verification succeeds. Use the original release workflow's
identity when verifying the copied installer.

## Updates stay on the mirror

The installer saves the mirror URL for future checks and updates. Repeat your
`APP_RELEASE_URL` override whenever you rerun the installer directly; updates
launched by the application preserve it automatically.

Advance the mirror's `version` pointer only after approving a release. Users
can then discover it with `<APP> update`. To require manual installation, leave
`<APP> update --automatic=false`; unattended updates are disabled by default.

See [release sources and mirrors]({{% relref "docs/architecture" %}}#release-sources-and-mirrors)
for source persistence and signing behavior.
