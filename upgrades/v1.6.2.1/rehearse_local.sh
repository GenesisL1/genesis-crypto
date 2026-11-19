#!/usr/bin/env bash
# Rehearse upgrade v1.6.2.1 end to end on a throwaway single-validator chain on
# this machine, with the real binaries:
#
#   1. the old binary starts a chain (chain id genesis_29-2) whose genesis holds
#      listed airdrop accounts covering every outcome of the proposal's rule;
#   2. the new binary's `airdrop-recovery simulate` previews the result on a
#      copy of the node;
#   3. proposal/make_proposal.py builds the upgrade proposal, which is
#      submitted, voted and passed;
#   4. the old binary halts at the upgrade height, setup/lift-airdrop-block.sh
#      lifts the temporary block in app.toml and the new binary takes over;
#   5. verify_upgrade.py checks the result, and every listed account set up in
#      genesis must have the outcome the rule gives it.
#
#   bash upgrades/v1.6.2.1/rehearse_local.sh OLD_GENESISD NEW_GENESISD [WORKDIR]
#
# OLD_GENESISD is genesisd built from tag v1.6.2, NEW_GENESISD from the
# v1.6.2.1 release. Needs python3 and the free local ports 36656-36658, 31317
# and 39090. It never touches ~/.genesis: everything lives in WORKDIR (default:
# a new temporary directory). Takes about three minutes.
set -euo pipefail

[ $# -ge 2 ] || { sed -n '2,24p' "$0"; exit 2; }
OLD=$(readlink -f "$1")
NEW=$(readlink -f "$2")
W=${3:-$(mktemp -d /tmp/gl1-rehearsal.XXXXXX)}
HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(cd "$HERE/../.." && pwd)
H=$W/home
CHAIN=genesis_29-2
RPC=http://127.0.0.1:36657
LCD=http://127.0.0.1:31317
NODE=tcp://127.0.0.1:36657
TX=(--chain-id "$CHAIN" --node "$NODE" --keyring-backend test --home "$H" --from val
    --gas 3000000 --fees 1000000000000000el1 -y --output json)
Q=(--node "$NODE" --output json)
PID=

log() { printf '\n== %s\n' "$*"; }
die() { printf '\nREHEARSAL FAILED: %s\n(logs and files: %s)\n' "$*" "$W" >&2; exit 1; }
cleanup() { [ -n "$PID" ] && kill "$PID" 2>/dev/null && wait "$PID" 2>/dev/null; true; }
trap cleanup EXIT

[ -x "$OLD" ] && [ -x "$NEW" ] || die "both binaries must exist and be executable"
command -v python3 >/dev/null || die "python3 is required"
for port in 36656 36657 36658 31317 39090; do
    python3 - "$port" <<'EOF' || die "local port $port is in use"
import socket, sys
s = socket.socket(); s.bind(("127.0.0.1", int(sys.argv[1]))); s.close()
EOF
done
mkdir -p "$W"
rm -rf "$H"

height() {
    python3 - "$RPC" <<'EOF' 2>/dev/null || echo 0
import json, sys, urllib.request
print(json.load(urllib.request.urlopen(sys.argv[1] + "/status", timeout=5))["result"]["sync_info"]["latest_block_height"])
EOF
}

wait_height() {  # wait_height H TIMEOUT_SECONDS
    local deadline=$((SECONDS + $2))
    until [ "$(height)" -ge "$1" ]; do
        [ $SECONDS -lt $deadline ] || die "height $1 not reached within $2 s (now $(height))"
        sleep 1
    done
}

start_node() {  # start_node BINARY LOGFILE
    "$1" start --home "$H" >"$2" 2>&1 &
    PID=$!
}

stop_node() {
    [ -n "$PID" ] || return 0
    kill "$PID" 2>/dev/null || true
    wait "$PID" 2>/dev/null || true
    PID=
}

# toml_set FILE SECTION KEY VALUE: set KEY in [SECTION] ("" = top level).
toml_set() {
    python3 - "$@" <<'EOF'
import re, sys
path, section, key, value = sys.argv[1:]
lines = open(path).read().split("\n")
current, done = "", False
for i, line in enumerate(lines):
    m = re.match(r"\s*\[([^\]]+)\]\s*$", line)
    if m:
        current = m.group(1)
        continue
    if current == section and re.match(rf"\s*{re.escape(key)}\s*=", line):
        lines[i] = f"{key} = {value}"
        done = True
        break
if not done:
    sys.exit(f"{path}: no {key} in [{section}]")
open(path, "w").write("\n".join(lines))
EOF
}

log "1. chain with listed accounts in genesis (old binary: $("$OLD" version 2>&1 | head -1))"
"$OLD" init rehearsal --chain-id "$CHAIN" --home "$H" >/dev/null 2>&1
"$OLD" keys add val --keyring-backend test --home "$H" --output json >"$W/val.json" 2>"$W/keys.log"
VAL=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["address"])' "$W/val.json")
"$OLD" genesis add-genesis-account "$VAL" 1000000000000000000000000el1 --home "$H"

