#!/usr/bin/env bash

# Lifecycle E2E harness: runs the rendered installer end-to-end inside
# Incus system containers for a set of distros, using a local fake release
# directory served over file:// (curl supports file URLs) and
# APP_SKIP_VERIFY=true so no cosign signatures are needed. The fake uses the
# production root-version plus immutable releases/<version>/ layout.
#
# What this covers: dependency checks, download/checksum/unzip paths, binary
# install + migration, PATH bootstrap, real user-systemd installation and
# lifecycle, the graceful non-systemd degrade, and a fake immutable-root
# (ostree) check. Managed cases verify systemd readiness, the app's service
# wrapper, worker IPC, HTTPS health, graceful shutdown, exclusive migration
# locking, replacement, restart, detached uninstall, and retained recovery
# state. Every distro finishes by uninstalling and checking the result.
# What it can't cover: a guest kernel distinct from the host, or cosign
# verification (needs signed artifacts; covered by installing a real release).
#
# Usage:
#   ./scripts/test-lifecycle-e2e.sh                       # full distro list
#   ./scripts/test-lifecycle-e2e.sh --distros "debian alpine"
#   ./scripts/test-lifecycle-e2e.sh --release-dir out/release-test
#   KEEP_FAILED=true ./scripts/test-lifecycle-e2e.sh      # retain failures
#
# Each invocation writes per-case output and failure diagnostics beneath
# out/lifecycle-e2e-logs/<run>/. KEEP_FAILED retains failed/incomplete instances
# (including the active instance if the harness is interrupted) and prints the
# associated Incus name and temporary harness path for later inspection.
# --- BEGIN template ---
# The default locally built run also cuts and exercises focused no-update,
# no-service, and headless-service installers once on Debian.
# --- END template ---
#
# Requires a locally initialized Incus daemon. The caller needs access through
# incus-admin (root-equivalent) or passwordless sudo. First run pulls
# system images. Incus 6.0 LTS supports older kernels used by WSL; current Incus
# releases can require a newer kernel.
#
# Distro images map onto the supported-distro families (see the distro notes in
# docs/content/docs/getting-started/operate.md):
#   debian  -> Debian, MX Linux, Raspberry Pi OS
#   ubuntu  -> Ubuntu, Mint, Pop!_OS, Zorin
#   fedora  -> Fedora (ostree variants: see immutable fake)
#   arch    -> Arch, CachyOS, Manjaro, SteamOS, Omarchy
#   alpine  -> Alpine (musl + busybox userland, no systemd)
#   opensuse-> openSUSE Tumbleweed (MicroOS/Aeon/Kalpa: see immutable fake)
#   void    -> Void (glibc, runit, no systemd)
#   rocky   -> Rocky/Alma/RHEL family
# NixOS remains outside the established matrix for now. Incus makes a future
# full-system case possible; this change keeps the existing distro scope.

set -euo pipefail

cd "$(dirname "$0")/.."

DEFAULT_DISTROS="debian ubuntu fedora arch alpine opensuse void rocky"
DISTROS="$DEFAULT_DISTROS"
RELEASE_DIR=""
SKIP_FAKES=false
# Enable this for the template's example `hash` command and worker-backed
# SQLite IPC. Set it to false if you removed that example; the
# installer and service lifecycle checks still run ofc.
TEST_EXAMPLE_HASH="false"
# The retained assignments are ordered fallbacks for finalized source shapes.
SCENARIO="no-service"
# --- BEGIN service ---
SCENARIO="headless"
# --- BEGIN service.https ---
SCENARIO="default"
# --- END service.https ---
# --- END service ---
# --- BEGIN template ---
FOCUSED_ROOT=""
# --- BEGIN service ---
TEST_EXAMPLE_HASH="true" # upstream true, clone default to false so they don't need to edit this manually.
# --- END service ---
# --- END template ---

while [[ $# -gt 0 ]]; do
  case "$1" in
    --distros) DISTROS="$2"; shift 2 ;;
    --release-dir) RELEASE_DIR="$2"; shift 2 ;;
    --no-fakes) SKIP_FAKES=true; shift ;;
    --scenario) SCENARIO="$2"; shift 2 ;;
    *) printf "error: unknown argument '%s'\n" "$1" >&2; exit 1 ;;
  esac
done

case "$SCENARIO" in
  default|no-update|no-service|headless) ;;
  *) printf "error: unknown installer scenario '%s'\n" "$SCENARIO" >&2; exit 1 ;;
esac

case "${KEEP_FAILED:-false}" in
  true) KEEP_FAILED=true ;;
  false|"") KEEP_FAILED=false ;;
  *) echo "error: KEEP_FAILED must be 'true' or 'false'" >&2; exit 1 ;;
esac

case "$TEST_EXAMPLE_HASH" in
  true|false) ;;
  *) echo "error: TEST_EXAMPLE_HASH must be 'true' or 'false'" >&2; exit 1 ;;
esac

command -v incus >/dev/null 2>&1 || { echo "error: incus is required" >&2; exit 1; }

INCUS=(incus)
direct_incus=false
if incus info >/dev/null 2>&1; then
  direct_incus=true
fi
if $direct_incus &&
    { [[ "$(id -u)" == "0" ]] || [[ " $(id -nG) " == *" incus-admin "* ]]; }; then
  :
elif command -v sudo >/dev/null 2>&1 && sudo -n incus info >/dev/null 2>&1; then
  # Prefer the admin socket over a reachable but restricted incus-user socket:
  # host-path disk devices require full local daemon access.
  INCUS=(sudo -n incus)
elif ! $direct_incus; then
  echo "error: cannot reach the local Incus daemon" >&2
  echo "initialize it with 'sudo incus admin init --minimal', then grant this user incus-admin access" >&2
  exit 1
else
  echo "error: Incus is reachable, but host-path test devices require incus-admin access" >&2
  exit 1
fi

# Source the build configuration and shared renderer without running its main.
requested_release_dir="$RELEASE_DIR"
# shellcheck source=build.sh
source scripts/build.sh
RELEASE_DIR="$requested_release_dir"

case "$(uname -m)" in
  x86_64|amd64) HOST_GOARCH="amd64" ;;
  aarch64|arm64) HOST_GOARCH="arm64" ;;
  *) echo "error: unsupported host arch $(uname -m)" >&2; exit 1 ;;
esac
BIN_ASSET="linux-$HOST_GOARCH"

# Fake release directory ------------------------------------------------------

