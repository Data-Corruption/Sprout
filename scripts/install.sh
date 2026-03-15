#!/bin/sh

# Target: ~POSIX Linux x86_64/amd64 or aarch64/arm64, user-level install, optional systemd --user unit
# Requires: curl, gzip, mktemp, install, sha256sum, sed, awk, (and systemd if SERVICE=true)
# Example: curl -fsSL https://cd.example.com/release/install.sh | sh

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

APP_BIN="$HOME/.local/bin/$APP_NAME"
APP_DATA_DIR="$HOME/.$APP_NAME"
APP_ENV_FILE="$APP_DATA_DIR/$APP_NAME.env"
RELEASE_URL_FILE="$APP_DATA_DIR/release-url"

SERVICE_NAME="$APP_NAME.service"
SERVICE_FILE="$HOME/.config/systemd/user/$SERVICE_NAME"
SERVICE_READY_TIMEOUT_SECONDS=90

RUNTIME_DIR="${XDG_RUNTIME_DIR}"
RUNTIME_DIR="${RUNTIME_DIR:-/tmp/${APP_NAME}-${USER}}" # fallback
INSTANCES_DIR="$RUNTIME_DIR/$APP_NAME/instances"
LOCK_FILE="$RUNTIME_DIR/$APP_NAME/migrate.lock"

# Globals used by rollback/cleanup --------------------------------------------
temp_dir=""
old_app_bin=""
old_service_file=""
old_release_url_file=""
service_exists=0
service_was_enabled=0
service_was_active=0
release_url_exists=0
default_port=""

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

normalize_release_url() {
    normalized=$(printf '%s' "$1" | tr -d '\r' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//; s#/*$##')
    [ -n "$normalized" ] || return 1
    printf '%s/\n' "$normalized"
}

# Check if a port is in use. Returns 0 if in use, 1 if free.
port_in_use() {
    port=$1
    # Try multiple methods for portability
    if command -v ss >/dev/null 2>&1; then
        ss -tlnH 2>/dev/null | awk '{print $4}' | grep -qE "(:|^)${port}$"
    elif command -v netstat >/dev/null 2>&1; then
        netstat -tln 2>/dev/null | awk '{print $4}' | grep -qE "(:|^)${port}$"
    else
        # Fallback: try to bind briefly (requires timeout or will block)
        # This is less reliable, so we just return "not in use" if we can't check
        return 1
    fi
}

rollback() {
    rb=0
    if [ -n "$old_app_bin" ] && [ -s "$old_app_bin" ]; then
        printf 'Restoring previous installation...\n'
        mv -f "$old_app_bin" "$APP_BIN" || errf '   Error: Failed to restore old binary'
        rb=1
    fi
    if [ "$SERVICE" = "true" ] && [ "$service_exists" -eq 1 ]; then
        systemctl --user stop "$SERVICE_NAME" >/dev/null 2>&1 || :
        systemctl --user reset-failed "$SERVICE_NAME" >/dev/null 2>&1 || :
        if [ -n "$old_service_file" ] && [ -s "$old_service_file" ]; then
            printf 'Restoring previous service configuration ...\n'
            mv -f "$old_service_file" "$SERVICE_FILE" || errf '   Error: Failed to restore old service unit file'
            rb=1
        fi
        systemctl --user daemon-reload >/dev/null 2>&1 || :
        if [ "$service_was_enabled" -eq 1 ]; then
            systemctl --user enable "$SERVICE_NAME" >/dev/null 2>&1 || :
        else
            systemctl --user disable "$SERVICE_NAME" >/dev/null 2>&1 || :
        fi
        if [ "$service_was_active" -eq 1 ]; then
            systemctl --user start "$SERVICE_NAME" >/dev/null 2>&1 || :
        fi
    fi
    if [ "$release_url_exists" -eq 1 ] && [ -n "$old_release_url_file" ] && [ -s "$old_release_url_file" ]; then
        mv -f "$old_release_url_file" "$RELEASE_URL_FILE" || errf '   Error: Failed to restore release URL file'
        rb=1
    elif [ "$release_url_exists" -eq 0 ] && [ -f "$RELEASE_URL_FILE" ]; then
        rm -f "$RELEASE_URL_FILE" || errf '   Error: Failed to remove new release URL file'
        rb=1
    fi
    if [ "$rb" -eq 1 ]; then printf 'Rolled back to previous version.\n'; fi
}


on_exit () {
    code=$?
    [ "$code" -ne 0 ] && rollback
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
RELEASE_URL=$(normalize_release_url "${APP_RELEASE_URL:-$RELEASE_URL}") ||
    fatalf 'Release URL is empty or invalid'

# OS
[ "$uname_s" = "Linux" ] || fatalf 'This application is only supported on Linux. Detected OS: %s' "$uname_s"
# Architecture
case "$uname_m" in
    x86_64|amd64) BIN_ASSET_NAME="linux-amd64.gz" ;;
    aarch64|arm64) BIN_ASSET_NAME="linux-arm64.gz" ;;
    *) fatalf 'This application is only supported on x86_64/amd64 or aarch64/arm64. Detected architecture: %s' "$uname_m" ;;