# Eight listed accounts, one or more per outcome. The vesting account is
# created by add-genesis-account; the "signed" one gets sequence 1 below.
python3 - "$REPO/app/migration_data/untouched.csv.gz" "$W/fixtures.json" <<'EOF'
import csv, gzip, json, sys
atom, evmos = [], []
with gzip.open(sys.argv[1], "rt", newline="") as f:
    for r in csv.DictReader(f):
        (atom if r["airdrop"] == "ATOM" else evmos).append(r["address"])
        if len(atom) >= 5 and len(evmos) >= 3:
            break
fixtures = [
    # address, airdrop, el1 balance, setup, outcome the rule gives
    (atom[0], "ATOM", "11000000000000000000", "plain", "recovered"),
    (atom[1], "ATOM", "11002000000000000000", "plain", "recovered"),
    (atom[2], "ATOM", "12000000000000000000", "plain", "extra_balance"),
    (atom[3], "ATOM", "11000000000000000000", "signed", "signed"),
    (atom[4], "ATOM", "11000000000000000000", "vesting", "not_plain_account"),
    (evmos[0], "Evmos", "5000000000000000000", "plain", "recovered"),
    (evmos[1], "Evmos", "4900000000000000000", "plain", "below_airdrop"),
    (evmos[2], "Evmos", "5500000000000000000", "plain", "recovered"),
]
json.dump([dict(zip(("address", "airdrop", "el1", "setup", "expect"), f)) for f in fixtures],
          open(sys.argv[2], "w"), indent=1)
EOF
NOW=$(date +%s)
while IFS=' ' read -r addr el1 setup; do
    if [ "$setup" = vesting ]; then
        "$OLD" genesis add-genesis-account "$addr" "${el1}el1" --home "$H" --vesting-amount "${el1}el1" \
            --vesting-start-time "$NOW" --vesting-end-time $((NOW + 315360000))
    else
        "$OLD" genesis add-genesis-account "$addr" "${el1}el1" --home "$H"
    fi
