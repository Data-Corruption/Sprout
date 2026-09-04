# Remote release publication and recovery state machine.
#
# This file is sourced by ../build.sh. It owns distribution configuration,
# release policy, immutable objects, installer publication, promotion, tagging,
# and retention. Keep durable remote writes in protocol order.

RELEASE_CHECKSUM_OBJECTS=(
  linux-amd64.gz
  linux-arm64.gz
  windows-amd64.exe.gz
  windows-arm64.exe.gz
  version
)
RELEASE_OBJECTS=(
  "${RELEASE_CHECKSUM_OBJECTS[@]}"
  checksums.txt
  checksums.txt.cosign.bundle
)

# Remote inspection uses one status convention throughout this file:
# 0 is present/verified, 1 is absent/incomplete, and 2 is invalid or unreadable.
REMOTE_ABSENT=1
REMOTE_UNUSABLE=2

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
  if [[ "$MODE" == "ci" ]]; then
    VERSION=$(sed -n 's/^## \[\(.*\)\] - .*/\1/p' CHANGELOG.md | head -n 1)
    if [[ -z "$VERSION" ]]; then
      printf "error: no release heading found in CHANGELOG.md\n" >&2
      printf "  The publisher reads its version from the first heading shaped like:\n" >&2
      printf "    ## [vX.Y.Z] - YYYY-MM-DD\n" >&2
      printf "  Add one for the release you intend to publish; the commented-out\n" >&2
      printf "  example near the top of CHANGELOG.md shows the expected form.\n" >&2
      printf "  Once a heading exists it stays, and later pushes that do not change\n" >&2
      printf "  it re-verify the promoted release and finish without republishing.\n" >&2
      exit 1
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
    PUBLISH_REMOTE="r2:$R2_BUCKET"
    RCLONE_ARGS=(--s3-env-auth --s3-no-check-bucket)
    UPLOAD_ARGS=(--header-upload "$NO_CACHE" --ignore-times)
  fi
}

