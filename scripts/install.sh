#!/bin/sh

# Target: ~POSIX Linux x86_64/amd64 or aarch64/arm64, user-level install, optional systemd --user unit
# Requires: curl, gzip, mktemp, install, sha256sum, sed, awk, flock, stat,
#           od, date, sync, readlink
#           cosign is used if present, otherwise a pinned, sha256-verified
#           build is installed to ~/.local/bin automatically.
#           systemd --user is optional: without it (Alpine, Void, WSL, ...)
#           only the binary is installed and the service step is skipped.
# Examples:
#   curl -fsSL https://releases.sproutcli.dev/install.sh | sh
#   ~/.<app>/maintenance/install.sh --uninstall  # offline
#
# Mirrors: run with APP_RELEASE_URL=https://mirror.example.com/ to install from
# a byte-for-byte mirror of the official release artifacts. All cosign
# signatures remain valid (they are URL-independent). Mirror installs do not
# write the release-url file, which disables update checks and self-update by
# design. See docs/content/docs/getting-started/mirror.md.
#
# Testing: APP_SKIP_VERIFY=true skips cosign signature verification (the plain
# sha256 check still runs). Only for local/matrix installer testing against
# unsigned artifacts - never use it for real installs.

# print logo, i made this with https://manytools.org/hacker-tools/ascii-banner/ <3
cat << 'EOF'
 ______     ______   ______     ______     __  __     ______  
/\  ___\   /\  == \ /\  == \   /\  __ \   /\ \/\ \   /\__  _\ 
\ \___  \  \ \  _-/ \ \  __<   \ \ \/\ \  \ \ \_\ \  \/_/\ \/ 
 \/\_____\  \ \_\    \ \_\ \_\  \ \_____\  \ \_____\    \ \_\ 
  \/_____/   \/_/     \/_/ /_/   \/_____/   \/_____/     \/_/ 
                                                              
EOF

set -u
umask 077

# Vars ------------------------------------------------------------------------

# set by build.sh before uploading
APP_NAME="<APP_NAME>"
RELEASE_URL="<RELEASE_URL>"
SERVICE="<SERVICE>"
SERVICE_DESC="<SERVICE_DESC>"
SERVICE_ARGS="<SERVICE_ARGS>"
# cosign keyless identity of the CI workflow that signed this release
CERT_IDENTITY="<CERT_IDENTITY>"
OIDC_ISSUER="<OIDC_ISSUER>"
# deliberately pinned cosign release bootstrapped when the user doesn't have
# cosign; sha256s come from the cosign release's own checksums file
COSIGN_VERSION="<COSIGN_VERSION>"
COSIGN_SHA_LINUX_AMD64="<COSIGN_SHA_LINUX_AMD64>"
COSIGN_SHA_LINUX_ARM64="<COSIGN_SHA_LINUX_ARM64>"

# testing escape hatch: skip signature verification (sha256 check still runs)
SKIP_VERIFY="${APP_SKIP_VERIFY:-false}"

case "${HOME:-}" in
    /*) ;;
    *) printf 'HOME must be a nonempty absolute path.\n' >&2; exit 1 ;;
esac

APP_BIN="$HOME/.local/bin/$APP_NAME"
STORAGE_DIR="$HOME/.$APP_NAME"
APP_DATA_DIR="$STORAGE_DIR/data"
CONTROL_DIR="$STORAGE_DIR/control"
STATE_FILE="$CONTROL_DIR/state.json"
OPERATION_LOCK="$CONTROL_DIR/operation.lock"
LIFECYCLE_LOCK="$CONTROL_DIR/lifecycle.lock"
INSTANCES_DIR="$CONTROL_DIR/instances"
MAINTENANCE_DIR="$STORAGE_DIR/maintenance"
CACHED_INSTALLER="$MAINTENANCE_DIR/install.sh"
CACHED_INSTALLER_BUNDLE="$MAINTENANCE_DIR/install.sh.cosign.bundle"
JOBS_DIR="$MAINTENANCE_DIR/jobs"
LOGS_DIR="$STORAGE_DIR/logs"
MAINTENANCE_LOG="$LOGS_DIR/maintenance.log"
# --- BEGIN update ---
RELEASE_URL_FILE="$MAINTENANCE_DIR/release-url"
# --- END update ---

SERVICE_NAME="$APP_NAME.service"
SERVICE_FILE="$HOME/.config/systemd/user/$SERVICE_NAME"
SERVICE_WANTS_LINK="$HOME/.config/systemd/user/default.target.wants/$SERVICE_NAME"
SERVICE_READY_TIMEOUT_SECONDS=90
LOCK_TIMEOUT_SECONDS=300

USER_NAME="${USER:-$(id -un)}" # $USER is not always exported
USER_ID=$(id -u)

# Globals used by rollback/cleanup --------------------------------------------
temp_dir=""
old_app_bin=""
old_service_file=""
app_bin_exists=0
binary_changed=0
fresh_install=1
service_exists=0
service_was_enabled=0
service_was_active=0
service_touched=0
default_port=""
migration_nonce=""
migration_started=0
state_transition_written=0
state_before_exists=0
state_before_file=""
state_phase=""
state_version=""
state_target_version=""
state_nonce=""
state_changed_at=""
state_epoch=""
transaction_phase=""
transaction_epoch=""
recovering_transition=0
cached_installer_exists=0
cached_bundle_exists=0
cached_installer_changed=0
old_cached_installer=""
old_cached_bundle=""
# --- BEGIN update ---
old_release_url_file=""
release_url_exists=0
release_url_changed=0
# --- END update ---

# stdout colors
if [ -z "${NO_COLOR:-}" ] && [ -t 1 ]; then
  GREEN=$(printf '\033[32m')
  RST_OUT=$(printf '\033[0m')
else
  GREEN= ; RST_OUT=
fi

# stderr colors
if [ -z "${NO_COLOR:-}" ] && [ -t 2 ]; then
  YELLOW=$(printf '\033[33m')
  RED=$(printf '\033[31m')
  RST_ERR=$(printf '\033[0m')
else
  YELLOW= ; RED= ; RST_ERR=
fi

successf() { fmt=$1; shift; printf '%s'"$fmt"'%s\n' "${GREEN:-}" "$@" "${RST_OUT:-}"; }
warnf()    { fmt=$1; shift; printf '%s'"$fmt"'%s\n' "${YELLOW:-}" "$@" "${RST_ERR:-}" >&2; }
errf()     { fmt=$1; shift; printf '%s'"$fmt"'%s\n' "${RED:-}"   "$@" "${RST_ERR:-}" >&2; }
fatalf()   { errf "$@"; exit 1; }

MODE=install
case $# in
    0) ;;
    1)
        case "$1" in
            --update) MODE=update ;;
            --uninstall) MODE=uninstall ;;
            *) fatalf 'Unknown option: %s' "$1" ;;
        esac
        ;;
    *) fatalf 'Usage: %s [--update|--uninstall]' "$0" ;;
esac

normalize_release_url() {
    normalized=$(printf '%s' "$1" | tr -d '\r' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//; s#/*$##')
    [ -n "$normalized" ] || return 1
    printf '%s/\n' "$normalized"
}

validate_version() {
    printf '%s\n' "$1" | awk '
      /^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$/ {
        prerelease=$0
        sub(/\+.*/, "", prerelease)
        if (prerelease ~ /-/) {
          sub(/^v[0-9]+\.[0-9]+\.[0-9]+-/, "", prerelease)
          count=split(prerelease, identifiers, ".")
          for (i=1; i<=count; i++) {
            if (identifiers[i] ~ /^[0-9]+$/ && identifiers[i] !~ /^(0|[1-9][0-9]*)$/) exit 1
          }
        }
        valid=1
      }
      END { exit valid ? 0 : 1 }
    '
}

printf '%s\n' "$APP_NAME" | awk '/^[A-Za-z0-9_][A-Za-z0-9._-]*$/ { valid=1 } END { exit valid ? 0 : 1 }' ||
    fatalf 'Application name must match [A-Za-z0-9_][A-Za-z0-9._-]*'