done < <(python3 -c 'import json,sys
for f in json.load(open(sys.argv[1])): print(f["address"], f["el1"], f["setup"])' "$W/fixtures.json")
EXPECTED_EL1=$(python3 -c 'import json,sys
print(sum(int(f["el1"]) for f in json.load(open(sys.argv[1])) if f["expect"] == "recovered"))' "$W/fixtures.json")

python3 - "$H/config/genesis.json" "$W/fixtures.json" <<'EOF'
import json, sys
path, fixtures = sys.argv[1], json.load(open(sys.argv[2]))
g = json.load(open(path))
def walk(x):
    if isinstance(x, dict):
        return {k: walk(v) for k, v in x.items()}
    if isinstance(x, list):
        return [walk(v) for v in x]
    return "el1" if x in ("stake", "aphoton") else x
g["app_state"] = walk(g["app_state"])
gov = g["app_state"]["gov"]["params"]
gov.update(voting_period="20s", expedited_voting_period="10s", max_deposit_period="60s",
           min_deposit=[{"denom": "el1", "amount": "1000000000000000000000"}],
           expedited_min_deposit=[{"denom": "el1", "amount": "2000000000000000000000"}])
g["app_state"]["feemarket"]["params"]["no_base_fee"] = True
signed = {f["address"] for f in fixtures if f["setup"] == "signed"}
for acc in g["app_state"]["auth"]["accounts"]:
    base = acc.get("base_account") or acc.get("base_vesting_account", {}).get("base_account") or acc
    if base.get("address") in signed:
        base["sequence"] = "1"
json.dump(g, open(path, "w"), indent=1)
EOF
"$OLD" genesis gentx val 100000000000000000000000el1 --chain-id "$CHAIN" --keyring-backend test --home "$H" >/dev/null 2>&1
"$OLD" genesis collect-gentxs --home "$H" >/dev/null 2>&1
"$OLD" genesis validate --home "$H" >/dev/null || die "genesis does not validate"

C=$H/config
toml_set "$C/config.toml" "" proxy_app '"tcp://127.0.0.1:36658"'
toml_set "$C/config.toml" rpc laddr '"tcp://127.0.0.1:36657"'
toml_set "$C/config.toml" rpc pprof_laddr '""'
toml_set "$C/config.toml" p2p laddr '"tcp://127.0.0.1:36656"'
toml_set "$C/config.toml" consensus timeout_commit '"1s"'
toml_set "$C/app.toml" "" minimum-gas-prices '"0el1"'
toml_set "$C/app.toml" "" pruning '"nothing"'
toml_set "$C/app.toml" api enable true
toml_set "$C/app.toml" api address '"tcp://127.0.0.1:31317"'
toml_set "$C/app.toml" grpc address '"127.0.0.1:39090"'
toml_set "$C/app.toml" json-rpc enable false
# The temporary airdrop block, as validators run it today.
python3 - "$C/app.toml" "$W/fixtures.json" <<'EOF'
import json, sys
path, fixtures = sys.argv[1], json.load(open(sys.argv[2]))
text = open(path).read()
block = "# Temporary airdrop block (published list)\nblocked-addresses = [\n" + \
        "".join(f'  "{f["address"]}",\n' for f in fixtures) + "]\n\n"
i = text.index("\n[")  # top-level keys must come before the first table
open(path, "w").write(text[:i + 1] + block + text[i + 1:])
EOF

start_node "$OLD" "$W/old.log"
wait_height 3 60
echo "chain running at height $(height)"

log "2. dry run with the new binary on a copy of the node"
stop_node
rm -rf "$W/copy" && cp -a "$H" "$W/copy"
start_node "$OLD" "$W/old.log.2"
"$NEW" airdrop-recovery simulate --home "$W/copy" --accounts-out "$W/dryrun-accounts.csv" >"$W/dryrun.json" 2>"$W/dryrun.log" ||
    die "simulate failed: $(tail -5 "$W/dryrun.log")"
cat "$W/dryrun.json"
python3 - "$W/dryrun.json" "$W/dryrun-accounts.csv" "$W/fixtures.json" "$EXPECTED_EL1" <<'EOF' || die "dry run does not match the fixtures"
import csv, json, sys
report, fixtures, expected = json.load(open(sys.argv[1])), json.load(open(sys.argv[3])), int(sys.argv[4])
rows = {r["address"]: r for r in csv.DictReader(open(sys.argv[2]))}
bad = [f"{f['address']}: {rows[f['address']]['outcome']} != {f['expect']}" for f in fixtures
       if rows[f["address"]]["outcome"] != f["expect"]]
bad += [] if int(report["recovered_el1"]) == expected else [f"recovered_el1 {report['recovered_el1']} != {expected}"]
bad += [] if report["recovered_accounts"] == 4 and len(rows) == 579281 else ["wrong counts"]
print("\n".join(bad) or "dry run matches the fixtures: 4 accounts, %s el1" % expected)
sys.exit(1 if bad else 0)
EOF
DRY_HEIGHT=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["height"])' "$W/dryrun.json")
DRY_L1=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["recovered_l1"])' "$W/dryrun.json")
wait_height $((DRY_HEIGHT + 2)) 60