esac
BIN_ASSET_NAME_SHA256="${BIN_ASSET_NAME}.sha256"
# Disallow root
[ "$(id -u)" -ne 0 ] || fatalf 'Running as root is unsafe. Please run as a non-root user.'
# Dependencies
missing=''
for bin in curl gzip mktemp install sha256sum sed awk flock; do
    command -v "$bin" >/dev/null 2>&1 || missing="${missing}${missing:+ }$bin"
done
[ -z "$missing" ] || fatalf 'Missing required tools: %s\nPlease install them and try again.' "$missing"

# Service pre-checks ----------------------------------------------------------
if [ "$SERVICE" = "true" ]; then
    # require systemd >= 246 (needed for used features)
    systemdVersion=$(systemctl --user --version 2>/dev/null \
        | awk 'NR==1 {print $2}' \
        | sed 's/^\([0-9][0-9]*\).*/\1/')
    [ -n "$systemdVersion" ] || fatalf 'systemd --user not available (required for SERVICE=true)'
    [ "$systemdVersion" -ge 246 ] || fatalf 'systemd ≥ 246 required, found %s' "$systemdVersion"

    # test if systemctl --user actually works
    if ! systemctl --user daemon-reload >/dev/null 2>&1; then
        warnf 'systemctl --user is not functional (common in WSL). Skipping service setup.'
        SERVICE="false"
    fi

    # track prior state
    if systemctl --user cat "$SERVICE_NAME" >/dev/null 2>&1; then
        service_exists=1
        if systemctl --user is-enabled --quiet "$SERVICE_NAME"; then service_was_enabled=1; fi
        if systemctl --user is-active  --quiet "$SERVICE_NAME"; then service_was_active=1; fi
    fi

    # if active, stop it
    if [ "$service_exists" -eq 1 ] && [ "$service_was_active" -eq 1 ]; then
        printf 'Stopping active service ...\n'
        systemctl --user stop "$SERVICE_NAME" || fatalf 'Failed to stop active service'
    fi
fi

# Create directories ---------------------------------------------------------

mkdir -p "$(dirname "$SERVICE_FILE")" "$APP_DATA_DIR" || { rc=$?; fatalf 'failed to create install dirs (rc=%d)' "$rc"; }

# Download -------------------------------------------------------------------
ver_url="${RELEASE_URL}version"
bin_url="${RELEASE_URL}${BIN_ASSET_NAME}"
bin_url_sha256="${RELEASE_URL}${BIN_ASSET_NAME_SHA256}"

# make temp dir
temp_dir=$(mktemp -d) || { rc=$?; fatalf 'failed to create temp dir (rc=%d)' "$rc"; }

