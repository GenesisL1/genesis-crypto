# Upgrade v1.6.2.1: release checklist

For whoever releases the upgrade and submits the proposal. Validators follow [VALIDATOR_GUIDE.md](VALIDATOR_GUIDE.md). Tick each item in order; each one assumes the previous ones are done.

## 1. Before the upgrade proposal: the signal

- [ ] The signal proposal ("Return Unclaimed ATOM & Evmos Airdrop L1 to the Community Pool") is submitted and has **passed**. Note its id: it goes into the upgrade proposal.

## 2. Review and merge

- [ ] Review the change set against `v1.6.2` (`git diff v1.6.2...`). [REVIEW.md](REVIEW.md) explains each change. Code: `app/airdrop_recovery.go`, `app/upgrades.go`, `app/migration_data/untouched.csv.gz`, `cmd/cronosd/cmd/airdrop_recovery.go`, one line in `cmd/cronosd/cmd/root.go` and the version in `default.nix`; and the Go guard: `app/toolchain_guard.go`, a check at the top of `app.New` and of `cmd/cronosd/main.go`, and `check-go-version` in the `Makefile` (see [REVIEW.md](REVIEW.md#unicode-tables-build-with-go-12312-to-125x)). The rest is tests, scripts and documents.
- [ ] Check that the embedded list is the published one:
      `sha256sum app/migration_data/untouched.csv.gz` → `1297cfe6dceed738d7fc34b6412826b9f3f249461bfcc1418fecff32c2e8321b`, the digest in the published repository's `SHA256SUMS`.
- [ ] On a clean checkout, all of these pass:

      go vet ./app/ ./cmd/cronosd/...
      go test ./app/... ./cmd/...
      make build && ./build/genesisd airdrop-recovery verify-list

- [ ] The Go checks work:

      GOTOOLCHAIN=go1.26.11 make build     # stops at check-go-version, as does 1.27+ (Go downloads 1.26.11 for this)
      go test -count=1 -v -run 'TestToolchain|TestUnicode|TestBuildSetting' ./app/   # passes on the release toolchain

      With today's dependencies, Go 1.26 and later do not compile genesisd and `GOEXPERIMENT=jsonv2` does not link
      (bytedance/sonic), so a refused binary cannot be built from this tree; REVIEW.md shows the refusals with the guard
      built alone.

- [ ] Rehearsal with the real binaries. Build `v1.6.2` into one directory and this branch into another, then:

      bash upgrades/v1.6.2.1/rehearse_local.sh /path/to/genesisd-v1.6.2 ./build/genesisd

      It must end with `REHEARSAL PASSED`. It takes about two minutes and uses only local ports.
- [ ] Optional, slow: `AIRDROP_FULL_SCALE=1 go test ./app -run FullScale -v -timeout 60m` measures the upgrade block with all 579,281 accounts qualifying.
- [ ] Merge into the main branch of `GenesisL1/genesis-crypto`.

## 3. Release

- [ ] Tag the merged commit: `git tag -s v1.6.2.1 -m "v1.6.2.1"` (or `-a` without a signing key) and push the tag. Record the full commit: `git rev-parse v1.6.2.1^{commit}`.
- [ ] `make build` on the tag: `genesisd version --long` shows version `1.6.2.1` and that commit.
- [ ] Optional: attach Linux binaries to a GitHub release, as for v1.6.2 (`genesis_1.6.2_Linux_x86_64.tar.gz` and so on), with their SHA-256 sums. Build them with go1.23.12, as the nix release build does, and check before publishing: `go version -m bin/genesisd` must print `go1.23.12`. Cosmovisor nodes that download binaries take these, so they set the toolchain for part of the network. Have a second person build the same tag and compare the `airdrop-recovery verify-list` output and `version --long` before publishing.

## 4. Dry run on mainnet state

- [ ] On a non-validating mainnet node, stopped for a minute or copied:

      genesisd airdrop-recovery simulate --home <copy> --accounts-out accounts.csv > report.json

- [ ] Sanity checks on `report.json`: `listed` is 579281; `recovered_accounts` plus the `skipped` counts is 579281; `recovered_l1` is close to 5,171,456.52 L1, the list's total at block 14,008,743, less what the accounts that have since signed, or received 1 L1 or more, hold.
- [ ] A second person runs the dry run at the **same height** on another node and gets identical figures.
- [ ] Publish `report.json` and `accounts.csv` with their SHA-256 sums, so anyone can check any account.
- [ ] Note `height`, `recovered_accounts`, `recovered_l1` and `elapsed`: the proposal quotes the first three, and `elapsed` tells you roughly how long the upgrade block will take.

## 5. Upgrade height

- [ ] Choose the time: a weekday, around 14:00 UTC, away from holidays, at least 24 hours after voting ends.
- [ ] Compute the height from the current block time:

      python3 upgrades/v1.6.2.1/upgrade_height.py --target 2026-12-03T14:00:00Z --rpc https://<rpc>

      It averages the last 20,000 blocks and warns if the upgrade would come less than 24 hours after the end of the 618,033-second voting period.

## 6. Upgrade proposal

- [ ] Build it (every value is checked, and the summary is checked against the chain's limits):

      python3 upgrades/v1.6.2.1/proposal/make_proposal.py \
        --signal-id <id> --height <height> --upgrade-time "2026-12-03 14:00" \
        --tag v1.6.2.1 --commit <40-character commit> \
        --dryrun-height <height> --dryrun-accounts <n> --dryrun-l1 <L1> \
        [--binary linux/amd64 <url> <sha256>]

- [ ] Read `proposal.json`. Explorers render its summary as Markdown.
- [ ] Submit with the standard 10,000 L1 deposit (already in `proposal.json`):

      genesisd tx gov submit-proposal proposal.json --from <key> --chain-id genesis_29-2 \
        --gas auto --gas-adjustment 1.5 --gas-prices <price>el1

- [ ] Check it on chain: `genesisd query gov proposal <id>` shows plan `v1.6.2.1`, the height and the info with tag, commit and guide link.

## 7. Communication

- [ ] At submission, post in the validator channels: proposal id, height and expected time, tag and commit, the `verify-list` line, the dry-run figures and a link to the validator guide.
- [ ] When voting ends: the plan is scheduled (`genesisd query upgrade plan`). Ask every validator to confirm that the binary is built and checked, or installed for cosmovisor.
- [ ] Reminders at T−48 h, T−24 h and T−1 h, with the expected time recomputed by `upgrade_height.py`.
- [ ] Agree beforehand where validators coordinate at the upgrade and who decides on a fallback (patch release or skip; see the guide).

## 8. Upgrade

- [ ] Watch the halt at the height, `applying upgrade "v1.6.2.1"`, `airdrop recovery finished`, and the first new blocks once more than two thirds of the voting power is back.
- [ ] Note the upgrade block's duration from the timestamps of `<height>`−1 and `<height>`.

## 9. Afterwards

- [ ] `python3 upgrades/v1.6.2.1/verify_upgrade.py --height <height> --rpc <rpc> --lcd <lcd>` ends with `ALL CHECKS PASSED`; post its output.
- [ ] Remind validators who run cosmovisor to lift the airdrop block (`setup/lift-airdrop-block.sh`).
- [ ] Update the upgrade table in `README.md` with the height, and note in the published list's repository that the recovery ran at that height.
- [ ] Announce the result: L1 recovered, accounts recovered, community pool after the upgrade, and the `airdrop_recovery` event in the block results.
