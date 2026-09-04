#!/usr/bin/env bash

# Fast local tests for the release state machine. Uses rclone's local backend
# and a tiny checksum-backed cosign stand-in; no network, OIDC, or bucket
# credentials are required.

set -euo pipefail
cd "$(dirname "$0")/.."

# shellcheck source=build.sh
source scripts/build.sh

fail() {
  printf 'release test failed: %s\n' "$*" >&2
  exit 1
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

if TAILWIND_VERSION=v9.9.9 bash -c 'source scripts/build.sh; validate_pins' >/dev/null 2>&1; then
  fail "tool version override was accepted without checksum overrides"
fi
printf 'verified tool\n' > "$tmp/tool"
tool_sha=$(sha256sum "$tmp/tool" | awk '{print $1}')
verify_sha256 "$tmp/tool" "$tool_sha" "test tool"
printf 'tampered\n' >> "$tmp/tool"
if verify_sha256 "$tmp/tool" "$tool_sha" "test tool" >/dev/null 2>&1; then
  fail "tampered cached tool passed checksum verification"
fi
[[ "$(semver_compare v2.0.0 v1.9.9)" == "1" ]] || fail "stable SemVer ordering failed"
[[ "$(semver_compare v1.0.0 v1.0.0-rc.1)" == "1" ]] || fail "prerelease SemVer ordering failed"
[[ "$(semver_compare v1.0.0-rc.2 v1.0.0-rc.10)" == "-1" ]] || fail "numeric prerelease ordering failed"
[[ "$(semver_compare v1.0.0 v9223372036854775808.0.0)" == "-1" ]] || fail "large SemVer ordering overflowed"
select_release_mode v2.0.0 v1.0.0 true
$RELEASE_TAG_ONLY || fail "previously promoted release did not enter tag-only retry mode"
if (select_release_mode v2.0.0 v1.0.0 false >/dev/null 2>&1); then
  fail "unpromoted release was allowed to move the root pointer backward"
fi
if validate_version v1.0.0-01 >/dev/null 2>&1; then
  fail "invalid numeric prerelease identifier was accepted"
fi
if ensure_forward_promotion v3.0.0 v2.0.0 >/dev/null 2>&1; then
  fail "an older workflow was allowed to roll back the promoted version"
fi

# A CHANGELOG.md carrying no release heading is a misconfiguration, not an
# empty release: publishing nothing while reporting success would hide it.
changelog_dir="$tmp/changelog"
mkdir -p "$changelog_dir"
printf '# Changelog\n\n<!--\n// ## [v0.1.0] - YYYY-MM-DD\n-->\n' > "$changelog_dir/CHANGELOG.md"
if changelog_output=$(cd "$changelog_dir" && MODE=ci resolve_version 2>&1); then
  fail "a CHANGELOG.md without a release heading was accepted"
fi
[[ "$changelog_output" == *'## [vX.Y.Z]'* ]] ||
  fail "missing release heading did not explain the expected format"

printf '# Changelog\n\n## [v1.4.2] - 2026-01-02\n\n### Added\n\n- Thing.\n' \
  > "$changelog_dir/CHANGELOG.md"
changelog_version=$(cd "$changelog_dir" && MODE=ci resolve_version && printf '%s' "$VERSION")
[[ "$changelog_version" == "v1.4.2" ]] ||
  fail "release heading was not parsed into VERSION (got '$changelog_version')"

mkdir -p "$tmp/bin"
cat > "$tmp/bin/cosign" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
mode=$1
shift
bundle=""
file=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --bundle) bundle=$2; shift 2 ;;
    --certificate-identity|--certificate-oidc-issuer) shift 2 ;;
    --yes) shift ;;
    *) file=$1; shift ;;
  esac
done
case "$mode" in
  sign-blob) sha256sum "$file" | awk '{print $1}' > "$bundle" ;;
  verify-blob)
    [[ "$(tr -d '\r\n' < "$bundle")" == "$(sha256sum "$file" | awk '{print $1}')" ]]
    ;;
  *) exit 2 ;;
