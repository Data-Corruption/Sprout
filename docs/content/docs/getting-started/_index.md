---
title: Getting started
weight: 1
---

Follow these steps to create an application and publish its first release.
[Transplant](create/#with-transplant) can handle the first three for you.

## Prerequisites

- Linux or WSL on `amd64` or `arm64`.
- Go at least as new as the version in the repository's `go.mod`.
- Bash, `curl`, and `gcc` or `cc` for the race-enabled tests.

On NixOS, run `nix develop` in the repository root to load the development tools.

## Steps

1. [Create your project](create/): copy the template and clone your repository.
2. [Choose features](features/): decide which service and update features to keep.
3. [Cut and rename](cut/): remove unused features and set your project values.
4. [Build and run](build/): develop and test locally.
5. [Publish a release](release/): configure hosting and GitHub Actions, then ship.
6. [Install and operate](operate/): install, configure, and update the released app.

If you need to distribute approved releases from another host, follow
[Run a mirror](mirror/).

See [Architecture]({{% relref "docs/architecture" %}}) when you need to change how
the application works.
