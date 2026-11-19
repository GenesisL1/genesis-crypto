#!/usr/bin/env python3
"""Check upgrade v1.6.2.1 from outside the chain (standard library only).

Given the upgrade height U, it reads:

1. the airdrop_recovery event in the results of block U (CometBFT RPC);
2. the community pool and the el1 total supply at U-2, U-1 and U (LCD, header x-cosmos-block-height): the pool must
   grow by the recovered amount plus its usual per-block inflow, and total supply by its usual per-block mint only;
3. a sample of listed accounts at U-1 and U: each account the proposal's rule selects at U-1 must be empty at U,
   and every other listed account must keep its balance.

    python3 verify_upgrade.py --height 14147367 --rpc http://127.0.0.1:26657 --lcd http://127.0.0.1:1317
    python3 verify_upgrade.py --height 14147367 --sample 0           # totals only
    python3 verify_upgrade.py --height 14147367 --sample 579281      # every listed account (slow)
    python3 verify_upgrade.py --height 14147367 --address genesis1...  # also check these accounts

The node must still hold state at U-2 (default pruning keeps it for weeks). Errors are reported, never read as a
pass. Exit code 0 means every check passed.
"""
import argparse
import csv
import gzip
import json
import os
import random
import sys
import time
import urllib.error
import urllib.request
from decimal import Decimal

WEI = 10 ** 18
HERE = os.path.dirname(os.path.abspath(__file__))
DEFAULT_LIST = os.path.normpath(os.path.join(HERE, "..", "..", "app", "migration_data", "untouched.csv.gz"))
AIRDROP = {"ATOM": 11 * WEI, "Evmos": 5 * WEI}
UA = {"User-Agent": "genesisl1-v1.6.2.1-verify/1.0"}


class NotFound(Exception):
    pass


def get_json(url, headers=None):
    req = urllib.request.Request(url, headers={**UA, **(headers or {})})
    try:
        with urllib.request.urlopen(req, timeout=120) as r:
            return json.loads(r.read())
    except urllib.error.HTTPError as e:
        body = e.read()[:300].decode(errors="replace")
        if e.code == 404 or "not found" in body.lower():
            raise NotFound(body) from None
        raise RuntimeError(f"HTTP {e.code} from {url}: {body}") from None


def lcd(base, path, height):
    return get_json(base.rstrip("/") + path, {"x-cosmos-block-height": str(height)})


def l1(wei):
    sign = "-" if wei < 0 else ""
    wei = abs(int(wei))
    return f"{sign}{wei // WEI:,}.{wei % WEI:018d}".rstrip("0").rstrip(".")


def recovery_event(rpc, height):
    res = get_json(f"{rpc.rstrip('/')}/block_results?height={height}")
    res = res.get("result", res)
    events = res.get("finalize_block_events") or res.get("begin_block_events") or []
    for ev in events:
        if ev.get("type") == "airdrop_recovery":
            return {a["key"]: a["value"] for a in ev.get("attributes", [])}
    return None


def community_pool(base, height):
    pool = lcd(base, "/cosmos/distribution/v1beta1/community_pool", height).get("pool") or []
    for c in pool:
        if c.get("denom") == "el1":
            return Decimal(c["amount"])
    return Decimal(0)


def supply(base, height):
    return int(lcd(base, "/cosmos/bank/v1beta1/supply/by_denom?denom=el1", height)["amount"]["amount"])


def account_state(base, addr, height):
    """(exists, sequence, has_pubkey, plain, el1 balance)."""
    try:
        acc = lcd(base, f"/cosmos/auth/v1beta1/accounts/{addr}", height)["account"]
    except NotFound:
        acc = None
    bal = lcd(base, f"/cosmos/bank/v1beta1/balances/{addr}/by_denom?denom=el1", height)
    wei = int((bal.get("balance") or {}).get("amount") or 0)
    if acc is None:
        return False, 0, False, True, wei
    typ = acc.get("@type", "")
    b = acc.get("base_account") or acc.get("base_vesting_account", {}).get("base_account") or acc
    seq = int(b.get("sequence") or 0)
    pk = bool(b.get("pub_key") or b.get("public_key"))
    code_hash = (acc.get("code_hash") or "").lower()
    empty = {"", "0x" + "0" * 64, "0xc5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470"}
    plain = "Vesting" not in typ and "ModuleAccount" not in typ and code_hash in empty
    return True, seq, pk, plain, wei


def expected_outcome(state, airdrop):
    exists, seq, pk, plain, wei = state
    if not exists:
        return "no_account"
    if seq != 0 or pk:
        return "signed"
    if not plain:
        return "not_plain_account"
    if wei < airdrop:
        return "below_airdrop"
    if wei >= airdrop + WEI:
        return "extra_balance"
    return "recovered"