esac
EOF
chmod +x "$tmp/bin/cosign"
PATH="$tmp/bin:$PATH"

PUBLISH_REMOTE="$tmp/remote"
RCLONE_ARGS=()
UPLOAD_ARGS=(--ignore-times)
CERT_IDENTITY="test-identity"
OIDC_ISSUER="test-issuer"
RELEASE_DIR="$tmp/staging"
VERSION="v1.2.3"
VERSION_DIR="$RELEASE_DIR/releases/$VERSION"
mkdir -p "$PUBLISH_REMOTE" "$VERSION_DIR"

printf 'old\n' > "$PUBLISH_REMOTE/same-size"
printf 'new\n' > "$tmp/same-size"
touch -r "$PUBLISH_REMOTE/same-size" "$tmp/same-size"
upload_remote_file "$tmp/same-size" "same-size"
[[ "$(tr -d '\r\n' < "$PUBLISH_REMOTE/same-size")" == "new" ]] ||
  fail "publication write skipped different same-size, same-mtime bytes"

printf 'partial\n' > "$PUBLISH_REMOTE/releases-partial"
for file in linux-amd64.gz linux-arm64.gz windows-amd64.exe.gz windows-arm64.exe.gz; do
  printf '%s\n' "$file" > "$VERSION_DIR/$file"
done
printf '%s\n' "$VERSION" > "$VERSION_DIR/version"
(
  cd "$VERSION_DIR"
  sha256sum linux-*.gz windows-*.gz version > checksums.txt
)
sign_application_release

# A partial prefix is safe to resume because it has never been promoted.
mkdir -p "$PUBLISH_REMOTE/releases/$VERSION"
printf 'interrupted upload\n' > "$PUBLISH_REMOTE/releases/$VERSION/linux-amd64.gz"
upload_application_release
verify_remote_release "$VERSION" "$tmp/verified" ||
  fail "uploaded immutable prefix did not verify"

promote_release
[[ "$(read_remote_version)" == "$VERSION" ]] || fail "root version was not promoted"
promote_release
ensure_promotion_marker
marker_before=$(rclone cat "$PUBLISH_REMOTE/.state/promotions/$VERSION")
ensure_promotion_marker
marker_after=$(rclone cat "$PUBLISH_REMOTE/.state/promotions/$VERSION")
[[ "$marker_before" == "$marker_after" ]] || fail "promotion marker was replaced"

mkdir -p "$RELEASE_DIR"
printf '# installer one\n' > "$RELEASE_DIR/install.sh"
installer_needs_update install.sh || fail "missing installer was considered current"
publish_installer install.sh
if installer_needs_update install.sh; then
  fail "equal installer bytes requested a new signature"
fi

printf '# installer two\n' > "$RELEASE_DIR/install.sh"
installer_needs_update install.sh || fail "changed installer was not detected"
publish_installer install.sh

# Simulate interruption after staging but before both root objects were
# replaced. The verified transaction permits an automatic, exact-byte retry.
printf '# installer three\n' > "$RELEASE_DIR/install.sh"
installer_sha=$(sha256sum "$RELEASE_DIR/install.sh" | awk '{print $1}')
stage=$(installer_stage_prefix install.sh "$installer_sha")
cosign sign-blob --yes --bundle "$tmp/install.sh.cosign.bundle" "$RELEASE_DIR/install.sh"
upload_remote_file "$RELEASE_DIR/install.sh" "$stage/install.sh"
upload_remote_file "$tmp/install.sh.cosign.bundle" "$stage/install.sh.cosign.bundle"
rm -f "$PUBLISH_REMOTE/install.sh.cosign.bundle"
installer_needs_update install.sh || fail "verified staged recovery was rejected"
publish_installer install.sh

# Without a matching staging transaction, an invalid root pair must fail
# closed instead of being silently overwritten.
printf '# installer four\n' > "$RELEASE_DIR/install.sh"
rm -f "$PUBLISH_REMOTE/install.sh.cosign.bundle"
if (installer_needs_update install.sh >/dev/null 2>&1); then
  fail "invalid unstaged installer pair was accepted"
