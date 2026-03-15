#!/bin/bash

# Build script for local development, CI releases, and operated mirrors.
#
# Local:
#   ./scripts/build.sh
#     Build frontend assets, run tests, and build linux-amd64 + linux-arm64.
#
# Fast local:
#   ./scripts/build.sh --fast
#     Skip tests and build only the current host architecture.
#
# CI:
#   CI=true ./scripts/build.sh
#     Always refresh release scripts and the version marker. Only build, tag,
#     and upload binaries when the latest CHANGELOG version is not already tagged.
#
# Mirror:
#   ./scripts/build.sh --mirror <dir> [--release-url <url>]
#     Build a complete release artifact set for an operated mirror. If
#     --release-url is set, that URL is baked into the generated install scripts.

set -euo pipefail
umask 022

# Config --------------------------------------------------------------

APP_NAME="sprout"
RELEASE_URL="https://cd.example.com/"
CONTACT_URL="https://github.com/DataCorruption/Sprout"
DEFAULT_LOG_LEVEL="warn"

SERVICE="true"
SERVICE_DESC="Sprout daemon"
SERVICE_ARGS="service run"
SERVICE_DEFAULT_PORT="8484"

# -----------------------------------------------------------------------------

TAILWIND_VERSION="${TAILWIND_VERSION:-v4.1.18}"
DAISYUI_VERSION="${DAISYUI_VERSION:-v5.5.14}"

OUT_DIR="out"
RELEASE_DIR="$OUT_DIR/release"
JS_DIR="./internal/ui/assets/js"
CSS_DIR="./internal/ui/assets/css"
GO_MAIN_PATH="./cmd"

NO_CACHE='Cache-Control: no-store, max-age=0, must-revalidate' # unneeded with cache rule but just in case

MODE="local"
MIRROR_DIR=""
MIRROR_RELEASE_URL=""
VERSION="vX.X.X" # dev/test version
SHOULD_BUILD_BINARIES=true
SHOULD_TAG_VERSION=false
FAST_LOCAL=false
HOST_GOARCH=""
BUILD_OUTS=()

# Helpers ---------------------------------------------------------------------

# run_step "success_msg" "fail_msg" command [args...]
# Runs a command, prints success or failure message, exits on failure.
run_step() {
  local success_msg="$1"
  local fail_msg="$2"
  shift 2
  local output
  if output="$("$@" 2>&1)"; then
    printf '🟢 %s\n' "$success_msg"
    [[ -n "${VERBOSE:-}" && -n "$output" ]] && printf '%s\n' "$output" || true
  else
    local status=$?
    printf '\n🔴 %s:\n' "$fail_msg"
    printf '%s\n' "$output"
    exit $status
  fi
}

# download_file "output_path" "url"
# Downloads a file, with status output.
download_file() {
  run_step "Downloaded $2" "Failed to download $2" curl -fsSL -o "$1" "$2"
}

# check_var "key" "expected"
# Verifies a build variable matches the expected value.
# Handles both string values ("key":"value") and non-string values (key:value or key:true).
check_var() {
  local key="$1"
  local expected="$2"
  local actual
  # Try string value first, then non-string (bool/number)
  actual=$(echo "$BUILD_VARS" | grep -oP "\"$key\":\"[^\"]*\"" | cut -d'"' -f4) || true
  if [[ -z "$actual" ]]; then
    actual=$(echo "$BUILD_VARS" | grep -oP "\"$key\":[^,}]+" | cut -d':' -f2)
  fi
  if [[ "$actual" != "$expected" ]]; then
    echo "🔴 Error: $key mismatch. Expected '$expected', got '$actual'"
    exit 1
  fi
}

# Stages ----------------------------------------------------------------------

dep_check() {
  local required_bins=(go gcc sed awk sha256sum gzip)
  if [[ "$MODE" != "local" || "$FAST_LOCAL" != "true" ]]; then
    required_bins+=(aarch64-linux-gnu-gcc) # cross compile stuff for arm support
  fi

  for bin in "${required_bins[@]}"; do
    if ! command -v "$bin" >/dev/null 2>&1; then
      printf "error: '$bin' is required but not installed or not in \$PATH\n" >&2
      exit 1
    fi
  done

  if [[ "$MODE" != "local" || "$FAST_LOCAL" != "true" ]] && command -v dpkg-query >/dev/null 2>&1 && ! dpkg-query -W -f='${Status}\n' libc6-dev-arm64-cross 2>/dev/null | grep -qx 'install ok installed'; then
    printf "error: 'libc6-dev-arm64-cross' package is required but not installed\n" >&2
    exit 1
  fi
}