if [[ -z "$RELEASE_DIR" ]]; then
  RELEASE_DIR="out/release-test"
  FAKE_VERSION="v0.0.0-dev"
  VERSION_PREFIX="$RELEASE_DIR/releases/$FAKE_VERSION"
  echo ">> Building production-mode installer test binary ..."
  if command -v ensure_embed_placeholders >/dev/null 2>&1; then
    ensure_embed_placeholders
  fi
  BUILD_KIND="prod"
  DEV_MODE=false
  # Detached maintenance validates with the identity baked into the binary.
  # Keep the unsigned fixture binary and its rendered installer on the same
  # test identity; the in-container cosign stub supplies only the signature
  # verification bypass needed by this local harness.
  CERT_IDENTITY="test-identity"
  OIDC_ISSUER="test-issuer"
  go_build
  rm -rf "$RELEASE_DIR" && mkdir -p "$VERSION_PREFIX"

  gzip -c -n "out/$BIN_ASSET" > "$VERSION_PREFIX/$BIN_ASSET.gz"
  printf '%s\n' "$FAKE_VERSION" > "$RELEASE_DIR/version"
  printf '%s\n' "$FAKE_VERSION" > "$VERSION_PREFIX/version"
  PIN_VERSION="v0.0.1-pin-test"
  PIN_PREFIX="$RELEASE_DIR/releases/$PIN_VERSION"
  mkdir -p "$PIN_PREFIX"
  cp -f "$VERSION_PREFIX/$BIN_ASSET.gz" "$PIN_PREFIX/$BIN_ASSET.gz"
  printf '%s\n' "$PIN_VERSION" > "$PIN_PREFIX/version"

  # Render install.sh through build.sh; the baked release URL is the
  # in-container mount point, so installs count as "official" and exercise the
  # release-url write. Cosign fields are placeholders: APP_SKIP_VERIFY skips
  # signature verification and the pinned-cosign bootstrap entirely.
  (
    RELEASE_URL="file:///release/"
    COSIGN_VERSION="v0.0.0"
    COSIGN_SHA_LINUX_AMD64="0000000000000000000000000000000000000000000000000000000000000000"
    COSIGN_SHA_LINUX_ARM64="$COSIGN_SHA_LINUX_AMD64"
    render_installer scripts/install.sh "$RELEASE_DIR/install.sh"
  )

  (cd "$VERSION_PREFIX" && sha256sum "$BIN_ASSET.gz" version > checksums.txt)
  (cd "$PIN_PREFIX" && sha256sum "$BIN_ASSET.gz" version > checksums.txt)

  # Build a distinct candidate and inject a harness-only failure immediately
  # after the installer crosses its point of no return. The canonical,
  # unmodified controller repairs the retained transitional state.
  if [[ "$SCENARIO" == "default" ]]; then
    FAULT_VERSION="v0.0.1-fault-test"
    FAULT_ROOT="$RELEASE_DIR/fault"
    FAULT_PREFIX="$FAULT_ROOT/releases/$FAULT_VERSION"
    mkdir -p "$FAULT_PREFIX"
    VERSION="$FAULT_VERSION"
    go_build
    gzip -c -n "out/$BIN_ASSET" > "$FAULT_PREFIX/$BIN_ASSET.gz"
    printf '%s\n' "$FAULT_VERSION" > "$FAULT_ROOT/version"
    printf '%s\n' "$FAULT_VERSION" > "$FAULT_PREFIX/version"
    (
      cd "$FAULT_PREFIX"
      sha256sum "$BIN_ASSET.gz" version > checksums.txt
    )
    (
      # The same controller is exercised later through the detached Go path
      # over this in-container HTTP origin. Earlier direct recovery calls use
      # APP_RELEASE_URL=file:// as an intentional mirror override.
      RELEASE_URL="http://127.0.0.1:18080/fault/"
      CERT_IDENTITY="test-identity"
      OIDC_ISSUER="test-issuer"
      COSIGN_VERSION="v0.0.0"
      COSIGN_SHA_LINUX_AMD64="0000000000000000000000000000000000000000000000000000000000000000"
      COSIGN_SHA_LINUX_ARM64="$COSIGN_SHA_LINUX_AMD64"
      render_installer scripts/install.sh "$FAULT_ROOT/install.sh"
    )
    # Detached update selection verifies both downloads before executing the
    # controller. The harness verifier intentionally accepts this placeholder.
    : > "$FAULT_ROOT/install.sh.cosign.bundle"
    awk '
      { print }
      /^migration_started=1$/ {
        print "fatalf '\''injected post-migration-boundary failure'\''"
      }
    ' "$FAULT_ROOT/install.sh" > "$FAULT_ROOT/install-fault.sh"
    chmod +x "$FAULT_ROOT/install-fault.sh" "$FAULT_ROOT/install.sh"
  fi
fi
RELEASE_DIR=$(readlink -f "$RELEASE_DIR")
echo ">> Using release dir: $RELEASE_DIR"

# In-container test script ----------------------------------------------------

HARNESS_DIR=$(mktemp -d)
cleanup_harness_setup() {
  [[ -z "${HARNESS_DIR:-}" ]] || rm -rf "$HARNESS_DIR"
}
trap cleanup_harness_setup EXIT
CONTAINER_TEST="$HARNESS_DIR/container-test.sh"
PIN_CURL="$HARNESS_DIR/pin-curl"
# Give every run an immutable mount source of its own. Besides making private
# supplied snapshots readable to overflow-mapped guest UIDs, this keeps a
# retained instance inspectable after a later run replaces out/release-test.
STAGED_RELEASE_DIR="$HARNESS_DIR/release"
mkdir -p "$STAGED_RELEASE_DIR"
cp -a "$RELEASE_DIR/." "$STAGED_RELEASE_DIR/"
chmod -R a+rX,go-w "$STAGED_RELEASE_DIR"
RELEASE_DIR="$STAGED_RELEASE_DIR"

RUN_TOKEN="$(date +%s)-$$-$RANDOM"
if [[ -n "${SPROUT_LIFECYCLE_E2E_LOG_RUN_DIR:-}" ]]; then
  # Focused child harnesses inherit the top-level run directory so their logs
  # survive removal of the temporary finalized source tree.
  RUN_LOG_DIR="$SPROUT_LIFECYCLE_E2E_LOG_RUN_DIR"
else
  RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)-$SCENARIO-$$-$RANDOM"
  RUN_LOG_DIR="$PWD/out/lifecycle-e2e-logs/$RUN_ID"
fi
mkdir -p "$RUN_LOG_DIR"
RUN_LOG_DIR=$(cd "$RUN_LOG_DIR" && pwd)
SPROUT_LIFECYCLE_E2E_LOG_RUN_DIR="$RUN_LOG_DIR"
export SPROUT_LIFECYCLE_E2E_LOG_RUN_DIR
echo ">> Lifecycle E2E logs: $RUN_LOG_DIR"

ACTIVE_INSTANCES=()
declare -A ACTIVE_INSTANCE_SET=()
RETAINED_INSTANCES=()

instance_owned_by_run() {
  local instance=$1
  local owner
  owner=$("${INCUS[@]}" config get "$instance" user.sprout-test-owner 2>/dev/null) || return 2
  [[ "$owner" == "$RUN_TOKEN" ]] || return 1
}

instance_is_present() {
  local instance=$1
  local names
  names=$("${INCUS[@]}" list "$instance" --format csv --columns n 2>/dev/null) || return 2
  while IFS= read -r name; do
    [[ "$name" == "$instance" ]] && return 0
  done <<< "$names"
  return 1
}

delete_owned_instance() {
  local instance=$1
  local owner_rc
  local presence_rc
  [[ "${ACTIVE_INSTANCE_SET[$instance]:-}" == "1" ]] || return 0
  if instance_is_present "$instance"; then
    :
  else
    presence_rc=$?
    if [[ "$presence_rc" == "1" ]]; then
      unset 'ACTIVE_INSTANCE_SET[$instance]'
      return 0
    fi
    return "$presence_rc"
  fi
  if instance_owned_by_run "$instance"; then
    :
  else
    owner_rc=$?
    if [[ "$owner_rc" == "1" ]]; then
      echo "warning: refusing to delete unowned Incus instance $instance" >&2
      unset 'ACTIVE_INSTANCE_SET[$instance]'
      return 2
    fi
    return "$owner_rc"
  fi
  "${INCUS[@]}" delete "$instance" --force || return
  unset 'ACTIVE_INSTANCE_SET[$instance]'
}

retain_owned_instance() {
  local instance=$1
  local owner_rc
  local presence_rc
  [[ "${ACTIVE_INSTANCE_SET[$instance]:-}" == "1" ]] || return 0
  if instance_is_present "$instance"; then
    :
  else
    presence_rc=$?
    if [[ "$presence_rc" == "1" ]]; then
      unset 'ACTIVE_INSTANCE_SET[$instance]'
      # Distinguish "nothing was created" from successful retention so callers
      # do not advertise a nonexistent debug instance.
      return 3
    fi
    echo "warning: could not determine whether Incus instance $instance exists" >&2
    return "$presence_rc"
  fi
  if instance_owned_by_run "$instance"; then
    :
  else
    owner_rc=$?
    if [[ "$owner_rc" == "1" ]]; then
      echo "warning: refusing to retain unowned Incus instance $instance" >&2
      unset 'ACTIVE_INSTANCE_SET[$instance]'
      return 2
    fi
    echo "warning: could not verify ownership of Incus instance $instance" >&2
    return "$owner_rc"
  fi
  unset 'ACTIVE_INSTANCE_SET[$instance]'
  RETAINED_INSTANCES+=("$instance")
  echo ">> Retained failed Incus instance: $instance" >&2
  echo ">> Retained harness files: $HARNESS_DIR" >&2
  echo ">> Failure logs: $RUN_LOG_DIR" >&2
}