# output paths
dwld_out="$temp_dir/$BIN_ASSET_NAME"
hash_out="$temp_dir/$BIN_ASSET_NAME_SHA256"
gzip_out=${dwld_out%".gz"}

curl_opts="-sS --fail --location --show-error --connect-timeout 5 --retry-all-errors --retry 3 --retry-delay 1 --max-time 300"

# get version
version=$(curl $curl_opts "$ver_url") # not needed, but useful info for the user

# print install header
INSTALL_SYMBOL=''
case $(printf %s "${LC_ALL:-${LANG:-}}" | tr '[:upper:]' '[:lower:]') in
  *utf-8*|*utf8*) [ -t 1 ] && INSTALL_SYMBOL='📦 ' ;;
esac
printf '%sInstalling %s %s ...\n' "$INSTALL_SYMBOL" "$APP_NAME" "$version"

# download bin and checksum
printf 'Downloading binary ...\n'
curl $curl_opts -o "$dwld_out" "$bin_url" || { rc=$?; fatalf 'Download of binary failed (rc=%d)' "$rc"; }
printf 'Downloading checksum ...\n'
curl $curl_opts -o "$hash_out" "$bin_url_sha256" || { rc=$?; fatalf 'Download of checksum failed (rc=%d)' "$rc"; }