clean_out_dir() {
  rm -rf "$OUT_DIR" && mkdir -p "$OUT_DIR"
  printf '🟢 Cleaned out directory\n'
}

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --mirror)
        [[ -z "${2:-}" ]] && { printf "error: --mirror requires a directory argument\n" >&2; exit 1; }
        MIRROR_DIR="$2"
        shift 2
        ;;
      --release-url)
        [[ -z "${2:-}" ]] && { printf "error: --release-url requires a URL argument\n" >&2; exit 1; }
        MIRROR_RELEASE_URL="$2"
        shift 2
        ;;
      --fast)
        FAST_LOCAL=true
        shift
        ;;
      *)
        printf "error: unknown argument '%s'\n" "$1" >&2
        exit 1
        ;;
    esac
  done

  if [[ -n "$MIRROR_RELEASE_URL" && -z "$MIRROR_DIR" ]]; then
    printf "error: --release-url requires --mirror\n" >&2
    exit 1
  fi
}

detect_mode() {
  if [[ -n "$MIRROR_DIR" ]]; then
    MODE="mirror"
  elif [[ "${CI:-}" == "true" ]]; then
    MODE="ci"
  else
    MODE="local"
  fi
}

validate_mode_flags() {
  if [[ "$FAST_LOCAL" == "true" && "$MODE" != "local" ]]; then
    printf "error: --fast is only supported for local builds\n" >&2
    exit 1
  fi
}

detect_host_arch() {
  case "$(uname -m)" in
    x86_64|amd64)
      HOST_GOARCH="amd64"
      ;;
    aarch64|arm64)
      HOST_GOARCH="arm64"
      ;;
    *)
      printf "error: unsupported host architecture '%s'\n" "$(uname -m)" >&2
      exit 1
      ;;
  esac
}

require_distribution_config() {
  if [[ "$MODE" == "ci" ]]; then
    if ! command -v rclone >/dev/null 2>&1; then
      printf "error: 'rclone' is required but not installed or not in \$PATH\n" >&2
      exit 1
    fi
    if [[ -z "${R2_ACCESS_KEY_ID:-}" || -z "${R2_SECRET_ACCESS_KEY:-}" || -z "${R2_ACCOUNT_ID:-}" || -z "${R2_BUCKET:-}" ]]; then
      printf "🔴 Distribution not configured\n" >&2
      exit 1
    fi
  fi
}

resolve_version() {
  if [[ "$MODE" == "ci" || "$MODE" == "mirror" ]]; then
    VERSION=$(sed -n 's/^## \[\(.*\)\] - .*/\1/p' CHANGELOG.md | head -n 1)
    if [[ -z "$VERSION" ]]; then
      printf "No version found in CHANGELOG.md\n"
      exit 0
    fi
  fi
}

configure_distribution() {
  if [[ "$MODE" == "ci" ]]; then
    export RCLONE_CONFIG_R2_TYPE=s3
    export RCLONE_CONFIG_R2_PROVIDER=Cloudflare
    export RCLONE_CONFIG_R2_ACCESS_KEY_ID="$R2_ACCESS_KEY_ID"
    export RCLONE_CONFIG_R2_SECRET_ACCESS_KEY="$R2_SECRET_ACCESS_KEY"
    export RCLONE_CONFIG_R2_ENDPOINT="https://${R2_ACCOUNT_ID}.r2.cloudflarestorage.com"
  fi
}

resolve_release_policy() {
  SHOULD_BUILD_BINARIES=true
  SHOULD_TAG_VERSION=false

  if [[ "$MODE" == "ci" ]]; then
    if git show-ref --verify --quiet "refs/tags/$VERSION"; then
      SHOULD_BUILD_BINARIES=false
    else
      SHOULD_TAG_VERSION=true
    fi
  fi
}

