#!/usr/bin/env bash

# Pinned third-party build inputs: the single source of truth for every tool
# version and hash this repository fetches.
#
# Two modes:
#
#   source scripts/vendor.sh
#     Define the pin variables and the fetch functions. No network, no side
#     effects. scripts/build.sh does this in place of an inline pin block.
#
#   ./scripts/vendor.sh [--refetch] [tool ...]
#     Ensure the named tools exist under tools/, verified against the pins,
#     then print "<tool>=<absolute path>" for each on stdout. Progress goes to
#     stderr so a caller can capture the paths. With no tool arguments every
#     tool supported on the current host is ensured.
#
# --refetch re-downloads even when a verified copy is cached and ignores a
# candidate found on PATH. Go-installed tools are rebuilt on every run except
# for a goimports on PATH whose embedded module version matches the pin.
#
# Cached downloads are re-verified against their pinned hash before every use,
# so tools/ stays incidental scratch space rather than a trusted boundary.
#
# Deliberately not owned here:
#   - GitHub Actions in `uses:` clauses. Those must be literal commit SHAs; no
#     expression can supply them, so they live directly in the workflow files.
#   - docs/go.mod, which is Go's own pin for the Hugo theme module.
#   - The <COSIGN_*> placeholders in install.sh and install.ps1. Those are
#     standalone `curl | sh` artifacts that cannot source anything; the values
#     below are rendered into them by render_installer at build time.

# Versions --------------------------------------------------------------------

DEFAULT_ESBUILD_VERSION="v0.28.1"
DEFAULT_TAILWIND_VERSION="v4.3.2"
DEFAULT_DAISYUI_VERSION="v5.6.10"
DEFAULT_COSIGN_VERSION="v3.1.3"
DEFAULT_RCLONE_VERSION="v1.75.0"
DEFAULT_SHELLCHECK_VERSION="v0.11.0"
# --- BEGIN template ---
DEFAULT_GOIMPORTS_VERSION="v0.49.0"
DEFAULT_HUGO_VERSION="0.164.0"
# Floating majors would let a wrangler release change a deploy silently.
DEFAULT_WRANGLER_VERSION="4.125.0"
# --- END template ---

ESBUILD_VERSION="${ESBUILD_VERSION:-$DEFAULT_ESBUILD_VERSION}"
TAILWIND_VERSION="${TAILWIND_VERSION:-$DEFAULT_TAILWIND_VERSION}"
DAISYUI_VERSION="${DAISYUI_VERSION:-$DEFAULT_DAISYUI_VERSION}"
COSIGN_VERSION="${COSIGN_VERSION:-$DEFAULT_COSIGN_VERSION}"
RCLONE_VERSION="${RCLONE_VERSION:-$DEFAULT_RCLONE_VERSION}"
SHELLCHECK_VERSION="${SHELLCHECK_VERSION:-$DEFAULT_SHELLCHECK_VERSION}"
# --- BEGIN template ---
GOIMPORTS_VERSION="${GOIMPORTS_VERSION:-$DEFAULT_GOIMPORTS_VERSION}"
HUGO_VERSION="${HUGO_VERSION:-$DEFAULT_HUGO_VERSION}"
WRANGLER_VERSION="${WRANGLER_VERSION:-$DEFAULT_WRANGLER_VERSION}"
# --- END template ---

# Hashes ----------------------------------------------------------------------
#
# Download hashes are deliberate source pins. A version override must carry
# matching hash overrides; cached files are checked again before every use.
# Anything installed with `go install` carries no hash here: the Go module
# checksum database already authenticates it.

