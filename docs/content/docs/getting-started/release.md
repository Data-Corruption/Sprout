---
title: 5. Publish a release
weight: 5
---

A new version heading in `CHANGELOG.md`, pushed to `main`, triggers a release
through GitHub Actions. Set up the host and repository once, then use the
[publish steps](#publish) for each release.

## Set up Cloudflare R2

Use a domain in the same Cloudflare account as the bucket.

1. In **Storage & databases → R2 object storage**, create a bucket.
2. Open the bucket's **Settings → Custom Domains → Add** and attach your release
   hostname, for example `cd.example.com`.
3. In your domain's **Rules**, create a cache rule matching that hostname and
   set **Bypass cache**. The root `version` file must stay current.
4. From the R2 overview, open **Manage API tokens** and create a token with
   **Object Read & Write** access to this bucket. Save the access key ID and
   secret access key; also note your account ID and bucket name.

In the project block of `scripts/build.sh`, set:

```sh
RELEASE_URL="https://cd.example.com/"
```

Keep the trailing `/`. To share a bucket, use a path such as
`https://cd.example.com/my-app/`; publication then stays under `my-app/`.
`R2_BUCKET` is always just the bucket name.

Path segments may contain letters, digits, `-`, `_`, `.`, and `~`. Do not use
empty segments, `.` or `..`, URL escapes, credentials, a query, or a fragment.

[Cloudflare's R2 guide](https://developers.cloudflare.com/r2/buckets/public-buckets/#connect-a-bucket-to-a-custom-domain)
has more detail on connecting the hostname. Other S3-compatible hosts require changes to the publication code under
`scripts/build/`.

## Configure GitHub Actions

In your application's GitHub repository:

1. Open **Settings → Actions → General → Workflow permissions**, select
   **Read and write permissions**, and click **Save**.
2. Open **Settings → Secrets and variables → Actions → Secrets** and add:

   | Repository secret | Value |
   |---|---|
   | `R2_ACCESS_KEY_ID` | R2 token access key ID |
   | `R2_SECRET_ACCESS_KEY` | R2 token secret access key |
   | `R2_ACCOUNT_ID` | Cloudflare account ID |
   | `R2_BUCKET` | Bucket name, without a path |

3. In the **Variables** tab, add the repository variable `CI_ENABLED` with
   value `true`. CI stays disabled until this is set.

If the permissions setting is unavailable, check the organization policy.
[GitHub's workflow settings documentation](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/enabling-features-for-your-repository/managing-github-actions-settings-for-a-repository)
covers the available controls.

Keep `.github/workflows/release.yml` at that exact path, on `main`. Its path and
repository are part of the signing identity trusted by installed applications.
You can edit its contents.

## Publish

1. Check all production targets locally:

   ```sh
   ./scripts/build.sh --prod-all
   ```

2. Add a version heading at the top of `CHANGELOG.md`, above previous releases:

   ```md
   ## [v1.0.0] - 2026-09-06

   Initial release.
   ```

   For later releases, use a new version greater than the current one.

3. Commit the application changes and changelog, then push or merge them to
   `main`. Open the repository's **Actions** tab and follow the release workflow.

CI tests, builds, signs, and uploads the release. It promotes the root `version`
file after verifying the uploaded artifacts, then pushes the Git tag. See
[release internals]({{% relref "docs/architecture" %}}#releases) for the artifact
layout and publication order.

## Verify the release

Check that the release host serves your new version:

```sh
curl -fsSL https://cd.example.com/version
```

Use your configured `RELEASE_URL`, including any path prefix. Then follow
[Install and operate]({{% relref "docs/getting-started/operate" %}}) on a test machine.

After publishing a second release, run `<APP> update` from an older installation.
With `update.apply` retained, confirm the update and check that the app starts
on the new version. With the dashboard retained, you can also decline the CLI
prompt, refresh the dashboard, and apply the update from its notice.

## Retry an interrupted release

For a runner, network, or upload interruption, use **Re-run failed jobs** on the
original workflow run, or:

```sh
gh run rerun RUN_ID --failed
```

The publisher verifies remote state and resumes completed work. Keep the
original source and version for the retry. Do not create the tag by hand or
push a dummy commit to restart publication.

If the code or tests need a fix, commit it with a new version heading. Never
replace published bytes or move an existing tag.

For a repeated integrity error, inspect the logs and remote objects before
retrying again.

{{% details title="Investigate a repeated integrity error" closed="true" %}}

Read the CI error and inspect the bucket before changing anything.

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

Preserve remote objects and logs while you investigate.

{{% /details %}}

If you change the release scripts or installers, run the
[installer and release tests]({{% relref "docs/getting-started/build" %}}#test-installer-or-release-changes)
before publishing.