validate_private_dir() {
    private_dir=$1
    private_label=$2
    [ ! -L "$private_dir" ] || fatalf '%s is a symlink: %s' "$private_label" "$private_dir"
    [ -d "$private_dir" ] || fatalf '%s is not a directory: %s' "$private_label" "$private_dir"
    runtime_owner=$(stat -c '%u' "$private_dir") ||
        fatalf 'Failed to inspect %s owner: %s' "$private_label" "$private_dir"
    [ "$runtime_owner" = "$USER_ID" ] ||
        fatalf '%s %s is owned by UID %s, expected %s' "$private_label" "$private_dir" "$runtime_owner" "$USER_ID"
    runtime_mode=$(stat -c '%a' "$private_dir") ||
        fatalf 'Failed to inspect %s permissions: %s' "$private_label" "$private_dir"
    [ "$runtime_mode" = "700" ] ||
        fatalf '%s %s has mode %s, expected 700' "$private_label" "$private_dir" "$runtime_mode"
}

ensure_private_dir() {
    private_dir=$1
    private_label=$2
    if [ ! -e "$private_dir" ] && [ ! -L "$private_dir" ]; then
        # shellcheck disable=SC2174 # umask 077 makes any created parent 0700 too
        (umask 077; mkdir -p -m 700 "$private_dir") ||
            fatalf 'Failed to create %s: %s' "$private_label" "$private_dir"
    fi
    validate_private_dir "$private_dir" "$private_label"
}

validate_private_file() {
    private_file=$1
    private_label=$2
    [ ! -L "$private_file" ] || fatalf '%s is a symlink: %s' "$private_label" "$private_file"
    [ -f "$private_file" ] || fatalf '%s is not a regular file: %s' "$private_label" "$private_file"
    private_owner=$(stat -c '%u' "$private_file") ||
        fatalf 'Failed to inspect %s owner: %s' "$private_label" "$private_file"
    [ "$private_owner" = "$USER_ID" ] ||
        fatalf '%s %s is owned by UID %s, expected %s' "$private_label" "$private_file" "$private_owner" "$USER_ID"
    private_mode=$(stat -c '%a' "$private_file") ||
        fatalf 'Failed to inspect %s permissions: %s' "$private_label" "$private_file"
    [ "$private_mode" = "600" ] ||
        fatalf '%s %s has mode %s, expected 600' "$private_label" "$private_file" "$private_mode"
}

ensure_private_file() {
    private_file=$1
    private_label=$2
    if [ ! -e "$private_file" ] && [ ! -L "$private_file" ]; then
        (umask 077; : > "$private_file") ||
            fatalf 'Failed to create %s: %s' "$private_label" "$private_file"
    fi
    validate_private_file "$private_file" "$private_label"
}

ensure_lifecycle_layout() {
    ensure_private_dir "$STORAGE_DIR" "storage directory"
    ensure_private_dir "$CONTROL_DIR" "control directory"
    ensure_private_dir "$INSTANCES_DIR" "instances directory"
    ensure_private_dir "$MAINTENANCE_DIR" "maintenance directory"
    ensure_private_dir "$JOBS_DIR" "maintenance jobs directory"
    ensure_private_dir "$LOGS_DIR" "logs directory"
    ensure_private_file "$OPERATION_LOCK" "operation lock"
    ensure_private_file "$LIFECYCLE_LOCK" "lifecycle lock"
    ensure_private_file "$MAINTENANCE_LOG" "maintenance log"
}

clear_instance_markers() {
    markers_dir=$1
    [ -d "$markers_dir" ] || return 0
    for marker in "$markers_dir"/* "$markers_dir"/.[!.]* "$markers_dir"/..?*; do
        if [ ! -e "$marker" ] && [ ! -L "$marker" ]; then
            continue
        fi
        if [ -f "$marker" ] || [ -L "$marker" ]; then
            rm -f "$marker" ||
                fatalf 'Failed to remove stale instance marker: %s' "$marker"
        fi
    done
}

generate_token() {
    [ -r /dev/urandom ] || fatalf 'OS random source /dev/urandom is unavailable'
    generated_token=$(od -An -N32 -tx1 /dev/urandom | tr -d '[:space:]') ||
        fatalf 'Failed to generate lifecycle token'
    [ ${#generated_token} -eq 64 ] ||
        fatalf 'OS random source returned an invalid lifecycle token'
    printf '%s\n' "$generated_token"
}

validate_token() {
    [ ${#1} -eq 64 ] || return 1
    case "$1" in *[!0-9a-f]*) return 1 ;; esac
    return 0
}

state_field() {
    state_key=$1
    printf '%s\n' "$state_json" |
        sed -n 's/.*"'"$state_key"'":"\([^"]*\)".*/\1/p'
}

load_state() {
    state_phase=""
    state_version=""
    state_target_version=""
    state_nonce=""
    state_changed_at=""
    state_epoch=""
    [ -e "$STATE_FILE" ] || return 0
    validate_private_file "$STATE_FILE" "lifecycle state"
    state_json=$(sed -n '1p' "$STATE_FILE") || fatalf 'Failed to read lifecycle state'
    [ -n "$state_json" ] || fatalf 'Lifecycle state is empty: %s' "$STATE_FILE"
    state_phase=$(state_field phase)
    state_version=$(state_field version)
    state_target_version=$(state_field targetVersion)
    state_nonce=$(state_field nonce)
    state_changed_at=$(state_field changedAt)
    state_epoch=$(state_field installationEpoch)
    validate_token "$state_epoch" || fatalf 'Lifecycle state has an invalid installationEpoch'
    [ -n "$state_changed_at" ] || fatalf 'Lifecycle state has an empty changedAt timestamp'
    case "$state_phase" in
        ready)
            validate_version "$state_version" || fatalf 'Lifecycle state has an invalid ready version: %s' "$state_version"
            [ -z "$state_target_version" ] && [ -z "$state_nonce" ] ||
                fatalf 'Ready lifecycle state contains transitional fields'
            ;;
        installing)
            [ -z "$state_version" ] || validate_version "$state_version" ||
                fatalf 'Lifecycle state has an invalid installed version: %s' "$state_version"
            validate_version "$state_target_version" ||
                fatalf 'Lifecycle state has an invalid install target: %s' "$state_target_version"
            validate_token "$state_nonce" || fatalf 'Lifecycle state has an invalid nonce'
            ;;
        updating)
            validate_version "$state_version" || fatalf 'Lifecycle state has an invalid installed version: %s' "$state_version"
            validate_version "$state_target_version" || fatalf 'Lifecycle state has an invalid update target: %s' "$state_target_version"
            validate_token "$state_nonce" || fatalf 'Lifecycle state has an invalid nonce'
            ;;
        uninstalling)
            validate_version "$state_version" ||
                fatalf 'Lifecycle state has an invalid uninstall version: %s' "$state_version"
            [ -z "$state_target_version" ] || fatalf 'Uninstalling lifecycle state has a target version'
            [ -z "$state_nonce" ] || fatalf 'Uninstalling lifecycle state has a migration nonce'
            ;;
        uninstalled)
            [ -z "$state_version" ] && [ -z "$state_target_version" ] && [ -z "$state_nonce" ] ||
                fatalf 'Uninstalled lifecycle state contains installation fields'
            ;;
        *) fatalf 'Lifecycle state has an invalid phase: %s' "$state_phase" ;;
    esac
}

write_state() {
    write_phase=$1
    write_version=$2
    write_target=$3
    write_nonce=$4
    write_epoch=$5
    write_changed_at=$(date -u '+%Y-%m-%dT%H:%M:%SZ') ||
        fatalf 'Failed to create lifecycle timestamp'
    state_tmp=$(mktemp "$CONTROL_DIR/.state.json.XXXXXX") ||
        fatalf 'Failed to stage lifecycle state'
    chmod 600 "$state_tmp" || { rm -f "$state_tmp"; fatalf 'Failed to make staged lifecycle state private'; }
    printf '{"phase":"%s","version":"%s","targetVersion":"%s","nonce":"%s","changedAt":"%s","installationEpoch":"%s"}\n' \
        "$write_phase" "$write_version" "$write_target" "$write_nonce" "$write_changed_at" "$write_epoch" > "$state_tmp" || {
        rm -f "$state_tmp"
        fatalf 'Failed to write staged lifecycle state'
    }
    sync || { rm -f "$state_tmp"; fatalf 'Failed to flush staged lifecycle state'; }
    mv -f "$state_tmp" "$STATE_FILE" || {
        rm -f "$state_tmp"
        fatalf 'Failed to publish lifecycle state atomically'
    }
    sync || fatalf 'Failed to durably publish lifecycle state'
    validate_private_file "$STATE_FILE" "lifecycle state"
}

