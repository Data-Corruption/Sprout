---
title: Getting Started
weight: 1
---

These docs break usage into six steps. By the end you'll have your own app
users can install. The steps don't really dig into / explain how things work at
a deep level, for that see [this]({{% relref "docs/architecture" %}}) or read
the source.

## Development Prerequisites  

- Linux (WSL works) on `amd64` or `arm64`.
- Go, a version equal to or greater than the one in `go.mod`.
- Bash and `curl` (already present on most distros).
- `gcc` or `cc` (only cause `go test -race` still needs it).

{{< callout type="information" >}}
On NixOS, running `nix develop` in your repo's root gives you everything needed
for development. If you happen to already have `tailwindcss` on `PATH`, the
build will use that instead.
{{< /callout >}}

## Steps

**Configure** ([Transplant](create/#with-transplant) automates these for convenience)

1. [Create your project](create/) - Use the GitHub template / setup your repo.
2. [Choose features](features/) - Update behavior and service shape. Decide what you don't want.
3. [Cut and rename](cut/) - Cut what you don't want and rename the go module.

**Develop**

4. [Build and run](build/) - Build, run, and test the app locally.

**Release**

5. [Publish](release/) - Create a Cloudflare R2 bucket, set repo secrets, create releases via changelog.
6. [Install and operate](operate/) - What end-user usage looks like.
7. [End-user mirror](mirror/) [*Optional*] - Skip unless you want to support end-user mirrors.

## Outside these steps:

- [How it be]({{% relref "docs/architecture" %}})
- [Why it be]({{% relref "docs/philosophy" %}})