fi

test_retention() {
  local first_age_hours=$1
  rm -rf "$PUBLISH_REMOTE"
  mkdir -p "$PUBLISH_REMOTE/releases" "$PUBLISH_REMOTE/.state/promotions"
  local version age
  for version in v1.0.0 v1.1.0 v1.2.0; do
    case "$version" in
      v1.0.0) age=$first_age_hours ;;
      v1.1.0) age=2 ;;
      v1.2.0) age=1 ;;
    esac
    mkdir -p "$PUBLISH_REMOTE/releases/$version"
    printf '%s\n' "$version" > "$PUBLISH_REMOTE/releases/$version/version"
    {
      date -u -d "$age hours ago" +%Y-%m-%dT%H:%M:%S.%NZ
      printf '%040d\n' 0
    } > "$PUBLISH_REMOTE/.state/promotions/$version"
  done
  cleanup_old_releases
}

test_retention 25
[[ ! -e "$PUBLISH_REMOTE/releases/v1.0.0" ]] || fail "expired third-newest release was retained"
[[ -e "$PUBLISH_REMOTE/releases/v1.1.0" && -e "$PUBLISH_REMOTE/releases/v1.2.0" ]] ||
  fail "one of the newest two releases was deleted"

test_retention 23
[[ -e "$PUBLISH_REMOTE/releases/v1.0.0" ]] || fail "24-hour age floor was ignored"

test_retention 25
mkdir -p "$PUBLISH_REMOTE/.state/promotions"
{
  date -u -d "25 hours ago" +%Y-%m-%dT%H:%M:%S.%NZ
  printf '%040d\n' 0
} > "$PUBLISH_REMOTE/.state/promotions/v1.0.0"
cleanup_old_releases
[[ ! -e "$PUBLISH_REMOTE/.state/promotions/v1.0.0" ]] ||
  fail "interrupted retention cleanup did not remove stale promotion state"

rm -rf "$PUBLISH_REMOTE"
mkdir -p "$PUBLISH_REMOTE/releases/v1.0.0" "$PUBLISH_REMOTE/.state/promotions"
printf 'v1.0.0\n' > "$PUBLISH_REMOTE/releases/v1.0.0/version"
{
  date -u +%Y-%m-%dT%H:%M:%S.%NZ
  printf '%040d\n' 0
} > "$PUBLISH_REMOTE/.state/promotions/v1.0.0"
cleanup_old_releases
[[ -e "$PUBLISH_REMOTE/releases/v1.0.0" ]] || fail "sole first release was deleted"

# Tagging is last and idempotent: a rerun after promotion can retry only this
# operation without rebuilding artifacts.
mkdir -p "$tmp/git-origin"
git init --bare -q "$tmp/git-origin"
git init -q "$tmp/git-work"
(
  cd "$tmp/git-work"
  printf 'release\n' > file
  git add file
  git -c user.name=release-test -c user.email=release-test@example.invalid commit -qm initial
  git remote add origin "$tmp/git-origin"
  git push -q -u origin HEAD:main
  VERSION="v9.9.9"
  mkdir -p "$PUBLISH_REMOTE/.state/promotions"
  {
    date -u +%Y-%m-%dT%H:%M:%S.%NZ
    git rev-parse HEAD
  } > "$PUBLISH_REMOTE/.state/promotions/$VERSION"
  tag_release
  tag_release
  [[ -n "$(git ls-remote --refs --tags origin refs/tags/v9.9.9)" ]] ||
    fail "tag-only retry did not leave the remote tag"
  printf 'wrong tag target\n' >> file
  git add file
  git -c user.name=release-test -c user.email=release-test@example.invalid commit -qm second
  git tag -f v9.9.9 >/dev/null
  git push -q -f origin refs/tags/v9.9.9
  if (tag_release) >/dev/null 2>&1; then
    fail "existing remote tag on the wrong commit was accepted"
  fi
)

printf 'All release publication tests passed.\n'
