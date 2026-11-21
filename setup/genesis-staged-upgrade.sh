#!/usr/bin/env bash
set -Eeuo pipefail

###############################################################################
# GenesisL1 staged upgrade script
#
# Purpose:
#   - Clone and build upgrade binary before halt height.
#   - Keep old node running until upgrade/halt.
#   - After halt, back up critical files, migrate configs, install new binary,
#     and restart systemd service.
#
# Default target:
#   v1.6.2.1 (return of the untouched airdrop to the community pool). Its
#   height comes from the passed upgrade proposal and its commit from the
#   release, so both must be given:
#
#     UPGRADE_HEIGHT=<height> EXPECTED_COMMIT=<commit> \
#       bash setup/genesis-staged-upgrade.sh
#
#   See upgrades/v1.6.2.1/VALIDATOR_GUIDE.md. Not for nodes run by
#   cosmovisor: those get the binary in cosmovisor/upgrades/v1.6.2.1/bin.
###############################################################################

REPO_URL="${REPO_URL:-https://github.com/GenesisL1/genesis-crypto.git}"

UPGRADE_NAME="${UPGRADE_NAME:-v1.6.2.1}"
UPGRADE_REF="${UPGRADE_REF:-v1.6.2.1}"
# Required: the height in the passed upgrade proposal.
UPGRADE_HEIGHT="${UPGRADE_HEIGHT:-}"

# Pin this for safety: the commit published with the release. Required; set it
# to an empty string only if you deliberately want to skip the check.
EXPECTED_COMMIT="${EXPECTED_COMMIT-REQUIRED}"

BINARY="${BINARY:-genesisd}"
SERVICE="${SERVICE:-genesisd}"

NODE_HOME="${NODE_HOME:-$HOME/.genesis}"
RPC_URL="${RPC_URL:-http://127.0.0.1:26657}"

WORK_DIR="${WORK_DIR:-$HOME/genesis-upgrades/$UPGRADE_NAME}"
SRC_DIR="$WORK_DIR/src"
BUILD_DIR="$WORK_DIR/build"
STAGED_BIN="$BUILD_DIR/$BINARY"

POLL_SECONDS="${POLL_SECONDS:-5}"
RPC_FAILS_AFTER_HEIGHT="${RPC_FAILS_AFTER_HEIGHT:-3}"

NETWORK="${NETWORK:-mainnet}"
LEDGER_ENABLED="${LEDGER_ENABLED:-true}"

# v1.6.2.1 changes no configuration, so the migration is off by default.
RUN_CONFIG_MIGRATION="${RUN_CONFIG_MIGRATION:-0}"
# Remove the temporary blocked-addresses list from app.toml after the halt.
LIFT_AIRDROP_BLOCK="${LIFT_AIRDROP_BLOCK:-1}"
START_AFTER_INSTALL="${START_AFTER_INSTALL:-1}"

# Manual override. Use only if you have verified the node halted correctly.
ASSUME_HALTED="${ASSUME_HALTED:-0}"

LOCK_FILE="/tmp/${BINARY}-${UPGRADE_NAME}-staged-upgrade.lock"

if [[ "$(id -u)" -eq 0 ]]; then
  SUDO=()
else
  SUDO=(sudo)
fi

log() {
  printf '[%s] %s\n' "$(date -u '+%F %T UTC')" "$*"
}

die() {
  log "ERROR: $*"
  exit 1
}

is_number() {
  [[ "${1:-}" =~ ^[0-9]+$ ]]
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "Required command not found: $1"
}

read_rpc_height() {
  local json height

  json="$(curl -sfS --max-time 2 "$RPC_URL/status" 2>/dev/null || true)"
  [[ -n "$json" ]] || return 1

  if command -v jq >/dev/null 2>&1; then
    height="$(printf '%s' "$json" | jq -r '.result.sync_info.latest_block_height // empty')"
  else
    height="$(printf '%s' "$json" | grep -oE '"latest_block_height":"?[0-9]+' | grep -oE '[0-9]+' | head -n1 || true)"
  fi

  is_number "$height" || return 1
  printf '%s\n' "$height"
}

read_local_validator_height() {
  local state_file="$NODE_HOME/data/priv_validator_state.json"
  local height=""

  [[ -f "$state_file" ]] || return 1

  if command -v jq >/dev/null 2>&1; then
    height="$(jq -r '.height // empty' "$state_file" 2>/dev/null || true)"
  else
    height="$(grep -oE '"height":"?[0-9]+' "$state_file" | grep -oE '[0-9]+' | head -n1 || true)"
  fi

  is_number "$height" || return 1
  printf '%s\n' "$height"
}

