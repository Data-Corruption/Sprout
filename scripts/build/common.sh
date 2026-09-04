# Shared build primitives.
#
# This file is sourced by ../build.sh. Keep it focused on command execution,
# verified downloads, invocation parsing, and host/build-mode detection.

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
# Downloads a file, with status output. Retries: release infrastructure is the
# most common source of transient 5xx responses in CI.
download_file() {
  run_step "Downloaded $2" "Failed to download $2" \
    curl -fsSL --retry 3 --retry-all-errors --connect-timeout 10 --max-time 300 -o "$1" "$2"
}

validate_sha256() {
  local value="$1"
  local label="$2"
  if [[ ! "$value" =~ ^[0-9a-fA-F]{64}$ ]]; then
    printf "error: %s must be a 64-character SHA-256 value\n" "$label" >&2
    exit 1
  fi
}

verify_sha256() {
  local path="$1"
  local expected="${2,,}"
  local label="$3"
  validate_sha256 "$expected" "$label"
  local actual
  actual=$(sha256sum "$path" | awk '{print tolower($1)}')
  if [[ "$actual" != "$expected" ]]; then
    printf "error: %s checksum mismatch for %s: expected %s, got %s\n" "$label" "$path" "$expected" "$actual" >&2
    return 1
  fi
}

download_verified() {
  local path="$1"
  local url="$2"
  local expected="$3"
  local label="$4"
  if [[ ! -f "$path" || "${REFETCH_TOOLS:-false}" == "true" ]]; then
    local tmp
    tmp=$(mktemp "${path}.download.XXXXXX")
    if ! download_file "$tmp" "$url" || ! verify_sha256 "$tmp" "$expected" "$label"; then
      rm -f "$tmp"
      return 1
    fi
    mv -f "$tmp" "$path"
  fi
  if ! verify_sha256 "$path" "$expected" "$label"; then
    printf "error: remove the cached file or rerun with REFETCH_TOOLS=true\n" >&2
    return 1
  fi
}

# check_var "key" "expected"
# Verifies a build variable matches the expected value.
# Handles both string values ("key":"value") and non-string values (key:value or key:true).
check_var() {
  local key="$1"
  local expected="$2"
  local actual
  local string_entry
  # Keep the matched entry separate because an empty parsed string is valid;
  # it must not be mistaken for a missing string entry.
  string_entry=$(printf '%s\n' "$BUILD_VARS" | grep -oP "\"$key\":\"[^\"]*\"") || true
  if [[ -n "$string_entry" ]]; then
    actual=$(printf '%s\n' "$string_entry" | cut -d'"' -f4)
  else
    actual=$(printf '%s\n' "$BUILD_VARS" | grep -oP "\"$key\":[^,}]+" | cut -d':' -f2)
  fi
  if [[ "$actual" != "$expected" ]]; then
    echo "🔴 Error: $key mismatch. Expected '$expected', got '$actual'"
    exit 1
  fi
}

dep_check() {
  # The build itself is pure Go; gcc is only needed by the host-toolchain
  # test.sh run before production builds.
  local required_bins=(go sed awk sha256sum gzip)
  if [[ "$BUILD_KIND" != "dev" ]]; then
    required_bins+=(gcc)
  fi
  if [[ "$MODE" == "ci" ]]; then
    # cosign is not listed: prepare_local_build_context vendors the pinned one.
    required_bins+=(curl)
  fi

  for bin in "${required_bins[@]}"; do
    if ! command -v "$bin" >/dev/null 2>&1; then
      printf "error: '$bin' is required but not installed or not in \$PATH\n" >&2
      exit 1
    fi
  done
}

clean_out_dir() {
  rm -rf "$OUT_DIR" && mkdir -p "$OUT_DIR"
  printf '🟢 Cleaned out directory\n'
}

parse_args() {
  local kind_set=false
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --prod|--prod-all)
        if [[ "$kind_set" == "true" ]]; then
          printf "error: --prod and --prod-all are mutually exclusive\n" >&2
          exit 1
        fi
        kind_set=true
        BUILD_KIND="${1#--}"
        shift
        ;;
      *)
        printf "error: unknown argument '%s'\n" "$1" >&2
        exit 1
        ;;
    esac
  done
  # only the default dev build bakes DevMode
  [[ "$BUILD_KIND" == "dev" ]] || DEV_MODE=false
}

detect_mode() {
  if [[ "${CI:-}" == "true" ]]; then
    MODE="ci"
    if [[ -z "${GITHUB_REPOSITORY:-}" ]]; then
      printf "🔴 GITHUB_REPOSITORY not set; cannot derive cosign identity\n" >&2
      exit 1
    fi
    CERT_IDENTITY="https://github.com/${GITHUB_REPOSITORY}/.github/workflows/release.yml@refs/heads/main"
  else
    MODE="local"
  fi
}

validate_mode_flags() {
  if [[ "$MODE" == "ci" && "$BUILD_KIND" != "prod-all" ]]; then
    printf "error: CI builds must use --prod-all\n" >&2
    exit 1
  fi
}

validate_app_name() {
  if [[ ! "$APP_NAME" =~ ^[A-Za-z0-9_][A-Za-z0-9._-]*$ ]]; then
    printf "error: APP_NAME must match [A-Za-z0-9_][A-Za-z0-9._-]*\n" >&2
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
