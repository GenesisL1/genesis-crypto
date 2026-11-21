# Upgrade v1.6.2.1: return of the untouched airdrop, and a Go toolchain guard

Everything for the `v1.6.2.1` chain upgrade of GenesisL1 (`genesis_29-2`). It does two things: it returns the 2022 ATOM and Evmos airdrop allocations that were never touched to the community pool, as the signal proposal asked, and it adds a guard against a chain split caused by validators building with different Go versions (see [Go toolchain guard](#go-toolchain-guard)). The scripts need bash and Python 3 with its standard library, nothing else.

| You are | Read |
|---|---|
| a validator or node operator | [VALIDATOR_GUIDE.md](VALIDATOR_GUIDE.md) |
| releasing the upgrade and submitting the proposal | [RELEASE_CHECKLIST.md](RELEASE_CHECKLIST.md) |
| reviewing the code | [REVIEW.md](REVIEW.md), then `app/airdrop_recovery.go`, `app/upgrades.go` and `app/toolchain_guard.go` |

## What the upgrade does

### Return of the untouched airdrop

At the upgrade height, before any transaction of that block, the new binary reads the published list of 579,281 untouched airdrop accounts, which is compiled into it and checked against its SHA-256 (`1297cfe6dceed738d7fc34b6412826b9f3f249461bfcc1418fecff32c2e8321b`, from [GenesisL1/genesisl1-untouched-airdrop](https://github.com/GenesisL1/genesisl1-untouched-airdrop)). For each account it applies the rule of the proposal:

| The account at the upgrade height | Outcome |
|---|---|
| does not exist | skipped (`no_account`) |
| has signed a transaction (sequence above 0, or a public key set) | skipped (`signed`) |
| is a module, vesting or contract account | skipped (`not_plain_account`) |
| holds less than its airdrop (11 L1 for ATOM, 5 L1 for Evmos) | skipped (`below_airdrop`) |
| holds its airdrop plus 1 L1 or more | skipped (`extra_balance`) |
| otherwise: never signed, plain, holds its airdrop plus less than 1 L1 | its whole L1 balance goes to the community pool (`recovered`) |

These are the scope conditions of the signal proposal. Its two other requirements, that nothing has ever left the account and that nothing is staked or unbonding, follow from "never signed" on GenesisL1: every send, IBC transfer, EVM transaction and delegation is a transaction signed by the account, which raises its sequence above 0.

Nothing else changes in the airdrop recovery: no parameter, store or configuration format, and total supply stays the same. The upgrade checks its own result (supply unchanged, community pool and distribution module up by exactly the recovered amount) and aborts, committing nothing, if anything is off. The upgrade block's results carry one `airdrop_recovery` event with the totals instead of a transfer event per account.

After the upgrade the temporary `blocked-addresses` list goes from `app.toml` (`setup/lift-airdrop-block.sh`).

### Go toolchain guard

A security advisory sent to Cosmos chains in November 2026 showed that one transaction can split a chain whose validators run binaries built with different Go versions. Go compiles its Unicode tables into every binary: Go 1.21 to 1.26 ship Unicode 15.0.0, Go 1.27 ships Unicode 17.0.0. Functions such as `strings.ToUpper` then give different results for some characters, so nodes on each side can store different state and their app hashes differ. If neither side holds more than 2/3 of the voting power the chain halts; otherwise the smaller side drops out and is jailed for downtime.

GenesisL1 has the code path the advisory describes: when an IBC token arrives for the first time, ibc-go v10.1.1 stores `strings.ToUpper(baseDenom)` as its symbol, and anyone can send a token with a crafted denom. Today no validator can be on the other side, because a pinned dependency (`bytedance/sonic`) only compiles with Go up to 1.25. That is an accident of the dependency set, and it ends with the first dependency update. This release makes the protection deliberate.

| | |
|---|---|
| Build with | Go **1.23.12 to 1.25.x**. The release binaries use go1.23.12. |
| Do not use | Go 1.26 or later. Go 1.27 changes results, and 1.26 cannot compile this release. |
| Check the binary you run now | `genesisd version --long \| grep '^go:'` should show go1.23 to go1.25. If not, rebuild v1.6.2 before the upgrade. |
| Check the whole validator set | `/cosmos/base/tendermint/v1beta1/node_info` returns `application_version.go_version`. |
| Pin the toolchain if needed | `GOTOOLCHAIN=go1.23.12 make build` |

What the release adds:

- `genesisd` refuses to start, with a message saying what to do, if the Unicode tables compiled into it are not the expected ones (Unicode 15.0.0), or if it was built with `GOEXPERIMENT=jsonv2`. This holds however it was built, since the check looks at what is compiled in, not at the name of the toolchain.
- `make build`, `make install` and `make test` stop early on an unsupported Go version.
- `setup/upgrade.sh` and `setup/genesis-staged-upgrade.sh` run the binary they built and stop, long before the halt, if it refuses to run.

The guard adds no consensus logic and does not change the state machine. The full analysis and the test evidence are in [REVIEW.md](REVIEW.md#unicode-tables-build-with-go-12312-to-125x).

## Cost of the upgrade block

Measured with `TestAirdropRecoveryFullScale` in the worst case, all 579,281 accounts recovered, on a 2-vCPU test machine with a disk-backed database:

| | |
|---|---|
| Upgrade block (`FinalizeBlock`) | 53 s |
| Commit | 1.8 s |
| Memory held until the commit | +161 MiB |
| Go heap peak, garbage included | about 1.9 GiB |

So the chain pauses for about a minute at the upgrade height, plus the time validators take to switch binaries. `genesisd airdrop-recovery simulate` reports the time on your own node and state (`elapsed`).

## Files

| File | What it is |
|---|---|
| [VALIDATOR_GUIDE.md](VALIDATOR_GUIDE.md) | Check the running binary, build, check, switch at the halt (cosmovisor, staged script, by hand), lift the block, verify, troubleshoot. |
| [RELEASE_CHECKLIST.md](RELEASE_CHECKLIST.md) | Review, tag, dry run on mainnet state, height, proposal, communication, the day itself, afterwards. |
| [REVIEW.md](REVIEW.md) | Review of the contributed branch, what changed and why, the Go toolchain guard, and the test evidence. |
| [upgrade_height.py](upgrade_height.py) | Turns a target UTC time into a block height from the chain's recent block time, and checks the gap after the voting period. |
| [proposal/make_proposal.py](proposal/make_proposal.py), [proposal/summary.md](proposal/summary.md) | Build `proposal.json` (`MsgSoftwareUpgrade`) from the release and dry-run figures, within the chain's governance limits. |
| [verify_upgrade.py](verify_upgrade.py) | After the upgrade: checks the event, the community pool and supply around the upgrade, and listed accounts against the rule. |
| [rehearse_local.sh](rehearse_local.sh) | Full rehearsal on a throwaway local chain with the real old and new binaries. |
| [setup/lift-airdrop-block.sh](../../setup/lift-airdrop-block.sh) | Removes `blocked-addresses` from `app.toml`, with a backup. |
| [setup/genesis-staged-upgrade.sh](../../setup/genesis-staged-upgrade.sh), [setup/upgrade.sh](../../setup/upgrade.sh) | The existing upgrade scripts, now for v1.6.2.1, with a check of the binary they build. |

In the binary:

```
genesisd airdrop-recovery verify-list                 # check the embedded list
genesisd airdrop-recovery simulate --home <copy>      # run the recovery on a node's state, save nothing
```