# verify checksum
printf 'Verifying checksum ...\n'
expected_sum=$(cut -d' ' -f1 "$hash_out" | tr -d '\r\n') # read the first field (the hash)
[ ${#expected_sum} -eq 64 ] || fatalf 'Invalid checksum format'
actual_sum=$(sha256sum "$dwld_out" | awk '{print $1}' | tr -d '\r\n') # same deal
[ -n "$actual_sum" ] || fatalf 'Failed to compute hash of downloaded file'
[ "$expected_sum" = "$actual_sum" ] || fatalf 'Checksum mismatch! Expected %s, got %s' "$expected_sum" "$actual_sum"

# unzip
printf 'Unzipping ...\n'
gzip -dc "$dwld_out" > "$gzip_out" || { rc=$?; fatalf 'Failed to unzip (rc=%d)' "$rc"; }

# Backup (for rollback) -------------------------------------------------------
if [ -f "$APP_BIN" ] || [ "$service_exists" -eq 1 ]; then
    printf 'Backing up current installation ...\n'
fi

if [ -f "$APP_BIN" ]; then
    old_app_bin="$temp_dir/$APP_NAME.old"
    cp -f "$APP_BIN" "$old_app_bin" || { rc=$?; fatalf 'Failed to backup existing binary (rc=%d)' "$rc"; }
fi

if [ "$SERVICE" = "true" ] && [ "$service_exists" -eq 1 ]; then
    old_service_file="$temp_dir/$SERVICE_NAME.old"
    systemctl --user cat "$SERVICE_NAME" > "$old_service_file" || { rc=$?; fatalf 'Failed to backup existing service unit file (rc=%d)' "$rc"; }
fi

if [ -f "$RELEASE_URL_FILE" ]; then
    release_url_exists=1
    old_release_url_file="$temp_dir/release-url.old"
    cp -f "$RELEASE_URL_FILE" "$old_release_url_file" || { rc=$?; fatalf 'Failed to backup existing release URL file (rc=%d)' "$rc"; }
fi

# Install ---------------------------------------------------------------------
printf 'Writing binary to %s ...\n' "$APP_BIN"
install -Dm755 "$gzip_out" "$APP_BIN" || { rc=$?; fatalf 'Failed to install binary (rc=%d)' "$rc"; }

printf 'Writing release source to %s ...\n' "$RELEASE_URL_FILE"
printf '%s\n' "$RELEASE_URL" > "$RELEASE_URL_FILE" || { rc=$?; fatalf 'Failed to write release URL file (rc=%d)' "$rc"; }

# Stop running instances ------------------------------------------------------

# create runtime dirs / lock file
mkdir -p "$RUNTIME_DIR/$APP_NAME/instances" || { rc=$?; fatalf 'Failed to create runtime dirs (rc=%d)' "$rc"; }
if [ ! -f "$LOCK_FILE" ]; then
    mkdir -p "$(dirname "$LOCK_FILE")" || { rc=$?; fatalf 'Failed to create lock file dir (rc=%d)' "$rc"; }
    touch "$LOCK_FILE" || { rc=$?; fatalf 'Failed to create lock file (rc=%d)' "$rc"; }
fi

# send TERM to instances
if [ -d "$INSTANCES_DIR" ]; then
    printf "Shutting down running instances ...\n"
    for pidfile in "$INSTANCES_DIR"/*; do
        [ -f "$pidfile" ] || continue
        pid=$(basename "$pidfile")
        case "$pid" in ''|*[!0-9]*) continue ;; esac

        # Verify process exists and binary path matches
        if [ -d "/proc/$pid" ]; then
            actual_bin=$(readlink -f "/proc/$pid/exe" 2>/dev/null || echo "")
            expected_bin=$(readlink -f "$APP_BIN" 2>/dev/null || echo "")
            if [ "$actual_bin" = "$expected_bin" ]; then
                kill -TERM "$pid" 2>/dev/null || :
            fi
        fi
    done
fi

# acquire exclusive lock
printf "Acquiring migration lock ...\n"
lock_fd=9 # arbitrary unused fd, might be an issue in the future if the script is modified to use more fds
eval "exec $lock_fd>\"\$LOCK_FILE\"" || fatalf 'Failed to open lock file'
if ! flock -x -w 120 "$lock_fd"; then
    fatalf 'Timeout waiting for exclusive lock. Active instances:\n%s' "$(ls "$INSTANCES_DIR" 2>/dev/null || echo 'none')"
fi

# final safety check
if [ -d "$INSTANCES_DIR" ] && [ -n "$(find "$INSTANCES_DIR" -type f -newer "$LOCK_FILE" 2>/dev/null)" ]; then
    fatalf 'Some instances left hanging pid files. Aborting out of caution. Check: %s' "$INSTANCES_DIR"
fi

# verify install / get version / migrate
printf 'Verifying installation (this may take a few moments if migrating) ...\n'
out=$("$APP_BIN" -m 2>&1) || fatalf '%s -m failed:\n%s' "$APP_BIN" "$out"
effective_version=$(printf '%s\n' "$out" | awk 'NR==1{print; exit}') ||
    fatalf 'Failed to parse version from:\n%s' "$out"
[ -n "$effective_version" ] || fatalf 'Empty version output:\n%s' "$out"

# get build vars (for default port)
build_vars=$("$APP_BIN" --build-vars 2>&1) || fatalf 'Failed to get build vars:\n%s' "$build_vars"
default_port=$(printf '%s' "$build_vars" | sed -n 's/.*"serviceDefaultPort":\([0-9]*\).*/\1/p')
[ -n "$default_port" ] || fatalf 'Failed to parse default port from build vars:\n%s' "$build_vars"

# release lock
[ -n "${lock_fd:-}" ] && eval "exec $lock_fd>&-" || :

# Service ---------------------------------------------------------------------
if [ "$SERVICE" = "true" ]; then
    [ "$service_exists" -eq 1 ] && printf 'Updating service ...\n' || printf 'Setting up service ...\n'

    # escape % -> %% in args (no ${var//%/%%} in POSIX)
    safe_args=$(printf '%s' "$SERVICE_ARGS" | sed 's/%/%%/g') || fatalf 'Failed to escape service args'

    # write unit file
    {
        printf '%s\n' "[Unit]"
        printf 'Description=%s\n' "$SERVICE_DESC"
        printf '%s\n' "StartLimitIntervalSec=600"
        printf '%s\n' "StartLimitBurst=5"
        printf '%s\n' "# NOTE: network-online.target is likely broken for user services."
        printf '%s\n' "# App will still handle unready net starts gracefully with retries and a timeout."
        printf '%s\n' "Wants=network-online.target"
        printf '%s\n' "After=network-online.target"
        printf '%s\n' ""
        printf '%s\n' "[Service]"
        printf '%s\n' "Type=notify"
        printf 'ExecStart=%s %s\n' "$APP_BIN" "$safe_args"
        printf 'WorkingDirectory=%s\n' "$APP_DATA_DIR"
        printf '%s\n' "Restart=always"
        printf '%s\n' "RestartSec=1"
        printf '%s\n' "LimitNOFILE=65535"
        printf 'TimeoutStartSec=%ss\n' "$SERVICE_READY_TIMEOUT_SECONDS"
        printf '%s\n' "RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK"
        printf '%s\n' "Environment=PATH=%h/.local/bin:/usr/local/bin:/usr/bin:/bin"
        printf 'EnvironmentFile=-%s\n' "$APP_ENV_FILE"
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

    if [ "$service_exists" -eq 1 ]; then
        if [ "$service_was_active" -eq 1 ]; then
            printf "Restarting service ...\n"
            systemctl --user start "$SERVICE_NAME" || { rc=$?; fatalf 'Failed to start service (rc=%d)' "$rc"; }
        else
            printf "Service updated; leaving it stopped (was inactive).\n"
        fi
    else
        # check if default port is available before first start
        if port_in_use "$default_port"; then
            fatalf 'Default port %d is already in use.\nEither free the port or configure a different port in:\n    %s\nThen start the service with: systemctl --user start %s' "$default_port" "$APP_ENV_FILE" "$SERVICE_NAME"
        fi
        printf "Starting service ...\n"
        systemctl --user start "$SERVICE_NAME" || { rc=$?; fatalf 'Failed to start service (rc=%d)' "$rc"; }
    fi

    if ! loginctl show-user "$USER" 2>/dev/null | grep -q 'Linger=yes'; then
       warnf 'If you want the service to run when you are not logged in, run:'
       warnf '    sudo loginctl enable-linger %s' "$USER"
    fi
fi

# Add to PATH -----------------------------------------------------------------
MARK_OPEN='# >>> PATH bootstrap: ~/.local/bin >>>'
MARK_CLOSE='# <<< PATH bootstrap <<<'
PATH_BLOCK='if [ -d "$HOME/.local/bin" ]; then
  case ":$PATH:" in
    *":$HOME/.local/bin:"*) : ;;
    *) PATH="$HOME/.local/bin:$PATH" ;;
  esac
fi
export PATH'

# append PATH block to the given file if not already present.
add_path_block() {
  tgt=$1
  [ -f "$tgt" ] || return 0
  # if the opening marker exists, do nothing.
  if awk -v m="$MARK_OPEN" 'index($0,m){found=1} END{exit found?0:1}' "$tgt"; then
    return 0
  fi
  # append the block
  {
    printf '\n%s\n' "$MARK_OPEN"
    printf '%s\n' "$PATH_BLOCK"
    printf '%s\n' "$MARK_CLOSE"
  } >>"$tgt"
}

add_path_block "$HOME/.bashrc"
add_path_block "$HOME/.zshrc"
add_path_block "$HOME/.profile"
add_path_block "$HOME/.bash_profile"

# Success! --------------------------------------------------------------------
successf 'Installed: %s (%s)' "$APP_NAME" "$effective_version"
warnf    'Open a new terminal or refresh this one with: exec "$SHELL" -l || exec sh -l'
successf '    Run:       %s -h     # for help' "$APP_NAME"
if [ "$SERVICE" = "true" ]; then
  successf '    Run:       %s service  # for service management cheat sheet' "$APP_NAME"
fi
