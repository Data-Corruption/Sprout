# Development Guide

## Prerequisites

- **Go**: Version 1.23 or higher
- **Environment**: Linux or WSL (Windows Subsystem for Linux)
- **Architecture**: `amd64` / `x86_64` runner for local verification. The build script also produces `linux-arm64` release artifacts.

## Architecture

Before diving into the code, check out [ARCHITECTURE.md](ARCHITECTURE.md) to understand the high-level design, core components, and data flow.

This CI/CD pipeline is built on GitHub Actions and Cloudflare R2. The Cloudflare R2 bucket stores release artifacts. If you want the old self-hosted Forgejo runner setup with the aggressive cached checkout flow, see the Codeberg version of this repo: `REPLACE_WITH_CODEBERG_URL`

## Steps

### 1. Use this Template
Click the "Use this template" button on GitHub to create a new repository based on Sprout.

### 2. Enable GitHub Actions

**Repo Settings → Actions → General → Workflow permissions:** Read and write permissions

**Repo Settings → Secrets and variables → Actions:** Create the variable `CI_ENABLED` = `true`

### 3. Setup Cloudflare R2

This assumes you have a domain and Cloudflare account. If you don't, get one. 

This project is setup so you can swap this part out if you want. This is all handled in `scripts/build.sh` using runner secrets for upload auth. The release host serves a simple flat directory, and the generated `install.sh` persists its configured release URL into `~/.APP_NAME/release-url` so later update checks keep using the approved source:
```
release/
  install.ps1
  install.sh
  linux-arm64.gz.sha256
  linux-arm64.gz
  linux-amd64.gz.sha256
  linux-amd64.gz
  version
```

Go to Cloudflare dashboard, create an account if you don't have one. Get a domain if you don't have one.

In the main dashboard, select **Storage & databases → R2 object storage** (sign up for free tier, will be fine for small / medium projects. You can switch to self host later easily) → **Overview → Create bucket**.
- Name: `YOUR-APP-cd`
- Region: `Auto`
- Default Storage Class: `Standard`

After creation, **Bucket Settings → Custom Domains → Add**:  
`cd.yourdomain.com`

In the dashboard, select **Account home**, then the domain you want to use. Now select **Rules → Overview → Create rule → Cache Rule**.
- Name: `Bypass cache for YOUR-APP CD`
- Custom filter expression - When incoming requests match...
  - Field: `Hostname`
  - Operator: `equals`
  - Value: `YOUR-APP-cd.yourdomain.com`
- Then
  - Action: `Bypass cache`

In **R2 object storage → Overview** on the right under Account Details, click **{}Manage** API Tokens. Kinda easy to miss. **Create User API Token**:
- Token Name: `YOUR-APP CD`
- Permissions: `Object Read & Write`

After creation, copy the:
- Access Key ID
- Secret Access Key

Back in the **R2 object storage → Overview** Account Details
- Copy the Account ID
- Copy the Bucket Name e.g. `YOUR-APP-cd`

Open your repository, **Settings → Actions → Secrets** Add the following secrets:
- `R2_ACCESS_KEY_ID` = paste Access Key ID
- `R2_SECRET_ACCESS_KEY` = paste Secret Access Key
- `R2_ACCOUNT_ID` = paste Account ID
- `R2_BUCKET` = paste Bucket Name

### 4. Clone your new repository
```sh
  git clone https://github.com/YOUR_USERNAME/YOUR_REPO.git
  cd YOUR_REPO
```

### 5. Configure the Template
All configuration is done at the top of `scripts/build.sh`:
- `APP_NAME`: Your application name (binary name).
- `RELEASE_URL`: URL baked into the generated install scripts, e.g. `https://cd.yourdomain.com/release/`. The installer writes this into the app's `release-url` file, which is then used for update checks and self-updates.
- `CONTACT_URL`: This is used in the User-Agent. It's currently unused, but if you start making requests to other services it's a good idea to add it to the request headers. Your apps landing page or repo URL is fine.
- `DEFAULT_LOG_LEVEL`: The default log level (e.g. `debug`, `info`, `warn`, `error`).
- `SERVICE`: Set to "true" or "false" to enable/disable the daemon.
- `SERVICE_DESC`: Description for the systemd service.
- `SERVICE_ARGS`: Arguments to pass to the binary when running as a daemon. Unless you have a specific reason, leave this as `service run`.
- `SERVICE_DEFAULT_PORT`: The default port the service listens on (e.g. `8484`).

### 6. **Build the project**:
   ```sh
   ./scripts/build.sh
   ```

   For a quicker local smoke build on the current machine, you can skip tests and build only the host architecture:
   ```sh
   ./scripts/build.sh --fast
   ```

### 7. **Test it**:
   ```sh
   ./bin/linux-amd64 service run
   ```

Dev (non CI) builds set the app version to `v.X.X.X` which disabled update related features. This is useful for testing / conditionally enabling things you don't want in dev.

## Release Workflow

This project uses a changelog-driven release process:

1. Insert an entry to `CHANGELOG.md` under # Changelog, describing your changes. See [CHANGELOG.md](CHANGELOG.md) for example.
2. Push your changes to the `main` branch.
3. GitHub Actions will automatically build the project and upload it to the release bucket. Users should see the update within a day or so.

To see how the update process works, see the [settings page](../internal/platform/http/router/settings/settings.go).  
To test it:
- publish a new release
- run `YOUR_APP update --check` to force a check, otherwise it will wait and only check ~once a day.
- visit/refresh `http://localhost:8484` in your browser.
- you should see a notification about an update. Click **restart → enable update → confirm** and the app will update, just like magic ✨

## Staged Mirror Workflow

Organizations that require controlled distribution, for compliance review, vulnerability scanning, or internal approval gates can operate an internal mirror that serves as the sole update source for deployed instances.

The release layout is intentionally flat (no nested paths or API calls), so mirroring requires only a static file host.

### Option A: Mirror upstream artifacts

1. Copy the approved release artifacts to your internal endpoint: `install.sh`, `install.ps1`, `version`, `linux-amd64.gz`, `linux-amd64.gz.sha256`, `linux-arm64.gz`, `linux-arm64.gz.sha256`.
2. Verify upstream SHA-256 checksums before publishing.

### Option B: Rebuild from source

1. Check out the approved release tag or commit.
2. Build and package into a ready-to-serve release directory:
   ```
   ./scripts/build.sh --mirror /path/to/release --release-url https://internal.example.com/release/
   ```
   This produces `install.sh`, `install.ps1`, gzipped binaries, SHA-256 checksums, and a `version` file. The `--release-url` flag bakes your mirror URL into the install scripts so deployed instances self-update from your endpoint. If omitted, the default upstream URL is used.

### Validation (applies to both options)

1. Run vulnerability and malware scanning against the artifacts.
2. Test in a representative environment before promoting to production.
3. Optionally re-sign binaries with an internal certificate or signing process.
4. Publish the validated artifact set to your internal endpoint.

### Deployment

Install from the mirror's `install.sh` or `install.ps1`. The installer writes the mirror URL into `~/.APP_NAME/release-url`, which pins all future self-updates to that endpoint. No additional configuration is needed.

Distribute the mirror URL through your existing configuration management or deployment tooling rather than relying on users to discover it.

### Mirror lifecycle

The mirror URL is persistent infrastructure, deployed instances depend on it for updates indefinitely. If the mirror endpoint changes or is decommissioned, update `~/.APP_NAME/release-url` on all deployed instances before retiring the old endpoint. Retain archived artifacts and checksums according to your organization's audit retention policy.