frontend_build() {
  # Download tools if missing (or requested in CI)
  [[ "$MODE" == "ci" && "${REFETCH_TOOLS:-false}" == "true" ]] && rm -f esbuild tailwindcss "$CSS_DIR/daisyui.mjs" "$CSS_DIR/daisyui-theme.mjs"
  [[ -f esbuild ]] || curl -fsSL https://esbuild.github.io/dl/latest | sh
  [[ -f tailwindcss ]] || download_file tailwindcss "https://github.com/tailwindlabs/tailwindcss/releases/download/${TAILWIND_VERSION}/tailwindcss-linux-x64"
  [[ -f "$CSS_DIR/daisyui.mjs" ]] || download_file "$CSS_DIR/daisyui.mjs" "https://github.com/saadeghi/daisyui/releases/download/${DAISYUI_VERSION}/daisyui.mjs"
  [[ -f "$CSS_DIR/daisyui-theme.mjs" ]] || download_file "$CSS_DIR/daisyui-theme.mjs" "https://github.com/saadeghi/daisyui/releases/download/${DAISYUI_VERSION}/daisyui-theme.mjs"

  chmod +x tailwindcss esbuild
  run_step "Tailwind CSS built" "Tailwind CSS failed" ./tailwindcss -i "$CSS_DIR/input.css" -o "$CSS_DIR/output.css" --minify
  run_step "JavaScript bundled" "JavaScript bundling failed" ./esbuild "$JS_DIR/src/main.js" --bundle --minify --outfile="$JS_DIR/output.js"
}

frontend_hash_assets() {
  local assets_dir="./internal/ui/assets"
  local manifest="$assets_dir/manifest.json"
  
  # Patterns to ignore (matched against relative path from assets/)
  local ignore_patterns=(
    "css/input.css"
    "css/daisyui.mjs"
    "css/daisyui-theme.mjs"
    "js/src/*"
    "manifest.json"
  )
  
  is_ignored() {
    local file="$1"
    for pattern in "${ignore_patterns[@]}"; do
      # shellcheck disable=SC2053
      if [[ "$file" == $pattern ]]; then
        return 0
      fi
    done
    return 1
  }
  
  # Build manifest as JSON
  local first=true
  printf '{' > "$manifest"
  
  while IFS= read -r -d '' file; do
    # Get relative path from assets dir
    local rel_path="${file#$assets_dir/}"
    
    # Skip ignored files
    if is_ignored "$rel_path"; then
      continue
    fi
    
    # Compute hash (first 16 chars of SHA256)
    local hash
    hash=$(sha256sum "$file" | cut -c1-16)
    
    # Add comma before all but first entry
    if $first; then
      first=false
    else
      printf ','
    fi >> "$manifest"
    
    # Write JSON entry
    printf '"%s":"%s"' "$rel_path" "$hash" >> "$manifest"
  done < <(find "$assets_dir" -type f -print0 | sort -z)
  
  printf '}' >> "$manifest"
  
  printf '🟢 Generated asset manifest\n'
}

tests() {
  run_step "Tests passed" "Tests failed" go test -race ./...
}

go_build() {
  local pkg="sprout/internal/build"
  local ldflags="-X '${pkg}.name=$APP_NAME'"
  ldflags+=" -X '${pkg}.version=$VERSION'"
  ldflags+=" -X '${pkg}.contactURL=$CONTACT_URL'"
  ldflags+=" -X '${pkg}.defaultLogLevel=$DEFAULT_LOG_LEVEL'"
  ldflags+=" -X '${pkg}.serviceEnabled=$SERVICE'"
  ldflags+=" -X '${pkg}.serviceDesc=$SERVICE_DESC'"
  ldflags+=" -X '${pkg}.serviceArgs=$SERVICE_ARGS'"
  ldflags+=" -X '${pkg}.serviceDefaultPort=$SERVICE_DEFAULT_PORT'"

  BUILD_OUTS=()
  VERIFY_BUILD_OUT="$OUT_DIR/linux-amd64"

  local targets=(amd64 arm64)
  if [[ "$MODE" == "local" && "$FAST_LOCAL" == "true" ]]; then
    targets=("$HOST_GOARCH")
    VERIFY_BUILD_OUT="$OUT_DIR/linux-$HOST_GOARCH"
  fi

  local target build_out cc
  for target in "${targets[@]}"; do
    build_out="$OUT_DIR/linux-$target"
    cc="gcc"
    if [[ "$target" == "arm64" && "$HOST_GOARCH" != "arm64" ]]; then
      cc="aarch64-linux-gnu-gcc"
    fi

    GOOS=linux GOARCH="$target" CC="$cc" CGO_ENABLED=1 go build -trimpath -buildvcs=false -ldflags="$ldflags" -o "$build_out" "$GO_MAIN_PATH"
    BUILD_OUTS+=("$build_out")
    printf "🟢 Built %s\n" "$build_out"
  done
}

