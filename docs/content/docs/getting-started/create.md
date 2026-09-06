---
title: 1. Create your project
weight: 1
---

Use [Sprout as a GitHub template](https://github.com/Data-Corruption/Sprout/generate),
then clone your new repository:

```sh
git clone https://github.com/YOU/YOUR_APP.git
cd YOUR_APP
```

Development requires Linux or WSL. Choose either the setup wizard or the manual
steps below.

## With Transplant

Install [Transplant](https://github.com/Data-Corruption/Transplant), then run it
inside your new repository:

```sh
curl -fsSL https://releases.sproutcli.dev/transplant/install.sh | sh
transplant
```

The wizard selects features, renames the module, fills in project values, and
handles the inherited license and docs. It runs tests and a dev build, then
offers a setup commit. Use `transplant --preview` to review without changing files.

Continue to [Build and run]({{% relref "docs/getting-started/build" %}}).
For scripted setup, see the [Transplant README](https://github.com/Data-Corruption/Transplant#script-it).

## Manual setup

Create a branch so you can undo setup before building on it:

```sh
git switch -c setup
./scripts/test.sh
```

The tests should pass on the unmodified template. Next,
[choose your features]({{% relref "docs/getting-started/features" %}}), then
[cut and rename]({{% relref "docs/getting-started/cut" %}}). Complete the cut step
even if you keep every feature; it also renames your module and removes the
setup tooling.
