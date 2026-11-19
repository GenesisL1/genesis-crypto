#!/bin/bash

set -eu

# Source the _variables.sh file
. "$(cd "$(dirname "$0")"/.. && pwd)/_variables.sh"

# Temporary genesis file
TEMP_GENESIS="$CONFIG_DIR/genesis.json.tmp"

# Fetch genesis.json
if wget -qO "$TEMP_GENESIS" "$STATE_URL"; then
    mv "$TEMP_GENESIS" "$CONFIG_DIR/genesis.json"
    echo "Added genesis.json file to $CONFIG_DIR"
else
    echo "ERROR: Failed to fetch genesis.json from URL: $STATE_URL"
    echo "Please manually download genesis.json and place it in: $CONFIG_DIR"
    echo ""
    rm -f "$TEMP_GENESIS"
fi