service_is_active() {
  "${SUDO[@]}" systemctl is-active --quiet "$SERVICE"
}

recent_logs_show_upgrade() {
  # The exact halt message of the upgrade module. It means the old binary
  # refused block UPGRADE_HEIGHT, so the block before it is fully committed.
  # (The RPC height alone can show that block while it is still being applied.)
  "${SUDO[@]}" journalctl -u "$SERVICE" -n 300 --no-pager 2>/dev/null \
    | grep -Fq -e "UPGRADE \"${UPGRADE_NAME}\" NEEDED at height: ${UPGRADE_HEIGHT}:" \
               -e "UPGRADE \\\"${UPGRADE_NAME}\\\" NEEDED at height: ${UPGRADE_HEIGHT}:"   # plain or JSON logs
}

detect_active_binary_path() {
  local path=""

  path="$("${SUDO[@]}" systemctl show "$SERVICE" -p ExecStart --value 2>/dev/null \
    | sed -n 's/.*path=\([^ ;]*\).*/\1/p' \
    | head -n1 || true)"

  if [[ -n "$path" && -e "$path" ]]; then
    readlink -f "$path" 2>/dev/null || printf '%s\n' "$path"
    return 0
  fi

  path="$(command -v "$BINARY" || true)"
  if [[ -n "$path" && -e "$path" ]]; then
    readlink -f "$path" 2>/dev/null || printf '%s\n' "$path"
    return 0
  fi

  if command -v go >/dev/null 2>&1; then
    path="$(go env GOPATH)/bin/$BINARY"
    printf '%s\n' "$path"
    return 0
  fi

  return 1
}

prepare_source_and_build() {
  need_cmd git
  need_cmd make
  need_cmd go
  need_cmd curl
  need_cmd systemctl

  mkdir -p "$WORK_DIR"

  if [[ ! -d "$SRC_DIR/.git" ]]; then
    log "Cloning $REPO_URL into $SRC_DIR"
    git clone "$REPO_URL" "$SRC_DIR"
  else
    log "Source directory already exists; fetching updates/tags"
    git -C "$SRC_DIR" fetch --all --tags --prune
  fi

  log "Checking out upgrade ref: $UPGRADE_REF"
  git -C "$SRC_DIR" fetch --all --tags --prune
  git -C "$SRC_DIR" checkout --detach "$UPGRADE_REF"

  local actual_commit
  actual_commit="$(git -C "$SRC_DIR" rev-parse HEAD)"

  log "Checked out commit: $actual_commit"

  if [[ -n "$EXPECTED_COMMIT" && "$actual_commit" != "$EXPECTED_COMMIT" ]]; then
    die "Checked out commit does not match EXPECTED_COMMIT.
Expected: $EXPECTED_COMMIT
Actual:   $actual_commit

Set EXPECTED_COMMIT='' only if you intentionally want to disable this check."
  fi

  rm -rf "$BUILD_DIR"
  mkdir -p "$BUILD_DIR"

  log "Downloading Go modules"
  (
    cd "$SRC_DIR"
    go mod download
  )

  log "Building staged binary; old node remains running"
  (
    cd "$SRC_DIR"

    if command -v ionice >/dev/null 2>&1; then
      ionice -c2 -n7 nice -n 10 make build \
        BUILDDIR="$BUILD_DIR" \
        NETWORK="$NETWORK" \
        LEDGER_ENABLED="$LEDGER_ENABLED"
    else
      nice -n 10 make build \
        BUILDDIR="$BUILD_DIR" \
        NETWORK="$NETWORK" \
        LEDGER_ENABLED="$LEDGER_ENABLED"
    fi
  )

  [[ -x "$STAGED_BIN" ]] || die "Staged binary was not created: $STAGED_BIN"

  # genesisd refuses to run if it was built with other Unicode tables than the
  # network's (app/toolchain_guard.go). Stop now, long before the halt, instead
  # of installing a binary that will not start.
  "$STAGED_BIN" version --long >/dev/null \
    || die "The staged binary refuses to run (message above). Rebuild with Go 1.23.12 to 1.25.x, for example by running this script with GOTOOLCHAIN=go1.23.12."

  log "Staged binary built successfully:"
  "$STAGED_BIN" version --long | grep -E '^(version|commit|go):' || true
}