backup_state() {
    state_before_file="$temp_dir/state.before"
    if [ -f "$STATE_FILE" ]; then
        cp -f "$STATE_FILE" "$state_before_file" || fatalf 'Failed to back up lifecycle state'
        state_before_exists=1
    else
        state_before_exists=0
    fi
}

restore_state() {
    [ "$state_transition_written" -eq 1 ] || return 0
    if [ "$state_before_exists" -eq 1 ]; then
        restore_tmp=$(mktemp "$CONTROL_DIR/.state.restore.XXXXXX") || return 1
        cp -f "$state_before_file" "$restore_tmp" || { rm -f "$restore_tmp"; return 1; }
        chmod 600 "$restore_tmp" || { rm -f "$restore_tmp"; return 1; }
        mv -f "$restore_tmp" "$STATE_FILE" || { rm -f "$restore_tmp"; return 1; }
    else
        rm -f "$STATE_FILE" || return 1
    fi
    sync || return 1
    state_transition_written=0
}

acquire_operation_lock() {
    printf 'Acquiring maintenance operation lock ...\n'
    exec 8>"$OPERATION_LOCK" || fatalf 'Failed to open operation lock'
    operation_tries=0
    until flock -x -n 8 2>/dev/null; do
        operation_tries=$((operation_tries + 1))
        if [ "$operation_tries" -ge "$LOCK_TIMEOUT_SECONDS" ]; then
            fatalf 'Timeout waiting for another maintenance operation to finish'
        fi
        sleep 1
    done
}

pid_matches_app() {
    checked_pid=$1
    [ -d "/proc/$checked_pid" ] || return 1
    actual_bin=$(readlink "/proc/$checked_pid/exe" 2>/dev/null) || return 1
    actual_bin=${actual_bin% (deleted)}
    [ "$actual_bin" = "$expected_app_bin" ]
}