TAILWIND_SHA_LINUX_AMD64_OVERRIDE="${TAILWIND_SHA_LINUX_AMD64:-}"
TAILWIND_SHA_LINUX_ARM64_OVERRIDE="${TAILWIND_SHA_LINUX_ARM64:-}"
DAISYUI_SHA_OVERRIDE="${DAISYUI_SHA:-}"
DAISYUI_THEME_SHA_OVERRIDE="${DAISYUI_THEME_SHA:-}"
COSIGN_SHA_LINUX_AMD64_OVERRIDE="${COSIGN_SHA_LINUX_AMD64:-}"
COSIGN_SHA_LINUX_ARM64_OVERRIDE="${COSIGN_SHA_LINUX_ARM64:-}"
COSIGN_SHA_WINDOWS_AMD64_OVERRIDE="${COSIGN_SHA_WINDOWS_AMD64:-}"
RCLONE_SHA_LINUX_AMD64_OVERRIDE="${RCLONE_SHA_LINUX_AMD64:-}"
SHELLCHECK_SHA_LINUX_AMD64_OVERRIDE="${SHELLCHECK_SHA_LINUX_AMD64:-}"
SHELLCHECK_SHA_LINUX_ARM64_OVERRIDE="${SHELLCHECK_SHA_LINUX_ARM64:-}"
# --- BEGIN template ---
HUGO_SHA_LINUX_AMD64_OVERRIDE="${HUGO_SHA_LINUX_AMD64:-}"
# --- END template ---

TAILWIND_SHA_LINUX_AMD64="${TAILWIND_SHA_LINUX_AMD64:-5036c4fb4328e0bcdbb6065c70d8ac9452e0d4c947113a788a8f94fd390425c1}"
TAILWIND_SHA_LINUX_ARM64="${TAILWIND_SHA_LINUX_ARM64:-394ddccc2402cfa3abd97dfba56f3587781a3d6e6ce66e65ceada14beb7664b8}"
DAISYUI_SHA="${DAISYUI_SHA:-72c6fe3329ddb1f27834d3d42356c504769ca7d9874a81a2acdd41497f7453a8}"
DAISYUI_THEME_SHA="${DAISYUI_THEME_SHA:-c24197355c095626005288728aa156ce193299c848cd7652b633c59e5afafe8d}"
COSIGN_SHA_LINUX_AMD64="${COSIGN_SHA_LINUX_AMD64:-4629c757b7618056f8ddd7e2625ae9fdd94c0372a65049520bc7d9df9efc7f71}"
COSIGN_SHA_LINUX_ARM64="${COSIGN_SHA_LINUX_ARM64:-c5d324e091826b0d7a78eb16fef316450b4eb9aaec045611c08ba06f5e73220a}"
COSIGN_SHA_WINDOWS_AMD64="${COSIGN_SHA_WINDOWS_AMD64:-9fe59be0eca1271873ce019061335eb1ac419b7059202e797828467ddabe33be}"
RCLONE_SHA_LINUX_AMD64="${RCLONE_SHA_LINUX_AMD64:-aa2804e08f48250e71009c727124b6341cd0288465804a9a09d14663cabafbaa}"
SHELLCHECK_SHA_LINUX_AMD64="${SHELLCHECK_SHA_LINUX_AMD64:-b7af85e41cc99489dcc21d66c6d5f3685138f06d34651e6d34b42ec6d54fe6f6}"
SHELLCHECK_SHA_LINUX_ARM64="${SHELLCHECK_SHA_LINUX_ARM64:-68a8133197a50beb8803f8d42f9908d1af1c5540d4bb05fdfca8c1fa47decefc}"
# --- BEGIN template ---
# The upstream Hugo checksums file ships from the same release as the archive,
# so verifying against it would only catch transfer corruption.
HUGO_SHA_LINUX_AMD64="${HUGO_SHA_LINUX_AMD64:-fea17b8c076f950bb2e9f9486667bdaa29422883888d509d63931c73e8a9b3a4}"
# --- END template ---

# Downloaded build tools (gitignored). Release-critical tools land here pinned
# by version and hash; the `go install` ones are authenticated through the Go
# checksum database instead. Anything else under tools/, such as the gocache,
# gomodcache, gopath, and hugo-cache directories, is incidental local scratch
# space rather than a hermetic build boundary, and nothing may depend on its
# contents.
TOOLS_DIR="${TOOLS_DIR:-./tools}"

# Resolved tool locations, set by the fetchers below.
VENDOR_ESBUILD=""
VENDOR_TAILWIND=""
VENDOR_DAISYUI=""
VENDOR_COSIGN=""
VENDOR_RCLONE=""
VENDOR_SHELLCHECK=""
# --- BEGIN template ---
VENDOR_HUGO=""
VENDOR_GOIMPORTS=""
# --- END template ---

# Signing binary. Defaults to whatever `cosign` resolves to on PATH so local
# harnesses can substitute a stand-in; vendor_cosign repoints it at the pinned
# download, which is what CI signs with.
COSIGN_BIN="${COSIGN_BIN:-cosign}"