wait_for_upgrade_halt() {
  local min_safe_height=$((UPGRADE_HEIGHT - 1))
  local last_height=0
  local height_ready=0
  local rpc_fail_count=0
  local h=""

  log "Waiting for upgrade $UPGRADE_NAME at height $UPGRADE_HEIGHT"
  log "Safe trigger starts once observed height is >= $min_safe_height"

  while true; do
    if [[ "$ASSUME_HALTED" == "1" ]]; then
      log "ASSUME_HALTED=1 set; proceeding without automatic halt detection"
      return 0
    fi

    if h="$(read_rpc_height 2>/dev/null)"; then
      last_height="$h"
      rpc_fail_count=0

      log "Current RPC height: $last_height / target $UPGRADE_HEIGHT"

      if (( last_height >= min_safe_height )); then
        height_ready=1
      fi

      if (( last_height >= UPGRADE_HEIGHT )); then
        log "RPC height is >= upgrade height; proceeding to install"
        return 0
      fi
    else
      if (( last_height == 0 )); then
        if h="$(read_local_validator_height 2>/dev/null)"; then
          last_height="$h"
          log "RPC unavailable; local priv_validator_state height: $last_height"
          if (( last_height >= min_safe_height )); then
            height_ready=1
          fi
        fi
      fi

      if (( height_ready == 1 )); then
        rpc_fail_count=$((rpc_fail_count + 1))
        log "RPC unavailable after safe height; failure count $rpc_fail_count/$RPC_FAILS_AFTER_HEIGHT"
      else
        log "RPC unavailable, but safe height has not been observed yet"
      fi
    fi

    if (( height_ready == 1 )); then
      if ! service_is_active; then
        log "Service is no longer active after safe height; treating as upgrade halt"
        return 0
      fi

      if recent_logs_show_upgrade; then
        log "Service logs show the upgrade halt (UPGRADE \"$UPGRADE_NAME\" NEEDED); proceeding"
        return 0
      fi

      if (( rpc_fail_count >= RPC_FAILS_AFTER_HEIGHT )); then
        log "RPC failed repeatedly after safe height; proceeding cautiously"
        return 0
      fi
    fi

    sleep "$POLL_SECONDS"
  done
}

backup_critical_files() {
  local active_bin="$1"
  local backup_dir="$WORK_DIR/backups/$(date -u '+%Y%m%dT%H%M%SZ')"

  mkdir -p "$backup_dir"
  chmod 700 "$backup_dir"

  log "Creating critical backup in $backup_dir"

  if [[ -d "$NODE_HOME/config" ]]; then
    cp -a "$NODE_HOME/config" "$backup_dir/config"
  fi

  if [[ -f "$NODE_HOME/data/priv_validator_state.json" ]]; then
    mkdir -p "$backup_dir/data"
    cp -a "$NODE_HOME/data/priv_validator_state.json" "$backup_dir/data/priv_validator_state.json"
  fi

  for keyring_dir in "$NODE_HOME"/keyring-*; do
    [[ -e "$keyring_dir" ]] || continue
    cp -a "$keyring_dir" "$backup_dir/"
  done

  if [[ -n "$active_bin" && -e "$active_bin" ]]; then
    cp -a "$active_bin" "$backup_dir/${BINARY}.pre-${UPGRADE_NAME}"
  fi

  chmod -R go-rwx "$backup_dir" || true
  log "Backup complete: $backup_dir"
}

stop_service_cleanly() {
  log "Stopping $SERVICE"
  "${SUDO[@]}" systemctl stop "$SERVICE" || true

  for _ in $(seq 1 60); do
    if ! service_is_active; then
      log "$SERVICE is stopped"
      return 0
    fi
    sleep 1
  done

  die "$SERVICE did not stop within 60 seconds"
}

run_config_migration() {
  if [[ "$RUN_CONFIG_MIGRATION" != "1" ]]; then
    log "Skipping config migration because RUN_CONFIG_MIGRATION=$RUN_CONFIG_MIGRATION"
    return 0
  fi

  local migrate_script="$SRC_DIR/setup/migrate-configs.sh"
  local config_toml="$NODE_HOME/config/config.toml"
  local app_toml="$NODE_HOME/config/app.toml"

  [[ -f "$migrate_script" ]] || die "Config migration script not found: $migrate_script"
  [[ -f "$config_toml" ]] || die "config.toml not found: $config_toml"
  [[ -f "$app_toml" ]] || die "app.toml not found: $app_toml"

  log "Running config migration from staged source"
  bash "$migrate_script" "$config_toml" "$app_toml"
}

