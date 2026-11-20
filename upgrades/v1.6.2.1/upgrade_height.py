#!/usr/bin/env python3
"""Pick the block height for a software upgrade from a target time (standard library only).

It reads the latest block and the block WINDOW heights earlier from a CometBFT RPC, measures the average block time
between them, and estimates the height that the chain reaches at --target. It also checks that the target leaves
enough room after the governance vote: a plan height the chain has already passed when the proposal executes makes
the proposal fail.

    python3 upgrade_height.py --target 2026-12-03T14:00:00Z
    python3 upgrade_height.py --target 2026-12-03T14:00:00Z --rpc http://127.0.0.1:26657 --submit 2026-11-24T12:00:00Z

Run it again just before submitting the proposal: block time drifts.
"""
import argparse
import datetime as dt
import json
import sys
import urllib.request

VOTING_PERIOD = 618033   # seconds, GenesisL1 governance voting period (proposal #130)
MIN_MARGIN = 24 * 3600   # seconds between the end of voting and the upgrade


def parse_time(s):
    """RFC 3339 with up to nanoseconds and a Z or +hh:mm suffix."""
    s = s.strip().replace("Z", "+00:00")
    if "." in s:
        head, rest = s.split(".", 1)
        frac, tz = rest, ""
        for sign in "+-":
            if sign in rest:
                frac, tz = rest.split(sign, 1)
                tz = sign + tz
                break
        s = f"{head}.{frac[:6].ljust(6, '0')}{tz}"
    t = dt.datetime.fromisoformat(s)
    if t.tzinfo is None:
        raise ValueError(f"time without a timezone: {s}")
    return t.astimezone(dt.timezone.utc)


def rpc(url, path):
    req = urllib.request.Request(url.rstrip("/") + path, headers={"User-Agent": "genesisl1-upgrade-height/1.0"})
    with urllib.request.urlopen(req, timeout=30) as r:
        body = json.loads(r.read())
    return body.get("result", body)


def block_time(url, height):
    header = rpc(url, f"/block?height={height}")["block"]["header"]
    return parse_time(header["time"])


def main():
    ap = argparse.ArgumentParser(description="Estimate the upgrade height for a target UTC time")
    ap.add_argument("--target", required=True, help="when the upgrade should happen, e.g. 2026-12-03T14:00:00Z")
    ap.add_argument("--rpc", default="http://127.0.0.1:26657", help="CometBFT RPC (default http://127.0.0.1:26657)")
    ap.add_argument("--window", type=int, default=20000, help="blocks to average over (default 20000, about 3 days)")
    ap.add_argument("--submit", help="when the proposal will be submitted (default: now)")
    ap.add_argument("--voting-period", type=int, default=VOTING_PERIOD, help=f"seconds (default {VOTING_PERIOD})")
    a = ap.parse_args()

    target = parse_time(a.target)
    submit = parse_time(a.submit) if a.submit else dt.datetime.now(dt.timezone.utc)

    status = rpc(a.rpc, "/status")["sync_info"]
    latest = int(status["latest_block_height"])
    latest_time = parse_time(status["latest_block_time"])
    base = max(1, latest - a.window)
    base_time = block_time(a.rpc, base)
    avg = (latest_time - base_time).total_seconds() / (latest - base)

    seconds_ahead = (target - latest_time).total_seconds()
    if seconds_ahead <= 0:
        sys.exit(f"--target {target:%Y-%m-%d %H:%M} UTC is not after the latest block time {latest_time:%Y-%m-%d %H:%M} UTC")
    height = latest + round(seconds_ahead / avg)

    voting_end = submit + dt.timedelta(seconds=a.voting_period)
    margin = (target - voting_end).total_seconds()

    print(f"latest block      {latest} at {latest_time:%Y-%m-%d %H:%M:%S} UTC")
    print(f"average block     {avg:.3f} s over blocks {base}-{latest}")
    print(f"target time       {target:%Y-%m-%d %H:%M:%S} UTC ({seconds_ahead / 86400:.2f} days ahead)")
    print(f"upgrade height    {height}")
    print(f"voting ends       {voting_end:%Y-%m-%d %H:%M:%S} UTC if submitted {submit:%Y-%m-%d %H:%M} UTC with the full deposit")
    drift = 0.05 * seconds_ahead
    print(f"uncertainty       a 5% change in block time moves the upgrade by about {drift / 3600:.1f} hours")
    if margin < MIN_MARGIN:
        print(f"WARNING           only {margin / 3600:.1f} hours between the end of voting and the upgrade; "
              f"leave at least {MIN_MARGIN // 3600} hours so validators can install the binary", file=sys.stderr)
        return 1
    print(f"margin            {margin / 3600:.1f} hours between the end of voting and the upgrade")
    return 0


if __name__ == "__main__":
    sys.exit(main())
