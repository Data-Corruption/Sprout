#!/usr/bin/env bash

# Build script for local development and CI releases.
#
# This is the public build entrypoint and the place to edit project settings.
# The implementation is split by responsibility under scripts/build/ so the
# normal build and release order are visible here without loading the remote
# publication state machine.
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
# CI:
#   CI=true ./scripts/build.sh --prod-all
#     Resume or publish the CHANGELOG version under releases/<version>, promote
#     the root version pointer, and push the Git tag last. Root installers are
#     re-signed only when their deterministically rendered bytes change.
#
# Release terminology:
#   candidate: the VERSION selected from the latest CHANGELOG release heading;
#   immutable prefix: releases/<version>/ and its signed application artifacts;
#   staged: a candidate prefix that is uploaded and remotely verified, but is
#     not necessarily selected by the root version pointer yet;
#   current/promoted: the version selected by the root version object, which
#     new installer runs read once and pin for their whole run;
#   promotion: replacing that root pointer only after the prefix is verified;
#   promotion marker: durable timestamp/commit history used for retention and
#     tag retries after the root pointer has advanced; the Git tag is pushed last.
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
JS_DIR="./internal/ui/assets/js"
CSS_DIR="./internal/ui/assets/css"
ASSETS_DIR="./internal/ui/assets"
GO_MAIN_PATH="./cmd"

NO_CACHE='Cache-Control: no-store, max-age=0, must-revalidate' # unneeded with cache rule but just in case

MODE="local"
BUILD_KIND="dev" # dev | prod | prod-all
VERSION="v0.0.0-dev" # dev version marker (valid semver prerelease, so x/mod/semver handles it; any real release compares newer)
DEV_MODE=true # baked into BuildInfo; true only for the default dev build
HOST_GOARCH=""
BUILD_OUTS=()
VERSION_DIR=""
PUBLISH_REMOTE=""
RCLONE_ARGS=()
UPLOAD_ARGS=()

# Release-plan outputs. resolve_release_policy sets these together during the
# read-only inspection phase; later phases consume them in main's visible order.
RELEASE_BUILD_REQUIRED=true
RELEASE_CURRENT_VERSION=""
RELEASE_INSTALLERS_TO_PUBLISH=()
RELEASE_TARGET_TAG_EXISTS=false
RELEASE_TAG_ONLY=false

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
# access. detect_mode derives CERT_IDENTITY in CI, the only mode that renders
# the install scripts.
OIDC_ISSUER="https://token.actions.githubusercontent.com"
CERT_IDENTITY=""

# shellcheck source=build/common.sh
source "$BUILD_SCRIPT_DIR/build/common.sh"
# shellcheck source=build/artifacts.sh
source "$BUILD_SCRIPT_DIR/build/artifacts.sh"
# shellcheck source=build/release.sh
source "$BUILD_SCRIPT_DIR/build/release.sh"

# Build phases ----------------------------------------------------------------

parse_and_validate_invocation() {
  parse_args "$@"
  detect_mode
  validate_mode_flags
  validate_app_name
  detect_host_arch
  validate_pins
}

prepare_local_build_context() {
  clean_out_dir
  dep_check
  # Only CI signs, and it must sign with exactly the cosign the installers
  # bootstrap for themselves. Both read the pin from scripts/vendor.sh.
  if [[ "$MODE" == "ci" ]]; then
    vendor_cosign
  fi
  require_distribution_config
  resolve_version
  validate_version "$VERSION"
  VERSION_DIR="$RELEASE_DIR/releases/$VERSION"
  configure_distribution
}

inspect_release_state() {
  [[ "$MODE" == "ci" ]] || return 0
  resolve_release_policy
}

finish_tag_only_retry() {
  [[ "$MODE" == "ci" ]] && $RELEASE_TAG_ONLY || return 1
  ensure_promotion_marker
  tag_release
  cleanup_old_releases
}

build_candidate_if_needed() {
  if $RELEASE_BUILD_REQUIRED; then
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
  elif [[ "$MODE" == "ci" ]]; then
    printf "🟢 Skipping binary build for tagged version\n"
  fi
}

upload_and_verify_candidate() {
  package_installers
  if $RELEASE_BUILD_REQUIRED; then
    package_binaries
    write_release_version
    generate_checksums
    sign_application_release
    upload_application_release
  fi
  write_root_version_candidate
}

plan_installer_updates() {
  plan_installer_publication
}

publish_verified_installers() {
  test_changed_installers
  publish_installers
}

promote_record_tag_and_retain() {
  promote_release
  ensure_promotion_marker
  tag_release
  cleanup_old_releases
}

# Main ------------------------------------------------------------------------

main() {
  parse_and_validate_invocation "$@"

  prepare_local_build_context
  inspect_release_state

  if finish_tag_only_retry; then
    return
  fi

  build_candidate_if_needed

  if [[ "$MODE" == "ci" ]]; then
    upload_and_verify_candidate
    plan_installer_updates
    publish_verified_installers
    promote_record_tag_and_retain
  fi
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
