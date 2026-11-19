#!/bin/bash

set -eu

# Source the _variables.sh file
. "$(cd "$(dirname "$0")"/.. && pwd)/_variables.sh"

# Fetch latest value
if RPC_SERVERS=$(wget -qO - "$RPC_SERVERS_URL"); then

    # Grab only the first line
    RPC_SERVERS=$(printf '%s\n' "$RPC_SERVERS" | head -n 1)

    if [ -z "$RPC_SERVERS" ] || [ "$RPC_SERVERS" = '""' ]; then
        echo "No rpc_servers found. Skipping."
    else
        # Ensure value is enclosed in double quotes
        if ! printf '%s\n' "$RPC_SERVERS" | grep -q '^".*"$'; then RPC_SERVERS="\"$RPC_SERVERS\""; fi

        # Update config.toml
        sed -i "s#^rpc_servers *=.*#rpc_servers = $RPC_SERVERS#" "$CONFIG_DIR/config.toml"
        echo "Added rpc_servers = $RPC_SERVERS"
    fi
else
    echo "Failed to fetch rpc_servers. Skipping."
fi