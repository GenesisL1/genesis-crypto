#!/bin/bash

cat <<"EOF"

  /$$$$$$                                          /$$                 /$$         /$$       
 /$$__  $$                                        |__/                | $$       /$$$$       
| $$  \__/  /$$$$$$  /$$$$$$$   /$$$$$$   /$$$$$$$ /$$  /$$$$$$$      | $$      |_  $$       
| $$ /$$$$ /$$__  $$| $$__  $$ /$$__  $$ /$$_____/| $$ /$$_____/      | $$        | $$       
| $$|_  $$| $$$$$$$$| $$  \ $$| $$$$$$$$|  $$$$$$ | $$|  $$$$$$       | $$        | $$       
| $$  \ $$| $$_____/| $$  | $$| $$_____/ \____  $$| $$ \____  $$      | $$        | $$       
|  $$$$$$/|  $$$$$$$| $$  | $$|  $$$$$$$ /$$$$$$$/| $$ /$$$$$$$/      | $$$$$$$$ /$$$$$$     
 \______/  \_______/|__/  |__/ \_______/|_______/ |__/|_______/       |________/|______/     
                                                                                             
EOF

# Root of the current repository
REPO_ROOT=$(cd "$(dirname "$0")"/.. && pwd)

# Source the variables file
. "$REPO_ROOT/utils/_variables.sh"

echo ""
echo "This script should only be used if your node halted (!) and you have to perform a software upgrade!"
echo "NOTE: a backup of all your important files in $NODE_DIR/ will be created in $NODE_DIR-backup/."
echo ""
read -p "Do you want to continue? (y/N): " ANSWER

ANSWER=$(echo "$ANSWER" | tr 'A-Z' 'a-z')  # Convert to lowercase

if [ "$ANSWER" != "y" ]; then
    echo "Aborted."
    exit 1
fi

# Stop services
systemctl stop $BINARY_NAME

# cd to root of the repository
cd $REPO_ROOT

# System update and installation of dependencies
. ./setup/dependencies.sh

# Backup important files in the node directory
rsync -av --include='data/priv_validator_state.json' --exclude='data/*' "$NODE_DIR/" "$NODE_DIR-backup/"

# Migrate configs. v1.6.2.1 changes no configuration, and running the v1.6.2
# migration again would reset settings such as versiondb and telemetry, so it is
# off unless asked for.
if [ "${RUN_CONFIG_MIGRATION:-0}" = "1" ]; then
    sh ./setup/migrate-configs.sh "$CONFIG_DIR/config.toml" "$CONFIG_DIR/app.toml"
else
    echo "v1.6.2.1 needs no config migration: skipped (set RUN_CONFIG_MIGRATION=1 to run it anyway)."
fi

# v1.6.2.1: the temporary airdrop block ends with this upgrade.
if grep -qE '^[[:space:]]*blocked-addresses[[:space:]]*=' "$CONFIG_DIR/app.toml" 2>/dev/null; then
    printf "Remove the temporary airdrop blocked-addresses list from app.toml now? (Y/n): "
    read LIFT
    LIFT=$(echo "$LIFT" | tr 'A-Z' 'a-z')
    if [ "$LIFT" != "n" ]; then
        bash ./setup/lift-airdrop-block.sh "$CONFIG_DIR/app.toml"
    fi
fi

# Install binaries. Build exactly from the release's go.mod and go.sum (make
# uses -mod=readonly): every validator must run the same code. make stops on Go
# 1.26 or later, and genesisd refuses to run if it was built with other Unicode
# tables than the network's (Makefile check-go-version, app/toolchain_guard.go).
make install && "${GOBIN:-$(go env GOPATH)/bin}/$BINARY_NAME" version --long | grep -E '^(version|commit|go):' && {
    echo ""
    echo "Upgrade was a success!"
    echo ""
    echo "When ready, turn on your node again using 'systemctl start $BINARY_NAME'!"
}