VENDOR_REFETCH="${VENDOR_REFETCH:-false}"

VENDOR_FETCHABLE=(esbuild tailwind daisyui cosign rclone shellcheck)
# --- BEGIN template ---
VENDOR_FETCHABLE+=(hugo goimports)
# --- END template ---

# Pin validation --------------------------------------------------------------

require_hash_overrides() {
  local tool="$1"
  local version="$2"
  local default_version="$3"
  shift 3
  if [[ "$version" == "$default_version" ]]; then
    return
  fi
  local value
  for value in "$@"; do
    if [[ -z "$value" ]]; then
      printf "error: overriding %s version to %s requires all matching SHA-256 overrides\n" "$tool" "$version" >&2
      exit 1
    fi
  done
}

validate_pins() {
  require_hash_overrides "Tailwind" "$TAILWIND_VERSION" "$DEFAULT_TAILWIND_VERSION" \
    "$TAILWIND_SHA_LINUX_AMD64_OVERRIDE" "$TAILWIND_SHA_LINUX_ARM64_OVERRIDE"
  require_hash_overrides "DaisyUI" "$DAISYUI_VERSION" "$DEFAULT_DAISYUI_VERSION" \
    "$DAISYUI_SHA_OVERRIDE" "$DAISYUI_THEME_SHA_OVERRIDE"
  require_hash_overrides "cosign" "$COSIGN_VERSION" "$DEFAULT_COSIGN_VERSION" \
    "$COSIGN_SHA_LINUX_AMD64_OVERRIDE" "$COSIGN_SHA_LINUX_ARM64_OVERRIDE" "$COSIGN_SHA_WINDOWS_AMD64_OVERRIDE"
  require_hash_overrides "rclone" "$RCLONE_VERSION" "$DEFAULT_RCLONE_VERSION" \
    "$RCLONE_SHA_LINUX_AMD64_OVERRIDE"
  require_hash_overrides "shellcheck" "$SHELLCHECK_VERSION" "$DEFAULT_SHELLCHECK_VERSION" \
    "$SHELLCHECK_SHA_LINUX_AMD64_OVERRIDE" "$SHELLCHECK_SHA_LINUX_ARM64_OVERRIDE"
  # --- BEGIN template ---
  require_hash_overrides "Hugo" "$HUGO_VERSION" "$DEFAULT_HUGO_VERSION" \
    "$HUGO_SHA_LINUX_AMD64_OVERRIDE"
  # --- END template ---

  validate_sha256 "$TAILWIND_SHA_LINUX_AMD64" "TAILWIND_SHA_LINUX_AMD64"
  validate_sha256 "$TAILWIND_SHA_LINUX_ARM64" "TAILWIND_SHA_LINUX_ARM64"
  validate_sha256 "$DAISYUI_SHA" "DAISYUI_SHA"
  validate_sha256 "$DAISYUI_THEME_SHA" "DAISYUI_THEME_SHA"
  validate_sha256 "$COSIGN_SHA_LINUX_AMD64" "COSIGN_SHA_LINUX_AMD64"
  validate_sha256 "$COSIGN_SHA_LINUX_ARM64" "COSIGN_SHA_LINUX_ARM64"
  validate_sha256 "$COSIGN_SHA_WINDOWS_AMD64" "COSIGN_SHA_WINDOWS_AMD64"
  validate_sha256 "$RCLONE_SHA_LINUX_AMD64" "RCLONE_SHA_LINUX_AMD64"
  validate_sha256 "$SHELLCHECK_SHA_LINUX_AMD64" "SHELLCHECK_SHA_LINUX_AMD64"
  validate_sha256 "$SHELLCHECK_SHA_LINUX_ARM64" "SHELLCHECK_SHA_LINUX_ARM64"
  # --- BEGIN template ---
  validate_sha256 "$HUGO_SHA_LINUX_AMD64" "HUGO_SHA_LINUX_AMD64"
  # --- END template ---
}

# Fetchers --------------------------------------------------------------------

vendor_abs() {
  printf '%s/%s\n' "$(cd "$(dirname "$1")" && pwd)" "$(basename "$1")"
}

