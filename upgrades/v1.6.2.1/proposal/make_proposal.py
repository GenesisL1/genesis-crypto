#!/usr/bin/env python3
"""Build proposal.json for the v1.6.2.1 software upgrade (standard library only).

It fills summary.md with the release and dry-run figures, checks GenesisL1's governance limits and writes a proposal
for `genesisd tx gov submit-proposal`. Every value is checked; nothing is left as a placeholder.

    python3 make_proposal.py --signal-id 137 --height 14147367 --upgrade-time "2026-12-03 14:00" \\
        --tag v1.6.2.1 --commit <40-hex commit of the tag> \\
        --dryrun-height 14120000 --dryrun-accounts 575123 --dryrun-l1 5120345.67 \\
        [--binary linux/amd64 <url> <sha256>] [--out proposal.json]
"""
import argparse
import json
import os
import re
import sys

PLAN_NAME = "v1.6.2.1"
GOV_AUTHORITY = "genesis10d07y265gmmuvt4z0w9aw880jnsr700jyt8njx"   # gov module account on genesis_29-2
DEFAULT_DEPOSIT = "10000000000000000000000el1"                       # 10,000 L1, as recent proposals
DEFAULT_TITLE = "Software Upgrade v1.6.2.1: Return Untouched ATOM & Evmos Airdrop L1 to the Community Pool"
MAX_SUMMARY = 40 * 255   # this chain's gov keeper: 40 x MaxMetadataLen (255) bytes
MAX_METADATA = 255
MAX_TITLE = 140
REPO = "https://github.com/GenesisL1/genesis-crypto"
HERE = os.path.dirname(os.path.abspath(__file__))


def main():
    ap = argparse.ArgumentParser(description="Build proposal.json for software upgrade v1.6.2.1")
    ap.add_argument("--signal-id", type=int, required=True, help="id of the passed signal proposal")
    ap.add_argument("--height", type=int, required=True, help="upgrade height (see upgrade_height.py)")
    ap.add_argument("--upgrade-time", required=True, help='expected UTC time of the upgrade, e.g. "2026-12-03 14:00"')
    ap.add_argument("--tag", required=True, help="release tag, e.g. v1.6.2.1")
    ap.add_argument("--commit", required=True, help="full 40-character commit the tag points to")
    ap.add_argument("--dryrun-height", type=int, required=True, help="height the simulate command ran at")
    ap.add_argument("--dryrun-accounts", type=int, required=True, help="recovered_accounts from simulate")
    ap.add_argument("--dryrun-l1", required=True, help="recovered_l1 from simulate, e.g. 5120345.67")
    ap.add_argument("--binary", nargs=3, action="append", metavar=("PLATFORM", "URL", "SHA256"),
                    help="a prebuilt binary for cosmovisor auto-download, e.g. linux/amd64 <url> <sha256>")
    ap.add_argument("--deposit", default=DEFAULT_DEPOSIT, help=f"initial deposit (default {DEFAULT_DEPOSIT})")
    ap.add_argument("--title", default=DEFAULT_TITLE)
    ap.add_argument("--metadata", default="", help=f"optional link, at most {MAX_METADATA} bytes")
    ap.add_argument("--out", default="proposal.json")
    a = ap.parse_args()

    errors = []
    if not re.fullmatch(r"[0-9a-f]{40}", a.commit):
        errors.append("--commit must be the full 40-character lower-case hex commit")
    if a.height <= a.dryrun_height:
        errors.append("--height must be above --dryrun-height")
    if not 0 < a.dryrun_accounts <= 579281:
        errors.append("--dryrun-accounts must be between 1 and 579281")
    if not re.fullmatch(r"\d+(\.\d+)?", a.dryrun_l1.replace(",", "")):
        errors.append("--dryrun-l1 must be a number of L1, e.g. 5120345.67")
    if not re.fullmatch(r"\d+el1", a.deposit):
        errors.append("--deposit must look like 10000000000000000000000el1")
    if len(a.title) > MAX_TITLE:
        errors.append(f"--title is {len(a.title)} characters; at most {MAX_TITLE}")
    if len(a.metadata.encode()) > MAX_METADATA:
        errors.append(f"--metadata is {len(a.metadata.encode())} bytes; at most {MAX_METADATA}")
    for platform, url, sha in a.binary or []:
        if not re.fullmatch(r"[a-z0-9]+/[a-z0-9]+", platform):
            errors.append(f"--binary platform {platform!r} should look like linux/amd64")
        if not url.startswith("https://"):
            errors.append(f"--binary URL {url!r} must be https")
        if not re.fullmatch(r"[0-9a-f]{64}", sha):
            errors.append(f"--binary sha256 {sha!r} must be 64 lower-case hex characters")
    if errors:
        sys.exit("\n".join(errors))

    dryrun_l1 = a.dryrun_l1.replace(",", "")
    whole, _, frac = dryrun_l1.partition(".")
    dryrun_l1 = f"{int(whole):,}" + (f".{frac}" if frac else "")

    with open(os.path.join(HERE, "summary.md"), encoding="utf-8") as f:
        summary = f.read()
    values = {
        "SIGNAL_ID": str(a.signal_id),
        "HEIGHT": f"{a.height:,}",
        "UPGRADE_TIME_UTC": a.upgrade_time,
        "TAG": a.tag,
        "COMMIT": a.commit,
        "DRYRUN_HEIGHT": f"{a.dryrun_height:,}",
        "DRYRUN_ACCOUNTS": f"{a.dryrun_accounts:,}",
        "DRYRUN_L1": dryrun_l1,
    }
    for key, value in values.items():
        summary = summary.replace("{{" + key + "}}", value)
    left = sorted(set(re.findall(r"\{\{(\w+)\}\}", summary)))
    if left:
        sys.exit(f"summary.md still has placeholders: {', '.join(left)}")
    size = len(summary.encode())
    if size > MAX_SUMMARY:
        sys.exit(f"summary is {size} bytes; the chain accepts at most {MAX_SUMMARY}")

    info = {"tag": a.tag, "commit": a.commit,
            "guide": f"{REPO}/blob/{a.tag}/upgrades/{PLAN_NAME}/VALIDATOR_GUIDE.md"}
    if a.binary:
        info["binaries"] = {p: f"{u}?checksum=sha256:{s}" for p, u, s in a.binary}

    proposal = {
        "messages": [{
            "@type": "/cosmos.upgrade.v1beta1.MsgSoftwareUpgrade",
            "authority": GOV_AUTHORITY,
            "plan": {
                "name": PLAN_NAME,
                "height": str(a.height),
                "info": json.dumps(info, separators=(",", ":")),
            },
        }],
        "metadata": a.metadata,
        "deposit": a.deposit,
        "title": a.title,
        "summary": summary,
        "expedited": False,
    }
    with open(a.out, "w", encoding="utf-8") as f:
        json.dump(proposal, f, indent=2, ensure_ascii=False)
        f.write("\n")

    print(f"wrote {a.out}: plan {PLAN_NAME} at height {a.height}, summary {size} of {MAX_SUMMARY} bytes")
    print("submit with:")
    print(f"  genesisd tx gov submit-proposal {a.out} --from <key> --chain-id genesis_29-2 \\")
    print("    --gas auto --gas-adjustment 1.5 --gas-prices <your node's minimum gas price>el1")
    return 0


if __name__ == "__main__":
    sys.exit(main())
