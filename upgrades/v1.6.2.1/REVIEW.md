# Review of the contributed v1.6.2.1g branch, and what changed

The starting point was ZENODE's branch [`zenodeapp/genesis-crypto@v1.6.2.1g-patch`](https://github.com/zenodeapp/genesis-crypto/tree/v1.6.2.1g-patch) (commits `dde533a` and `1e0bd0b`, on top of GenesisL1 `v1.6.2`, `ee86be8`). Its approach is sound and is kept: an upgrade handler named `v1.6.2.1` (omitted the 'g') that runs the module migrations and then moves the untouched airdrop balances to the community pool, with the address list compiled into the binary. Its address list is the published one: the same 579,281 addresses as `blocked-addresses.toml` and `untouched.csv.gz` in [GenesisL1/genesisl1-untouched-airdrop](https://github.com/GenesisL1/genesisl1-untouched-airdrop).

Mainnet runs v1.6.2 (upgrade proposal #121, height 13,093,000), so the new binary no longer needs the v1.6.2 handler; dropping it, as the branch does, is correct.

## Findings and changes

| # | Contributed branch | Risk | Now |
|---|---|---|---|
| 1 | Moves the **whole el1 balance** of every listed account whose sequence is 0. | It would also take L1 that anyone sent to a listed address after the list was made, which is not airdrop and not what the signal proposal describes. | The proposal's rule is enforced exactly. An account is recovered only if it exists, has never signed (sequence 0 **and** no public key), is a plain account (not a module, vesting or contract account) and holds at least its airdrop (11 L1 for ATOM, 5 L1 for Evmos) and less than 1 L1 above it. Every other account is left untouched. (The proposal's other conditions, nothing ever sent and nothing staked or unbonding, follow from "never signed", since each needs a transaction signed by the account.) The list's airdrop type is what makes this exact, so the binary now embeds the published CSV, which has it. |
| 2 | One `FundCommunityPool` call per account. | About 2.3 million bank events (four per transfer) in the upgrade block's results: block results of several hundred MB that RPC nodes, explorers and indexers may fail to serve or index, and all of them held in memory during the block. | Transfers emit no events (each account's events are dropped at once) and the community pool is credited once, with the same bookkeeping as `FundCommunityPool`. One `airdrop_recovery` event carries the totals; the per-account detail is reproducible from the published list and `genesisd airdrop-recovery simulate`. |
| 3 | `RegisterUpgradeHandlers` returns `true` at the upgrade height without setting a store loader. | `app.go` then skips `MaxVersionStoreLoader`, which keeps IAVL from loading past the versiondb version. At the upgrade restart, a node running versiondb whose two databases disagree (for example after an unclean stop) would exit instead of starting. | Returns `false`: the upgrade changes no stores, so the default loader stays, as at every other height. The function no longer reads `upgrade-info.json`, so a missing or unreadable file cannot panic the node. |
| 4 | A transfer error ends the upgrade with an error, after some accounts were already moved. | Correct, since nothing is committed when the handler fails; kept on purpose. The bank keeper debits the sender before it checks send restrictions, so skipping a failed account instead could lose coins. | Same, with the reason written down. Post-conditions added: recovered + skipped = listed; total supply unchanged; community pool and distribution module balance each grew by exactly the recovered amount. Any violation aborts the upgrade, so a wrong result cannot be committed. |
| 5 | The address list is a 30 MB JSON file without a checksum. | Nothing ties the binary to the published, checksummed list. | The binary embeds `untouched.csv.gz` byte for byte (sha256 `1297cfe6…8321b`, 14.8 MB) and refuses to run if its digest, header, sort order, address encoding, airdrop types or per-type counts (379,167 ATOM, 200,114 Evmos) differ. `genesisd airdrop-recovery verify-list` prints the check. |
| 6 | No tests, no way to preview the result. | The first run would have been on mainnet. | Unit and integration tests (below), `genesisd airdrop-recovery simulate` for a dry run on real state, and `rehearse_local.sh` for a full rehearsal with real binaries. |
| 7 | Logs gas diagnostics on every 10,000 accounts. | None; noise only. | Progress every 50,000 accounts and one summary line. |

The setup scripts were adapted too:

- `genesis-staged-upgrade.sh` targets v1.6.2.1, requires the upgrade height and the release commit, refuses to run under cosmovisor and lifts the airdrop block after the halt.
- `upgrade.sh` offers to lift the block. It no longer runs `go mod tidy` before building, which could change `go.mod` and `go.sum` on a validator's machine: every validator must build exactly the released dependencies.
- Both skip the config migration by default. v1.6.2.1 needs none, and running the v1.6.2 migration again would reset settings such as `versiondb` and telemetry to their defaults.
- `lift-airdrop-block.sh` is new.

## Evidence

All of the following ran against this branch, built with the module versions pinned in `go.sum`:

- `go vet ./app/ ./cmd/cronosd/...`: clean. `go test ./app/... ./cmd/...`: all pass.
- `TestAirdropRecoveryEmbeddedList`: the embedded file matches the published digest, row count, counts per type, first and last address.
- `TestParseAirdropRecoveryListRejectsBadInput`: wrong digest, header, field count, order, duplicates, address prefix or encoding, airdrop amount or type, and counts are all rejected.
- `TestClassifyAirdropAccount`: every branch of the rule, including the 1 L1 boundary (just under recovers, exactly 1 L1 extra does not), Ethereum accounts with an empty or zero code hash (plain) and contracts (not plain).
- `TestRunAirdropRecovery`: accounts at listed addresses covering every outcome plus an unlisted one; outcomes, counts, balances, supply, community pool, distribution balance, exactly one event, and a second run that moves nothing.
- `TestAirdropRecoveryUpgradeBlock`: the plan is scheduled and the upgrade block is finalized and committed through BaseApp: the upgrade module applies the plan, the summary event reaches the block results, no transfer event from a listed account appears, the committed state is right.
- `TestNewBinaryRunsOnlyFromUpgradeHeight`: the mainnet switch. With v1.6.2 as the last completed upgrade, a new process with this binary applies the upgrade when started at the upgrade height, and refuses to run (`upgrade handler is missing for v1.6.2 upgrade plan`) when started earlier.
- `rehearse_local.sh` with the real v1.6.2 and v1.6.2.1 binaries: genesis with listed accounts for every outcome; `simulate` on a copy matches; the proposal from `make_proposal.py` passes; the old binary halts with `UPGRADE "v1.6.2.1" NEEDED`; `lift-airdrop-block.sh` removes the block; the new binary applies the upgrade; `verify_upgrade.py` reports `ALL CHECKS PASSED` and every account got the outcome the rule gives it.
- `TestAirdropRecoveryFullScale` (opt-in): all 579,281 accounts qualify, on a disk database; see `README.md` for the measured time and memory.