verify_build() {
  # Only verify the amd64 binary on the amd64 runner.
  BUILD_VARS=$("$VERIFY_BUILD_OUT" --build-vars)
  export BUILD_VARS

  check_var "name" "$APP_NAME"
  check_var "version" "$VERSION"
  check_var "contactURL" "$CONTACT_URL"
  check_var "defaultLogLevel" "$DEFAULT_LOG_LEVEL"
  check_var "serviceEnabled" "$SERVICE"
  check_var "serviceDesc" "$SERVICE_DESC"
  check_var "serviceArgs" "$SERVICE_ARGS"
  check_var "serviceDefaultPort" "$SERVICE_DEFAULT_PORT"

  printf "🟢 Build variables verified\n"
}

package_installers() {
  local release_url="${MIRROR_RELEASE_URL:-$RELEASE_URL}"
  mkdir -p "$RELEASE_DIR"

  sed -e "s|<APP_NAME>|$APP_NAME|g" \
      -e "s|<RELEASE_URL>|$release_url|g" \
      -e "s|<SERVICE>|$SERVICE|g" \
      -e "s|<SERVICE_DESC>|$SERVICE_DESC|g" \
      -e "s|<SERVICE_ARGS>|$SERVICE_ARGS|g" \
      "./scripts/install.sh" > "$RELEASE_DIR/install.sh"
  printf "🟢 Processed install.sh\n"

  sed -e "s|<APP_NAME>|$APP_NAME|g" \
      -e "s|<RELEASE_URL>|$release_url|g" \
      -e "s|<SERVICE>|\$$SERVICE|g" \
      "./scripts/install.ps1" > "$RELEASE_DIR/install.ps1"
  printf "🟢 Processed install.ps1\n"
}

package_binaries() {
  mkdir -p "$RELEASE_DIR"

  local build_out gzip_out sha_out
  for build_out in "${BUILD_OUTS[@]}"; do
    gzip_out="$RELEASE_DIR/$(basename "$build_out").gz"
    gzip -c -n -- "$build_out" > "$gzip_out"
    printf "🟢 Gzipped %s\n" "$build_out"

    sha_out="$gzip_out.sha256"
    (
      cd "$(dirname "$gzip_out")" || exit 1
      sha256sum "$(basename "$gzip_out")" > "$(basename "$sha_out")"
    )
    printf "🟢 Generated checksum %s\n" "$sha_out"
  done
}

write_release_version() {
  mkdir -p "$RELEASE_DIR"
  echo "$VERSION" > "$RELEASE_DIR/version"
  printf "🟢 Release packaged in %s\n" "$RELEASE_DIR"
}

distribute() {
  if $SHOULD_TAG_VERSION; then
    # GIT_TERMINAL_PROMPT=0 ensures failure instead of hang if auth fails
    run_step "Tagged $VERSION" "Failed to tag $VERSION" git tag "$VERSION"
    run_step "Pushed $VERSION" "Failed to push $VERSION" env GIT_TERMINAL_PROMPT=0 git push origin "$VERSION"
  fi

  local f
  for f in "$RELEASE_DIR"/*; do
    run_step "Uploaded $(basename "$f")" "Failed to upload $(basename "$f")" rclone copyto "$f" "r2:$R2_BUCKET/$(basename "$f")" --header-upload "$NO_CACHE" --s3-env-auth --s3-no-check-bucket
  done
}

mirror() {
  mkdir -p "$MIRROR_DIR"
  cp "$RELEASE_DIR"/* "$MIRROR_DIR/"
  printf "🟢 Release artifacts copied to %s\n" "$MIRROR_DIR"
}

# Main ------------------------------------------------------------------------

main() {
  clean_out_dir
  parse_args "$@"
  detect_mode
  validate_mode_flags
  detect_host_arch
  dep_check
  require_distribution_config
  resolve_version
  configure_distribution
  resolve_release_policy

  if $SHOULD_BUILD_BINARIES; then
    frontend_build
    frontend_hash_assets
    if [[ "$FAST_LOCAL" == "true" ]]; then
      printf "🟢 Skipping tests in fast local mode\n"
    else
      tests
    fi
    go_build
    verify_build
  elif [[ "$MODE" == "ci" ]]; then
    printf "🟢 Skipping binary build for tagged version\n"
  fi

  if [[ "$MODE" == "ci" || "$MODE" == "mirror" ]]; then
    package_installers
    if $SHOULD_BUILD_BINARIES; then
      package_binaries
    fi
    write_release_version
  fi

  if [[ "$MODE" == "ci" ]]; then
    distribute
  elif [[ "$MODE" == "mirror" ]]; then
    mirror
  fi
}

main "$@"