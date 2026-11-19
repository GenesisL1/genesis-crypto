#!/bin/sh

set -eu

# Source the _variables.sh file
. "$(cd "$(dirname "$0")"/.. && pwd)/_variables.sh"

# Function to fetch and update config values
update_config() {
    URL="$1"
    KEY="$2"
    NAME="$3"

    # Fetch latest value
    if VALUE=$(wget -qO - "$URL"); then

        # Grab only the first line
        VALUE=$(printf '%s\n' "$VALUE" | head -n 1)

        if [ -z "$VALUE" ] || [ "$VALUE" = '""' ]; then
            echo "No $NAME found. Skipping."
        else
            # Ensure value is enclosed in double quotes
            if ! printf '%s\n' "$VALUE" | grep -q '^".*"$'; then VALUE="\"$VALUE\""; fi

            # Update config.toml
            sed -i "s#^$KEY *=.*#$KEY = $VALUE#" "$CONFIG_DIR/config.toml"
            echo "Added $KEY = $VALUE"
        fi

    else
        echo "Failed to fetch $NAME. Skipping."
    fi
}

# Update seeds and persistent peers
update_config "$SEEDS_URL" "seeds" "seeds"
update_config "$PEERS_URL" "persistent_peers" "persistent peers"