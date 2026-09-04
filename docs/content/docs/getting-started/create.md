---
title: 1. Create your project
weight: 1
---

Sprout is a template, so there is no "sprout" dependency that exists afterwards.
You take a copy of a working application and it becomes yours immediately.

The manual path is complete and supported. A planned interactive generator
will automate the same steps as convenience tooling; it is not required to
build or ship an application from Sprout.

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

## Planned convenience generator

The planned generator is a small separate CLI that will ask what you're
building, keep or cut the
[supported features]({{% relref "docs/getting-started/features" %}}), rename the
project, run the real test suite, and remove template-only setup machinery. It
will call the same finalization path documented in
[step 3]({{% relref "docs/getting-started/cut" %}}), not introduce a second
templating system.

---

Step 2 is a quick guide on the optional features you can cut. Take a peek,
decide what you wanna keep, then in step 3 cut what you don't. Even if you plan
to keep everything, visit step 3 to remove the cut markers and setup machinery.