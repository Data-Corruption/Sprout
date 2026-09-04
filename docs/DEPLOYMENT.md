# Deploying the docs site to Cloudflare

Reference for the one-time Cloudflare setup and the Hugo deploy pipeline.

## Background: Pages vs Workers Static Assets

Cloudflare **Pages** (both its git-integration and "Direct Upload" modes) was frozen for new features in April 2025. It still works, but Cloudflare directs all new projects to **Workers Static Assets**: the same global CDN and free static-asset serving, but modeled as a Worker that serves a directory of files. There is no separate "Pages project" — just a worker named in a small config file, deployed with plain `wrangler deploy`.

For this static Hugo site the practical differences are:

- Config lives in `wrangler.jsonc` next to the site instead of dashboard build settings.
- Deploys are pushed from CI (`wrangler deploy`) instead of Cloudflare pulling from git.
- `_headers` / `_redirects` files are Pages-only and ignored here (we don't use them).
- No automatic PR preview deployments. If we want previews later, a PR workflow running `wrangler versions upload` returns a shareable preview URL without touching production.

## What's in the repo

- `docs/hugo.yaml` — site, theme, navigation, canonical URL, and `out/` publish directory.
- `docs/go.mod` and `go.sum` — the pinned, checksum-verified Hextra module.
- `docs/wrangler.jsonc` — worker name (`sprout-docs`) and the assets block pointing at `out/`, with `not_found_handling: "404-page"` so Hugo's `404.html` is served for bad URLs.
- `.github/workflows/docs.yml` — when the repository variable `DOCS_ENABLED` is
  `true`, pushes to `main` touching `docs/**` install pinned Hugo Extended,
  verify Hextra, build, and run `npx wrangler@4 deploy` from `docs`.

CI downloads Hugo Extended 0.164.0 directly from the official GitHub release
and validates the archive against Hugo's published SHA-256 checksums before
executing it. The workflow then runs `go mod download`, `go mod verify`, and a
warning-fatal production build. Hugo and Hextra version changes should update
the workflow and module files together and pass a local production build.

## One-time Cloudflare setup

1. **Copy the account ID.** Cloudflare dashboard → Workers & Pages (or any zone overview) → the Account ID is in the right-hand sidebar.
2. **Create an API token.** Dashboard → profile icon → My Profile → API Tokens → Create Token → use the **"Edit Cloudflare Workers"** template. Under Account Resources, restrict it to this one account; under Zone Resources, restrict to the zone that will host the docs domain. Create and copy the token — it's shown once.
3. **Add GitHub repo secrets.** Repo → Settings → Secrets and variables → Actions → New repository secret (same flow as the `R2_*` secrets):
   - `CLOUDFLARE_ACCOUNT_ID` — the account ID from step 1
   - `CLOUDFLARE_API_TOKEN` — the token from step 2
4. **Enable docs deployment.** In the same GitHub settings area, add the
   repository variable `DOCS_ENABLED` with the exact value `true`. Until then
   the docs workflow runs only a small gate job that reports deployment is
   disabled. This is independent of the release workflow's `CI_ENABLED`
   variable.
5. **First deploy.** Push a change under `docs/` to `main` (or run the workflow manually if it has `workflow_dispatch`). The first `wrangler deploy` creates the `sprout-docs` worker automatically — no pre-creation step. The site is immediately live at `sprout-docs.<account-subdomain>.workers.dev`.
6. **Attach the custom domain.** Dashboard → Workers & Pages → `sprout-docs` → Settings → Domains & Routes → Add → Custom domain → the docs domain. Update `baseURL` in `hugo.yaml` to the same HTTPS URL, including its trailing slash. The domain's zone must be on this Cloudflare account; DNS and the certificate are handled automatically.

## Local deploy / debugging

Install Go, Hugo Extended, and Node.js (only Wrangler needs Node.js), then:

```sh
cd docs
go mod download
go mod verify
HUGO_ENV=production HUGO_ENVIRONMENT=production \
  hugo --gc --minify --panicOnWarning
npx wrangler@4 deploy   # prompts for browser login, or set CLOUDFLARE_API_TOKEN
```

Before a real deploy, `npx wrangler@4 deploy --dry-run` validates and packages
the generated assets without changing Cloudflare. `npx wrangler@4 versions
upload` deploys a preview version with its own URL without changing production.

## Token hygiene

- The token can deploy any worker on the account, not just this one — treat it like the R2 keys.
- If it leaks, revoke it in the dashboard (My Profile → API Tokens) and mint a new one; nothing else references it.
- Rotation is just replacing the repo secret; no redeploy needed.