for_each_marked_instance() {
    instance_signal=$1
    for pidfile in "$INSTANCES_DIR"/*; do
        [ -f "$pidfile" ] || continue
        instance_pid=$(basename "$pidfile")
        case "$instance_pid" in ''|*[!0-9]*) continue ;; esac
        if pid_matches_app "$instance_pid"; then
            kill "-$instance_signal" "$instance_pid" 2>/dev/null || :
        fi
    done
}

marked_instances_remain() {
    for pidfile in "$INSTANCES_DIR"/*; do
        [ -f "$pidfile" ] || continue
        instance_pid=$(basename "$pidfile")
        case "$instance_pid" in ''|*[!0-9]*) continue ;; esac
        pid_matches_app "$instance_pid" && return 0
    done
    return 1
}

stop_registered_service() {
    :
    # --- BEGIN service ---
    if [ -f "$SERVICE_FILE" ] && command -v systemctl >/dev/null 2>&1; then
        # --no-block lets the common 15-second drain budget govern the process
        # rather than systemd's usually longer default stop timeout.
        systemctl --user stop --no-block "$SERVICE_NAME" >/dev/null 2>&1 ||
            warnf 'Failed to request service stop; marked instances will still be drained.'
    fi
    # --- END service ---
}

remove_registered_service() {
    :
    # --- BEGIN service ---
    registered_service_present=0
    if [ -f "$SERVICE_FILE" ] || [ -L "$SERVICE_FILE" ]; then
        registered_service_present=1
        [ ! -L "$SERVICE_FILE" ] || fatalf 'Refusing to remove symlinked service file: %s' "$SERVICE_FILE"
    fi
    if [ "$registered_service_present" -eq 1 ] && command -v systemctl >/dev/null 2>&1; then
        systemctl --user disable "$SERVICE_NAME" >/dev/null 2>&1 || :
        systemctl --user reset-failed "$SERVICE_NAME" >/dev/null 2>&1 || :
    fi
    if [ "$registered_service_present" -eq 1 ]; then
        rm -f "$SERVICE_FILE" || fatalf 'Failed to remove service file: %s' "$SERVICE_FILE"
    fi
    if [ -L "$SERVICE_WANTS_LINK" ]; then
        rm -f "$SERVICE_WANTS_LINK" || fatalf 'Failed to remove service enablement link: %s' "$SERVICE_WANTS_LINK"
    elif [ -e "$SERVICE_WANTS_LINK" ]; then
        fatalf 'Refusing to remove non-symlink service enablement path: %s' "$SERVICE_WANTS_LINK"
    fi
    if [ "$registered_service_present" -eq 1 ] && command -v systemctl >/dev/null 2>&1; then
        systemctl --user daemon-reload >/dev/null 2>&1 ||
            warnf 'Failed to reload the user systemd manager after service removal.'
    fi
    # --- END service ---
}

drain_instances() {
    if app_bin_parent=$(readlink -f "$(dirname "$APP_BIN")" 2>/dev/null); then
        expected_app_bin="$app_bin_parent/$APP_NAME"
    else
        # A never-installed or already-uninstalled app may not have ~/.local/bin.
        # It cannot have a matching live executable, but keeping the literal
        # path makes the idempotent cleanup path independent of that directory.
        expected_app_bin="$APP_BIN"
    fi

    if marked_instances_remain; then
        printf 'Shutting down running instances ...\n'
        for_each_marked_instance TERM
        drain_second=0
        while marked_instances_remain && [ "$drain_second" -lt 15 ]; do
            sleep 1
            drain_second=$((drain_second + 1))
        done
        if marked_instances_remain; then
            warnf 'Graceful shutdown timed out; forcing remaining marked instances.'
            # Revalidation happens again inside the helper immediately before
            # every KILL, protecting against stale markers and PID reuse.
            for_each_marked_instance KILL
        fi
    fi
}

acquire_lifecycle_lock() {
    printf 'Acquiring lifecycle lock ...\n'
    exec 9>"$LIFECYCLE_LOCK" || fatalf 'Failed to open lifecycle lock'
    lifecycle_tries=0
    until flock -x -n 9 2>/dev/null; do
        lifecycle_tries=$((lifecycle_tries + 1))
        if [ "$lifecycle_tries" -ge "$LOCK_TIMEOUT_SECONDS" ]; then
            fatalf 'Timeout waiting for lifecycle lock. Active instances:\n%s' "$(ls "$INSTANCES_DIR" 2>/dev/null || printf 'none')"
        fi
        sleep 1
    done
    lifecycle_lock_acquired=1
    clear_instance_markers "$INSTANCES_DIR"
}

release_lifecycle_lock() {
    exec 9>&- || :
    lifecycle_lock_acquired=""
}

validate_job_expectations() {
    expectation_action=$1
    expected_epoch=${APP_MAINTENANCE_EXPECT_EPOCH:-}
    expected_version=${APP_MAINTENANCE_EXPECT_VERSION:-}
    if [ -z "$expected_epoch" ] && [ -z "$expected_version" ]; then
        return 1
    fi
    [ -n "$expected_epoch" ] ||
        fatalf 'Detached maintenance expectations must include an installation epoch'
    validate_token "$expected_epoch" || fatalf 'Detached maintenance job has an invalid expected epoch'
    [ "$state_phase" = "ready" ] ||
        fatalf '%s job expected ready state, found %s' "$expectation_action" "${state_phase:-absent}"
    [ "$state_epoch" = "$expected_epoch" ] || fatalf 'Maintenance job belongs to an obsolete installation epoch'
    if [ "$expectation_action" = "update" ]; then
        [ -n "$expected_version" ] ||
            fatalf 'Detached update expectations must include the installed version'
        validate_version "$expected_version" || fatalf 'Detached maintenance job has an invalid expected version: %s' "$expected_version"
        [ "$state_version" = "$expected_version" ] ||
            fatalf 'Maintenance job expected version %s, found %s' "$expected_version" "$state_version"
    elif [ -n "$expected_version" ]; then
        validate_version "$expected_version" || fatalf 'Detached maintenance job has an invalid expected version: %s' "$expected_version"
        [ "$state_version" = "$expected_version" ] ||
            fatalf 'Maintenance job expected version %s, found %s' "$expected_version" "$state_version"
    fi
    return 0
}

run_uninstall() {
    load_state
    if validate_job_expectations uninstall; then
        : # expectations validated for a detached job
    elif [ -n "${APP_MAINTENANCE_EXPECT_EPOCH:-}${APP_MAINTENANCE_EXPECT_VERSION:-}" ]; then
        fatalf 'Invalid detached maintenance expectations'
    fi

    if [ -n "$state_epoch" ]; then
        transaction_epoch=$state_epoch
    else
        transaction_epoch=$(generate_token) || fatalf 'Failed to generate installation epoch'
    fi

    if [ -n "$state_phase" ] && [ "$state_phase" != "uninstalled" ]; then
        uninstall_version=$state_version
        if [ -z "$uninstall_version" ]; then
            # A failed first installation has no committed version yet; its
            # validated target names the candidate whose artifacts we remove.
            uninstall_version=$state_target_version
        fi
        validate_version "$uninstall_version" ||
            fatalf 'Cannot publish uninstalling state without an installation version'
        write_state uninstalling "$uninstall_version" "" "" "$transaction_epoch"
    fi

    stop_registered_service
    drain_instances
    acquire_lifecycle_lock

    remove_registered_service
    case "$APP_DATA_DIR" in
        "$STORAGE_DIR/data") ;;
        *) fatalf 'Refusing to remove unexpected data path: %s' "$APP_DATA_DIR" ;;
    esac
    if [ -e "$APP_DATA_DIR" ] || [ -L "$APP_DATA_DIR" ]; then
        [ ! -L "$APP_DATA_DIR" ] || fatalf 'Refusing to remove symlinked data directory: %s' "$APP_DATA_DIR"
        rm -rf "$APP_DATA_DIR" || fatalf 'Failed to remove application data: %s' "$APP_DATA_DIR"
    fi
    if [ -e "$APP_BIN" ] || [ -L "$APP_BIN" ]; then
        [ ! -L "$APP_BIN" ] || fatalf 'Refusing to remove symlinked application binary: %s' "$APP_BIN"
        rm -f "$APP_BIN" || fatalf 'Failed to remove application binary: %s' "$APP_BIN"
    fi
    clear_instance_markers "$INSTANCES_DIR"
    write_state uninstalled "" "" "" "$transaction_epoch"
    release_lifecycle_lock

    successf 'Uninstalled: %s' "$APP_NAME"
    successf 'Retained maintenance state and logs in: %s' "$STORAGE_DIR"
}

# --- BEGIN service.https ---
# Check if a port is in use. Returns 0 if in use, 1 if free or unknown.
# Checks both TCP listeners and bound UDP sockets. This is a preflight
# courtesy, not a safety property: when no socket-listing tool exists the
# check is skipped with a visible warning and the service reports the bind
# failure itself if the port turns out to be taken.
port_in_use() {
    port=$1
    if command -v ss >/dev/null 2>&1; then
        { ss -tlnH 2>/dev/null; ss -ulnH 2>/dev/null; } \
            | awk '{print $4}' | grep -qE "(:|^)${port}$"
    elif command -v netstat >/dev/null 2>&1; then
        { netstat -tln 2>/dev/null; netstat -uln 2>/dev/null; } \
            | awk '{print $4}' | grep -qE "(:|^)${port}$"
    else
        warnf 'Cannot check whether port %s is free: neither ss nor netstat is installed. Continuing; the service will fail to start if the port is taken.' "$port"
        return 1
    fi
}
# --- END service.https ---

rollback() {
    rb=0
    restart_old_service=0
    if [ "$binary_changed" -eq 1 ]; then
        printf 'Restoring previous installation ...\n'
        if [ "$app_bin_exists" -eq 1 ] && [ -n "$old_app_bin" ] && [ -s "$old_app_bin" ]; then
            mv -f "$old_app_bin" "$APP_BIN" || errf '   Error: Failed to restore old binary'
        else
            rm -f "$APP_BIN" || errf '   Error: Failed to remove new binary'
        fi
        rb=1
    fi
    # --- BEGIN service ---
    if [ "$SERVICE" = "true" ] && [ "$service_touched" -eq 1 ]; then
        systemctl --user stop "$SERVICE_NAME" >/dev/null 2>&1 || :
        systemctl --user reset-failed "$SERVICE_NAME" >/dev/null 2>&1 || :
        if [ "$service_exists" -eq 1 ] && [ -n "$old_service_file" ] && [ -s "$old_service_file" ]; then
            printf 'Restoring previous service configuration ...\n'
            mv -f "$old_service_file" "$SERVICE_FILE" || errf '   Error: Failed to restore old service unit file'
            rb=1
        elif [ "$service_exists" -eq 0 ]; then
            rm -f "$SERVICE_FILE" || errf '   Error: Failed to remove new service unit file'
            rb=1
        fi
        systemctl --user daemon-reload >/dev/null 2>&1 || :
        if [ "$service_exists" -eq 1 ] && [ "$service_was_enabled" -eq 1 ]; then
            systemctl --user enable "$SERVICE_NAME" >/dev/null 2>&1 || :
        else
            systemctl --user disable "$SERVICE_NAME" >/dev/null 2>&1 || :
        fi
        if [ "$service_was_active" -eq 1 ]; then
            restart_old_service=1
        fi
    fi
    # --- END service ---
    # --- BEGIN update ---
    if [ "$release_url_changed" -eq 1 ]; then
        if [ "$release_url_exists" -eq 1 ] && [ -n "$old_release_url_file" ] && [ -s "$old_release_url_file" ]; then
            mv -f "$old_release_url_file" "$RELEASE_URL_FILE" || errf '   Error: Failed to restore release URL file'
            rb=1
        elif [ "$release_url_exists" -eq 0 ] && [ -f "$RELEASE_URL_FILE" ]; then
            rm -f "$RELEASE_URL_FILE" || errf '   Error: Failed to remove new release URL file'
            rb=1
        fi
    fi
    # --- END update ---
    if [ "$cached_installer_changed" -eq 1 ]; then
        if ! restore_cached_installer; then
            errf '   Error: Failed to restore cached maintenance installer'
        else
            rb=1
        fi
    fi
    if ! restore_state; then
        errf '   Error: Failed to restore previous lifecycle state'
    fi
    # Never start a service while holding the exclusive lifecycle lock.
    [ -n "${lifecycle_lock_acquired:-}" ] && release_lifecycle_lock || :
    # --- BEGIN service ---
    if [ "$restart_old_service" -eq 1 ]; then
        systemctl --user start "$SERVICE_NAME" >/dev/null 2>&1 || :
    fi
    # --- END service ---
    if [ "$rb" -eq 1 ]; then printf 'Rolled back to previous version.\n'; fi
}


on_exit () {
    code=$?
    if [ "$code" -ne 0 ]; then
        if [ "$migration_started" -eq 0 ]; then
            rollback
        else
            errf 'Migration was invoked; retaining the new lifecycle state for recovery.'
        fi
    fi
    [ -n "$temp_dir" ] && [ -d "$temp_dir" ] && rm -rf "$temp_dir"
}

trap on_exit EXIT
trap 'exit 129' HUP   # 128+1
trap 'exit 130' INT   # 128+2
trap 'exit 131' QUIT  # 128+3
trap 'exit 141' PIPE  # 128+13
trap 'exit 143' TERM  # 128+15


# Platform Checks -------------------------------------------------------------
uname_s=$(uname -s)
uname_m=$(uname -m)

# OS
[ "$uname_s" = "Linux" ] || fatalf 'This application is only supported on Linux. Detected OS: %s' "$uname_s"
# Disallow root
[ "$(id -u)" -ne 0 ] || fatalf 'Running as root is unsafe. Please run as a non-root user.'

if [ "$MODE" != "uninstall" ]; then
    OFFICIAL_RELEASE_URL=$(normalize_release_url "$RELEASE_URL") ||
        fatalf 'Baked release URL is empty or invalid'
    RELEASE_URL=$(normalize_release_url "${APP_RELEASE_URL:-$RELEASE_URL}") ||
        fatalf 'Release URL is empty or invalid'

    # Architecture matters only when selecting downloaded assets. The cached
    # uninstall remains usable offline even on a machine whose architecture
    # can no longer run the installed binary.
    case "$uname_m" in
        x86_64|amd64)
            BIN_ASSET_NAME="linux-amd64.gz"
            COSIGN_ASSET="cosign-linux-amd64"
            COSIGN_SHA="$COSIGN_SHA_LINUX_AMD64"
            ;;
        aarch64|arm64)
            BIN_ASSET_NAME="linux-arm64.gz"
            COSIGN_ASSET="cosign-linux-arm64"
            COSIGN_SHA="$COSIGN_SHA_LINUX_ARM64"
            ;;
        *) fatalf 'This application is only supported on x86_64/amd64 or aarch64/arm64. Detected architecture: %s' "$uname_m" ;;
    esac
fi

# Immutable/image-based root detection: no usable host package manager, so
# pointing the user at dnf/apt/zypper would be a dead end.
#   - /run/ostree-booted: Fedora Silverblue/Kinoite, Bazzite, Bluefin, Aurora, SteamOS-likes
#   - transactional-update: openSUSE MicroOS, Aeon, Kalpa
is_immutable_root() {
    [ -f /run/ostree-booted ] && return 0
    command -v transactional-update >/dev/null 2>&1 && return 0
    return 1
}

# Dependencies (cosign is NOT required: it is bootstrapped below if missing)
missing=''
for bin in mktemp sed awk flock stat od date sync readlink; do
    command -v "$bin" >/dev/null 2>&1 || missing="${missing}${missing:+ }$bin"
done
if [ "$MODE" != "uninstall" ]; then
    for bin in curl gzip install sha256sum; do
        command -v "$bin" >/dev/null 2>&1 || missing="${missing}${missing:+ }$bin"
    done
fi
if [ -n "$missing" ]; then
    if is_immutable_root; then
        fatalf 'Missing required tools: %s\nThis looks like an immutable/image-based system. Install them into your home with:\n    brew install %s\nor use a distrobox/toolbox container.' "$missing" "$missing"
    fi
    fatalf 'Missing required tools: %s\nPlease install them and try again.' "$missing"
fi

ensure_lifecycle_layout
acquire_operation_lock

if [ "$MODE" = "uninstall" ]; then
    run_uninstall
    exit 0
fi

[ -f "$APP_BIN" ] && app_bin_exists=1 && fresh_install=0

load_state
if [ "$MODE" = "update" ]; then
    if ! validate_job_expectations update; then
        fatalf 'The --update mode must be admitted with expected installation epoch and version'
    fi
    transaction_phase=updating
    transaction_epoch=$state_epoch
else
    case "$state_phase" in
        ""|uninstalled)
            transaction_phase=installing
            transaction_epoch=$(generate_token) || fatalf 'Failed to generate installation epoch'
            ;;
        ready)
            transaction_phase=updating
            transaction_epoch=$state_epoch
            ;;
        installing|updating)
            # A direct installer invocation is the recovery path for a failed
            # install/update. Preserve the installation lifetime.
            transaction_phase=$state_phase
            transaction_epoch=$state_epoch
            recovering_transition=1
            ;;
        uninstalling)
            fatalf 'Uninstall is already in progress; run the cached installer with --uninstall to finish it'
            ;;
    esac
fi

# --- BEGIN service ---
# Service pre-checks ----------------------------------------------------------
# Non-systemd distros (Alpine/openrc, Void/runit, Artix, Devuan, ...) and
# environments where systemd --user is broken (some WSL setups) degrade
# gracefully: install the binary, skip the service.
if [ "$SERVICE" = "true" ]; then
    systemdVersion=''
    if command -v systemctl >/dev/null 2>&1; then
        systemdVersion=$(systemctl --user --version 2>/dev/null \
            | awk 'NR==1 {print $2}' \
            | sed 's/^\([0-9][0-9]*\).*/\1/')
    fi
    if [ -z "$systemdVersion" ]; then
        warnf 'systemd --user not available; installing binary only (no background service).'
        warnf 'Run the service manually with: %s service run' "$APP_NAME"
        SERVICE="false"
    elif [ "$systemdVersion" -lt 246 ]; then
        # 246 is needed for used unit features
        warnf 'systemd >= 246 required for the service (found %s); installing binary only.' "$systemdVersion"
        SERVICE="false"
    elif ! systemctl --user daemon-reload >/dev/null 2>&1; then
        warnf 'systemctl --user is not functional (common in WSL). Skipping service setup.'
        SERVICE="false"
    fi
fi

if [ "$SERVICE" = "true" ]; then
    # track prior state
    if systemctl --user cat "$SERVICE_NAME" >/dev/null 2>&1; then
        service_exists=1
        fresh_install=0
        if systemctl --user is-enabled --quiet "$SERVICE_NAME"; then service_was_enabled=1; fi
        if systemctl --user is-active  --quiet "$SERVICE_NAME"; then service_was_active=1; fi
    fi
fi
# --- END service ---

# Create directories ---------------------------------------------------------

ensure_private_dir "$APP_DATA_DIR" "application data directory"
# --- BEGIN service ---
mkdir -p "$(dirname "$SERVICE_FILE")" || { rc=$?; fatalf 'failed to create service dir (rc=%d)' "$rc"; }
# --- END service ---

# Download -------------------------------------------------------------------
# fetch [curl args...]: every download shares one retry and timeout policy.
fetch() {
    curl -sS --fail --location --show-error --connect-timeout 5 \
        --retry-all-errors --retry 3 --retry-delay 1 --max-time 300 "$@"
}
ver_url="${RELEASE_URL}version"

# Read the mutable root pointer exactly once, validate it before using it as a
# path component, and pin every remaining request to its immutable prefix.
pinned_version=$(fetch "$ver_url") ||
    { rc=$?; fatalf 'Download of version pointer failed (rc=%d)' "$rc"; }
pinned_version=$(printf '%s' "$pinned_version" | tr -d '\r\n')
validate_version "$pinned_version" ||
    fatalf 'Release host returned an invalid version pointer: %s' "$pinned_version"
release_prefix="${RELEASE_URL}releases/${pinned_version}/"
bin_url="${release_prefix}${BIN_ASSET_NAME}"
release_ver_url="${release_prefix}version"
checksums_url="${release_prefix}checksums.txt"
bundle_url="${release_prefix}checksums.txt.cosign.bundle"
installer_url="${RELEASE_URL}install.sh"
installer_bundle_url="${RELEASE_URL}install.sh.cosign.bundle"

# make temp dir
temp_dir=$(mktemp -d) || { rc=$?; fatalf 'failed to create temp dir (rc=%d)' "$rc"; }

# output paths
dwld_out="$temp_dir/$BIN_ASSET_NAME"
version_out="$temp_dir/version"
checksums_out="$temp_dir/checksums.txt"
bundle_out="$temp_dir/checksums.txt.cosign.bundle"
installer_out="$temp_dir/install.sh"
installer_bundle_out="$temp_dir/install.sh.cosign.bundle"
gzip_out=${dwld_out%".gz"}

# print install header
INSTALL_SYMBOL=''
case $(printf %s "${LC_ALL:-${LANG:-}}" | tr '[:upper:]' '[:lower:]') in
  *utf-8*|*utf8*) [ -t 1 ] && INSTALL_SYMBOL='📦 ' ;;