vendor_require_amd64() {
  [[ "$HOST_GOARCH" == "amd64" ]] && return 0
  printf "error: no %s download is pinned for %s\n" "$1" "$HOST_GOARCH" >&2
  return 1
}

vendor_require_bins() {
  local bin
  for bin in "$@"; do
    command -v "$bin" >/dev/null 2>&1 && continue
    printf "error: '%s' is required to vendor this tool but is not on \$PATH\n" "$bin" >&2
    return 1
  done
}

# vendor_go_tool <name> <package@version> <module> <version>
# Reinstalls on every run: the Go checksum database authenticates the source,
# and rebuilding is cheaper than deciding whether a binary in tools/ is stale.
vendor_go_tool() {
  local name="$1" package="$2" module="$3" version="$4"
  mkdir -p "$TOOLS_DIR"
  run_step "Vendored $name $version" "Failed to vendor $name $version" \
    env GOBIN="$(cd "$TOOLS_DIR" && pwd)" GOSUMDB=sum.golang.org GONOSUMDB= GOPRIVATE= \
      go install "$package"
  local installed
  installed=$(go version -m "$TOOLS_DIR/$name" | awk -v m="$module" '$1=="mod" && $2==m {print $3}')
  if [[ "$installed" != "$version" ]]; then
    printf "error: vendored %s reports version %q, want %q\n" "$name" "$installed" "$version" >&2
    return 1
  fi
  chmod +x "$TOOLS_DIR/$name"
}

vendor_esbuild() {
  vendor_go_tool esbuild \
    "github.com/evanw/esbuild/cmd/esbuild@${ESBUILD_VERSION}" \
    "github.com/evanw/esbuild" "$ESBUILD_VERSION"
  VENDOR_ESBUILD="$TOOLS_DIR/esbuild"
}

vendor_tailwind() {
  # The standalone is dynamically linked (glibc, and the -musl variant against
  # musl's loader), so it cannot run on NixOS. Local builds prefer a
  # tailwindcss from PATH, such as the one in the flake dev shell; CI always
  # downloads the pinned standalone so releases stay reproducible.
  if [[ "$VENDOR_REFETCH" != "true" && "${MODE:-local}" == "local" ]] &&
      command -v tailwindcss >/dev/null 2>&1; then
    VENDOR_TAILWIND=$(command -v tailwindcss)
    printf '🟢 Using tailwindcss from PATH (%s)\n' "$VENDOR_TAILWIND"
    return 0
  fi
  local asset sha
  case "$HOST_GOARCH" in
    amd64) asset="tailwindcss-linux-x64"; sha="$TAILWIND_SHA_LINUX_AMD64" ;;
    arm64) asset="tailwindcss-linux-arm64"; sha="$TAILWIND_SHA_LINUX_ARM64" ;;
    *)
      printf "error: no Tailwind download configured for %s\n" "$HOST_GOARCH" >&2
      return 1
      ;;
  esac
  mkdir -p "$TOOLS_DIR"
  download_verified "$TOOLS_DIR/tailwindcss" \
    "https://github.com/tailwindlabs/tailwindcss/releases/download/${TAILWIND_VERSION}/${asset}" \
    "$sha" "Tailwind $HOST_GOARCH"
  chmod +x "$TOOLS_DIR/tailwindcss"
  VENDOR_TAILWIND="$TOOLS_DIR/tailwindcss"
}

vendor_daisyui() {
  mkdir -p "$TOOLS_DIR"
  download_verified "$TOOLS_DIR/daisyui.mjs" \
    "https://github.com/saadeghi/daisyui/releases/download/${DAISYUI_VERSION}/daisyui.mjs" \
    "$DAISYUI_SHA" "DaisyUI module"
  download_verified "$TOOLS_DIR/daisyui-theme.mjs" \
    "https://github.com/saadeghi/daisyui/releases/download/${DAISYUI_VERSION}/daisyui-theme.mjs" \
    "$DAISYUI_THEME_SHA" "DaisyUI theme module"
  # The theme module is referenced by relative path from input.css.
  VENDOR_DAISYUI="$TOOLS_DIR/daisyui.mjs"
}

