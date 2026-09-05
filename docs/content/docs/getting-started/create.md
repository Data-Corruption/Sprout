---
title: 1. Create your project
weight: 1
---

Sprout is a template, so there is no "sprout" dependency that exists afterwards.
You take a copy of a working application and it becomes yours immediately.

Transplant automates steps 1–3 from inside your fresh template copy. The manual
path below is complete and supported too.

## With Transplant

Install Transplant on Linux or in WSL:

```sh
curl -fsSL https://releases.sproutcli.dev/transplant/install.sh | sh
```

Use [Sprout as a GitHub template](https://github.com/Data-Corruption/Sprout/generate),
then clone your new repository and run the wizard:

```sh
git clone https://github.com/YOU/YOUR_APP.git
cd YOUR_APP
transplant
```

It asks what you're building, previews this checkout's own `scripts/cut` plan,
fills in the project config, adds your copyright notice while preserving the
MIT notice, handles the inherited docs, runs tests and a dev build, and offers
a setup commit. `--preview` leaves the checkout alone; every answer has a flag
for scripted use. Development is Linux/WSL only.

After setup, head to [step 4]({{% relref "docs/getting-started/build" %}}).
Need more options? See the [Transplant README](https://github.com/Data-Corruption/Transplant#script-it).

## The manual path

Use [Sprout as a GitHub template](https://github.com/Data-Corruption/Sprout/generate)
rather than forking. A fork keeps an upstream relationship you don't need:
Sprout expects your tree to diverge immediately and permanently, and the
template button gives you a clean history instead of a permanent "N commits
behind" banner.

Clone your new repository and make a branch:

```sh
git clone https://github.com/YOU/YOUR_APP.git
cd YOUR_APP
git switch -c setup
```

That branch is your uh oh button. The next two steps delete source code and
rewrite every import path in the repository, and they are meant to be done once
on an untouched tree. Use the branch to undo a bad setup pass before you start
building on top.

You should now be able to run the tests on the unmodified template:

```sh
./scripts/test.sh
```

This creates compile-only frontend placeholders when a fresh clone has no
generated assets, then runs `go test -race ./...`. Green here gives you a clean
baseline for the changes that follow.

---

Step 2 is a quick guide on the optional features you can cut. Take a peek,
decide what you wanna keep, then in step 3 cut what you don't. Even if you plan
to keep everything, visit step 3 to remove the cut markers and setup machinery.