log "3. upgrade proposal built by make_proposal.py, submitted, voted"
U=$(( $(height) + 45 ))
COMMIT=$(git -C "$REPO" rev-parse HEAD 2>/dev/null || printf '%040d' 0)
python3 "$HERE/proposal/make_proposal.py" --signal-id 1 --height "$U" \
    --upgrade-time "$(date -u -d "+50 seconds" '+%Y-%m-%d %H:%M:%S')" --tag v1.6.2.1 --commit "$COMMIT" \
    --dryrun-height "$DRY_HEIGHT" --dryrun-accounts 4 --dryrun-l1 "$DRY_L1" --out "$W/proposal.json"
"$OLD" tx gov submit-proposal "$W/proposal.json" "${TX[@]}" >"$W/submit.json"
sleep 4
PROP=$("$OLD" query gov proposals "${Q[@]}" | python3 -c 'import json,sys
print(max(int(p["id"]) for p in json.load(sys.stdin)["proposals"]))')
"$OLD" tx gov vote "$PROP" yes "${TX[@]}" >"$W/vote.json"
echo "proposal $PROP: plan v1.6.2.1 at height $U"
deadline=$((SECONDS + 90))
until "$OLD" query gov proposal "$PROP" "${Q[@]}" | grep -q PROPOSAL_STATUS_PASSED; do
    [ $SECONDS -lt $deadline ] || die "proposal $PROP did not pass"
    sleep 2
done
"$OLD" query upgrade plan "${Q[@]}" | grep -q '"v1.6.2.1"' || die "no v1.6.2.1 plan after the proposal passed"
echo "proposal $PROP passed; plan scheduled"

log "4. halt at $U, lift the airdrop block, switch binaries"
deadline=$((SECONDS + 180))
until grep -q "UPGRADE \"v1.6.2.1\" NEEDED at height: $U" "$W/old.log.2"; do
    [ $SECONDS -lt $deadline ] || die "the old binary did not halt at $U"
    sleep 1
done
stop_node
echo "old binary halted: $(grep -o "UPGRADE \"v1.6.2.1\" NEEDED at height: $U" "$W/old.log.2" | head -1)"
bash "$REPO/setup/lift-airdrop-block.sh" "$C/app.toml"
! grep -q 'blocked-addresses' "$C/app.toml" || die "blocked-addresses is still in app.toml"
start_node "$NEW" "$W/new.log"
wait_height $((U + 3)) 180
grep -q "applying upgrade \"v1.6.2.1\" at height: $U" "$W/new.log" || die "no 'applying upgrade' line in new.log"
grep 'airdrop recovery finished' "$W/new.log" | head -1

log "5. verify_upgrade.py and the outcome of every listed account set up in genesis"
ARGS=()
while read -r addr; do ARGS+=(--address "$addr"); done < <(python3 -c 'import json,sys
for f in json.load(open(sys.argv[1])): print(f["address"])' "$W/fixtures.json")
python3 "$HERE/verify_upgrade.py" --height "$U" --rpc "$RPC" --lcd "$LCD" --sample 20 --seed 1 --rate 50 "${ARGS[@]}" |
    tee "$W/verify.log" || die "verify_upgrade.py reported a failure"
python3 - "$W/verify.log" "$W/fixtures.json" <<'EOF' || die "an account did not get the outcome the rule gives it"
import json, re, sys
seen = dict(re.findall(r"^ok\s+(\S+)\s+(\w+):", open(sys.argv[1]).read(), re.M))
bad = [f"{f['address']}: {seen.get(f['address'])} != {f['expect']}" for f in json.load(open(sys.argv[2]))
       if seen.get(f["address"]) != f["expect"]]
print("\n".join(bad) or "every listed account in genesis got the outcome the rule gives it")
sys.exit(1 if bad else 0)
EOF
stop_node
printf '\nREHEARSAL PASSED: plan v1.6.2.1 at height %s recovered %s el1 from 4 accounts; files in %s\n' \
    "$U" "$EXPECTED_EL1" "$W"