cleanup() {
  local preserve_harness=false
  for instance in "${ACTIVE_INSTANCES[@]}"; do
    [[ "${ACTIVE_INSTANCE_SET[$instance]:-}" == "1" ]] || continue
    if $KEEP_FAILED; then
      retain_owned_instance "$instance" || :
    else
      delete_owned_instance "$instance" >/dev/null 2>&1 || :
    fi
  done
  if (( ${#RETAINED_INSTANCES[@]} > 0 )); then
    preserve_harness=true
  fi
  # If daemon inspection failed, preserve the mounted sources as a precaution:
  # an instance we could neither verify nor remove may still depend on them.
  for instance in "${ACTIVE_INSTANCES[@]}"; do
    if [[ "${ACTIVE_INSTANCE_SET[$instance]:-}" == "1" ]]; then
      preserve_harness=true
      echo "warning: preserving harness files for unresolved Incus instance $instance" >&2
    fi
  done
  if ! $preserve_harness; then
    rm -rf "$HARNESS_DIR"
  elif (( ${#RETAINED_INSTANCES[@]} > 0 )); then
    echo ">> Retained ${#RETAINED_INSTANCES[@]} failed Incus instance(s)." >&2
    echo ">> Remove their harness files after deleting the instances: $HARNESS_DIR" >&2
  else
    echo ">> Preserved harness files: $HARNESS_DIR" >&2
  fi
  # --- BEGIN template ---
  [[ -z "$FOCUSED_ROOT" ]] || rm -rf "$FOCUSED_ROOT"
  # --- END template ---
}
trap cleanup EXIT
cat > "$PIN_CURL" <<'EOF'
#!/bin/sh
set -eu
for arg in "$@"; do
  if [ "$arg" = "file:///release/version" ]; then
    count_file=/tmp/sprout-version-read-count
    count=0
    [ ! -f "$count_file" ] || count=$(cat "$count_file")
    count=$((count + 1))
    printf '%s\n' "$count" > "$count_file"
    if [ "$count" -eq 1 ]; then
      cat /release/version
    else
      printf 'v0.0.1-pin-test\n'
    fi
    exit 0
  fi
done
exec /usr/bin/curl "$@"
EOF
chmod +x "$PIN_CURL"
cat > "$CONTAINER_TEST" <<EOF
#!/bin/sh
# Runs as root inside an Incus system container: install prerequisites
# (\$SETUP_CMD), create a non-root user and real user-manager session when
# available, then run the installer and smoke-test the result.
set -eu

tester_uid=""
tester_env=""
managed_service=0
service_has_https=0
release_http_pid=""

run_tester() {
  su - tester -c "\${tester_env}\$1"
}

dump_service_diagnostics() {
  [ -n "\$tester_uid" ] || return 0
  [ "\${TEST_INIT:-none}" = "systemd" ] || return 0
  echo "----- systemd diagnostics -----" >&2
  systemctl status --no-pager "user@\$tester_uid.service" >&2 || :
  journalctl -u "user@\$tester_uid.service" -n 100 --no-pager >&2 || :
  if [ -n "\$tester_env" ]; then
    run_tester 'systemctl --user status --no-pager $APP_NAME.service' >&2 || :
    run_tester 'journalctl --user -u $APP_NAME.service -n 200 --no-pager' >&2 || :
  fi
}

on_test_exit() {
  rc=\$?
  if [ -n "\$release_http_pid" ]; then
    kill "\$release_http_pid" 2>/dev/null || :
    wait "\$release_http_pid" 2>/dev/null || :
  fi
  if [ "\$rc" -ne 0 ]; then
    dump_service_diagnostics
  fi
  exit "\$rc"
}
trap on_test_exit EXIT

eval "\$SETUP_CMD"
# -s /bin/sh: minimal images (e.g. Void) may not ship bash, which useradd defaults to
useradd -m -s /bin/sh tester 2>/dev/null || adduser -D -s /bin/sh tester
tester_uid=\$(id -u tester)
if [ "\${TEST_INIT:-none}" = "systemd" ]; then
  loginctl enable-linger tester
  systemctl start "user@\$tester_uid.service"
  manager_tries=0
  until systemctl is-active --quiet "user@\$tester_uid.service"; do
    manager_tries=\$((manager_tries + 1))
    if [ "\$manager_tries" -ge 200 ]; then
      echo "timed out waiting for tester user manager" >&2
      exit 1
    fi
    sleep 0.05
  done
  test -d "/run/user/\$tester_uid"
  test "\$(stat -c %u "/run/user/\$tester_uid")" = "\$tester_uid"
  test "\$(stat -c %a "/run/user/\$tester_uid")" = "700"
  bus_tries=0
  until [ -S "/run/user/\$tester_uid/bus" ]; do
    bus_tries=\$((bus_tries + 1))
    if [ "\$bus_tries" -ge 200 ]; then
      echo "timed out waiting for tester user bus" >&2
      exit 1
    fi
    sleep 0.05
  done
  tester_env="XDG_RUNTIME_DIR=/run/user/\$tester_uid DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/\$tester_uid/bus "
else
  rm -rf "/run/user/\$tester_uid"
fi

service_scenario=0
case "\${TEST_SCENARIO:-default}" in
  default|no-update)
    service_scenario=1
    service_has_https=1
    ;;
  headless)
    service_scenario=1
    ;;
esac
if [ "\${TEST_INIT:-none}" = "systemd" ] && [ "\$service_scenario" = "1" ]; then
  managed_service=1
fi

wait_for_instance() {
  wait_control=\$1
  wait_pid=\$2
  wait_label=\$3
  wait_tries=0
  while [ ! -f "\$wait_control/instances/\$wait_pid" ]; do
    if ! kill -0 "\$wait_pid" 2>/dev/null; then
      echo "\$wait_label process \$wait_pid exited before registering" >&2
      test ! -f /home/tester/unmanaged.log || cat /home/tester/unmanaged.log >&2
      exit 1
    fi
    wait_tries=\$((wait_tries + 1))
    if [ "\$wait_tries" -ge 200 ]; then
      echo "timed out waiting for \$wait_label process \$wait_pid" >&2
      exit 1
    fi
    sleep 0.05
  done
}

wait_for_stopped() {
  wait_pid=\$1
  wait_label=\$2
  wait_control=\$3
  wait_tries=0
  while [ -f "\$wait_control/instances/\$wait_pid" ]; do
    if ! kill -0 "\$wait_pid" 2>/dev/null; then
      echo "\$wait_label process \$wait_pid exited with a stale instance marker" >&2
      exit 1
    fi
    wait_tries=\$((wait_tries + 1))
    if [ "\$wait_tries" -ge 200 ]; then
      echo "timed out waiting for \$wait_label process \$wait_pid to stop" >&2
      exit 1
    fi
    sleep 0.05
  done
}

service_main_pid() {
  run_tester 'systemctl --user show --property=MainPID --value $APP_NAME.service'
}

service_invocation_id() {
  run_tester 'systemctl --user show --property=InvocationID --value $APP_NAME.service'
}

probe_managed_service() {
  run_tester 'systemctl --user is-enabled --quiet $APP_NAME.service'
  run_tester 'systemctl --user is-active --quiet $APP_NAME.service'

  active_state=\$(run_tester 'systemctl --user show --property=ActiveState --value $APP_NAME.service')
  sub_state=\$(run_tester 'systemctl --user show --property=SubState --value $APP_NAME.service')
  result=\$(run_tester 'systemctl --user show --property=Result --value $APP_NAME.service')
  restarts=\$(run_tester 'systemctl --user show --property=NRestarts --value $APP_NAME.service')
  test "\$active_state" = "active"
  test "\$sub_state" = "running"
  test "\$result" = "success"
  test "\$restarts" = "0"

  status_output=\$(run_tester '"\$HOME/.local/bin/$APP_NAME" service status')
  printf '%s\n' "\$status_output"

  if [ "\${TEST_EXAMPLE_HASH:-true}" = "true" ]; then
    hash_input="lifecycle-e2e-service-probe"
    expected_hash=\$(printf '%s' "\$hash_input" | sha256sum | awk '{print \$1}')
    hash_output=\$(run_tester '"\$HOME/.local/bin/$APP_NAME" hash lifecycle-e2e-service-probe')
    expected_output="hello from service, here is your SHA-256: \$expected_hash"
    if [ "\$hash_output" != "\$expected_output" ]; then
      echo "unexpected service hash output: \$hash_output" >&2
      exit 1
    fi
  fi

  if [ "\$service_has_https" = "1" ]; then
    service_port=\$(printf '%s\n' "\$build_vars" |
      sed -n 's/.*"serviceDefaultPort":\([0-9]*\).*/\1/p')
    test -n "\$service_port"
    health_tries=0
    while :; do
      if health_body=\$(run_tester "curl --insecure --fail --silent --show-error --max-time 2 https://127.0.0.1:\$service_port/healthz") &&
         [ "\$health_body" = "ok" ]; then
        break
      fi
      health_tries=\$((health_tries + 1))
      if [ "\$health_tries" -ge 100 ]; then
        echo "timed out waiting for HTTPS health endpoint" >&2
        exit 1
      fi
      sleep 0.05
    done
  fi
}

if [ "\${TEST_PORT:-0}" = "1" ]; then
  install -d -m 755 /tmp/occupied-bin
  cat > /tmp/occupied-bin/ss <<'PORT_EOF'
#!/bin/sh
printf '%s\n' 'LISTEN 0 128 127.0.0.1:8484'
PORT_EOF
  chmod 755 /tmp/occupied-bin/ss
  if run_tester 'PATH=/tmp/occupied-bin:\$PATH APP_RELEASE_URL=file:///release/ APP_SKIP_VERIFY=true sh /release/install.sh' >/tmp/occupied-port.out 2>&1; then
    echo "fresh installer accepted an occupied default port" >&2
    exit 1
  fi
  grep -q 'config set --ui-bind 127.0.0.1:<port>' /tmp/occupied-port.out
  test ! -e "/home/tester/.local/bin/$APP_NAME"
fi

installer_output=\$(run_tester 'APP_RELEASE_URL=file:///release/ APP_SKIP_VERIFY=true sh /release/install.sh' 2>&1)
printf '%s\n' "\$installer_output"
run_tester '"\$HOME/.local/bin/$APP_NAME" --version'
build_vars=\$(run_tester '"\$HOME/.local/bin/$APP_NAME" --build-vars')
printf '%s\n' "\$build_vars" | grep -q '"name":"$APP_NAME"'
case "\${TEST_SCENARIO:-default}" in
  no-update)
    test ! -e "/home/tester/.$APP_NAME/maintenance/release-url"
    printf '%s\n' "\$build_vars" | grep -q '"serviceEnabled":true'
    printf '%s\n' "\$build_vars" | grep -q '"serviceDefaultPort":8484'
    ;;
  no-service)
    printf '%s\n' "\$build_vars" | grep -q '"serviceEnabled":false'
    printf '%s\n' "\$build_vars" | grep -q '"serviceDefaultPort":0'
    test ! -e "/home/tester/.config/systemd/user/$APP_NAME.service"
    ;;
  headless)
    printf '%s\n' "\$build_vars" | grep -q '"serviceEnabled":true'
    printf '%s\n' "\$build_vars" | grep -q '"serviceDefaultPort":0'
    ;;
  default)
    printf '%s\n' "\$build_vars" | grep -q '"serviceEnabled":true'
    printf '%s\n' "\$build_vars" | grep -q '"serviceDefaultPort":8484'
    ;;
