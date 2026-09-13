#!/usr/bin/env bash

# Release automation used by .github/workflows/release.yml.
#
#   ./scripts/ci.sh --plan
#     Check the release host and Git tag, then report which tests are needed.
#     Does not build binaries or change the release host or Git repository.
#
#   ./scripts/ci.sh --execute
#     Build and publish the CHANGELOG version, or finish an interrupted release.
#     The workflow runs this only after the tests requested by --plan pass.
#
# Both commands need GITHUB_REPOSITORY, the R2 credentials, and rclone on PATH.
# Execution also needs the build toolchain and GitHub's signing/tag permissions.
# Run from the repository root. Project settings stay in scripts/build.sh.

set -euo pipefail

CI_SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# Sourcing shares settings and build helpers without running a local build.
# shellcheck source=build.sh
source "$CI_SCRIPT_DIR/build.sh"
# shellcheck source=ci/release.sh
source "$CI_SCRIPT_DIR/ci/release.sh"

NO_CACHE='Cache-Control: no-store, max-age=0, must-revalidate'
PUBLISH_REMOTE=""
RCLONE_ARGS=()
UPLOAD_ARGS=()

# resolve_release_policy fills these from the release host and Git remote.
RELEASE_BUILD_REQUIRED=true
RELEASE_CURRENT_VERSION=""
RELEASE_INSTALLERS_TO_PUBLISH=()
RELEASE_TARGET_TAG_EXISTS=false
RELEASE_TAG_ONLY=false

configure_release_identity() {
  if [[ -z "${GITHUB_REPOSITORY:-}" ]]; then
    printf "error: GITHUB_REPOSITORY is required to identify the release signer\n" >&2
    exit 1
  fi
  CERT_IDENTITY="https://github.com/${GITHUB_REPOSITORY}/.github/workflows/release.yml@refs/heads/main"
}

prepare_release_context() {
  MODE="ci"
  BUILD_KIND="prod-all"
  DEV_MODE=false
  configure_release_identity
  validate_app_name
  detect_host_arch
  validate_pins
  require_distribution_config
  resolve_version
  validate_version "$VERSION"
  VERSION_DIR="$RELEASE_DIR/releases/$VERSION"
  configure_distribution
  clean_out_dir
  vendor_cosign
}

# The workflow uses these decisions to schedule tests. An upload without a
# Git tag needs E2E: the earlier attempt may not have passed its tests.
plan_release() {
  local e2e_required=false installer_tests_required=false
  if ! $RELEASE_TAG_ONLY; then
    if ! $RELEASE_TARGET_TAG_EXISTS; then
      e2e_required=true
    fi
    package_installers
    plan_installer_publication
    if [[ "${#RELEASE_INSTALLERS_TO_PUBLISH[@]}" -gt 0 ]]; then
      installer_tests_required=true
    fi
  fi

  printf 'e2e_required=%s\ninstaller_tests_required=%s\n' \
    "$e2e_required" "$installer_tests_required"
  if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
    printf 'e2e_required=%s\ninstaller_tests_required=%s\n' \
      "$e2e_required" "$installer_tests_required" >> "$GITHUB_OUTPUT"
  fi
}

# Publication order is shared by first attempts and retries. The remote probe
# has already downloaded any verified release we can reuse.
execute_release() {
  if $RELEASE_TAG_ONLY; then
    # A newer version is already public. Finish this older release's tag and
    # cleanup without replacing the current installers or version pointer.
    ensure_promotion_marker
    tag_release
    cleanup_old_releases
    return
  fi

  if $RELEASE_BUILD_REQUIRED; then
    build_binaries
  fi
  package_installers
  if $RELEASE_BUILD_REQUIRED; then
    package_binaries
    write_release_version
    generate_checksums
    sign_application_release
    upload_application_release
  fi
  write_root_version_candidate

  plan_installer_publication
  test_changed_installers
  publish_installers

  promote_release
  ensure_promotion_marker
  tag_release
  cleanup_old_releases
}

ci_main() {
  if [[ $# -ne 1 || ( "$1" != "--plan" && "$1" != "--execute" ) ]]; then
    printf 'Usage: ./scripts/ci.sh --plan | --execute\n' >&2
    return 2
  fi

  prepare_release_context
  resolve_release_policy

  case "$1" in
    --plan) plan_release ;;
    --execute) execute_release ;;
  esac
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  ci_main "$@"
fi