esac
if [ "$MODE" = "update" ]; then
    printf '%sUpdating %s to %s ...\n' "$INSTALL_SYMBOL" "$APP_NAME" "$pinned_version"
else
    printf '%sInstalling %s %s ...\n' "$INSTALL_SYMBOL" "$APP_NAME" "$pinned_version"
fi

# download the pinned release files
printf 'Downloading binary ...\n'
fetch -o "$dwld_out" "$bin_url" || { rc=$?; fatalf 'Download of binary failed (rc=%d)' "$rc"; }
printf 'Downloading release version ...\n'
fetch -o "$version_out" "$release_ver_url" || { rc=$?; fatalf 'Download of release version failed (rc=%d)' "$rc"; }
printf 'Downloading checksums ...\n'
fetch -o "$checksums_out" "$checksums_url" || { rc=$?; fatalf 'Download of checksums failed (rc=%d)' "$rc"; }
printf 'Downloading maintenance installer ...\n'
fetch -o "$installer_out" "$installer_url" || { rc=$?; fatalf 'Download of maintenance installer failed (rc=%d)' "$rc"; }

# ensure_cosign: use cosign from PATH if present, otherwise install the pinned
# release into ~/.local/bin, verifying it against the sha256 baked in at
# release time. Sets COSIGN_BIN.
COSIGN_BIN=''
ensure_cosign() {
    managed_cosign="$HOME/.local/bin/cosign"
    if command -v cosign >/dev/null 2>&1; then
        candidate_cosign=$(command -v cosign)
        if [ "$candidate_cosign" != "$managed_cosign" ]; then
            COSIGN_BIN="$candidate_cosign"
            return 0
        fi
    fi
    if [ -f "$managed_cosign" ]; then
        managed_actual=$(sha256sum "$managed_cosign" | awk '{print $1}' | tr -d '\r\n')
        if [ "$managed_actual" = "$COSIGN_SHA" ]; then
            COSIGN_BIN="$managed_cosign"
            return 0
        fi
        warnf 'Cached managed cosign failed checksum verification; replacing it.'
    fi
    printf 'Installing cosign %s to %s ...\n' "$COSIGN_VERSION" "$managed_cosign"
    cosign_dwld="$temp_dir/$COSIGN_ASSET"
    cosign_url="https://github.com/sigstore/cosign/releases/download/${COSIGN_VERSION}/${COSIGN_ASSET}"
    fetch -o "$cosign_dwld" "$cosign_url" || { rc=$?; fatalf 'Download of cosign failed (rc=%d)' "$rc"; }
    cosign_actual=$(sha256sum "$cosign_dwld" | awk '{print $1}' | tr -d '\r\n')
    [ "$cosign_actual" = "$COSIGN_SHA" ] || fatalf 'cosign checksum mismatch! Expected %s, got %s' "$COSIGN_SHA" "$cosign_actual"
    staged_cosign="$HOME/.local/bin/.cosign.$$.tmp"
    install -Dm755 "$cosign_dwld" "$staged_cosign" || { rc=$?; fatalf 'Failed to stage cosign (rc=%d)' "$rc"; }
    mv -f "$staged_cosign" "$managed_cosign" || { rc=$?; rm -f "$staged_cosign"; fatalf 'Failed to install cosign (rc=%d)' "$rc"; }
    COSIGN_BIN="$managed_cosign"
}