refuse_cosmovisor() {
  local exec_start
  exec_start="$("${SUDO[@]}" systemctl show "$SERVICE" -p ExecStart --value 2>/dev/null || true)"
  if grep -qi cosmovisor <<<"$exec_start"; then
    die "$SERVICE runs under cosmovisor; this script would replace the wrong binary.
Put the new binary at \$DAEMON_HOME/cosmovisor/upgrades/$UPGRADE_NAME/bin/$BINARY instead
(see upgrades/$UPGRADE_NAME/VALIDATOR_GUIDE.md)."
  fi
}

lift_airdrop_block() {
  if [[ "$LIFT_AIRDROP_BLOCK" != "1" ]]; then
    log "Leaving blocked-addresses in app.toml because LIFT_AIRDROP_BLOCK=$LIFT_AIRDROP_BLOCK"
    return 0
  fi

  local lift_script="$SRC_DIR/setup/lift-airdrop-block.sh"
  [[ -f "$lift_script" ]] || die "Lift script not found: $lift_script"

  log "Lifting the temporary airdrop block from app.toml"
  bash "$lift_script" "$NODE_HOME/config/app.toml"
}

install_staged_binary() {
  local active_bin="$1"
  local tmp_bin

  [[ -x "$STAGED_BIN" ]] || die "Missing staged binary: $STAGED_BIN"
  [[ -n "$active_bin" ]] || die "Could not determine active binary path"

  mkdir -p "$(dirname "$active_bin")"

  tmp_bin="${active_bin}.new.${UPGRADE_NAME}.$$"

  log "Installing staged binary"
  log "From: $STAGED_BIN"
  log "To:   $active_bin"

  if [[ -w "$(dirname "$active_bin")" ]]; then
    install -m 0755 "$STAGED_BIN" "$tmp_bin"
    mv -f "$tmp_bin" "$active_bin"
  else
    "${SUDO[@]}" install -m 0755 "$STAGED_BIN" "$tmp_bin"
    "${SUDO[@]}" mv -f "$tmp_bin" "$active_bin"
  fi

  log "Installed binary version:"
  "$active_bin" version || true
}

start_service() {
  if [[ "$START_AFTER_INSTALL" != "1" ]]; then
    log "Not starting service because START_AFTER_INSTALL=$START_AFTER_INSTALL"
    return 0
  fi

  log "Starting $SERVICE"
  "${SUDO[@]}" systemctl daemon-reload || true
  "${SUDO[@]}" systemctl start "$SERVICE"

  sleep 5

  if service_is_active; then
    log "$SERVICE started successfully"
  else
    log "$SERVICE did not become active. Recent logs:"
    "${SUDO[@]}" journalctl -u "$SERVICE" -n 120 --no-pager || true
    die "$SERVICE failed to start"
  fi
}

main() {
  exec 9>"$LOCK_FILE"
  flock -n 9 || die "Another staged upgrade process is already running: $LOCK_FILE"

  log "Starting staged upgrade script"
  log "Upgrade name:   $UPGRADE_NAME"
  log "Upgrade ref:    $UPGRADE_REF"
  log "Upgrade height: $UPGRADE_HEIGHT"
  log "Repo:           $REPO_URL"
  log "Node home:      $NODE_HOME"
  log "RPC URL:        $RPC_URL"
  log "Service:        $SERVICE"

  [[ -n "$UPGRADE_HEIGHT" ]] && is_number "$UPGRADE_HEIGHT" \
    || die "Set UPGRADE_HEIGHT to the height in the passed $UPGRADE_NAME upgrade proposal"
  [[ "$EXPECTED_COMMIT" != "REQUIRED" ]] \
    || die "Set EXPECTED_COMMIT to the commit published with the $UPGRADE_NAME release"
  refuse_cosmovisor

  prepare_source_and_build
  wait_for_upgrade_halt

  local active_bin
  active_bin="$(detect_active_binary_path || true)"
  [[ -n "$active_bin" ]] || die "Could not detect active $BINARY path. Set ACTIVE_BIN manually by editing script."

  log "Detected active binary path: $active_bin"

  stop_service_cleanly
  backup_critical_files "$active_bin"
  run_config_migration
  lift_airdrop_block
  install_staged_binary "$active_bin"
  start_service

  log "Upgrade $UPGRADE_NAME completed"
}

main "$@"
