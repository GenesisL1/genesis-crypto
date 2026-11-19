# Upgrade v1.6.2.1: validator guide

Upgrade v1.6.2.1 returns the untouched 2022 ATOM and Evmos airdrop allocations to the community pool, as signal proposal #`<SIGNAL_ID>` asked. It changes no parameters, no stores and no configuration format. Your node stops by itself at the upgrade height; you then start it with the new binary.

| | |
|---|---|
| Plan name | `v1.6.2.1` |
| Upgrade height | `<HEIGHT>` (proposal #`<UPGRADE_PROPOSAL_ID>`), expected around `<UPGRADE_TIME_UTC>` |
| Release | tag `v1.6.2.1`, commit `<COMMIT>` |
| Chain id | `genesis_29-2` |
| Runs on top of | `v1.6.2` (upgrade at height 13,093,000, proposal #121) |

Three rules matter more than anything else:

1. **Do not run the new binary before the upgrade height.** It refuses to process earlier blocks, and the old binary refuses to process the upgrade block.
2. **Build from the published commit only.** Every validator must run the same code, or the chain stops on an app-hash mismatch.
3. **Lift the temporary airdrop block after the upgrade.** The scripts below do it; with cosmovisor you do it yourself (step 5).

## 1. Build and check the binary (any time before the upgrade)

```bash
cd ~/genesis-crypto            # or a fresh clone of https://github.com/GenesisL1/genesis-crypto
git fetch --tags
git checkout v1.6.2.1
git rev-parse HEAD             # must print <COMMIT>
make build                     # writes ./build/genesisd
./build/genesisd version --long | grep -E '^(version|commit):'
./build/genesisd airdrop-recovery verify-list
```

`verify-list` must print exactly:

```
list OK: sha256 1297cfe6dceed738d7fc34b6412826b9f3f249461bfcc1418fecff32c2e8321b, block 14008743, 579281 accounts (379167 ATOM, 200114 Evmos)
```

That digest is the one published in [GenesisL1/genesisl1-untouched-airdrop](https://github.com/GenesisL1/genesisl1-untouched-airdrop) (`SHA256SUMS`). The list is compiled into the binary; nothing is downloaded at the upgrade.

## 2. Optional, recommended: preview the result on your own state

`simulate` runs exactly the upgrade code against your node's latest state and saves nothing. The database cannot be opened while the node runs, so use a copy of the home directory, or a non-validating node you can stop for a few minutes.

```bash
# On a copy (a minute of downtime): stop, copy, restart
sudo systemctl stop genesisd && cp -a ~/.genesis /data/genesis-copy && sudo systemctl start genesisd
./build/genesisd airdrop-recovery simulate --home /data/genesis-copy --accounts-out accounts.csv > report.json
cat report.json
```

The report goes to standard output, the logs to standard error. It looks like this (figures depend on the height):

```json
{
  "height": 14120000,
  "list_sha256": "1297cfe6dceed738d7fc34b6412826b9f3f249461bfcc1418fecff32c2e8321b",
  "listed": 579281,
  "recovered_accounts": 575000,
  "recovered_l1": "5150000.123456789012345678",
  "skipped": { "below_airdrop": 0, "extra_balance": 3, "no_account": 0, "not_plain_account": 0, "signed": 4278 },
  "elapsed": "1m12s"
}
```

At the same height your figures must equal the ones the release team publishes. `accounts.csv` has one row per listed account (address, airdrop, outcome, balance), so you can check any account. `elapsed` gives an idea of how long the upgrade block will take on your hardware: that block is slower than usual, which is expected.

## 3. Prepare the switch

Use the method that matches how your node runs.

### A. Cosmovisor

```bash
mkdir -p ~/.genesis/cosmovisor/upgrades/v1.6.2.1/bin
cp ./build/genesisd ~/.genesis/cosmovisor/upgrades/v1.6.2.1/bin/genesisd
~/.genesis/cosmovisor/upgrades/v1.6.2.1/bin/genesisd airdrop-recovery verify-list
```

Cosmovisor switches binaries at the halt by itself. Do not use B or C on a cosmovisor node.

### B. Staged script (systemd, no cosmovisor)

It builds the release while your node keeps running, waits for the halt, backs up your config, `priv_validator_state.json` and keyrings, lifts the airdrop block, installs the new binary and starts the service. Run it in `tmux` or `screen` so that it survives your SSH session:

```bash
cd ~/genesis-crypto && git fetch --tags && git checkout v1.6.2.1
UPGRADE_HEIGHT=<HEIGHT> EXPECTED_COMMIT=<COMMIT> bash setup/genesis-staged-upgrade.sh
```

### C. By hand after the halt

Wait until the node has halted (step 4), then:

```bash
cd ~/genesis-crypto && git fetch --tags && git checkout v1.6.2.1
sh setup/upgrade.sh          # stops the service, backs up, builds, installs; answer Y to lift the airdrop block
sudo systemctl start genesisd
```

### Backups

Before the upgrade height keep a copy of `~/.genesis/config/` and `~/.genesis/data/priv_validator_state.json`. If you have the disk space, a copy or snapshot of `~/.genesis/data` lets you recover from a damaged database without syncing again. **Never run two nodes with the same validator key.**

## 4. At the upgrade height

The old binary stops processing blocks with:

```
ERR UPGRADE "v1.6.2.1" NEEDED at height: <HEIGHT>: {"tag":"v1.6.2.1","commit":"<COMMIT>",...}
ERR CONSENSUS FAILURE!!! err="failed to apply block; error UPGRADE \"v1.6.2.1\" NEEDED at height: <HEIGHT>: ..."
```

This is the normal halt. The process may keep running with its RPC still answering at height `<HEIGHT>`−1; stop it before you start the new binary (scripts B and C and cosmovisor do this). It also writes `~/.genesis/data/upgrade-info.json`.

The new binary then applies the upgrade, logging one progress line every 50,000 accounts:

```
INF applying upgrade "v1.6.2.1" at height: <HEIGHT> module=x/upgrade
INF airdrop recovery started list_height=14008743 list_sha256=1297cfe6... listed=579281 module=airdrop-recovery
INF airdrop recovery progress listed=579281 processed=50000 recovered=... recovered_el1=... module=airdrop-recovery
INF airdrop recovery finished recovered=... recovered_el1=... skipped_signed=... community_pool_el1=... module=airdrop-recovery
```

The upgrade block itself takes about a minute (53 seconds on a 2-vCPU test machine with every listed account qualifying); keep about 2 GB of memory free for it. Blocks resume once validators holding more than two thirds of the voting power run the new binary. Stay reachable until then.

## 5. After the upgrade: lift the airdrop block

The signal proposal said that the temporary block ends with the upgrade. Scripts B and C do this for you. On a cosmovisor node, or if you skipped it, run:

```bash
bash ~/genesis-crypto/setup/lift-airdrop-block.sh ~/.genesis/config/app.toml
sudo systemctl restart genesisd
```

The script keeps a timestamped backup of `app.toml` and prints `nothing to lift` if the setting is already gone. After the restart the log shows `Setting ante handler without blacklist` instead of `Setting ante handler with blacklist size=579281`.

## 6. Check the result

```bash
python3 ~/genesis-crypto/upgrades/v1.6.2.1/verify_upgrade.py --height <HEIGHT> \
  --rpc http://127.0.0.1:26657 --lcd http://127.0.0.1:1317
```

It needs Python 3 and nothing else, a node whose REST API is on (`[api] enable = true` in `app.toml`) and which still holds the state of heights `<HEIGHT>`−2 to `<HEIGHT>` (any recent node does). It reads the upgrade block's `airdrop_recovery` event, checks the community pool and total supply around the upgrade, and checks 300 random listed accounts against the rule; add `--address genesis1...` to check particular accounts. It must end with `ALL CHECKS PASSED`.

## If something goes wrong

- **The new binary stops at the upgrade height with an error.** Do not improvise; post the full log in the validators' channel. Nothing from the upgrade block was committed, so the state is intact. The fix is a patch release with the same plan name: install it and start again, and the upgrade runs from the start.
- **The community decides not to upgrade after the halt.** The validators agree on it publicly, then each starts the **old** v1.6.2 binary with `--unsafe-skip-upgrades <HEIGHT>` (with cosmovisor, add the flag to the `start` arguments of the service). Blocks resume once more than two thirds of the voting power has done so.
- **`wrong Block.Header.AppHash` after the upgrade.** Your binary differs from the network's. Rebuild from the published commit, check it with step 1 and restart. If the database was written past the mismatch, restore your copy of `data` or sync again.
- **`BINARY UPDATED BEFORE TRIGGER`, or `wrong app version …, upgrade handler is missing for v1.6.2 upgrade plan`.** You started the new binary before the upgrade height. Put the old v1.6.2 binary back until the halt.