esac
if [ "\$managed_service" = "1" ]; then
  test -f "/home/tester/.config/systemd/user/$APP_NAME.service"
elif [ "\$service_scenario" = "1" ]; then
  test ! -e "/home/tester/.config/systemd/user/$APP_NAME.service"
  printf '%s\n' "\$installer_output" | grep -Eq 'systemd --user not available|systemctl --user is not functional'
fi

storage_dir="/home/tester/.$APP_NAME"
data_dir="\$storage_dir/data"
control_dir="\$storage_dir/control"
maintenance_dir="\$storage_dir/maintenance"
logs_dir="\$storage_dir/logs"
instances_dir="\$control_dir/instances"
state_file="\$control_dir/state.json"

for private_dir in "\$storage_dir" "\$data_dir" "\$control_dir" \
  "\$instances_dir" "\$maintenance_dir" "\$maintenance_dir/jobs" "\$logs_dir"; do
  test -d "\$private_dir"
  test "\$(stat -c %a "\$private_dir")" = "700"
done
for private_file in "\$state_file" "\$control_dir/operation.lock" \
  "\$control_dir/lifecycle.lock" "\$maintenance_dir/install.sh.cosign.bundle" \
  "\$logs_dir/maintenance.log"; do
  test -f "\$private_file"
  test "\$(stat -c %a "\$private_file")" = "600"
done
test -f "\$maintenance_dir/install.sh"
test "\$(stat -c %a "\$maintenance_dir/install.sh")" = "700"
if [ "\${TEST_SCENARIO:-default}" = "no-update" ]; then
  test ! -e "\$maintenance_dir/release-url"
else
  test -f "\$maintenance_dir/release-url"
  test "\$(stat -c %a "\$maintenance_dir/release-url")" = "600"
  grep -qx 'file:///release/' "\$maintenance_dir/release-url"
fi
grep -q '"phase":"ready"' "\$state_file"
installed_version=\$(tr -d '\r\n' < /release/version)
grep -q '"version":"'"\$installed_version"'"' "\$state_file"
grep -q '"targetVersion":""' "\$state_file"
grep -q '"nonce":""' "\$state_file"
state_changed_at=\$(sed -n 's/.*"changedAt":"\([^"]*\)".*/\1/p' "\$state_file")
test -n "\$state_changed_at"
state_epoch=\$(sed -n 's/.*"installationEpoch":"\([^"]*\)".*/\1/p' "\$state_file")
test "\${#state_epoch}" = "64"
test ! -e "\$storage_dir/run"
test ! -e "\$storage_dir/release-url"
run_tester '"\$HOME/.local/bin/$APP_NAME" config show >/dev/null'
test -f "\$control_dir/lifecycle.lock"

if [ "\$managed_service" = "1" ]; then
  managed_pid=\$(service_main_pid)
  case "\$managed_pid" in ''|0|*[!0-9]*) echo "invalid managed service PID: \$managed_pid" >&2; exit 1 ;; esac
  wait_for_instance "\$control_dir" "\$managed_pid" managed
  if run_tester '"\$HOME/.local/bin/$APP_NAME" service run' >/tmp/duplicate-service.out 2>&1; then
    echo "duplicate service process unexpectedly started" >&2
    exit 1
  fi
  grep -q 'service already running' /tmp/duplicate-service.out
  probe_managed_service

  old_managed_pid=\$managed_pid
  old_invocation_id=\$(service_invocation_id)
  test -n "\$old_invocation_id"
  run_tester '"\$HOME/.local/bin/$APP_NAME" service restart'
  managed_pid=\$(service_main_pid)
  new_invocation_id=\$(service_invocation_id)
  test -n "\$new_invocation_id"
  test "\$new_invocation_id" != "\$old_invocation_id"
  if [ "\$managed_pid" != "\$old_managed_pid" ]; then
    wait_for_stopped "\$old_managed_pid" wrapper-restarted "\$control_dir"
  fi
  wait_for_instance "\$control_dir" "\$managed_pid" wrapper-restarted
  probe_managed_service
fi

