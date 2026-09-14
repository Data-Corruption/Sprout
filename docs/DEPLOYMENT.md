# Deploying the documentation site

The site builds with Hugo and deploys the generated `out/` directory to
[Cloudflare Workers Static Assets](https://developers.cloudflare.com/workers/static-assets/).
No Worker script or separate Pages project is needed.

## Files and build inputs

- `hugo.yaml` sets the canonical site URL, title, navigation, and `out/` publish directory.
- `go.mod` and `go.sum` pin and verify the Hextra theme.
- `wrangler.jsonc` names the Worker and points its assets at `./out`. Its
  `404-page` setting serves Hugo's `404.html` for missing pages.
- `../scripts/vendor.sh` pins Hugo Extended (including its archive checksum)
  and Wrangler. The workflow obtains Hugo through this script and uses its
  exact Wrangler version.
- `../.github/workflows/docs.yml` verifies modules, builds with warnings treated
  as errors, and deploys when `DOCS_ENABLED` is `true`.

## Set the worker name and site URL

Do this before enabling deployment in a new repository. Transplant's `keep`
option retains the existing site configuration; it does not customize it.

1. **Choose your worker name.** In `docs/wrangler.jsonc`, replace the existing
   `name` value with a name for your project's docs, for example:

   ```jsonc
   "name": "my-project-docs"
   ```

   Use a distinct name within your Cloudflare account. Deployments update the
   Worker with that name; changing the name later targets a different Worker.

2. **Choose the public site URL.** In `docs/hugo.yaml`, replace `baseURL` with
   the HTTPS URL readers will use, including its trailing slash. For a custom
   domain:

   ```yaml
   baseURL: https://docs.example.com/
   ```

   To use Cloudflare's provided hostname instead, find or set your account's
   [workers.dev subdomain](https://developers.cloudflare.com/workers/configuration/routing/workers-dev/)
   in the dashboard and use:

   ```yaml
   baseURL: https://my-project-docs.your-account-subdomain.workers.dev/
   ```

   Substitute your actual worker name and account subdomain. `baseURL` controls
   generated canonical URLs, sitemap entries, and social links; it does not
   create a domain or change Cloudflare routing.

3. **Review the remaining site identity** listed below, then commit the config
   changes. Keep `publishDir: out` and `assets.directory: ./out` in sync if you
   change the output directory.

## Connect Cloudflare and GitHub

1. **Copy your Cloudflare account ID.** Find it in the Cloudflare dashboard for
   the account that will own the Worker.
2. **Create an API token.** Under My Profile → API Tokens, use the
   **Edit Cloudflare Workers** template and restrict Account Resources to that
   account. If you use a custom domain, scope Zone Resources to its zone.
3. **Add repository secrets.** In GitHub → Settings → Secrets and variables →
   Actions, create `CLOUDFLARE_ACCOUNT_ID` and `CLOUDFLARE_API_TOKEN` with those
   values. These credentials belong to the destination repository's account.
4. **Enable deployment.** In the same settings area, add the repository variable
   `DOCS_ENABLED` with the exact value `true`. Until then, the workflow's gate
   job reports that deployment is disabled. The release workflow's `CI_ENABLED`
   variable is independent.
5. **Deploy.** Push a site/config change to `main`, or open Actions → Docs →
   Run workflow. The first deployment creates the named Worker. Later
   deployments update it. If your default branch has another name, update
   `on.push.branches` in `.github/workflows/docs.yml`.
6. **For a custom domain**, open Workers & Pages → your Worker → Settings →
   Domains & Routes → Add → Custom domain, and enter the hostname used in
   `baseURL`. The zone must be active in your Cloudflare account. Cloudflare
   manages its DNS record and certificate; see the
   [custom domain guide](https://developers.cloudflare.com/workers/configuration/routing/custom-domains/).
   If you change `baseURL` after the first build, rebuild and deploy again.

## Local deploys and previews

Install Go, Hugo Extended, and Node.js (needed by Wrangler). From the repository
root, use Bash so local deploys read the same Wrangler pin as CI:

```sh
source scripts/vendor.sh
cd docs
go mod download
go mod verify
HUGO_ENV=production HUGO_ENVIRONMENT=production \
  hugo --gc --minify --panicOnWarning
npx --yes "wrangler@${WRANGLER_VERSION}" deploy --dry-run
npx --yes "wrangler@${WRANGLER_VERSION}" deploy
```

Wrangler can prompt for browser login locally, or use the
`CLOUDFLARE_API_TOKEN` and `CLOUDFLARE_ACCOUNT_ID` environment variables.
`--dry-run` packages and validates without deploying.

There are no automatic pull-request previews. After the initial deployment,
run `npx --yes "wrangler@${WRANGLER_VERSION}" versions upload` from the same
shell and directory to upload a version without changing production. Enable
`preview_urls` in `wrangler.jsonc` if needed; see Cloudflare's
[preview URL documentation](https://developers.cloudflare.com/workers/versions-and-deployments/preview-urls/).
The generated site still uses the configured `baseURL` for canonical URLs.

## Other values to review when reusing the site

- `hugo.yaml`: title, both descriptions, GitHub navigation URL, and social image.
- `content/_index.md` and `content/docs/`: project names, repository links,
  installation examples, and guides describing the template's features.
- `layouts/_partials/custom/footer.html`: author name and profile link.
- `static/favicon.svg`, `static/images/`, and
  `layouts/shortcodes/sprout-hero-image.html`: the inherited branding and artwork.
- `layouts/_partials/custom/head-end.html`: hardcoded social-image type, size,
  and alt text, plus the hero image path. It fails the build if the configured
  social image or the expected hero image is missing. Update this partial when
  replacing those assets.
- `go.mod`: the docs site's own module path still names the upstream repository;
  the app's module rename does not change it. Set it to your repository's docs
  module path if you want matching project identity.

The worker name, public URL, credentials, and deployment branch determine where
and when publishing happens. The remaining identity values need editorial
review; retaining them does not prevent Hugo from building.

## Token rotation

Replace `CLOUDFLARE_API_TOKEN` in the repository secrets to rotate credentials.
If a token leaks, revoke it in Cloudflare and create a replacement. No site
configuration change is needed.