validate_version() {
  local value="$1"
  if [[ ! "$value" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$ ]]; then
    printf "error: invalid release version %q\n" "$value" >&2
    return 1
  fi
  local without_build="${value%%+*}"
  local prerelease=""
  [[ "$without_build" == *-* ]] && prerelease="${without_build#*-}"
  local build=""
  [[ "$value" == *+* ]] && build="${value#*+}"
  local identifiers identifier
  for identifiers in "$prerelease" "$build"; do
    [[ -n "$identifiers" ]] || continue
    IFS='.' read -r -a parts <<< "$identifiers"
    for identifier in "${parts[@]}"; do
      if [[ -z "$identifier" || ( "$identifiers" == "$prerelease" && "$identifier" =~ ^[0-9]+$ && ${#identifier} -gt 1 && "$identifier" == 0* ) ]]; then
        printf "error: invalid release version %q\n" "$value" >&2
        return 1
      fi
    done
  done
}

# Prints -1, 0, or 1 using SemVer precedence (build metadata is ignored).
semver_compare() {
  local left="${1#v}" right="${2#v}"
  left="${left%%+*}"
  right="${right%%+*}"
  local left_core="$left" right_core="$right"
  local left_pre="" right_pre=""
  if [[ "$left" == *-* ]]; then
    left_core="${left%%-*}"
    left_pre="${left#*-}"
  fi
  if [[ "$right" == *-* ]]; then
    right_core="${right%%-*}"
    right_pre="${right#*-}"
  fi

  local left_parts right_parts i
  IFS='.' read -r -a left_parts <<< "$left_core"
  IFS='.' read -r -a right_parts <<< "$right_core"
  local left_id right_id
  for i in 0 1 2; do
    left_id="${left_parts[$i]}"
    right_id="${right_parts[$i]}"
    if (( ${#left_id} < ${#right_id} )); then printf '%s\n' -1; return; fi
    if (( ${#left_id} > ${#right_id} )); then printf '%s\n' 1; return; fi
    if [[ "$left_id" < "$right_id" ]]; then printf '%s\n' -1; return; fi
    if [[ "$left_id" > "$right_id" ]]; then printf '%s\n' 1; return; fi
  done

  if [[ -z "$left_pre" && -z "$right_pre" ]]; then printf '%s\n' 0; return; fi
  if [[ -z "$left_pre" ]]; then printf '%s\n' 1; return; fi
  if [[ -z "$right_pre" ]]; then printf '%s\n' -1; return; fi

  IFS='.' read -r -a left_parts <<< "$left_pre"
  IFS='.' read -r -a right_parts <<< "$right_pre"
  local max="${#left_parts[@]}"
  (( ${#right_parts[@]} > max )) && max="${#right_parts[@]}"
  for ((i=0; i<max; i++)); do
    if (( i >= ${#left_parts[@]} )); then printf '%s\n' -1; return; fi
    if (( i >= ${#right_parts[@]} )); then printf '%s\n' 1; return; fi
    left_id="${left_parts[$i]}"
    right_id="${right_parts[$i]}"
    [[ "$left_id" == "$right_id" ]] && continue
    if [[ "$left_id" =~ ^[0-9]+$ && "$right_id" =~ ^[0-9]+$ ]]; then
      if (( ${#left_id} < ${#right_id} )); then printf '%s\n' -1; return; fi
      if (( ${#left_id} > ${#right_id} )); then printf '%s\n' 1; return; fi
      [[ "$left_id" < "$right_id" ]] && printf '%s\n' -1 || printf '%s\n' 1
      return
    fi
    if [[ "$left_id" =~ ^[0-9]+$ ]]; then printf '%s\n' -1; return; fi
    if [[ "$right_id" =~ ^[0-9]+$ ]]; then printf '%s\n' 1; return; fi
    [[ "$left_id" < "$right_id" ]] && printf '%s\n' -1 || printf '%s\n' 1
    return
  done
  printf '%s\n' 0
}

ensure_forward_promotion() {
  local current="$1"
  local candidate="$2"
  [[ -z "$current" || "$current" == "$candidate" ]] && return
  local precedence
  precedence=$(semver_compare "$candidate" "$current")
  if [[ "$precedence" != "1" ]]; then
    printf "error: refusing to move the root version backward from %s to %s\n" "$current" "$candidate" >&2
    return 1
  fi
}

select_release_mode() {
  local current="$1"
  local candidate="$2"
  local was_promoted="$3"
  RELEASE_TAG_ONLY=false
  [[ -n "$current" && "$current" != "$candidate" ]] || return 0

  local precedence
  precedence=$(semver_compare "$candidate" "$current")
  if [[ "$precedence" == "1" ]]; then
    return 0
  fi
  if [[ "$was_promoted" == "true" ]]; then
    RELEASE_TAG_ONLY=true
    return 0
  fi
  ensure_forward_promotion "$current" "$candidate"
}

remote_file_exists() {
  local path="$1"
  local listing
  if ! listing=$(rclone lsf "$PUBLISH_REMOTE" --recursive --files-only --include "/$path" "${RCLONE_ARGS[@]}" 2>&1); then
    printf "error: failed to inspect remote object %s:\n%s\n" "$path" "$listing" >&2
    return 2
  fi
  local candidate
  while IFS= read -r candidate; do
    if [[ "$candidate" == "$path" ]]; then
      return 0
    fi
  done <<< "$listing"
  return 1
}

upload_remote_file() {
  local source="$1"
  local path="$2"
  run_step "Uploaded $path" "Failed to upload $path" \
    rclone copyto "$source" "$PUBLISH_REMOTE/$path" "${UPLOAD_ARGS[@]}" "${RCLONE_ARGS[@]}"
}

download_remote_file() {
  local path="$1"
  local destination="$2"
  mkdir -p "$(dirname "$destination")"
  run_step "Downloaded $path" "Failed to download $path" \
    rclone copyto "$PUBLISH_REMOTE/$path" "$destination" "${RCLONE_ARGS[@]}"
}

read_remote_version() {
  local status
  if remote_file_exists "version"; then
    :
  else
    status=$?
    [[ "$status" -eq "$REMOTE_ABSENT" ]] && return 1
    return "$status"
  fi
  local tmp
  tmp=$(mktemp)
  if ! rclone copyto "$PUBLISH_REMOTE/version" "$tmp" "${RCLONE_ARGS[@]}" >/dev/null 2>&1; then
    rm -f "$tmp"
    printf "error: failed to download remote version pointer\n" >&2
    return 2
  fi
  local value
  value=$(tr -d '\r\n' < "$tmp")
  rm -f "$tmp"
  validate_version "$value" || return 2
  printf '%s\n' "$value"
}

remote_tag_exists() {
  local output
  if ! output=$(env GIT_TERMINAL_PROMPT=0 git ls-remote --refs --tags origin "refs/tags/$VERSION" 2>&1); then
    printf "error: failed to inspect remote tag %s:\n%s\n" "$VERSION" "$output" >&2
    exit 1
  fi
  [[ -n "$output" ]]
}

verify_release_files() {
  local dir="$1"
  local version="$2"
  local file count
  for file in "${RELEASE_CHECKSUM_OBJECTS[@]}"; do
    count=$(awk -v file="$file" '$2 == file {count++} END {print count+0}' "$dir/checksums.txt")
    if [[ "$count" -ne 1 ]]; then
      printf "error: signed checksums contain %d entries for %s\n" "$count" "$file" >&2
      return 1
    fi
  done
  if ! (
    cd "$dir"
    sha256sum --check --strict checksums.txt >/dev/null
  ); then
    printf "error: release file checksum verification failed for %s\n" "$version" >&2
    return 1
  fi
  local actual_version
  actual_version=$(tr -d '\r\n' < "$dir/version")
  if [[ "$actual_version" != "$version" ]]; then
    printf "error: release prefix version is %q, want %q\n" "$actual_version" "$version" >&2
    return 1
  fi
}

# Returns 0 for a complete verified prefix, 1 for an absent/incomplete prefix,
# and 2 for a complete but invalid prefix.
verify_remote_release() {
  local version="$1"
  local destination="$2"
  local prefix="releases/$version"
  local file status
  for file in "${RELEASE_OBJECTS[@]}"; do
    if remote_file_exists "$prefix/$file"; then
      :
    else
      status=$?
      [[ "$status" -eq "$REMOTE_ABSENT" ]] && return 1
      return 2
    fi
  done

  rm -rf "$destination"
  mkdir -p "$destination"
  for file in "${RELEASE_OBJECTS[@]}"; do
    if ! rclone copyto "$PUBLISH_REMOTE/$prefix/$file" "$destination/$file" "${RCLONE_ARGS[@]}" >/dev/null 2>&1; then
      printf "error: failed to download %s/%s for verification\n" "$prefix" "$file" >&2
      return 2
    fi
  done
  if ! "$COSIGN_BIN" verify-blob \
      --bundle "$destination/checksums.txt.cosign.bundle" \
      --certificate-identity "$CERT_IDENTITY" \
      --certificate-oidc-issuer "$OIDC_ISSUER" \
      "$destination/checksums.txt" >/dev/null 2>&1; then
    printf "error: remote signature verification failed for %s/checksums.txt\n" "$prefix" >&2
    return 2
  fi
  verify_release_files "$destination" "$version" || return 2
  return 0
}

resolve_release_policy() {
  RELEASE_BUILD_REQUIRED=true
  RELEASE_TAG_ONLY=false
  local status target_cache="$OUT_DIR/remote-target" was_promoted=false

  if RELEASE_CURRENT_VERSION=$(read_remote_version); then
    printf "🟢 Current promoted release is %s\n" "$RELEASE_CURRENT_VERSION"
  else
    status=$?
    if [[ "$status" -eq "$REMOTE_ABSENT" ]]; then
      RELEASE_CURRENT_VERSION=""
      printf "🟢 No release is currently promoted\n"
    else
      exit "$status"
    fi
  fi

  if remote_tag_exists; then
    RELEASE_TARGET_TAG_EXISTS=true
  else
    RELEASE_TARGET_TAG_EXISTS=false
  fi

  if verify_remote_release "$VERSION" "$target_cache"; then
    if remote_file_exists ".state/promotions/$VERSION"; then
      was_promoted=true
    else
      status=$?
      [[ "$status" -eq "$REMOTE_ABSENT" ]] || exit "$status"
    fi
    select_release_mode "$RELEASE_CURRENT_VERSION" "$VERSION" "$was_promoted" || exit 1
    RELEASE_BUILD_REQUIRED=false
    mkdir -p "$VERSION_DIR"
    cp -f "$target_cache"/* "$VERSION_DIR/"
    printf "🟢 Reusing verified immutable release %s\n" "$VERSION"
    if $RELEASE_TAG_ONLY; then
      printf "🟢 Release %s was already promoted; only the final tag and retention steps will run\n" "$VERSION"
      return
    fi
  else
    status=$?
    if [[ "$status" -eq "$REMOTE_UNUSABLE" ]]; then
      printf "error: releases/%s is complete but invalid; refusing to replace an immutable prefix\n" "$VERSION" >&2
      exit 1
    fi
    if [[ "$RELEASE_CURRENT_VERSION" == "$VERSION" ]] || $RELEASE_TARGET_TAG_EXISTS; then
      printf "error: %s is promoted or tagged but its immutable prefix is incomplete\n" "$VERSION" >&2
      exit 1
    fi
    ensure_forward_promotion "$RELEASE_CURRENT_VERSION" "$VERSION" || exit 1
  fi

  if [[ -n "$RELEASE_CURRENT_VERSION" && "$RELEASE_CURRENT_VERSION" != "$VERSION" ]]; then
    if ! verify_remote_release "$RELEASE_CURRENT_VERSION" "$OUT_DIR/remote-current"; then
      printf "error: currently promoted release %s is incomplete or invalid\n" "$RELEASE_CURRENT_VERSION" >&2
      exit 1
    fi
  fi
}

upload_application_release() {
  local file
  for file in "${RELEASE_OBJECTS[@]}"; do
    upload_remote_file "$VERSION_DIR/$file" "releases/$VERSION/$file"
  done
  local verified="$OUT_DIR/verified-$VERSION"
  if ! verify_remote_release "$VERSION" "$verified"; then
    printf "error: uploaded release %s did not verify remotely\n" "$VERSION" >&2
    exit 1
  fi
  rm -rf "$VERSION_DIR"
  mkdir -p "$VERSION_DIR"
  cp -f "$verified"/* "$VERSION_DIR/"
  printf "🟢 Verified immutable release %s remotely\n" "$VERSION"
}

write_root_version_candidate() {
  cp -f "$VERSION_DIR/version" "$RELEASE_DIR/version"
}

verify_installer_pair() {
  local script="$1"
  local bundle="$2"
  local expected_sha="${3:-}"
  if ! "$COSIGN_BIN" verify-blob \
      --bundle "$bundle" \
      --certificate-identity "$CERT_IDENTITY" \
      --certificate-oidc-issuer "$OIDC_ISSUER" \
      "$script" >/dev/null 2>&1; then
    return 1
  fi
  if [[ -n "$expected_sha" ]]; then
    local actual_sha
    actual_sha=$(sha256sum "$script" | awk '{print $1}')
    [[ "$actual_sha" == "$expected_sha" ]] || return 1
  fi
}

installer_stage_prefix() {
  local name="$1"
  local sha="$2"
  printf '.staging/installers/%s/%s\n' "$name" "$sha"
}

remote_staged_installer_valid() {
  local name="$1"
  local sha="$2"
  local prefix
  prefix=$(installer_stage_prefix "$name" "$sha")
  local status
  if remote_file_exists "$prefix/$name"; then
    :
  else
    status=$?
    [[ "$status" -eq "$REMOTE_ABSENT" ]] && return 1
    return 2
  fi
  if remote_file_exists "$prefix/$name.cosign.bundle"; then
    :
  else
    status=$?
    [[ "$status" -eq "$REMOTE_ABSENT" ]] && return 1
    return 2
  fi
  local dir
  dir=$(mktemp -d)
  if ! rclone copyto "$PUBLISH_REMOTE/$prefix/$name" "$dir/$name" "${RCLONE_ARGS[@]}" >/dev/null 2>&1 ||
      ! rclone copyto "$PUBLISH_REMOTE/$prefix/$name.cosign.bundle" "$dir/$name.cosign.bundle" "${RCLONE_ARGS[@]}" >/dev/null 2>&1; then
    rm -rf "$dir"
    printf "error: failed to download staged %s pair\n" "$name" >&2
    return 2
  fi
  if verify_installer_pair "$dir/$name" "$dir/$name.cosign.bundle" "$sha"; then
    rm -rf "$dir"
    return 0
  fi
  rm -rf "$dir"
  return 1
}

# Returns 0 when publication is needed and 1 when the verified remote bytes
# already match. An invalid existing pair is recoverable only when this exact
# candidate has a complete verified staging transaction.
installer_needs_update() {
  local name="$1"
  local candidate="$RELEASE_DIR/$name"
  local candidate_sha
  candidate_sha=$(sha256sum "$candidate" | awk '{print $1}')
  local script_exists=false bundle_exists=false status

  if remote_file_exists "$name"; then
    script_exists=true
  else
    status=$?
    [[ "$status" -eq "$REMOTE_ABSENT" ]] || exit "$status"
  fi
  if remote_file_exists "$name.cosign.bundle"; then
    bundle_exists=true
  else
    status=$?
    [[ "$status" -eq "$REMOTE_ABSENT" ]] || exit "$status"
  fi

  if $script_exists && $bundle_exists; then
    local dir
    dir=$(mktemp -d)
    if ! rclone copyto "$PUBLISH_REMOTE/$name" "$dir/$name" "${RCLONE_ARGS[@]}" >/dev/null 2>&1 ||
        ! rclone copyto "$PUBLISH_REMOTE/$name.cosign.bundle" "$dir/$name.cosign.bundle" "${RCLONE_ARGS[@]}" >/dev/null 2>&1; then
      rm -rf "$dir"
      printf "error: failed to download existing remote %s pair\n" "$name" >&2
      exit 1
    fi
    if verify_installer_pair "$dir/$name" "$dir/$name.cosign.bundle"; then
      local remote_sha
      remote_sha=$(sha256sum "$dir/$name" | awk '{print $1}')
      rm -rf "$dir"
      if [[ "$remote_sha" == "$candidate_sha" ]]; then
        printf "🟢 %s is unchanged; keeping its existing signature\n" "$name"
        return 1
      fi
      return 0
    fi
    rm -rf "$dir"
  elif ! $script_exists && ! $bundle_exists; then
    return 0
  fi

  if remote_staged_installer_valid "$name" "$candidate_sha"; then
    printf "🟡 Resuming interrupted publication of %s\n" "$name"
    return 0
  else
    status=$?
    [[ "$status" -eq "$REMOTE_ABSENT" ]] || exit "$status"
  fi
  printf "error: existing remote %s pair is missing or invalid; explicit recovery is required\n" "$name" >&2
  exit 1
}

plan_installer_publication() {
  RELEASE_INSTALLERS_TO_PUBLISH=()
  local name
  for name in install.sh install.ps1; do
    if installer_needs_update "$name"; then
      RELEASE_INSTALLERS_TO_PUBLISH+=("$name")
    fi
  done
}

make_installer_test_snapshot() {
  local version="$1"
  local source_dir="$2"
  local destination="$3"
  rm -rf "$destination"
  mkdir -p "$destination/releases/$version"
  cp -f "$RELEASE_DIR/install.sh" "$RELEASE_DIR/install.ps1" "$destination/"
  local bundle
  for bundle in install.sh.cosign.bundle install.ps1.cosign.bundle; do
    [[ ! -f "$RELEASE_DIR/$bundle" ]] || cp -f "$RELEASE_DIR/$bundle" "$destination/"
  done
  cp -f "$source_dir"/* "$destination/releases/$version/"
  printf '%s\n' "$version" > "$destination/version"
}

test_changed_installers() {
  [[ "${#RELEASE_INSTALLERS_TO_PUBLISH[@]}" -gt 0 ]] || return

  if [[ -n "$RELEASE_CURRENT_VERSION" && "$RELEASE_CURRENT_VERSION" != "$VERSION" ]]; then
    local current_snapshot="$OUT_DIR/lifecycle-e2e-current"
    make_installer_test_snapshot "$RELEASE_CURRENT_VERSION" "$OUT_DIR/remote-current" "$current_snapshot"
    run_step "Installer works with current release $RELEASE_CURRENT_VERSION" "Installer failed against current release $RELEASE_CURRENT_VERSION" \
      bash scripts/test-lifecycle-e2e.sh --release-dir "$current_snapshot" --distros "debian" --no-fakes
  fi

  local staged_snapshot="$OUT_DIR/lifecycle-e2e-staged"
  make_installer_test_snapshot "$VERSION" "$VERSION_DIR" "$staged_snapshot"
  run_step "Installer works with staged release $VERSION" "Installer failed against staged release $VERSION" \
    bash scripts/test-lifecycle-e2e.sh --release-dir "$staged_snapshot" --distros "debian" --no-fakes
}

publish_installer() {
  local name="$1"
  local candidate="$RELEASE_DIR/$name"
  local candidate_sha
  candidate_sha=$(sha256sum "$candidate" | awk '{print $1}')
  local prefix
  prefix=$(installer_stage_prefix "$name" "$candidate_sha")

  local staged_status=0
  if remote_staged_installer_valid "$name" "$candidate_sha"; then
    :
  else
    staged_status=$?
    [[ "$staged_status" -eq "$REMOTE_ABSENT" ]] || exit "$staged_status"
    local bundle="$RELEASE_DIR/$name.cosign.bundle"
    run_step "Signed $name" "Failed to sign $name" \
      "$COSIGN_BIN" sign-blob --yes --bundle "$bundle" "$candidate"
    upload_remote_file "$candidate" "$prefix/$name"
    upload_remote_file "$bundle" "$prefix/$name.cosign.bundle"
    if remote_staged_installer_valid "$name" "$candidate_sha"; then
      :
    else
      staged_status=$?
      [[ "$staged_status" -eq "$REMOTE_ABSENT" ]] || exit "$staged_status"
      printf "error: staged %s pair failed remote verification\n" "$name" >&2
      exit 1
    fi
  fi

  local staged
  staged=$(mktemp -d)
  if ! rclone copyto "$PUBLISH_REMOTE/$prefix/$name" "$staged/$name" "${RCLONE_ARGS[@]}" >/dev/null 2>&1 ||
      ! rclone copyto "$PUBLISH_REMOTE/$prefix/$name.cosign.bundle" "$staged/$name.cosign.bundle" "${RCLONE_ARGS[@]}" >/dev/null 2>&1; then
    rm -rf "$staged"
    printf "error: failed to download verified staging transaction for %s\n" "$name" >&2
    exit 1
  fi

  # The staging transaction makes a retry safe if one of these two root writes
  # fails. Until both finish, verification fails closed for new installs.
  upload_remote_file "$staged/$name.cosign.bundle" "$name.cosign.bundle"
  upload_remote_file "$staged/$name" "$name"

  local verified
  verified=$(mktemp -d)
  if ! rclone copyto "$PUBLISH_REMOTE/$name" "$verified/$name" "${RCLONE_ARGS[@]}" >/dev/null 2>&1 ||
      ! rclone copyto "$PUBLISH_REMOTE/$name.cosign.bundle" "$verified/$name.cosign.bundle" "${RCLONE_ARGS[@]}" >/dev/null 2>&1; then
    rm -rf "$staged" "$verified"
    printf "error: failed to download published %s pair for verification\n" "$name" >&2
    exit 1
  fi
  if ! verify_installer_pair "$verified/$name" "$verified/$name.cosign.bundle" "$candidate_sha"; then
    rm -rf "$staged" "$verified"
    printf "error: published %s pair failed remote verification\n" "$name" >&2
    exit 1
  fi
  rm -rf "$staged" "$verified"
  run_step "Removed completed $name staging transaction" "Failed to remove completed $name staging transaction" \
    rclone purge "$PUBLISH_REMOTE/$prefix" "${RCLONE_ARGS[@]}"
  printf "🟢 Published and verified changed installer %s\n" "$name"
}

publish_installers() {
  local name
  for name in "${RELEASE_INSTALLERS_TO_PUBLISH[@]}"; do
    publish_installer "$name"
  done
}

promote_release() {
  local current status
  if current=$(read_remote_version); then
    if [[ "$current" == "$VERSION" ]]; then
      RELEASE_CURRENT_VERSION="$VERSION"
      printf "🟢 Release %s is already promoted\n" "$VERSION"
      return
    fi
  else
    status=$?
    [[ "$status" -eq "$REMOTE_ABSENT" ]] || exit "$status"
  fi
  ensure_forward_promotion "${current:-}" "$VERSION" || exit 1
  upload_remote_file "$VERSION_DIR/version" "version"
  current=$(read_remote_version) || {
    printf "error: failed to read back promoted version\n" >&2
    exit 1
  }
  [[ "$current" == "$VERSION" ]] || {
    printf "error: promoted version is %q, want %q\n" "$current" "$VERSION" >&2
    exit 1
  }
  RELEASE_CURRENT_VERSION="$VERSION"
  printf "🟢 Promoted release %s\n" "$VERSION"
}

ensure_promotion_marker() {
  local path=".state/promotions/$VERSION"
  local status
  if remote_file_exists "$path"; then
    local existing existing_timestamp existing_commit
    existing=$(mktemp)
    if ! rclone copyto "$PUBLISH_REMOTE/$path" "$existing" "${RCLONE_ARGS[@]}" >/dev/null 2>&1; then
      rm -f "$existing"
      printf "error: invalid promotion state for %s\n" "$VERSION" >&2
      exit 1
    fi
    existing_timestamp=$(tr -d '\r' < "$existing" | sed -n '1p')
    existing_commit=$(tr -d '\r' < "$existing" | sed -n '2p')
    if ! date -u -d "$existing_timestamp" +%s%N >/dev/null 2>&1 ||
        [[ ! "$existing_commit" =~ ^[0-9a-f]{40,64}$ ]]; then
      rm -f "$existing"
      printf "error: promotion state for %s is invalid\n" "$VERSION" >&2
      exit 1
    fi
    rm -f "$existing"
    return
  else
    status=$?
    [[ "$status" -eq "$REMOTE_ABSENT" ]] || exit "$status"
  fi
  local marker
  marker=$(mktemp)
  {
    date -u +%Y-%m-%dT%H:%M:%S.%NZ
    git rev-parse HEAD
  } > "$marker"
  upload_remote_file "$marker" "$path"
  rm -f "$marker"
  ensure_promotion_marker
}

promotion_commit() {
  local marker timestamp commit
  marker=$(mktemp)
  if ! rclone copyto "$PUBLISH_REMOTE/.state/promotions/$VERSION" "$marker" "${RCLONE_ARGS[@]}" >/dev/null 2>&1; then
    rm -f "$marker"
    printf "error: failed to read promotion state for %s\n" "$VERSION" >&2
    return 1
  fi
  timestamp=$(tr -d '\r' < "$marker" | sed -n '1p')
  commit=$(tr -d '\r' < "$marker" | sed -n '2p')
  rm -f "$marker"
  if ! date -u -d "$timestamp" +%s%N >/dev/null 2>&1 ||
      [[ ! "$commit" =~ ^[0-9a-f]{40,64}$ ]]; then
    printf "error: invalid promotion state for %s\n" "$VERSION" >&2
    return 1
  fi
  printf '%s\n' "$commit"
}

remote_tag_commit() {
  local output direct="" peeled=""
  if ! output=$(env GIT_TERMINAL_PROMPT=0 git ls-remote --tags origin \
      "refs/tags/$VERSION" "refs/tags/$VERSION^{}" 2>&1); then
    printf "error: failed to inspect remote tag %s:\n%s\n" "$VERSION" "$output" >&2
    return 2
  fi
  while read -r commit ref; do
    [[ -n "${commit:-}" ]] || continue
    if [[ "$ref" == "refs/tags/$VERSION^{}" ]]; then
      peeled="$commit"
    elif [[ "$ref" == "refs/tags/$VERSION" ]]; then
      direct="$commit"
    fi
  done <<< "$output"
  [[ -n "$peeled" || -n "$direct" ]] || return 1
  printf '%s\n' "${peeled:-$direct}"
}

tag_release() {
  local expected_commit remote_commit status
  expected_commit=$(promotion_commit) || exit 1
  if remote_commit=$(remote_tag_commit); then
    if [[ "$remote_commit" != "$expected_commit" ]]; then
      printf "error: remote tag %s points to %s, want promoted release commit %s\n" \
        "$VERSION" "$remote_commit" "$expected_commit" >&2
      exit 1
    fi
    printf "🟢 Tag %s is already on the promoted release commit\n" "$VERSION"
    return
  else
    status=$?
    [[ "$status" -eq "$REMOTE_ABSENT" ]] || exit "$status"
  fi
  if git show-ref --verify --quiet "refs/tags/$VERSION"; then
    local tagged_head
    tagged_head=$(git rev-parse "$VERSION^{commit}")
    if [[ "$tagged_head" != "$expected_commit" ]]; then
      printf "error: local tag %s points to %s, want promoted release commit %s\n" \
        "$VERSION" "$tagged_head" "$expected_commit" >&2
      exit 1
    fi
  else
    run_step "Tagged $VERSION" "Failed to tag $VERSION" git tag "$VERSION" "$expected_commit"
  fi
  run_step "Pushed $VERSION" "Failed to push $VERSION" env GIT_TERMINAL_PROMPT=0 git push origin "$VERSION"
  remote_commit=$(remote_tag_commit) || {
    printf "error: pushed tag %s could not be read back\n" "$VERSION" >&2
    exit 1
  }
  [[ "$remote_commit" == "$expected_commit" ]] || {
    printf "error: pushed tag %s does not point to promoted release commit %s\n" "$VERSION" "$expected_commit" >&2
    exit 1
  }
}

cleanup_old_releases() {
  local listing
  if ! listing=$(rclone lsf "$PUBLISH_REMOTE/.state/promotions" --recursive --files-only "${RCLONE_ARGS[@]}" 2>&1); then
    printf "error: failed to list promoted releases:\n%s\n" "$listing" >&2
    exit 1
  fi

  local records=() path version marker timestamp_ns marker_commit
  while IFS= read -r path; do
    [[ -n "$path" && "$path" != */* ]] || continue
    version="$path"
    validate_version "$version" || {
      printf "error: invalid promoted release path %q\n" "$path" >&2
      exit 1
    }
    marker=$(mktemp)
    if ! rclone copyto "$PUBLISH_REMOTE/.state/promotions/$path" "$marker" "${RCLONE_ARGS[@]}" >/dev/null 2>&1; then
      rm -f "$marker"
      printf "error: failed to read promotion state for %s\n" "$version" >&2
      exit 1
    fi
    marker_commit=$(tr -d '\r' < "$marker" | sed -n '2p')
    if ! timestamp_ns=$(date -u -d "$(tr -d '\r' < "$marker" | sed -n '1p')" +%s%N 2>/dev/null) ||
        [[ ! "$marker_commit" =~ ^[0-9a-f]{40,64}$ ]]; then
      rm -f "$marker"
      printf "error: invalid promotion timestamp for %s\n" "$version" >&2
      exit 1
    fi
    rm -f "$marker"
    records+=("$timestamp_ns $version")
  done <<< "$listing"

  mapfile -t records < <(printf '%s\n' "${records[@]}" | awk 'NF == 2' | sort -k1,1nr -k2,2r)
  [[ "${#records[@]}" -gt 2 ]] || {
    printf "🟢 Retention keeps all %d promoted release(s)\n" "${#records[@]}"
    return
  }

  local now_ns age_floor_ns=$((24 * 60 * 60 * 1000000000))
  now_ns=$(date -u +%s%N)
  local i promoted_ns release_status
  for ((i=0; i<2; i++)); do
    read -r promoted_ns version <<< "${records[$i]}"
    if remote_file_exists "releases/$version/version"; then
      :
    else
      release_status=$?
      printf "error: protected promotion state for %s has no release prefix (remote status %d)\n" "$version" "$release_status" >&2
      exit 1
    fi
  done

  for ((i=2; i<${#records[@]}; i++)); do
    read -r promoted_ns version <<< "${records[$i]}"
    if remote_file_exists "releases/$version/version"; then
      :
    else
      release_status=$?
      if [[ "$release_status" -eq "$REMOTE_ABSENT" ]]; then
        run_step "Finished cleanup state for $version" "Failed to finish cleanup state for $version" \
          rclone deletefile "$PUBLISH_REMOTE/.state/promotions/$version" "${RCLONE_ARGS[@]}"
        continue
      fi
      printf "error: could not inspect release prefix for %s (remote status %d)\n" "$version" "$release_status" >&2
      exit 1
    fi
    if (( now_ns - promoted_ns < age_floor_ns )); then
      printf "🟢 Retaining %s until its 24-hour age floor passes\n" "$version"
      continue
    fi
    run_step "Deleted expired release $version" "Failed to delete expired release $version" \
      rclone purge "$PUBLISH_REMOTE/releases/$version" "${RCLONE_ARGS[@]}"
    run_step "Deleted promotion state for $version" "Failed to delete promotion state for $version" \
      rclone deletefile "$PUBLISH_REMOTE/.state/promotions/$version" "${RCLONE_ARGS[@]}"
  done
}