if [ "\${TEST_FAULT:-0}" = "1" ]; then
  if run_tester 'APP_RELEASE_URL=file:///release/fault/ APP_SKIP_VERIFY=true sh /release/fault/install-fault.sh' >/tmp/fault.out 2>&1; then
    echo "post-boundary installer fault unexpectedly succeeded" >&2
    exit 1
  fi
  grep -q 'injected post-migration-boundary failure' /tmp/fault.out
  grep -q '"phase":"updating"' "\$state_file"
  grep -q '"targetVersion":"v0.0.1-fault-test"' "\$state_file"
  test "\$(sed -n 's/.*"installationEpoch":"\([^"]*\)".*/\1/p' "\$state_file")" = "\$state_epoch"
  installed_sum=\$(sha256sum "/home/tester/.local/bin/$APP_NAME" | awk '{print \$1}')
  expected_fault_sum=\$(gzip -dc "/release/fault/releases/v0.0.1-fault-test/$BIN_ASSET.gz" | sha256sum | awk '{print \$1}')
  test "\$installed_sum" = "\$expected_fault_sum"
  if run_tester '"\$HOME/.local/bin/$APP_NAME" config show' >/tmp/pending-transition.out 2>&1; then
    echo "normal startup accepted a transitional lifecycle state" >&2
    exit 1
  fi
  grep -Eq 'not ready|rerun the installer' /tmp/pending-transition.out
  run_tester 'APP_RELEASE_URL=file:///release/fault/ APP_SKIP_VERIFY=true sh /release/fault/install.sh'
  grep -q '"phase":"ready"' "\$state_file"
  grep -q '"version":"v0.0.1-fault-test"' "\$state_file"
  test "\$(sed -n 's/.*"installationEpoch":"\([^"]*\)".*/\1/p' "\$state_file")" = "\$state_epoch"
  run_tester '"\$HOME/.local/bin/$APP_NAME" config show >/dev/null'
  if [ "\$managed_service" = "1" ]; then
    run_tester 'systemctl --user is-active --quiet $APP_NAME.service'
    managed_pid=\$(service_main_pid)
    wait_for_instance "\$control_dir" "\$managed_pid" recovered-managed
    probe_managed_service
  fi
fi

if [ "\$managed_service" = "1" ]; then
  managed_pid=\$(service_main_pid)
  wait_for_instance "\$control_dir" "\$managed_pid" managed
  old_invocation_id=\$(service_invocation_id)
  test -n "\$old_invocation_id"
  exec 9<"/home/tester/.local/bin/$APP_NAME"
  old_inode=\$(stat -Lc %i "/proc/\$\$/fd/9")

  run_tester 'systemd-run --user --quiet --collect --unit=$APP_NAME-install-test-unrelated sleep 300'
  unrelated_pid=\$(run_tester 'systemctl --user show --property=MainPID --value $APP_NAME-install-test-unrelated.service')
  case "\$unrelated_pid" in ''|0|*[!0-9]*) echo "invalid unrelated service PID: \$unrelated_pid" >&2; exit 1 ;; esac
  : > "\$instances_dir/\$unrelated_pid"
  : > "\$instances_dir/999999999"
  : > "\$instances_dir/not-a-pid"

  live_output=\$(run_tester 'APP_RELEASE_URL=file:///release/ APP_SKIP_VERIFY=true sh /release/install.sh' 2>&1)
  printf '%s\n' "\$live_output"
  printf '%s\n' "\$live_output" | grep -q 'Acquiring lifecycle lock'
  printf '%s\n' "\$live_output" | grep -q 'Restarting service'
  new_managed_pid=\$(service_main_pid)
  new_invocation_id=\$(service_invocation_id)
  test -n "\$new_invocation_id"
  test "\$new_invocation_id" != "\$old_invocation_id"
  if [ "\$new_managed_pid" != "\$managed_pid" ]; then
    wait_for_stopped "\$managed_pid" old-managed "\$control_dir"
  fi
  kill -0 "\$unrelated_pid"
  test ! -e "\$instances_dir/\$unrelated_pid"
  test ! -e "\$instances_dir/999999999"
  test ! -e "\$instances_dir/not-a-pid"
  run_tester 'systemctl --user stop $APP_NAME-install-test-unrelated.service'
  new_inode=\$(stat -Lc %i "/home/tester/.local/bin/$APP_NAME")
  test "\$new_inode" != "\$old_inode"
  exec 9<&-
  wait_for_instance "\$control_dir" "\$new_managed_pid" restarted-managed
  run_tester '"\$HOME/.local/bin/$APP_NAME" config show >/dev/null'
  probe_managed_service
fi

# Exercise the real Go admission path once on the deep Debian case. The fault
# fixture is a genuinely newer, canonical release by this point: its earlier
# injected controller was a separate file, and the live reinstall above put
# the app back on the root fixture version.
if [ "\${TEST_DEEP_LIFECYCLE:-0}" = "1" ] && [ "\${TEST_FAULT:-0}" = "1" ]; then
  detached_update_epoch=\$(sed -n 's/.*"installationEpoch":"\([^"]*\)".*/\1/p' "\$state_file")
  detached_update_version=\$(tr -d '\r\n' < /release/fault/version)
  current_update_version=\$(sed -n 's/.*"version":"\([^"]*\)".*/\1/p' "\$state_file")
  test "\$current_update_version" != "\$detached_update_version"

  detached_release_url=http://127.0.0.1:18080/fault/
  python3 -m http.server 18080 --bind 127.0.0.1 --directory /release >/tmp/$APP_NAME-release-http.log 2>&1 &
  release_http_pid=\$!
  release_http_tries=0
  until curl --fail --silent "\${detached_release_url}version" >/dev/null; do
    release_http_tries=\$((release_http_tries + 1))
    if [ "\$release_http_tries" -ge 100 ]; then
      echo "timed out waiting for lifecycle fixture HTTP server" >&2
      cat /tmp/$APP_NAME-release-http.log >&2 || :
      exit 1
    fi
    sleep 0.05
  done

  # APP_SKIP_VERIFY reaches the detached controller, while this fake cosign
  # admits the unsigned remote installer fixture in the launcher's independent
  # verification step.
  run_tester 'printf "#!/bin/sh\nexit 0\n" >"\$HOME/.local/bin/cosign" && chmod 700 "\$HOME/.local/bin/cosign"'
  run_tester "printf '%s\\n' '\$detached_release_url' >'\$maintenance_dir/release-url'"
  detached_update_output=\$(run_tester 'APP_SKIP_VERIFY=true "\$HOME/.local/bin/$APP_NAME" update --yes' 2>&1)
  printf '%s\n' "\$detached_update_output"
  printf '%s\n' "\$detached_update_output" | grep -q 'Update accepted'
  printf '%s\n' "\$detached_update_output" | grep -qF "\$logs_dir/maintenance.log"

  detached_update_tries=0
  until grep -q '"phase":"ready"' "\$state_file" &&
        grep -q '"version":"'"\$detached_update_version"'"' "\$state_file"; do
    detached_update_tries=\$((detached_update_tries + 1))
    if [ "\$detached_update_tries" -ge 800 ]; then
      echo "timed out waiting for detached update to reach ready" >&2
      cat "\$state_file" >&2 || :
      cat "\$logs_dir/maintenance.log" >&2 || :
      exit 1
    fi
    sleep 0.05
  done

  detached_job_tries=0
  while find "\$maintenance_dir/jobs" -mindepth 1 -print -quit | grep -q .; do
    detached_job_tries=\$((detached_job_tries + 1))
    if [ "\$detached_job_tries" -ge 200 ]; then
      echo "detached update job directory was not cleaned" >&2
      exit 1
    fi
    sleep 0.05
  done
  grep -q 'maintenance update job started' "\$logs_dir/maintenance.log"
  grep -q 'maintenance update job finished (exit 0)' "\$logs_dir/maintenance.log"
  test "\$(sed -n 's/.*"installationEpoch":"\([^"]*\)".*/\1/p' "\$state_file")" = "\$detached_update_epoch"
  grep -q '"targetVersion":""' "\$state_file"
  grep -q '"nonce":""' "\$state_file"
  updated_build_vars=\$(run_tester '"\$HOME/.local/bin/$APP_NAME" --build-vars')
  printf '%s\n' "\$updated_build_vars" | grep -q '"version":"'"\$detached_update_version"'"'
  grep -qx "\$detached_release_url" "\$maintenance_dir/release-url"

  detached_managed_pid=\$(service_main_pid)
  case "\$detached_managed_pid" in ''|0|*[!0-9]*) echo "invalid detached-update service PID" >&2; exit 1 ;; esac
  wait_for_instance "\$control_dir" "\$detached_managed_pid" detached-updated-managed
  probe_managed_service
  kill "\$release_http_pid"
  wait "\$release_http_pid" 2>/dev/null || :
  release_http_pid=""
fi

if [ "\${TEST_PIN:-0}" = "1" ] && [ -d /release/releases/v0.0.1-pin-test ]; then
  install -d -m 755 /tmp/pin-bin
  install -m 755 /harness/pin-curl /tmp/pin-bin/curl
  rm -f /tmp/sprout-version-read-count
  pin_output=\$(run_tester 'PATH=/tmp/pin-bin:\$PATH APP_RELEASE_URL=file:///release/ APP_SKIP_VERIFY=true sh /release/install.sh')
  printf '%s\n' "\$pin_output" | grep -q 'Installing $APP_NAME v0.0.0-dev'
  test "\$(cat /tmp/sprout-version-read-count)" = "1"
fi
# A fresh login must resolve the installed binary directory, whether supplied
# by the distro's stock profile or the installer's bootstrap block.
login_path=\$(run_tester 'printf "%s\n" "\$PATH"')
case ":\$login_path:" in
  *":/home/tester/.local/bin:"*) ;;
  *) echo "fresh tester login PATH omits ~/.local/bin: \$login_path" >&2; exit 1 ;;
esac

# Uninstall is a detached, script-owned transaction. Give the test process a
# fake cosign which accepts the harness's intentionally unsigned cache, point
# the recorded source offline, then enter through the real Go command.
profile_sum=\$(sha256sum /home/tester/.profile | awk '{print \$1}')
ready_epoch=\$(sed -n 's/.*"installationEpoch":"\([^"]*\)".*/\1/p' "\$state_file")
ready_version=\$(sed -n 's/.*"version":"\([^"]*\)".*/\1/p' "\$state_file")
test "\${#ready_epoch}" = "64"
case "\$ready_version" in v*) ;; *) echo "invalid ready version: \$ready_version" >&2; exit 1 ;; esac