def main():
    ap = argparse.ArgumentParser(description="Check upgrade v1.6.2.1 after it ran")
    ap.add_argument("--height", type=int, required=True, help="the upgrade height U")
    ap.add_argument("--rpc", default="http://127.0.0.1:26657", help="CometBFT RPC (default http://127.0.0.1:26657)")
    ap.add_argument("--lcd", default="http://127.0.0.1:1317", help="LCD holding state at U-2 (default http://127.0.0.1:1317)")
    ap.add_argument("--list", default=DEFAULT_LIST, help="untouched.csv.gz (default: the copy in this repository)")
    ap.add_argument("--sample", type=int, default=300, help="listed accounts to check (default 300; 0 = none)")
    ap.add_argument("--seed", type=int, default=None, help="random seed for the sample")
    ap.add_argument("--address", action="append", default=[], metavar="ADDR",
                    help="also check this listed account and print its result (repeatable)")
    ap.add_argument("--rate", type=float, default=4.0, help="LCD requests a second (default 4)")
    a = ap.parse_args()
    u = a.height
    failures = 0

    def verdict(ok, text):
        nonlocal failures
        failures += not ok
        print(f"{'PASS' if ok else 'FAIL'}  {text}", flush=True)

    # 1. The summary event.
    ev = recovery_event(a.rpc, u)
    if ev is None:
        verdict(False, f"no airdrop_recovery event in block {u}: is {u} the upgrade height?")
        return 1
    recovered = int(ev.get("amount", "0el1").removesuffix("el1") or 0)
    print(f"event: {ev.get('recovered_accounts')} accounts recovered "
          f"({ev.get('recovered_atom_accounts')} ATOM, {ev.get('recovered_evmos_accounts')} Evmos), {l1(recovered)} L1; "
          f"skipped: no account {ev.get('skipped_no_account')}, signed {ev.get('skipped_signed')}, "
          f"not plain {ev.get('skipped_not_plain_account')}, below airdrop {ev.get('skipped_below_airdrop')}, "
          f"extra balance {ev.get('skipped_extra_balance')}")
    verdict(ev.get("list_sha256") == "1297cfe6dceed738d7fc34b6412826b9f3f249461bfcc1418fecff32c2e8321b",
            f"event names the published list (sha256 {ev.get('list_sha256')})")
    verdict(ev.get("listed") == "579281", f"event covers all {ev.get('listed')} listed accounts")

    # 2. Community pool and total supply around the upgrade.
    p2, p1, p0 = (community_pool(a.lcd, h) for h in (u - 2, u - 1, u))
    s2, s1, s0 = (supply(a.lcd, h) for h in (u - 2, u - 1, u))
    usual_inflow = p1 - p2
    other_inflow = (p0 - p1) - Decimal(recovered)
    print(f"community pool: {l1(int(p1))} L1 at U-1, {l1(int(p0))} L1 at U "
          f"(grew {l1(int(p0 - p1))} L1 = recovery {l1(recovered)} + other {l1(int(other_inflow))}; "
          f"usual per-block inflow {l1(int(usual_inflow))})")
    tolerance = max(usual_inflow.copy_abs() * 3, Decimal(WEI))
    verdict(Decimal(0) <= other_inflow <= tolerance,
            "community pool grew by the recovered amount plus a normal per-block inflow")
    usual_mint, mint_at_u = s1 - s2, s0 - s1
    print(f"total supply: {l1(s1)} L1 at U-1, {l1(s0)} L1 at U (change {l1(mint_at_u)}, usual {l1(usual_mint)})")
    verdict(abs(mint_at_u - usual_mint) <= max(abs(usual_mint) // 100, WEI // 1000),
            "total supply changed only by the usual per-block mint (no coins created or burned)")

    # 3. A sample of listed accounts, plus any given with --address, before and after.
    if a.sample > 0 or a.address:
        with gzip.open(a.list, "rt", newline="") as f:
            listed = {r["address"]: AIRDROP[r["airdrop"]] for r in csv.DictReader(f)}
        unknown = [x for x in a.address if x not in listed]
        if unknown:
            verdict(False, f"not on the list: {', '.join(unknown)}")
        rows = []
        if a.sample > 0:
            everything = list(listed.items())
            rows = everything if a.sample >= len(everything) else random.Random(a.seed).sample(everything, a.sample)
        named = [x for x in dict.fromkeys(a.address) if x in listed]
        chosen = {x for x, _ in rows}
        rows += [(x, listed[x]) for x in named if x not in chosen]
        gap = 3.0 / a.rate if a.rate > 0 else 0   # three requests per account
        bad = 0
        for addr, airdrop in rows:
            t = time.monotonic()
            try:
                before = account_state(a.lcd, addr, u - 1)
                after = int((lcd(a.lcd, f"/cosmos/bank/v1beta1/balances/{addr}/by_denom?denom=el1", u)
                             .get("balance") or {}).get("amount") or 0)
            except Exception as e:  # noqa: BLE001 - report every error as a failure
                bad += 1
                print(f"FAIL  {addr}  ERROR {type(e).__name__}: {e}", flush=True)
                continue
            outcome = expected_outcome(before, airdrop)
            ok = after == 0 if outcome == "recovered" else after == before[4]
            if not ok:
                bad += 1
                print(f"FAIL  {addr}  expected {outcome}: {l1(before[4])} L1 at U-1, {l1(after)} L1 at U "
                      f"(a transaction in block {u} itself can also change a balance: check the account's "
                      f"transactions in that block)", flush=True)
            elif addr in named:
                print(f"ok    {addr}  {outcome}: {l1(before[4])} L1 at U-1, {l1(after)} L1 at U", flush=True)
            time.sleep(max(0.0, gap - (time.monotonic() - t)))
        verdict(bad == 0, f"{len(rows) - bad} of {len(rows)} checked listed accounts match the proposal's rule")

    print(f"{'ALL CHECKS PASSED' if failures == 0 else f'{failures} CHECK(S) FAILED'} for upgrade v1.6.2.1 at block {u}")
    return 0 if failures == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
