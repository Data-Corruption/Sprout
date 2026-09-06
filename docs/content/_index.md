---
title: Sprout
toc: false
width: full
---

<section class="sp-hero" aria-labelledby="sp-hero-title">
  {{< sprout-hero-image >}}
  <div class="sp-hero__content">
    <h1 id="sp-hero-title">Sprout</h1>
    <p>
      The ultimate template for Go CLI apps and daemons.<br>
      All the hard parts done for you, wired up, and ready to go.
    </p>
    <div class="sp-hero__actions">
      <a class="sp-button sp-button--primary" href="{{< relref "docs/getting-started" >}}">
        Start growing
      </a>
      <a class="sp-button" href="https://github.com/Data-Corruption/Sprout">
        View on GitHub
      </a>
    </div>
  </div>
  <a
    class="sp-hero__credit"
    href="https://commons.wikimedia.org/wiki/File:Albert_Bierstadt_-_Mount_Corcoran.jpg"
    target="_blank"
    rel="noopener noreferrer"
    title="Albert Bierstadt, public domain, via Wikimedia Commons; color-adjusted with a gopher"
  >
    Albert Bierstadt · modified
  </a>
</section>

<div class="sp-intro">
  Built with <span class="bold-text">love</span> <!-- as much as i'm legally allowed to give --> and unhealthy amounts of caffeine, Sprout handles the stuff that's easy to underestimate: shared state, process lifecycle, authenticated HTTPS, releases, installation, and safe updates.
</div>

<hr class="sp-section-divider">

## What you get

<ul class="sp-features">
  <li><a href="{{< relref "docs/architecture" >}}#processes">Application structure</a>. One <code>App</code> container with shared initialization, cleanup, and error handling.</li>
  <li><a href="{{< relref "docs/architecture" >}}#sqlite">Shared state</a>. Embedded SQLite in WAL mode gives each process durable state and a way to communicate, all without cgo.</li>
  <li><a href="{{< relref "docs/getting-started/features" >}}">Optional features</a>. Keep or cut the service, dashboard, or update behavior during setup.</li>
  <li><a href="{{< relref "docs/getting-started/operate" >}}">Daemon</a>. A user systemd unit on Linux or scheduled task on Windows, with a binary-only install for systemd-less distros.</li>
  <li><a href="{{< relref "docs/getting-started/release" >}}">CI/CD</a>. Four build targets, signed artifacts, per-user installers, and resumable publication from a changelog entry.</li>
  <li><a href="{{< relref "docs/architecture" >}}#dashboard">Dashboard</a>. HTTPS, users, perms, and sessions. Tailwind, DaisyUI, and esbuild pinned / fetched by the build script. npm-free</li>
</ul>

<hr class="sp-section-divider">

## Sprout and GoReleaser solve different problems

<div class="sp-comparison">
  <p>
    <a href="https://goreleaser.com/">GoReleaser</a> is mature release
    automation for Go apps that already exist. It builds, packages, signs,
    and publishes across many source hosts, registries, and packaging
    ecosystems. If you already have an app and just need release automation,
    I'd use it.
  </p>
  <p>
    Sprout on the other hand is an app starting point and operating model.
    Its release flow is coupled to the app so an update can stop running
    processes, replace the binary, authorize and apply migrations, and restart
    safely. By coupling them and targeting just Windows / systemd based Linux
    distros, you get extremely robust direct to user release and operation.
  </p>
  <ul class="sp-comparison__choices">
    <li>
      <strong>Choose GoReleaser</strong> when you already have an application
      and want broad, established release automation, or need out of the
      box macOS support.
    </li>
    <li>
      <strong>Choose Sprout</strong> when you are starting a CLI or daemon and
      want its application, installation, service, state, and update lifecycle
      designed together.
    </li>
  </ul>
  <p class="sp-comparison__note">
    In theory you could <a href="https://youtu.be/31P1dFjiZOc">combine</a>
    them, but that'd mean recreating Sprout's pipeline across many package
    managers, and then you'd have N unique variants to maintain... With just
    Sprout, you have one universal transactional pipeline, only parts of it
    needing both a Windows and Linux implementation.
  </p>
</div>

<p class="sp-start-link">
  <a class="sp-button sp-button--primary" href="{{< relref "docs/getting-started" >}}">Start growing</a>
</p>