if [ "\${TEST_DEEP_LIFECYCLE:-0}" = "1" ]; then
  stale_epoch=0000000000000000000000000000000000000000000000000000000000000000
  if [ "\$stale_epoch" = "\$ready_epoch" ]; then
    stale_epoch=ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
  fi
  if run_tester "APP_MAINTENANCE_EXPECT_EPOCH=\$stale_epoch sh '\$maintenance_dir/install.sh' --uninstall" >/tmp/stale-uninstall.out 2>&1; then
    echo "cached controller accepted a stale installation epoch" >&2
    exit 1
  fi
  grep -q 'obsolete installation epoch' /tmp/stale-uninstall.out
  grep -q '"phase":"ready"' "\$state_file"

  # The managed service is the cooperative instance. This second app process
  # blocks in an input prompt and deliberately cannot finish on the first TERM,
  # proving the 15-second force path. A forged marker for unrelated sleep must
  # never cause that process to be signalled.
  uninstall_managed_pid=\$(service_main_pid)
  case "\$uninstall_managed_pid" in ''|0|*[!0-9]*) echo "invalid uninstall service PID" >&2; exit 1 ;; esac
  wait_for_instance "\$control_dir" "\$uninstall_managed_pid" uninstall-managed
  rm -f /tmp/$APP_NAME-block-input /tmp/$APP_NAME-block.log /tmp/$APP_NAME-block.pid
  mkfifo /tmp/$APP_NAME-block-input
  exec 7<>/tmp/$APP_NAME-block-input
  chown tester:tester /tmp/$APP_NAME-block-input
  run_tester 'setsid "\$HOME/.local/bin/$APP_NAME" uninstall </tmp/$APP_NAME-block-input >/tmp/$APP_NAME-block.log 2>&1 & echo \$! >/tmp/$APP_NAME-block.pid'
  noncooperative_pid=\$(cat /tmp/$APP_NAME-block.pid)
  case "\$noncooperative_pid" in ''|*[!0-9]*) echo "invalid non-cooperative PID" >&2; exit 1 ;; esac
  wait_for_instance "\$control_dir" "\$noncooperative_pid" non-cooperative

  run_tester 'systemd-run --user --quiet --collect --unit=$APP_NAME-uninstall-test-unrelated sleep 300'
  unrelated_uninstall_pid=\$(run_tester 'systemctl --user show --property=MainPID --value $APP_NAME-uninstall-test-unrelated.service')
  case "\$unrelated_uninstall_pid" in ''|0|*[!0-9]*) echo "invalid unrelated uninstall PID" >&2; exit 1 ;; esac
  : > "\$instances_dir/\$unrelated_uninstall_pid"
  marker_count=\$(find "\$instances_dir" -type f | wc -l)
  test "\$marker_count" -ge 3
fi

run_tester 'printf "#!/bin/sh\nexit 0\n" >"\$HOME/.local/bin/cosign" && chmod 700 "\$HOME/.local/bin/cosign"'
cosign_sum=\$(sha256sum /home/tester/.local/bin/cosign | awk '{print \$1}')
if [ -f "\$maintenance_dir/release-url" ]; then
  run_tester "printf '%s\\n' file:///definitely-offline/ >'\$maintenance_dir/release-url'"
fi

uninstall_output=\$(run_tester 'printf "y\n" | "\$HOME/.local/bin/$APP_NAME" uninstall' 2>&1)
printf '%s\n' "\$uninstall_output"
printf '%s\n' "\$uninstall_output" | grep -q 'Uninstall accepted'
printf '%s\n' "\$uninstall_output" | grep -qF "\$logs_dir/maintenance.log"

uninstall_tries=0
until grep -q '"phase":"uninstalled"' "\$state_file"; do
  uninstall_tries=\$((uninstall_tries + 1))
  if [ "\$uninstall_tries" -ge 600 ]; then
    echo "timed out waiting for detached uninstall" >&2
    cat "\$logs_dir/maintenance.log" >&2 || :
    test ! -f /tmp/$APP_NAME-block.log || cat /tmp/$APP_NAME-block.log >&2
    exit 1
  fi
  sleep 0.05
done

test ! -e "\$data_dir"
test ! -e "/home/tester/.local/bin/$APP_NAME"
test ! -e "/home/tester/.config/systemd/user/$APP_NAME.service"
test ! -L "/home/tester/.config/systemd/user/default.target.wants/$APP_NAME.service"
test -d "\$control_dir"
test -d "\$instances_dir"
test -d "\$maintenance_dir"
test -d "\$logs_dir"
test "\$(stat -c %a "\$control_dir")" = "700"
test "\$(stat -c %a "\$instances_dir")" = "700"
test "\$(stat -c %a "\$maintenance_dir")" = "700"
test "\$(stat -c %a "\$logs_dir")" = "700"
test -f "\$control_dir/operation.lock"
test -f "\$control_dir/lifecycle.lock"
test -f "\$state_file"
test "\$(stat -c %a "\$state_file")" = "600"
test -f "\$maintenance_dir/install.sh"
test -f "\$maintenance_dir/install.sh.cosign.bundle"
test "\$(stat -c %a "\$maintenance_dir/install.sh")" = "700"
test "\$(stat -c %a "\$maintenance_dir/install.sh.cosign.bundle")" = "600"
test -f "\$logs_dir/maintenance.log"
test -s "\$logs_dir/maintenance.log"
grep -q 'maintenance uninstall job started' "\$logs_dir/maintenance.log"
if [ "\${TEST_DEEP_LIFECYCLE:-0}" = "1" ]; then
  grep -q 'forcing remaining marked instances' "\$logs_dir/maintenance.log"
