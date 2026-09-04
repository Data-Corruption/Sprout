#!/usr/bin/env bash

# Public test entrypoint.
#
# Usage:
#   ./scripts/test.sh                              # race-enabled Go tests
#   ./scripts/test.sh -lint                        # shellcheck over the shell scripts
# --- BEGIN template ---
#   ./scripts/test.sh -cut                         # all 18 source shapes
# --- END template ---
#   ./scripts/test.sh -release                     # release state machine
#   ./scripts/test.sh -e2e [lifecycle options]     # Linux lifecycle E2E
#   ./scripts/test.sh -all                         # every available suite
#
# Lifecycle options are forwarded to test-lifecycle-e2e.sh, for example:
#   ./scripts/test.sh -e2e --distros "alpine void"

set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$ROOT"

usage() {
  cat <<'EOF'
Usage: ./scripts/test.sh [mode]

With no argument, run the race-enabled Go test suite.
  -lint     Run the pinned shellcheck over every shell script
EOF
  # --- BEGIN template ---
  cat <<'EOF'
  -cut      Test every supported source shape
EOF
  # --- END template ---
  cat <<'EOF'
  -release  Test the release publication state machine
  -e2e      Test the Linux lifecycle; remaining arguments go to test-lifecycle-e2e.sh
  -all      Run every available suite
EOF
}

require_no_args() {
  local mode=$1
  shift
  if [[ $# -ne 0 ]]; then
    printf "error: %s does not accept additional arguments\n" "$mode" >&2
    usage >&2
    exit 2
  fi
}

# --- BEGIN template ---
# The source-shape matrix finalizes real trees, and finalizing runs goimports.
ensure_cut_tools() {
  local resolved
  resolved=$(./scripts/vendor.sh goimports | sed -n 's/^goimports=//p')
  if [[ -z "$resolved" || ! -x "$resolved" ]]; then
    printf "error: vendor.sh returned no executable goimports path\n" >&2
    exit 1
  fi
  PATH="$(dirname "$resolved"):$PATH"
  export PATH
}
# --- END template ---

run_go_tests() {
  command -v go >/dev/null 2>&1 || {
    printf "error: 'go' is required but not installed or not in \$PATH\n" >&2
    exit 1
  }
  command -v gcc >/dev/null 2>&1 || {
    printf "error: 'gcc' is required by go test -race\n" >&2
    exit 1
  }

  # --- BEGIN service.https ---
  # Generated frontend outputs are gitignored. Empty compile-only placeholders
  # keep ordinary Go tests independent of the frontend toolchain.
  [[ -f internal/ui/assets/css/output.css ]] || : > internal/ui/assets/css/output.css
  [[ -f internal/ui/assets/js/output.js ]] || : > internal/ui/assets/js/output.js
  [[ -f internal/ui/assets/manifest.json ]] ||
    printf '{"css/output.css":"test","js/output.js":"test"}' > internal/ui/assets/manifest.json
  # --- END service.https ---

  go test -race ./...
}

# Entry points only; -x follows every `source` (scripts/build/*.sh) so the
# libraries are checked in the context that defines their variables.
run_shell_lint() {
  local shellcheck_bin
  shellcheck_bin=$(./scripts/vendor.sh shellcheck | sed -n 's/^shellcheck=//p')
  if [[ -z "$shellcheck_bin" || ! -x "$shellcheck_bin" ]]; then
    printf "error: vendor.sh returned no executable shellcheck path\n" >&2
    exit 1
  fi
  local scripts=(
    scripts/build.sh
    scripts/vendor.sh
    scripts/test.sh
    scripts/test-release.sh
    scripts/test-lifecycle-e2e.sh
    scripts/install.sh
    # --- BEGIN template ---
    scripts/cut
    # --- END template ---
  )
  "$shellcheck_bin" --external-sources --source-path=scripts --source-path=scripts/build "${scripts[@]}"
  printf '🟢 shellcheck passed (%d scripts)\n' "${#scripts[@]}"
}

# --- BEGIN template ---
run_cut_tests() {
  ensure_cut_tools
  go test -race ./cmd/cut ./cmd/cutmatrix ./internal/cut
  go run ./cmd/cutmatrix "$@"
}
# --- END template ---

run_release_tests() {
  bash scripts/test-release.sh
}

run_lifecycle_e2e() {
  bash scripts/test-lifecycle-e2e.sh "$@"
}

mode=${1:-}
[[ $# -eq 0 ]] || shift

case "$mode" in
  "")
    require_no_args "default" "$@"
    run_go_tests
    ;;
  -lint)
    require_no_args "-lint" "$@"
    run_shell_lint
    ;;
  # --- BEGIN template ---
  -cut)
    run_cut_tests "$@"
    ;;
  # --- END template ---
  -release)
    require_no_args "-release" "$@"
    run_release_tests
    ;;
  -e2e)
    run_lifecycle_e2e "$@"
    ;;
  -all)
    require_no_args "-all" "$@"
    run_go_tests
    run_shell_lint
    # --- BEGIN template ---
    run_cut_tests
    # --- END template ---
    run_release_tests
    run_lifecycle_e2e
    ;;
  -h|--help)
    require_no_args "$mode" "$@"
    usage
    ;;
  *)
    printf "error: unknown test mode '%s'\n" "$mode" >&2
    usage >&2
    exit 2
    ;;
esac
