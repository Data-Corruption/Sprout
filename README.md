# Sprout

Built with **love** <!-- as much as i'm legally allowed to give -->
and unhealthy amounts of caffeine, Sprout is a source template for per-user Go
CLI applications that may also need a background worker, a local HTTPS
dashboard, and signed self-updates. It's a complete example application, not a
framework or a library dependency: copy it, cut the features you don't need,
rename the module, and the result is ordinary Go and shell that belongs to you.

**Documentation: [sproutcli.dev](https://sproutcli.dev/)**

The site is built from [`docs/content/`](docs/content/docs/) in this
repository, so the same pages are readable here as plain Markdown if the site
is ever unreachable:

- [Getting started](docs/content/docs/getting-started/_index.md): features,
  cutting, building, releasing, installing, operating
- [Architecture](docs/content/docs/architecture.md): how it be
- [Philosophy](docs/content/docs/philosophy.md): why it be
- 🐝: dat's a bee
<!-- Bees communicate by wiggling, I'm not kidding. It's the cutest thing
ever. https://youtu.be/-7ijI-g4jHg -->

## Quick start

Install Transplant on Linux or in WSL:

```sh
curl -fsSL https://releases.sproutcli.dev/transplant/install.sh | sh
```

Use [Sprout as a GitHub template](https://github.com/Data-Corruption/Sprout/generate),
then clone your new repo and run the wizard:

```sh
git clone https://github.com/YOU/YOUR_APP.git
cd YOUR_APP
transplant
```

It walks you through features and project settings, previews the cut, and tests
and builds the result. The [getting started guide](https://sproutcli.dev/docs/getting-started/)
covers the next steps and the manual setup path. For more wizard options, see
[Transplant's README](https://github.com/Data-Corruption/Transplant#script-it).

## Requirements

Linux or WSL on `amd64`/`arm64`, the Go version in `go.mod`, Bash, and `curl`.
Race-enabled tests also need GCC. Release binaries target Linux and Windows 11
on both architectures; macOS and BSD are not currently supported.

## License

[MIT](LICENSE.md)

<!-- Hope you had fun, thanks for coming out. Stay safe and have a nice
drive home <3 xoxo -->