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

## Unicode tables: build with Go 1.23.12 to 1.25.x

A security advisory sent to Cosmos chains in November 2026 showed that one transaction can split a chain whose validators run binaries built with different Go versions. GenesisL1 has the code path the advisory describes. Today only a dependency's build limit keeps it from being reached, so the guard belongs in this release.

**Cause.** Go compiles its Unicode tables into every binary. Go 1.21 to 1.26 ship Unicode 15.0.0; Go 1.27 (August 2026) ships Unicode 17.0.0. Between the two, 55 characters change case (`strings.ToUpper`, `ToLower`, `EqualFold`) and 10,615 code points change `unicode.IsPrint`. When state-machine code passes user input to these functions, nodes on each side can store a different value, use different gas, or one succeeds where the other fails, so their app hashes differ. If neither side holds 2/3 of the voting power the chain halts; otherwise the smaller side drops out and is jailed for downtime. Go 1.27 also turns on `GOEXPERIMENT=jsonv2` by default, which rebuilds `encoding/json` on top of `encoding/json/v2` (Go 1.25 and 1.26 offer it as an opt-in experiment). The state machine uses `encoding/json` too, for legacy amino-JSON sign bytes and ICS-20 packet data, so this is a second reason to keep Go 1.27 out until a coordinated, tested upgrade.

**Where GenesisL1 is hit.** ibc-go v10.1.1: when an IBC token arrives for the first time, the transfer module stores `strings.ToUpper(baseDenom)` as its symbol (`modules/apps/transfer/keeper/keeper.go:213`, called from `relay.go:178`), and the only check on the base denom is that it is not blank. IBC clients and channels are permissionless, so anyone can send a token whose base denom is `ɤ` (U+0264): a node with Unicode 15 tables stores `ɤ`, a node with Unicode 17 tables stores `Ɤ` (U+A7CB). Checked and not affected:

- no wasmd;
- `golang.org/x/text` is v0.28.0, which has only Unicode 15 tables, so normalization and Ethermint's EIP-712 type names do not change with Go 1.27;
- the `strings.EqualFold` checks in the SDK (authz, feegrant, group) compare against bech32 addresses, which are ASCII, and none of the changed characters touch ASCII; `strings.TrimSpace` is unchanged;
- age's bech32 parser (`x/e2ee`) rejects every non-ASCII key with either table;
- `x/cronos` lowercases token-mapping symbols, but only the admin or governance can send that message.

**Why it cannot happen today, and why that is not enough.** v1.6.2 (running now) and v1.6.2.1 pin `github.com/bytedance/sonic` v1.14.0, which `cosmossdk.io/log` imports. It compiles only with Go up to 1.25 (`GoMapIterator` is defined for `go1.24 && !go1.26`), so Go 1.26 and 1.27 stop with `undefined: GoMapIterator`. Every Go that can build genesisd today (1.23.12 to 1.25.x) has the Unicode 15 tables, and the release binaries use go1.23.12, so no validator can be on the other side. Nothing else stops it: `go 1.23.12` in `go.mod` is only a minimum, the scripts build with whatever `go` is in `PATH`, and the guide did not mention Go. The first dependency update that brings a sonic, or an SDK logger, that supports newer Go removes this accidental protection. The guard makes it deliberate.

**Changes.** None of them changes the state machine: a binary built from this branch computes exactly what `3acbcbe` computes.

| File | Change |
|---|---|
| `app/toolchain_guard.go` | `CheckToolchain`: `unicode.Version` must be `15.0.0`; four characters that changed between 15 and 17 must still behave as in 15 (`ToUpper` U+0264, `ToLower` U+A7CB, `IsPrint` U+1C89, `EqualFold` U+1C89/U+1C8A); and the binary must not be built with `GOEXPERIMENT=jsonv2` (read from its build info). |
| `cmd/cronosd/main.go` | Runs the check before any command, not even `version`, and exits 1 with what to do. |
| `app/app.go` | `New` panics on the same check, for other programs that embed the app. |
| `Makefile` | `check-go-version`, run first by `build`, `install` and `test`: stops on Go 1.26 or later, which cannot compile this release (1.27 would also change results), or on no Go, before compiling, and prints the fix, `GOTOOLCHAIN=go1.23.12 make …`. It reads `go env GOVERSION`, so it follows `GOTOOLCHAIN`. |
| `setup/genesis-staged-upgrade.sh` | Runs the staged binary after the build and stops, long before the halt, if it refuses to run (the script used to ignore errors there). |
| `setup/upgrade.sh` | Prints version, commit and Go of the installed binary, and reports no success if it refuses to run. |
| `.github/workflows/sims.yml` | `go-version: '^1.22.0'` meant the newest Go (now 1.27, which cannot build this release); pinned to `1.23.12`. |
| Docs | `VALIDATOR_GUIDE.md`: the Go range, a step 0 to check the v1.6.2 binary running now, `go:` in the build check. `README.md`, `setup/dependencies.sh`, `RELEASE_CHECKLIST.md`, `CHANGELOG.md`. |

Why not only a check in the `Makefile`: `go build`, `go install`, Docker and other build paths skip it. The check in the binary covers all of them, because it tests the tables actually compiled in rather than the name of a toolchain. Why not raise the `go` line in `go.mod`: it is a minimum and cannot stop a newer Go. Later: once the dependencies build and test with Go 1.26 (same tables, still supported), raise `GO_MAX_MINOR` to 26; Go 1.27 or later needs a coordinated upgrade that changes `ConsensusUnicodeVersion` and its canaries, so that every node switches at the same height.

### Evidence (toolchain guard)

Toolchains go1.23.12, go1.25.14, go1.26.11 and go1.27.4 compiled from the Go sources at those tags, plus a stock go1.24.7. Every module was verified against `go.sum`.

- Published binaries: v1.6.2 and v1.6.2.1, amd64 and arm64, all report `go1.23.12` in `go version -m`; the v1.6.2.1 archives match the checksums in `proposal.json`.
- The `unicode` packages of go1.26.0 and go1.27.0, compared for every code point: 55 case mappings, 116 `SimpleFold` results and 10,615 `IsPrint` results differ; `IsSpace` and all of ASCII are unchanged; `ToUpper(U+0264)` is U+0264 in 1.26 and U+A7CB in 1.27.
- genesisd from this branch, built through `make build` with go1.23.12 (the release toolchain) and with go1.25.14: the binary runs, `airdrop-recovery verify-list` prints the expected line, `go vet ./app/ ./cmd/cronosd/...` is clean and `go test ./app/... ./cmd/...` passes. The guard runs in every test that builds the app.
- go1.26.11 and go1.27.4: `make build` stops at `check-go-version`; a direct `go build` fails on sonic (`undefined: GoMapIterator`).
- A go1.25.14 build with `GOEXPERIMENT=jsonv2` does not link: sonic links to `encoding/json` internals (`isValidNumber`) that the jsonv2 implementation lacks. Today, then, none of the setups the guard refuses can produce a genesisd binary at all; the guard is for the day the dependencies change.
- The guard alone, built with each toolchain: it passes on go1.23.12, go1.24.7, go1.25.14 and go1.26.11. With go1.25.14 and `GOEXPERIMENT=jsonv2` it refuses, and on go1.27.4 all four canaries trip; in both cases the binary exits 1 with the message.
- `check-go-version` against 11 version strings (release candidates, `X:` experiment suffixes, a devel build, no Go) and the 5 real toolchains; `GOTOOLCHAIN` switches it; `make clean` works without Go.