# No PATH preference: the whole point of pinning cosign here is that CI signs
# with the same version and bytes the installers bootstrap for themselves.
vendor_cosign() {
  local asset sha
  case "$HOST_GOARCH" in
    amd64) asset="cosign-linux-amd64"; sha="$COSIGN_SHA_LINUX_AMD64" ;;
    arm64) asset="cosign-linux-arm64"; sha="$COSIGN_SHA_LINUX_ARM64" ;;
    *)
      printf "error: no cosign download configured for %s\n" "$HOST_GOARCH" >&2
      return 1
      ;;
  esac
  mkdir -p "$TOOLS_DIR"
  download_verified "$TOOLS_DIR/cosign" \
    "https://github.com/sigstore/cosign/releases/download/${COSIGN_VERSION}/${asset}" \
    "$sha" "cosign $HOST_GOARCH"
  chmod +x "$TOOLS_DIR/cosign"
  VENDOR_COSIGN="$TOOLS_DIR/cosign"
  COSIGN_BIN=$(vendor_abs "$VENDOR_COSIGN")
}

vendor_rclone() {
  vendor_require_amd64 rclone
  vendor_require_bins unzip install
  mkdir -p "$TOOLS_DIR"
  local base="rclone-${RCLONE_VERSION}-linux-amd64"
  local archive="$TOOLS_DIR/$base.zip"
  download_verified "$archive" \
    "https://downloads.rclone.org/${RCLONE_VERSION}/${base}.zip" \
    "$RCLONE_SHA_LINUX_AMD64" "rclone amd64"
  local extract="$TOOLS_DIR/.rclone-extract"
  rm -rf "$extract"
  unzip -q "$archive" -d "$extract"
  install -m 0755 "$extract/$base/rclone" "$TOOLS_DIR/rclone"
  rm -rf "$extract"
  VENDOR_RCLONE="$TOOLS_DIR/rclone"
  printf '🟢 Vendored rclone %s\n' "$RCLONE_VERSION"
}

# Static binary; no PATH preference so local lint and CI agree on findings.
vendor_shellcheck() {
  vendor_require_bins tar install
  local asset sha
  case "$HOST_GOARCH" in
    amd64) asset="x86_64"; sha="$SHELLCHECK_SHA_LINUX_AMD64" ;;
    arm64) asset="aarch64"; sha="$SHELLCHECK_SHA_LINUX_ARM64" ;;
    *)
      printf "error: no shellcheck download configured for %s\n" "$HOST_GOARCH" >&2
      return 1
      ;;
  esac
  mkdir -p "$TOOLS_DIR"
  local base="shellcheck-${SHELLCHECK_VERSION}"
  local archive="$TOOLS_DIR/${base}.linux.${asset}.tar.gz"
  download_verified "$archive" \
    "https://github.com/koalaman/shellcheck/releases/download/${SHELLCHECK_VERSION}/${base}.linux.${asset}.tar.gz" \
    "$sha" "shellcheck $HOST_GOARCH"
  local extract="$TOOLS_DIR/.shellcheck-extract"
  rm -rf "$extract" && mkdir -p "$extract"
  # The archive carries upstream uid/gid; never try to reproduce them.
  tar --no-same-owner -xzf "$archive" -C "$extract"
  install -m 0755 "$extract/$base/shellcheck" "$TOOLS_DIR/shellcheck"
  rm -rf "$extract"
  VENDOR_SHELLCHECK="$TOOLS_DIR/shellcheck"
  printf '🟢 Vendored shellcheck %s\n' "$SHELLCHECK_VERSION"
}

# --- BEGIN template ---
vendor_hugo() {
  vendor_require_amd64 Hugo
  vendor_require_bins tar install
  mkdir -p "$TOOLS_DIR"
  local archive="$TOOLS_DIR/hugo_extended_${HUGO_VERSION}_linux-amd64.tar.gz"
  download_verified "$archive" \
    "https://github.com/gohugoio/hugo/releases/download/v${HUGO_VERSION}/hugo_extended_${HUGO_VERSION}_linux-amd64.tar.gz" \
    "$HUGO_SHA_LINUX_AMD64" "Hugo amd64"
  local extract="$TOOLS_DIR/.hugo-extract"
  rm -rf "$extract" && mkdir -p "$extract"
  tar -xzf "$archive" -C "$extract"
  install -m 0755 "$extract/hugo" "$TOOLS_DIR/hugo"
  rm -rf "$extract"
  VENDOR_HUGO="$TOOLS_DIR/hugo"
  printf '🟢 Vendored Hugo %s\n' "$HUGO_VERSION"
}

