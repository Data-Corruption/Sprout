# Sprout docs site

Public documentation for Sprout, built with [Hugo](https://gohugo.io/) and
[Hextra](https://imfing.github.io/hextra/). This in-repository site is the
current public surface. Moving presentation to its own repository and mirroring
canonical source-adjacent docs into it is deliberately later work.

## Local development

Install Go at the version declared in `go.mod` and the extended edition of
Hugo. CI pins Hugo 0.164.0; Hextra requires Hugo Extended 0.146.0 or newer.
See Hugo's [Linux installation
guide](https://gohugo.io/installation/linux/) for installation options.

```sh
cd docs
go mod download
hugo server --buildDrafts --disableFastRender
```

The landing page is `content/_index.md`. Public documentation lives under
`content/docs/`; directory structure and front-matter weights define the
Hextra sidebar. Site configuration and top navigation live in `hugo.yaml`, and
small theme overrides belong in `assets/css/custom.css`.

Run the production build before publishing:

```sh
HUGO_ENV=production HUGO_ENVIRONMENT=production \
  hugo --gc --minify --panicOnWarning
```

The build writes to `out/`. `refLinksErrorLevel: ERROR` and
`--panicOnWarning` make unresolved Hugo references and build warnings fail.
Hugo does not check arbitrary external links.

Hextra is pinned in `go.mod` and verified by `go.sum`. To deliberately update
it, choose a released version, update the module, inspect the diff, and rebuild:

```sh
hugo mod get github.com/imfing/hextra@v0.12.3
go mod verify
```

## Deployment (Cloudflare Workers Static Assets)

The site deploys automatically via
[`.github/workflows/docs.yml`](../.github/workflows/docs.yml). Pushes to
`main` touching `docs/**` install checksum-verified Hugo Extended, verify
the Hextra module, build the site, and deploy `out/` with `wrangler deploy`.
The worker is `sprout-docs`, configured in
[`wrangler.jsonc`](wrangler.jsonc).

One-time Cloudflare setup, local deploys, previews, and token rotation are
documented in [DEPLOYMENT.md](DEPLOYMENT.md).

Notes:

- The workflow path filter keeps app-only pushes to main from triggering docs rebuilds.
- The Cloudflare account ID and API token are repository secrets, never site
  configuration.
- There are no automatic PR previews. `npx wrangler@4 versions upload` creates
  a preview version without changing production.
