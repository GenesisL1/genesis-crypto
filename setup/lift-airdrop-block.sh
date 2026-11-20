#!/usr/bin/env bash
###############################################################################
# Removes the temporary `blocked-addresses` list from app.toml.
#
# Validators loaded the published list of untouched airdrop accounts into
# `blocked-addresses` while the community decided on their return to the
# community pool. Upgrade v1.6.2.1 performs that return, and the block ends
# with it. Run this after the node halted for v1.6.2.1 and before you start
# the new binary (setup/upgrade.sh and setup/genesis-staged-upgrade.sh call it).
#
#   bash setup/lift-airdrop-block.sh [path/to/app.toml]
#
# Default path: $HOME/.genesis/config/app.toml. A timestamped backup is kept
# beside the file. Nothing is changed when the setting is absent.
###############################################################################
set -euo pipefail

APP_TOML="${1:-$HOME/.genesis/config/app.toml}"

if [[ ! -f "$APP_TOML" ]]; then
  echo "app.toml not found: $APP_TOML" >&2
  exit 1
fi

if ! grep -qE '^[[:space:]]*blocked-addresses[[:space:]]*=' "$APP_TOML"; then
  echo "No blocked-addresses setting in $APP_TOML: nothing to lift."
  exit 0
fi

count=$(grep -cE '^[[:space:]]*"genesis1[0-9a-z]+",?[[:space:]]*$' "$APP_TOML" || true)
backup="$APP_TOML.before-airdrop-lift-$(date -u +%Y%m%dT%H%M%SZ)"
cp -p "$APP_TOML" "$backup"

# Drop the key and its array, which may span many lines: from the key line to
# the line that closes the array.
tmp="$(mktemp "${APP_TOML}.XXXXXX")"
awk '
  # The comment line that heads the published blocked-addresses.toml.
  skip == 0 && /^# app\.toml .* at the very top, above the first \[section\] line[[:space:]]*$/ { next }
  skip == 0 && /^[[:space:]]*blocked-addresses[[:space:]]*=/ {
    if ($0 !~ /\]/) { skip = 1 }   # multi-line array: skip until "]"
    next                             # single-line array: drop just this line
  }
  skip == 1 {
    if ($0 ~ /^[[:space:]]*\][[:space:]]*(#.*)?$/) { skip = 0 }
    next
  }
  { print }
' "$backup" > "$tmp"

if grep -qE '^[[:space:]]*blocked-addresses[[:space:]]*=' "$tmp"; then
  rm -f "$tmp"
  echo "Could not remove blocked-addresses from $APP_TOML; the file is unchanged." >&2
  exit 1
fi

# Check that the result still parses as TOML when Python 3.11+ is available.
if command -v python3 >/dev/null 2>&1 && python3 -c 'import tomllib' 2>/dev/null; then
  if ! python3 -c 'import sys, tomllib; tomllib.load(open(sys.argv[1], "rb"))' "$tmp"; then
    rm -f "$tmp"
    echo "The edited app.toml does not parse; $APP_TOML is unchanged." >&2
    exit 1
  fi
fi

# Write in place so the file keeps its owner and permissions.
cat "$tmp" > "$APP_TOML"
rm -f "$tmp"

if [[ "$count" -gt 0 ]]; then
  echo "Lifted the airdrop block: removed blocked-addresses ($count addresses) from $APP_TOML"
else
  echo "Lifted the airdrop block: removed blocked-addresses from $APP_TOML"
fi
echo "Backup: $backup"
