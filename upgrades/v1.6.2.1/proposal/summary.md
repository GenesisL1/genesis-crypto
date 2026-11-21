## Summary

This software upgrade carries out signal proposal #{{SIGNAL_ID}}. At block {{HEIGHT}}, upgrade v1.6.2.1 returns the untouched L1 coins (L1) of the 2022 ATOM and Evmos airdrops to the community pool: allocations that have never been claimed or used in about 4.5 years.

The release also adds a safeguard that makes validators build with a supported Go version, to prevent a possible chain split (see "Go toolchain guard" below). It changes no consensus logic.

A dry run of the upgrade code on mainnet state at block {{DRYRUN_HEIGHT}} moves {{DRYRUN_L1}} L1 from {{DRYRUN_ACCOUNTS}} accounts. The final figures are set at block {{HEIGHT}} and published in the upgrade's airdrop_recovery event.

A YES vote schedules the upgrade at block {{HEIGHT}}, expected around {{UPGRADE_TIME_UTC}} UTC. Validators must switch to genesisd v1.6.2.1 once their nodes halt at the upgrade height.

## What the upgrade does

### Return of the untouched airdrop

- It checks each of the 579,281 accounts on the published list of untouched airdrop accounts (block 14,008,743, SHA-256 1297cfe6dceed738d7fc34b6412826b9f3f249461bfcc1418fecff32c2e8321b) again at block {{HEIGHT}}.
- An account qualifies only if it still meets the signal proposal's rule: it has never signed a transaction (sequence 0, no public key), it is a plain user account (not a module, vesting or contract account), and it holds its airdrop and less than 1 L1 more (11 L1 for ATOM, 5 L1 for Evmos).
- A qualifying account's L1 balance moves to the community pool. Every other account is left exactly as it is, including any listed account that has since been used or has received other L1.
- No coins are created or burned. Before it commits, the upgrade checks that total supply is unchanged and that the community pool grew by exactly the amount moved. If any check fails, the upgrade stops and nothing is committed.
- It emits one airdrop_recovery event with the totals, not one event per account, so RPC nodes and explorers are not flooded.

Nothing else changes on chain: no parameters, no stores, no other modules.

### Go toolchain guard

A security advisory sent to Cosmos chains in November 2026 showed that validators running binaries built with different Go versions (Go 1.27 or later versus older) can compute different results for the same transaction, which can split or halt a chain. GenesisL1 has the code path the advisory describes. No validator can be on the other side today, because a pinned dependency only compiles with Go up to 1.25, but that is an accident of the dependency set and this release makes the protection deliberate:

- genesisd v1.6.2.1 refuses to start if the Unicode tables compiled into it are not the expected ones (Unicode 15), or if it was built with `GOEXPERIMENT=jsonv2`, however it was built.
- `make build`, `make install` and `make test` stop early on an unsupported Go version, and the upgrade scripts stop if the binary they built refuses to run.
- Validators must build with Go 1.23.12 to 1.25.x. The release binaries use go1.23.12.

The guard adds no consensus logic and does not change the state machine. Analysis and test evidence: https://github.com/GenesisL1/genesis-crypto/blob/{{TAG}}/upgrades/v1.6.2.1/REVIEW.md

## Code and verification

- Release: genesisd v1.6.2.1, tag {{TAG}}, commit {{COMMIT}} of GenesisL1/genesis-crypto.
- Upgrade code: app/airdrop_recovery.go and app/upgrades.go, with unit tests in app/airdrop_recovery_test.go. The toolchain guard is in app/toolchain_guard.go, with unit tests in app/toolchain_guard_test.go.
- The list built into the binary is byte-identical to untouched.csv.gz in GenesisL1/genesisl1-untouched-airdrop (same SHA-256 as above).
- Anyone running a node can preview the result on their own state: `genesisd airdrop-recovery simulate` runs the same code and saves nothing.
- After the upgrade, upgrades/v1.6.2.1/verify_upgrade.py checks the result independently from any node's RPC and LCD.

## For validators

- Check the Go version of the binary you run today (`genesisd version --long | grep '^go:'`). It should show go1.23 to go1.25. If it shows anything else, it was not built from the release sources: rebuild v1.6.2 now, before the upgrade, and restart.
- Prepare the v1.6.2.1 binary before block {{HEIGHT}}, but do not run it earlier. At block {{HEIGHT}} your node stops with: UPGRADE "v1.6.2.1" NEEDED.
- With cosmovisor, place the binary at cosmovisor/upgrades/v1.6.2.1/bin/genesisd. Without it, use setup/genesis-staged-upgrade.sh, or setup/upgrade.sh after the halt.
- Once your node is upgraded, remove the temporary blocked-addresses list from app.toml with setup/lift-airdrop-block.sh (the two scripts above do this for you). As the signal proposal said, the block ends with the upgrade.
- Full guide: upgrades/v1.6.2.1/VALIDATOR_GUIDE.md in the release.

## Voting options

- YES: schedule upgrade v1.6.2.1 at block {{HEIGHT}}.
- NO: do not schedule it. The untouched allocations stay where they are, and validators lift the temporary block.
- NO WITH VETO: you consider this proposal harmful or out of order.
- ABSTAIN: you count toward quorum without taking a side.