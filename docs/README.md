# Documentation site

This project's documentation site uses [Hugo](https://gohugo.io/) and
[Hextra](https://imfing.github.io/hextra/). Content, theme configuration, and
deployment configuration are in this directory.

## Local development

Install Go at the version declared in `go.mod` and Hugo Extended. The Hugo
version and checksum used by CI are in in `../scripts/vendor.sh`; Hextra's
minimum Hugo version is declared in `hugo.yaml`. On Linux amd64, run
`./scripts/vendor.sh hugo` from the repository root to obtain the pinned Hugo
binary. Otherwise, see Hugo's [installation guide](https://gohugo.io/installation/).

```sh
cd docs
go mod download
hugo server --buildDrafts --disableFastRender
```

The landing page is `content/_index.md`. Documentation is under
`content/docs/`; directory structure and front-matter weights define the
Hextra sidebar. Site configuration and top navigation is in `hugo.yaml`, and
small theme overrides belong in `assets/css/custom.css`.

Run the production build before publishing:

```sh
go mod verify
HUGO_ENV=production HUGO_ENVIRONMENT=production \
  hugo --gc --minify --panicOnWarning
```

The build writes to `out/`. `refLinksErrorLevel: ERROR` and
`--panicOnWarning` make unresolved Hugo references and build warnings fail.
Hugo does not check arbitrary external links.

Hextra is pinned in `go.mod` and verified by `go.sum`. To update it, choose a
released version, run `hugo mod get github.com/imfing/hextra@<version>`, verify
the modules, inspect the diff, and rebuild.

## Deployment

[`.github/workflows/docs.yml`](../.github/workflows/docs.yml) builds and deploys
`out/` to Cloudflare Workers Static Assets when the repository variable
`DOCS_ENABLED` is exactly `true`. It runs on pushes to `main` that change the
site, workflow, or listed build dependencies, and can also be run manually.

Before enabling it in a new repository, set your worker name in
[`wrangler.jsonc`](wrangler.jsonc), set the public site URL in
[`hugo.yaml`](hugo.yaml), and add your Cloudflare repository secrets.
[DEPLOYMENT.md](DEPLOYMENT.md) gives the complete setup, local deployment,
preview, and customization steps.

Transplant's `keep` docs option retains this site and its workflow. The
`markdown` and `none` options remove the deployment workflow.