fi
grep -q '"version":""' "\$state_file"
grep -q '"targetVersion":""' "\$state_file"
grep -q '"nonce":""' "\$state_file"
test -n "\$(sed -n 's/.*"changedAt":"\([^"]*\)".*/\1/p' "\$state_file")"
test "\$(sed -n 's/.*"installationEpoch":"\([^"]*\)".*/\1/p' "\$state_file")" = "\$ready_epoch"
test "\$(sha256sum /home/tester/.profile | awk '{print \$1}')" = "\$profile_sum"
test "\$(sha256sum /home/tester/.local/bin/cosign | awk '{print \$1}')" = "\$cosign_sum"
grep -q '# >>> PATH bootstrap: ~/.local/bin >>>' /home/tester/.profile
if [ "\${TEST_SCENARIO:-default}" = "no-update" ]; then
  test ! -e "\$maintenance_dir/release-url"
else
  test -f "\$maintenance_dir/release-url"
  grep -qx 'file:///definitely-offline/' "\$maintenance_dir/release-url"
fi

job_tries=0
while find "\$maintenance_dir/jobs" -mindepth 1 -print -quit | grep -q .; do
  job_tries=\$((job_tries + 1))
  if [ "\$job_tries" -ge 200 ]; then
    echo "detached maintenance job directory was not cleaned" >&2
    exit 1
  fi
  sleep 0.05
done
test -z "\$(find "\$instances_dir" -mindepth 1 -print -quit)"

if [ "\${TEST_DEEP_LIFECYCLE:-0}" = "1" ]; then
  stopped_tries=0
  while kill -0 "\$noncooperative_pid" 2>/dev/null; do
    process_state=\$(awk '{print \$3}' "/proc/\$noncooperative_pid/stat" 2>/dev/null || :)
    [ "\$process_state" = "Z" ] && break
    stopped_tries=\$((stopped_tries + 1))
    [ "\$stopped_tries" -lt 100 ] || break
    sleep 0.05
  done
  process_state=\$(awk '{print \$3}' "/proc/\$noncooperative_pid/stat" 2>/dev/null || :)
  if kill -0 "\$noncooperative_pid" 2>/dev/null && [ "\$process_state" != "Z" ]; then
    echo "non-cooperative marked app process survived uninstall" >&2
    exit 1
  fi
  kill -0 "\$unrelated_uninstall_pid"
  test ! -e "\$instances_dir/\$unrelated_uninstall_pid"
  run_tester 'systemctl --user stop $APP_NAME-uninstall-test-unrelated.service'
  exec 7>&-
  rm -f /tmp/$APP_NAME-block-input
fi

# Recovery invocation stays useful without the binary or network, and an
# already-uninstalled transaction is idempotent.
run_tester "APP_RELEASE_URL=offline sh '\$maintenance_dir/install.sh' --uninstall"
grep -q '"phase":"uninstalled"' "\$state_file"
test ! -e "\$data_dir"
test ! -e "/home/tester/.local/bin/$APP_NAME"
test "\$(sed -n 's/.*"installationEpoch":"\([^"]*\)".*/\1/p' "\$state_file")" = "\$ready_epoch"
test "\$(sha256sum /home/tester/.profile | awk '{print \$1}')" = "\$profile_sum"
echo "CONTAINER-TEST-PASSED"
EOF
chmod +x "$CONTAINER_TEST"
chmod 755 "$HARNESS_DIR"
sh -n "$CONTAINER_TEST"

# name|image|init|setup command (run as root before the test). Systemd images
# install a user-session D-Bus implementation where their minimal base omits it.
# Some images also lack su/useradd, which the harness needs (util-linux/shadow).
# shellcheck disable=SC2016 # setup commands are literal scripts run inside the guest
distro_spec() {
  case "$1" in
    debian)   echo "images:debian/trixie|systemd|apt-get update -qq && apt-get install -y -qq --no-install-recommends curl ca-certificates gzip dbus-user-session python3" ;;
    ubuntu)   echo "images:ubuntu/resolute|systemd|apt-get update -qq && apt-get install -y -qq --no-install-recommends curl ca-certificates gzip dbus-user-session" ;;
    fedora)   echo "images:fedora/44|systemd|dnf install -y -q curl gzip util-linux dbus-daemon" ;;
    arch)     echo "images:archlinux/current|systemd|pacman -Syu --noconfirm --needed --quiet curl gzip dbus" ;;
    alpine)   echo "images:alpine/3.24|openrc|apk add -q curl gzip" ;;
    opensuse) echo "images:opensuse/tumbleweed|systemd|zypper -q --non-interactive install curl gzip gawk shadow util-linux dbus-1" ;;
    void)     printf '%s\n' 'images:voidlinux/current|runit|chmod 1777 /tmp; missing_packages=""; for package in curl gzip shadow util-linux; do xbps-query "$package" >/dev/null 2>&1 || missing_packages="$missing_packages $package"; done; if [ -n "$missing_packages" ]; then xbps-install -Sy $missing_packages >/dev/null; fi' ;;
    rocky)    echo "images:rockylinux/9|systemd|dnf install -y -q --allowerasing curl gzip util-linux dbus-daemon" ;;
    *)        return 1 ;;
  esac
}

# Run matrix ------------------------------------------------------------------

instance_name() {
  local label=$1
  printf 'sprout-lifecycle-%s-%s-%s-%s\n' "$SCENARIO" "$label" "$$" "$RANDOM"
}

track_instance() {
  local instance=$1
  ACTIVE_INSTANCES+=("$instance")
  ACTIVE_INSTANCE_SET["$instance"]=1
}

run_logged() {
  local log_file=$1
  local command_rc
  local tee_rc
  local -a pipeline_status
  shift
  "$@" 2>&1 | tee -a "$log_file"
  pipeline_status=("${PIPESTATUS[@]}")
  command_rc=${pipeline_status[0]}
  tee_rc=${pipeline_status[1]}
  if (( command_rc != 0 )); then
    return "$command_rc"
  fi
  return "$tee_rc"
}

log_case_message() {
  local log_file=$1
  shift
  printf '%s\n' "$*" | tee -a "$log_file"
}

wait_for_container_boot() {
  local instance=$1
  local tries=0
  while (( tries < 480 )); do
    # shellcheck disable=SC2016 # literal script evaluated inside the guest
    if "${INCUS[@]}" exec "$instance" -- sh -c '
      if [ -d /run/systemd/system ]; then
        state=$(systemctl is-system-running 2>/dev/null || :)
        [ "$state" = "running" ] || [ "$state" = "degraded" ] || exit 1
      fi
      if command -v ip >/dev/null 2>&1; then
        ip -4 addr show scope global 2>/dev/null | grep -q "inet " || exit 1
        ip -4 route show default 2>/dev/null | grep -q . || exit 1
      fi
    ' >/dev/null 2>&1; then
      return 0
    fi
    tries=$((tries + 1))
    sleep 0.25
  done
  echo "error: timed out waiting for Incus instance $instance to boot" >&2
  return 1
}

launch_instance() {
  local image=$1
  local instance=$2
  "${INCUS[@]}" init "$image" "$instance" \
    --config security.privileged=false \
    --config "user.sprout-test-owner=$RUN_TOKEN" || return
  "${INCUS[@]}" config device add "$instance" release disk \
    "source=$RELEASE_DIR" path=/release readonly=true || return
  "${INCUS[@]}" config device add "$instance" harness disk \
    "source=$HARNESS_DIR" path=/harness readonly=true || return
  "${INCUS[@]}" start "$instance" || return
  wait_for_container_boot "$instance"
}

delete_instance() {
  local instance=$1
  delete_owned_instance "$instance" >/dev/null || {
    echo "warning: failed to delete Incus instance $instance; exit cleanup will retry" >&2
    return 1
  }
}

show_instance_diagnostics() {
  local instance=$1
  local owner_rc
  local presence_rc
  echo "----- Incus diagnostics: $instance -----"
  if [[ "${ACTIVE_INSTANCE_SET[$instance]:-}" != "1" ]]; then
    echo "diagnostics unavailable: instance is no longer tracked by this run"
    return 0
  fi
  if instance_is_present "$instance"; then
    :
  else
    presence_rc=$?
    if [[ "$presence_rc" == "1" ]]; then
      echo "diagnostics unavailable: instance was not created or no longer exists"
    else
      echo "diagnostics unavailable: failed to query Incus instance state"
    fi
    return 0
  fi
  if instance_owned_by_run "$instance"; then
    :
  else
    owner_rc=$?
    if [[ "$owner_rc" == "1" ]]; then
      echo "diagnostics withheld: instance is not owned by this harness run"
    else
      echo "diagnostics unavailable: failed to verify instance ownership"
    fi
    return 0
  fi
  "${INCUS[@]}" info "$instance" --show-log || :
}