vendor_goimports() {
  local candidate installed
  if [[ "$VENDOR_REFETCH" != "true" ]] && candidate=$(command -v goimports 2>/dev/null); then
    installed=$(go version -m "$candidate" | awk '$1=="mod" && $2=="golang.org/x/tools" {print $3}') || true
    if [[ "$installed" == "$GOIMPORTS_VERSION" ]]; then
      VENDOR_GOIMPORTS="$candidate"
      printf '🟢 Using pinned goimports from PATH (%s)\n' "$VENDOR_GOIMPORTS"
      return 0
    fi
    printf '🟡 Ignoring goimports %s from PATH; want %s\n' "${installed:-unknown}" "$GOIMPORTS_VERSION"
  fi
  vendor_go_tool goimports \
    "golang.org/x/tools/cmd/goimports@${GOIMPORTS_VERSION}" \
    "golang.org/x/tools" "$GOIMPORTS_VERSION"
  VENDOR_GOIMPORTS="$TOOLS_DIR/goimports"
}
# --- END template ---

vendor_ensure() {
  case "$1" in
    esbuild) vendor_esbuild ;;
    tailwind) vendor_tailwind ;;
    daisyui) vendor_daisyui ;;
    cosign) vendor_cosign ;;
    rclone) vendor_rclone ;;
    shellcheck) vendor_shellcheck ;;
    # --- BEGIN template ---
    hugo) vendor_hugo ;;
    goimports) vendor_goimports ;;
    # --- END template ---
    *)
      printf "error: unknown vendored tool '%s'\n" "$1" >&2
      printf "known tools: %s\n" "${VENDOR_FETCHABLE[*]}" >&2
      return 1
      ;;
  esac
}

vendor_resolved() {
  case "$1" in
    esbuild) printf '%s' "$VENDOR_ESBUILD" ;;
    tailwind) printf '%s' "$VENDOR_TAILWIND" ;;
    daisyui) printf '%s' "$VENDOR_DAISYUI" ;;
    cosign) printf '%s' "$VENDOR_COSIGN" ;;
    rclone) printf '%s' "$VENDOR_RCLONE" ;;
    shellcheck) printf '%s' "$VENDOR_SHELLCHECK" ;;
    # --- BEGIN template ---
    hugo) printf '%s' "$VENDOR_HUGO" ;;
    goimports) printf '%s' "$VENDOR_GOIMPORTS" ;;
    # --- END template ---
  esac
}

vendor_usage() {
  cat <<EOF
Usage: ./scripts/vendor.sh [--refetch] [tool ...]

Ensures pinned build tools exist under $TOOLS_DIR and prints "<tool>=<path>"
for each. With no tool arguments, every tool supported on this host is ensured.

Tools: ${VENDOR_FETCHABLE[*]}

  --refetch  Re-download cached files and ignore candidates found on PATH.
EOF
}

vendor_main() {
  local tools=()
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --refetch)
        VENDOR_REFETCH=true
        REFETCH_TOOLS=true
        shift
        ;;
      -h|--help)
        vendor_usage
        return 0
        ;;
      -*)
        printf "error: unknown argument '%s'\n" "$1" >&2
        vendor_usage >&2
        return 1
        ;;
      *)
        tools+=("$1")
        shift
        ;;
    esac
  done
  detect_host_arch
  validate_pins

  if [[ ${#tools[@]} -eq 0 ]]; then
    local candidate
    for candidate in "${VENDOR_FETCHABLE[@]}"; do
      case "$HOST_GOARCH:$candidate" in
        arm64:rclone|arm64:hugo) continue ;;
      esac
      tools+=("$candidate")
    done
  fi

  local tool
  for tool in "${tools[@]}"; do
    vendor_ensure "$tool" >&2
  done
  for tool in "${tools[@]}"; do
    printf '%s=%s\n' "$tool" "$(vendor_abs "$(vendor_resolved "$tool")")"
  done
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  set -euo pipefail
  umask 022
  export LC_ALL=C
  # shellcheck disable=SC1007 # empty CDPATH is deliberate so cd prints nothing
  cd "$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
  # shellcheck source=build/common.sh
  source "scripts/build/common.sh"
  vendor_main "$@"
fi