publish_cached_installer() {
    printf 'Refreshing cached maintenance installer ...\n'
    cached_installer_changed=1
    cached_bundle_tmp="$MAINTENANCE_DIR/.install.sh.cosign.bundle.$$.tmp"
    install -m 600 "$installer_bundle_out" "$cached_bundle_tmp" ||
        fatalf 'Failed to stage cached installer signature bundle'
    mv -f "$cached_bundle_tmp" "$CACHED_INSTALLER_BUNDLE" || {
        rm -f "$cached_bundle_tmp"
        fatalf 'Failed to publish cached installer signature bundle'
    }
    cached_installer_tmp="$MAINTENANCE_DIR/.install.sh.$$.tmp"
    install -m 700 "$installer_out" "$cached_installer_tmp" ||
        fatalf 'Failed to stage cached maintenance installer'
    mv -f "$cached_installer_tmp" "$CACHED_INSTALLER" || {
        rm -f "$cached_installer_tmp"
        fatalf 'Failed to publish cached maintenance installer'
    }
    sync || fatalf 'Failed to durably publish cached maintenance installer'
}

restore_cached_installer() {
    [ "$cached_installer_changed" -eq 1 ] || return 0
    if [ "$cached_bundle_exists" -eq 1 ]; then
        restore_bundle_tmp="$MAINTENANCE_DIR/.install.sh.cosign.bundle.restore.$$.tmp"
        install -m 600 "$old_cached_bundle" "$restore_bundle_tmp" || return 1
        mv -f "$restore_bundle_tmp" "$CACHED_INSTALLER_BUNDLE" || { rm -f "$restore_bundle_tmp"; return 1; }
    else
        rm -f "$CACHED_INSTALLER_BUNDLE" || return 1
    fi
    if [ "$cached_installer_exists" -eq 1 ]; then
        restore_installer_tmp="$MAINTENANCE_DIR/.install.sh.restore.$$.tmp"
        install -m 700 "$old_cached_installer" "$restore_installer_tmp" || return 1
        mv -f "$restore_installer_tmp" "$CACHED_INSTALLER" || { rm -f "$restore_installer_tmp"; return 1; }
    else
        rm -f "$CACHED_INSTALLER" || return 1
    fi
    sync || return 1
    cached_installer_changed=0
}

# verify the checksums signature (cosign keyless: signature + cert + Rekor
# proof travel in the bundle; trust is pinned to the CI workflow identity)
if [ "$SKIP_VERIFY" = "true" ] || [ "$SKIP_VERIFY" = "1" ]; then
    warnf 'APP_SKIP_VERIFY set: SKIPPING cosign signature verification. Testing only!'
    # Test fixtures do not have to publish a real bundle. Keep a placeholder so
    # the cached layout is complete; a real launcher/cosign will still reject it.
    fetch -o "$installer_bundle_out" "$installer_bundle_url" >/dev/null 2>&1 ||
        (umask 077; : > "$installer_bundle_out")
else
    ensure_cosign
    printf 'Downloading checksums signature ...\n'
    fetch -o "$bundle_out" "$bundle_url" || { rc=$?; fatalf 'Download of checksums signature failed (rc=%d)' "$rc"; }
    printf 'Downloading maintenance installer signature ...\n'
    fetch -o "$installer_bundle_out" "$installer_bundle_url" || { rc=$?; fatalf 'Download of maintenance installer signature failed (rc=%d)' "$rc"; }
    printf 'Verifying checksums signature ...\n'
    cosign_out=$("$COSIGN_BIN" verify-blob \
        --bundle "$bundle_out" \
        --certificate-identity "$CERT_IDENTITY" \
        --certificate-oidc-issuer "$OIDC_ISSUER" \
        "$checksums_out" 2>&1) || fatalf 'Signature verification of checksums.txt failed:\n%s' "$cosign_out"
    printf 'Verifying maintenance installer signature ...\n'
    cosign_out=$("$COSIGN_BIN" verify-blob \
        --bundle "$installer_bundle_out" \
        --certificate-identity "$CERT_IDENTITY" \
        --certificate-oidc-issuer "$OIDC_ISSUER" \
        "$installer_out" 2>&1) || fatalf 'Signature verification of install.sh failed:\n%s' "$cosign_out"
fi

