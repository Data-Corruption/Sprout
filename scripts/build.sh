#!/usr/bin/env bash

# Build binaries for local development and production checks.
#
# This is the public build entrypoint and the place to edit project settings.
# The implementation is split by responsibility under scripts/build/ so the
# local build order is visible here. scripts/ci.sh reuses these settings and
# build helpers when publishing a release.
#
# Dev (default):
#   ./scripts/build.sh
#     Build frontend assets and a DevMode binary for the current host. DevMode
#     bypasses HTTP auth, uses an isolated storage root (~/.APP_NAME-dev), and
#     forces debug logging. Doesn't run tests. Never use
#     as a release build.
#
# Production (host):
#   ./scripts/build.sh --prod
#     Run ./scripts/test.sh, then build a production binary (normal storage
#     dirs, auth on) for the current host.
#
# Production (all targets):
#   ./scripts/build.sh --prod-all
#     Run tests, then build every release target: linux-amd64/arm64 and
#     windows-amd64/arm64 (pure Go, plain GOOS/GOARCH cross-builds).
#
# Release automation lives in scripts/ci.sh.
#
# Mirrors: there is no build mode for mirrors. Signed release artifacts are
# portable - copy the release bucket byte-for-byte and install with
# APP_RELEASE_URL pointing at the copy; all cosign signatures stay valid.
# See docs/content/docs/getting-started/mirror.md.
#
# Dependencies: go, gcc (only when tests run: go test -race needs cgo), and
# curl. The build is pure Go (no cgo), so Linux release binaries are fully
# static and run on any distro, including NixOS.
#
# NixOS: the downloaded tailwind standalone is dynamically linked and won't
# run; local builds prefer a tailwindcss found on PATH instead. Use the repo
# flake (`nix develop`) or `nix shell nixpkgs#tailwindcss_4` before building.
# Note nixpkgs may lag TAILWIND_VERSION slightly - fine for local dev, CI
# always uses the pinned standalone.

set -euo pipefail
umask 022
export LC_ALL=C
SERVICE_DESC=""          # fallback for after cut
SERVICE_DEFAULT_PORT="0" # fallback for after cut

# Project config --------------------------------------------------------------
#
# Template adopters normally change values in this section and leave the build
# implementation alone.

APP_NAME="sprout"
# The URL path is also the publication prefix inside R2_BUCKET. End with /.
RELEASE_URL="https://releases.sproutcli.dev/"
CONTACT_URL="https://sproutcli.dev/"
DEFAULT_LOG_LEVEL="warn"

# --- BEGIN service ---
SERVICE_DESC="Sprout daemon"
# --- END service ---
# --- BEGIN service.https ---
SERVICE_DEFAULT_PORT="8484"
# --- END service.https ---

# Pinned build inputs ---------------------------------------------------------
#
# Every third-party tool version and hash lives in scripts/vendor.sh, which
# also knows how to fetch each one. Sourcing it defines variables and functions
# only: no network, no side effects. It owns TOOLS_DIR.

BUILD_SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=vendor.sh
source "$BUILD_SCRIPT_DIR/vendor.sh"

# Build paths and initial state -----------------------------------------------

OUT_DIR="out"
RELEASE_DIR="$OUT_DIR/release"
# --- BEGIN service.https ---
JS_DIR="./internal/ui/assets/js"
CSS_DIR="./internal/ui/assets/css"
ASSETS_DIR="./internal/ui/assets"
# --- END service.https ---
GO_MAIN_PATH="./cmd"

MODE="local" # frontend tool selection; ci.sh uses the pinned CI tools
BUILD_KIND="dev" # dev | prod | prod-all
VERSION="v0.0.0-dev" # dev version marker (valid semver prerelease, so x/mod/semver handles it; any real release compares newer)
DEV_MODE=true # baked into BuildInfo; true only for the default dev build
HOST_GOARCH=""
BUILD_OUTS=()
VERSION_DIR=""

# Template wiring -------------------------------------------------------------
#
# These values connect feature cuts, generated service commands, and release
# verification. They are not normal project configuration; changing them means
# changing Sprout's build/runtime contract.

SERVICE_ENABLED="false"
SERVICE_ARGS=""
# --- BEGIN service ---
SERVICE_ENABLED="true"
SERVICE_ARGS="service run"
# --- END service ---

# cosign keyless identity: only releases signed by this exact workflow on main
# verify. The subject includes the repository, so it is unforgeable without push
# access. ci.sh derives CERT_IDENTITY for release binaries and installers.
OIDC_ISSUER="https://token.actions.githubusercontent.com"
CERT_IDENTITY=""

# shellcheck source=build/common.sh
source "$BUILD_SCRIPT_DIR/build/common.sh"
# shellcheck source=build/artifacts.sh
source "$BUILD_SCRIPT_DIR/build/artifacts.sh"

# Build -----------------------------------------------------------------------

# ci.sh calls this after choosing the release version and signing identity.
# Keeping compilation here gives local and published binaries the same inputs.
build_binaries() {
  dep_check
  # --- BEGIN service.https ---
  frontend_build
  frontend_hash_assets
  # --- END service.https ---
  if [[ "$BUILD_KIND" == "dev" ]]; then
    printf "🟢 Skipping tests in dev mode\n"
  else
    bash "$BUILD_SCRIPT_DIR/test.sh"
  fi
  go_build
  verify_build
}

build_main() {
  parse_args "$@"
  validate_app_name
  detect_host_arch
  validate_pins
  clean_out_dir
  build_binaries
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  build_main "$@"
fi