failed=""
container_shell=(sh)
if [[ "${TRACE_CONTAINER:-false}" == "true" ]]; then
  container_shell+=(-x)
fi
for distro in $DISTROS; do
  spec=$(distro_spec "$distro") || { echo "error: unknown distro '$distro'" >&2; exit 1; }
  image=${spec%%|*}
  spec=${spec#*|}
  init=${spec%%|*}
  setup=${spec#*|}
  test_pin=0
  if [[ "$distro" == "alpine" && "$SCENARIO" == "default" ]]; then
    test_pin=1
  fi
  test_fault=0
  if [[ "$distro" == "debian" && "$SCENARIO" == "default" && -d "$RELEASE_DIR/fault" ]]; then
    test_fault=1
  fi
  test_port=0
  if [[ "$distro" == "debian" && "$SCENARIO" == "default" ]]; then
    test_port=1
  fi
  test_deep_lifecycle=0
  if [[ "$distro" == "debian" && "$SCENARIO" == "default" ]]; then
    test_deep_lifecycle=1
  fi

  echo ""
  echo "=============================================================="
  echo ">> Testing $SCENARIO installer on $distro ($image)"
  echo "=============================================================="
  instance=$(instance_name "$distro")
  log_file="$RUN_LOG_DIR/$SCENARIO-$distro.log"
  : > "$log_file"
  log_case_message "$log_file" "scenario=$SCENARIO distro=$distro image=$image instance=$instance"
  track_instance "$instance"
  distro_ok=false
  if run_logged "$log_file" launch_instance "$image" "$instance" &&
     run_logged "$log_file" "${INCUS[@]}" exec "$instance" -- env \
       "SETUP_CMD=$setup" \
       "TEST_INIT=$init" \
       "TEST_SCENARIO=$SCENARIO" \
       "TEST_EXAMPLE_HASH=$TEST_EXAMPLE_HASH" \
       "TEST_PIN=$test_pin" \
       "TEST_FAULT=$test_fault" \
       "TEST_PORT=$test_port" \
       "TEST_DEEP_LIFECYCLE=$test_deep_lifecycle" \
       "${container_shell[@]}" /harness/container-test.sh; then
    distro_ok=true
    log_case_message "$log_file" ">> $SCENARIO/$distro: PASS"
  else
    log_case_message "$log_file" ">> $SCENARIO/$distro: FAIL"
    run_logged "$log_file" show_instance_diagnostics "$instance" || :
    failed="$failed $SCENARIO/$distro"
  fi
  if ! $distro_ok && $KEEP_FAILED; then
    retain_rc=0
    retain_owned_instance "$instance" || retain_rc=$?
    case "$retain_rc" in
      0)
        printf 'retained_instance=%s\nharness_dir=%s\n' "$instance" "$HARNESS_DIR" >> "$log_file"
        ;;
      3)
        log_case_message "$log_file" ">> No failed instance was created to retain."
        ;;
      *)
        failed="$failed $SCENARIO/$distro-retain"
        ;;
    esac
  else
    if ! delete_instance "$instance"; then
      log_case_message "$log_file" ">> $SCENARIO/$distro: FAIL (instance cleanup)"
      run_logged "$log_file" show_instance_diagnostics "$instance" || :
      failed="$failed $SCENARIO/$distro-cleanup"
    fi
  fi
done

# Immutable-root fake: on an ostree system with missing deps, the installer
# must point at brew/distrobox instead of a nonexistent host package manager.
if ! $SKIP_FAKES; then
  echo ""
  echo "=============================================================="
  echo ">> Testing immutable-root fake (ostree marker + missing deps)"
  echo "=============================================================="
  immutable_instance=$(instance_name "immutable")
  immutable_log="$RUN_LOG_DIR/$SCENARIO-immutable-fake.log"
  : > "$immutable_log"
  log_case_message "$immutable_log" "scenario=$SCENARIO case=immutable-fake image=images:debian/trixie instance=$immutable_instance"
  track_instance "$immutable_instance"
  immutable_ok=false
  if run_logged "$immutable_log" launch_instance "images:debian/trixie" "$immutable_instance"; then
    if run_logged "$immutable_log" "${INCUS[@]}" exec "$immutable_instance" -- sh -c '
        touch /run/ostree-booted
        rm -f /usr/local/bin/curl /usr/bin/curl /bin/curl
        useradd -m -s /bin/sh tester
        su - tester -c "APP_RELEASE_URL=file:///release/ APP_SKIP_VERIFY=true sh /release/install.sh" 2>&1 || true
      '; then
      if grep -q 'brew install' "$immutable_log"; then
        immutable_ok=true
      fi
    fi
  fi
  if $immutable_ok; then
    log_case_message "$immutable_log" ">> immutable-fake: PASS"
  else
    log_case_message "$immutable_log" ">> immutable-fake: FAIL (expected brew hint in output)"
    run_logged "$immutable_log" show_instance_diagnostics "$immutable_instance" || :
    failed="$failed immutable-fake"
  fi
  if ! $immutable_ok && $KEEP_FAILED; then
    retain_rc=0
    retain_owned_instance "$immutable_instance" || retain_rc=$?
    case "$retain_rc" in
      0)
        printf 'retained_instance=%s\nharness_dir=%s\n' "$immutable_instance" "$HARNESS_DIR" >> "$immutable_log"
        ;;
      3)
        log_case_message "$immutable_log" ">> No failed instance was created to retain."
        ;;
      *)
        failed="$failed immutable-fake-retain"
        ;;
    esac
  else
    if ! delete_instance "$immutable_instance"; then
      log_case_message "$immutable_log" ">> immutable-fake: FAIL (instance cleanup)"
      run_logged "$immutable_log" show_instance_diagnostics "$immutable_instance" || :
      failed="$failed immutable-fake-cleanup"
    fi
  fi
fi

echo ""
if [[ -n "$failed" ]]; then
  echo "FAILED:$failed"
  echo "Logs: $RUN_LOG_DIR"
  exit 1
fi

# --- BEGIN template ---
if [[ -z "$requested_release_dir" && "$SCENARIO" == "default" ]]; then
  command -v tar >/dev/null 2>&1 || {
    echo "error: tar is required to prepare focused installer source variants" >&2
    exit 1
  }
  FOCUSED_ROOT=$(mktemp -d)
  focused_names=(no-update no-service headless)
  focused_cuts=(update service service.https)
  for i in "${!focused_names[@]}"; do
    name=${focused_names[$i]}
    cuts=${focused_cuts[$i]}
    source_dir="$FOCUSED_ROOT/$name"
    mkdir -p "$source_dir"
    tar \
      --exclude='./.git' \
      --exclude='./out' \
      --exclude='./tools' \
      --exclude='./node_modules' \
      --exclude='./docs/resources/_gen' \
      -cf - . | (cd "$source_dir" && tar -xf -)

    echo ""
    echo "=============================================================="
    echo ">> Preparing focused $name installer (cut args: $cuts)"
    echo "=============================================================="
    if ! (
      cd "$source_dir" &&
      ./scripts/cut --finalize \
        --module "example.com/sprout-focused/$name" "$cuts" &&
      bash scripts/test-lifecycle-e2e.sh \
        --scenario "$name" \
        --distros "debian" \
        --no-fakes
    ); then
      echo "error: focused installer $name failed (cut args: $cuts)" >&2
      echo "retained source: $source_dir" >&2
      FOCUSED_ROOT=""
      exit 1
    fi
  done
  rm -rf "$FOCUSED_ROOT"
  FOCUSED_ROOT=""
fi
# --- END template ---

printf 'All lifecycle E2E tests passed (scenario: %s; distros: %s).\n' \
  "$SCENARIO" "$DISTROS"