# verify checksum of the downloaded binary against the (verified) checksums.txt
printf 'Verifying checksum ...\n'
expected_sum=$(awk -v f="$BIN_ASSET_NAME" '$2 == f {print $1; exit}' "$checksums_out" | tr -d '\r\n')
[ ${#expected_sum} -eq 64 ] || fatalf 'No valid checksum for %s in checksums.txt' "$BIN_ASSET_NAME"
actual_sum=$(sha256sum "$dwld_out" | awk '{print $1}' | tr -d '\r\n')
[ -n "$actual_sum" ] || fatalf 'Failed to compute hash of downloaded file'
[ "$expected_sum" = "$actual_sum" ] || fatalf 'Checksum mismatch! Expected %s, got %s' "$expected_sum" "$actual_sum"

expected_version_sum=$(awk '$2 == "version" {print $1; exit}' "$checksums_out" | tr -d '\r\n')
[ ${#expected_version_sum} -eq 64 ] || fatalf 'No valid checksum for version in checksums.txt'
actual_version_sum=$(sha256sum "$version_out" | awk '{print $1}' | tr -d '\r\n')
[ "$expected_version_sum" = "$actual_version_sum" ] ||
    fatalf 'Version checksum mismatch! Expected %s, got %s' "$expected_version_sum" "$actual_version_sum"
signed_version=$(tr -d '\r\n' < "$version_out")
[ "$signed_version" = "$pinned_version" ] ||
    fatalf 'Pinned version %s does not match signed release version %s' "$pinned_version" "$signed_version"

# unzip
printf 'Unzipping ...\n'
gzip -dc "$dwld_out" > "$gzip_out" || { rc=$?; fatalf 'Failed to unzip (rc=%d)' "$rc"; }
chmod 700 "$gzip_out" || { rc=$?; fatalf 'Failed to make staged candidate executable (rc=%d)' "$rc"; }

# Preflight the staged binary before stopping any running installation.
candidate_build_vars=$("$gzip_out" --build-vars 2>&1) ||
    fatalf 'Staged candidate --build-vars failed:\n%s' "$candidate_build_vars"
candidate_name=$(printf '%s' "$candidate_build_vars" | sed -n 's/.*"name":"\([^"]*\)".*/\1/p')
candidate_version=$(printf '%s' "$candidate_build_vars" | sed -n 's/.*"version":"\([^"]*\)".*/\1/p')
[ "$candidate_name" = "$APP_NAME" ] ||
    fatalf 'Staged candidate name %s does not match installer name %s' "$candidate_name" "$APP_NAME"
[ "$candidate_version" = "$pinned_version" ] ||
    fatalf 'Staged candidate version %s does not match signed version %s' "$candidate_version" "$pinned_version"
# --- BEGIN service ---
if [ "$SERVICE" = "true" ]; then
    : # Retain a valid service-only block when service.https is cut.
    # --- BEGIN service.https ---
    default_port=$(printf '%s' "$candidate_build_vars" | sed -n 's/.*"serviceDefaultPort":\([0-9]*\).*/\1/p')
    [ -n "$default_port" ] ||
        fatalf 'Failed to parse default port from staged candidate build vars:\n%s' "$candidate_build_vars"
    if [ "$fresh_install" -eq 1 ] && port_in_use "$default_port"; then
        fatalf 'Default port %d is already in use.\nFree it for the initial installation. After installation, persist a different listener with:\n    %s config set --ui-bind 127.0.0.1:<port>' "$default_port" "$APP_NAME"
    fi
    # --- END service.https ---
fi
# --- END service ---

# Backup (for rollback) -------------------------------------------------------
if [ -f "$APP_BIN" ] || [ "$service_exists" -eq 1 ]; then
    printf 'Backing up current installation ...\n'
fi

if [ -f "$APP_BIN" ]; then
    old_app_bin="$temp_dir/$APP_NAME.old"
    cp -f "$APP_BIN" "$old_app_bin" || { rc=$?; fatalf 'Failed to backup existing binary (rc=%d)' "$rc"; }
fi

# --- BEGIN service ---
if [ "$SERVICE" = "true" ] && [ "$service_exists" -eq 1 ]; then
    old_service_file="$temp_dir/$SERVICE_NAME.old"
    systemctl --user cat "$SERVICE_NAME" > "$old_service_file" || { rc=$?; fatalf 'Failed to backup existing service unit file (rc=%d)' "$rc"; }
fi
# --- END service ---

# --- BEGIN update ---
if [ -f "$RELEASE_URL_FILE" ]; then
    release_url_exists=1
    old_release_url_file="$temp_dir/release-url.old"
    cp -f "$RELEASE_URL_FILE" "$old_release_url_file" || { rc=$?; fatalf 'Failed to backup existing release URL file (rc=%d)' "$rc"; }
fi
# --- END update ---

if [ -L "$CACHED_INSTALLER" ] || [ -L "$CACHED_INSTALLER_BUNDLE" ]; then
    fatalf 'Cached maintenance installer paths must not be symlinks'
fi
if { [ -e "$CACHED_INSTALLER" ] && [ ! -f "$CACHED_INSTALLER" ]; } ||
   { [ -e "$CACHED_INSTALLER_BUNDLE" ] && [ ! -f "$CACHED_INSTALLER_BUNDLE" ]; }; then
    fatalf 'Cached maintenance installer paths must be regular files'
fi
if [ -f "$CACHED_INSTALLER" ]; then
    cached_installer_exists=1
    old_cached_installer="$temp_dir/install.sh.old"
    cp -f "$CACHED_INSTALLER" "$old_cached_installer" || fatalf 'Failed to back up cached maintenance installer'
fi
if [ -f "$CACHED_INSTALLER_BUNDLE" ]; then
    cached_bundle_exists=1
    old_cached_bundle="$temp_dir/install.sh.cosign.bundle.old"
    cp -f "$CACHED_INSTALLER_BUNDLE" "$old_cached_bundle" || fatalf 'Failed to back up cached installer signature bundle'
fi

# Publish transition and stop running instances -------------------------------
# Backing up the state lets pre-migration failures restore a usable ready state.
# Once migration is invoked, recovery deliberately remains fail-closed in this
# transition, with its nonce authorizing only this installer invocation.
backup_state
migration_nonce=$(generate_token) || fatalf 'Failed to generate migration nonce'
state_transition_written=1
write_state "$transaction_phase" "$state_version" "$pinned_version" "$migration_nonce" "$transaction_epoch"

# --- BEGIN service ---
if [ "$SERVICE" = "true" ] && [ "$service_exists" -eq 1 ] && [ "$service_was_active" -eq 1 ]; then
    service_touched=1
    printf 'Stopping active service ...\n'
    systemctl --user stop --no-block "$SERVICE_NAME" || fatalf 'Failed to request active service stop'
fi
# --- END service ---

drain_instances
acquire_lifecycle_lock

# The installer script is the recovery tool. Publish its verified bundle first
# and the script last as the pair's commit point while lifecycle is exclusive.
publish_cached_installer

# Install ---------------------------------------------------------------------
printf 'Writing binary to %s ...\n' "$APP_BIN"
binary_changed=1
install -Dm755 "$gzip_out" "$APP_BIN" || { rc=$?; fatalf 'Failed to install binary (rc=%d)' "$rc"; }

# --- BEGIN update ---
# The release-url file enables update checks and self-update. It is only
# written for installs from the official release URL:
# mirror installs (APP_RELEASE_URL override) must not self-update, since that
# would execute the mirror's install.sh.
# See docs/content/docs/getting-started/mirror.md.
if [ "$RELEASE_URL" = "$OFFICIAL_RELEASE_URL" ]; then
    printf 'Writing release source to %s ...\n' "$RELEASE_URL_FILE"
    release_url_changed=1
    printf '%s\n' "$RELEASE_URL" > "$RELEASE_URL_FILE" || { rc=$?; fatalf 'Failed to write release URL file (rc=%d)' "$rc"; }
elif [ -f "$RELEASE_URL_FILE" ]; then
    printf 'Mirror install: removing release source file (disables in-app updates) ...\n'
    release_url_changed=1
    rm -f "$RELEASE_URL_FILE" || { rc=$?; fatalf 'Failed to remove release URL file (rc=%d)' "$rc"; }
fi
# --- END update ---

# --- BEGIN service ---
# Install the service definition before migration so the binary, release
# source, and service state cross the migration boundary together.
if [ "$SERVICE" = "true" ]; then
    service_touched=1
    [ "$service_exists" -eq 1 ] && printf 'Updating service ...\n' || printf 'Setting up service ...\n'

    # The user manager expands %h after parsing. Emitting canonical paths in
    # that form avoids serializing special characters from the account home.
    safe_args=$(printf '%s' "$SERVICE_ARGS" | sed 's/%/%%/g') || fatalf 'Failed to escape service args'

    # write unit file
    {
        printf '%s\n' "[Unit]"
        printf 'Description=%s\n' "$SERVICE_DESC"
        printf '%s\n' "StartLimitIntervalSec=600"
        printf '%s\n' "StartLimitBurst=5"
        printf '%s\n' ""
        printf '%s\n' "[Service]"
        printf '%s\n' "Type=notify"
        printf 'ExecStart=%%h/.local/bin/%s %s\n' "$APP_NAME" "$safe_args"
        printf 'WorkingDirectory=%%h/.%s/data\n' "$APP_NAME"
        printf '%s\n' "Restart=always"
        printf '%s\n' "RestartSec=1"
        printf '%s\n' "LimitNOFILE=65535"
        printf 'TimeoutStartSec=%ss\n' "$SERVICE_READY_TIMEOUT_SECONDS"
        printf '%s\n' "RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK"
        # /home/linuxbrew is where Homebrew lives on Linux; on image-based
        # systems it is often the only place user-installed tools exist.
        printf '%s\n' "Environment=PATH=%h/.local/bin:/home/linuxbrew/.linuxbrew/bin:/usr/local/bin:/usr/bin:/bin"
        printf 'EnvironmentFile=-%%h/.%s/data/%s.env\n' "$APP_NAME" "$APP_NAME"
        printf '%s\n' ""
        printf '%s\n' "[Install]"
        printf '%s\n' "WantedBy=default.target"
    } > "$SERVICE_FILE" || fatalf 'Failed to write service unit file'

    systemctl --user daemon-reload || { rc=$?; fatalf 'Failed to reload systemd daemon (rc=%d)' "$rc"; }

    if [ "$service_exists" -eq 1 ]; then
        if [ "$service_was_enabled" -eq 1 ]; then
            systemctl --user enable "$SERVICE_NAME" || { rc=$?; fatalf 'Failed to re-enable service (rc=%d)' "$rc"; }
            systemctl --user reset-failed "$SERVICE_NAME" || :
        else
            systemctl --user disable "$SERVICE_NAME" || { rc=$?; fatalf 'Failed to re-disable service (rc=%d)' "$rc"; }
        fi
    else
        systemctl --user enable "$SERVICE_NAME" || { rc=$?; fatalf 'Failed to enable service (rc=%d)' "$rc"; }
        systemctl --user reset-failed "$SERVICE_NAME" || :
    fi
fi
# --- END service ---

printf 'Verifying installation (this may take a few moments if migrating) ...\n'
migration_started=1
out=$(APP_MAINTENANCE_NONCE="$migration_nonce" "$APP_BIN" --migrate 2>&1) ||
    fatalf '%s --migrate failed:\n%s' "$APP_BIN" "$out"
effective_version=$(printf '%s\n' "$out" | awk 'NR==1{print $NF; exit}') ||
    fatalf 'Failed to parse version from:\n%s' "$out"
[ "$effective_version" = "$pinned_version" ] ||
    fatalf 'Migration reported version %s, expected %s' "$effective_version" "$pinned_version"
write_state ready "$effective_version" "" "" "$transaction_epoch"
state_transition_written=0

# The ready state is committed while exclusive; instances may start only after
# the lock is released.
release_lifecycle_lock

# --- BEGIN service ---
# Start only after releasing the exclusive lifecycle lock.
if [ "$SERVICE" = "true" ]; then
    if [ "$service_exists" -eq 1 ]; then
        if [ "$service_was_active" -eq 1 ] ||
           { [ "$recovering_transition" -eq 1 ] && [ "$service_was_enabled" -eq 1 ]; }; then
            printf "Restarting service ...\n"
            systemctl --user start "$SERVICE_NAME" || { rc=$?; fatalf 'Failed to start service (rc=%d)' "$rc"; }
        else
            printf "Service updated; leaving it stopped (was inactive).\n"
        fi
    else
        printf "Starting service ...\n"
        systemctl --user start "$SERVICE_NAME" || { rc=$?; fatalf 'Failed to start service (rc=%d)' "$rc"; }
    fi

    if ! loginctl show-user "$USER_NAME" 2>/dev/null | grep -q 'Linger=yes'; then
       warnf 'If you want the service to run when you are not logged in, run:'
       warnf '    sudo loginctl enable-linger %s' "$USER_NAME"
    fi
fi
# --- END service ---

# Add to PATH -----------------------------------------------------------------
MARK_OPEN='# >>> PATH bootstrap: ~/.local/bin >>>'
MARK_CLOSE='# <<< PATH bootstrap <<<'
# shellcheck disable=SC2016 # literal shell text written into the user's profile
PATH_BLOCK='if [ -d "$HOME/.local/bin" ]; then
  case ":$PATH:" in
    *":$HOME/.local/bin:"*) : ;;
    *) PATH="$HOME/.local/bin:$PATH" ;;
  esac
fi
export PATH'

write_path_block() {
  printf '%s\n%s\n%s\n' "$MARK_OPEN" "$PATH_BLOCK" "$MARK_CLOSE"
}

# Append the PATH block to an existing shell profile if not already present.
# The optional second argument permits creating a missing profile. Creation is
# staged beside the target and published with a no-clobber hard link, so an
# interrupted write never exposes a partial profile. Explicit symlinks and
# non-regular files are rejected. All failures remain best-effort so a managed
# or immutable profile does not fail an otherwise successful install.
add_path_block() {
  tgt=$1
  create_missing=${2:-false}

  if [ -L "$tgt" ]; then
    warnf 'Cannot update symlink %s; add ~/.local/bin to PATH yourself.' "$tgt"
    return 0
  fi
  if [ -e "$tgt" ] && [ ! -f "$tgt" ]; then
    warnf 'Cannot update non-regular file %s; add ~/.local/bin to PATH yourself.' "$tgt"
    return 0
  fi
  if [ ! -e "$tgt" ]; then
    [ "$create_missing" = "true" ] || return 0
    if (
      umask 077
      staged_profile=$(mktemp "$HOME/.profile.sprout.XXXXXX") || exit 1
      trap 'rm -f "$staged_profile"' 0
      trap 'exit 1' HUP INT QUIT PIPE TERM
      write_path_block >"$staged_profile" || exit 1
      ln "$staged_profile" "$tgt" 2>/dev/null
    ) 2>/dev/null; then
      return 0
    fi
    warnf 'Cannot safely create %s; add ~/.local/bin to PATH yourself.' "$tgt"
    return 0
  fi
  # Only a complete, ordered marker pair proves an earlier append finished.
  if awk -v o="$MARK_OPEN" -v c="$MARK_CLOSE" \
      'index($0,o){if(pending) invalid=1; pending=1; seen=1}
       index($0,c){if(!pending) invalid=1; pending=0}
       END{exit (seen && !pending && !invalid)?0:1}' "$tgt"; then
    return 0
  fi
  # An opening marker without its close may contain partial shell syntax. Do
  # not append after or rewrite user-owned profile content automatically.
  if awk -v o="$MARK_OPEN" 'index($0,o){found=1} END{exit found?0:1}' "$tgt"; then
    warnf 'Found an incomplete PATH bootstrap in %s; repair it manually.' "$tgt"
    return 0
  fi
  [ -w "$tgt" ] || { warnf 'Cannot write to %s; add ~/.local/bin to PATH yourself.' "$tgt"; return 0; }
  # append the block
  printf '\n%s\n%s\n%s\n' "$MARK_OPEN" "$PATH_BLOCK" "$MARK_CLOSE" >>"$tgt" ||
    warnf 'Failed appending PATH block to %s' "$tgt"
}

# The current process PATH may be a one-shot override, so it cannot prove that
# a fresh login will retain ~/.local/bin. Always install an idempotent bootstrap
# in the available shell profiles and ensure the POSIX login profile exists.
add_path_block "$HOME/.bashrc"
add_path_block "$HOME/.zshrc"
add_path_block "$HOME/.profile" true
add_path_block "$HOME/.bash_profile"

# Success! --------------------------------------------------------------------
if [ "$MODE" = "update" ]; then
  successf 'Updated: %s (%s)' "$APP_NAME" "$effective_version"
else
  successf 'Installed: %s (%s)' "$APP_NAME" "$effective_version"
fi
# shellcheck disable=SC2016 # the $SHELL is advice for the user to type, not to expand here
warnf    'Open a new terminal or refresh this one with: exec "$SHELL" -l || exec sh -l'
successf '    Run:       %s -h     # for help' "$APP_NAME"
# --- BEGIN service ---
if [ "$SERVICE" = "true" ]; then
  successf '    Run:       %s service  # for service management cheat sheet' "$APP_NAME"
fi
# --- END service ---
