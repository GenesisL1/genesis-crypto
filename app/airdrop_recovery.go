package app

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	vestingexported "github.com/cosmos/cosmos-sdk/x/auth/vesting/exported"
	distrtypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	ethermint "github.com/evmos/ethermint/types"
)

// airdropRecoveryListGz is the published list of untouched airdrop accounts at
// block 14,008,743. It is byte-identical to untouched.csv.gz in
// https://github.com/GenesisL1/genesisl1-untouched-airdrop, whose SHA256SUMS
// file lists the same digest as AirdropRecoveryListSHA256. Anyone can check the
// file in this repository with `sha256sum app/migration_data/untouched.csv.gz`.
//
//go:embed migration_data/untouched.csv.gz
var airdropRecoveryListGz []byte

const (
	// AirdropRecoveryUpgradeName is the upgrade plan name. It must match the
	// plan name in the software upgrade proposal and the cosmovisor directory.
	AirdropRecoveryUpgradeName = "v1.6.2.1"

	// AirdropRecoveryListSHA256 is the SHA-256 digest of the embedded list.
	AirdropRecoveryListSHA256 = "1297cfe6dceed738d7fc34b6412826b9f3f249461bfcc1418fecff32c2e8321b"

	// AirdropRecoveryListHeight is the block height at which the list was computed.
	AirdropRecoveryListHeight int64 = 14_008_743

	// AirdropRecoveryDenom is the base denom of the L1 coin (1 L1 = 10^18 el1).
	AirdropRecoveryDenom = "el1"

	// AirdropRecoveryEventType is the type of the single summary event the
	// recovery emits.
	AirdropRecoveryEventType = "airdrop_recovery"

	airdropRecoveryListedATOM  = 379_167
	airdropRecoveryListedEvmos = 200_114

	airdropRecoveryCSVHeader = "address,airdrop,airdrop_amount_l1,l1_at_h"
	airdropRecoveryHRP       = "genesis"
	airdropRecoveryLogEvery  = 50_000
)

// oneL1 is 1 L1 in el1.
var oneL1 = sdkmath.NewIntWithDecimal(1, 18)

// AirdropKind identifies which 2022 airdrop an account received.
type AirdropKind uint8

const (
	AirdropKindATOM AirdropKind = iota + 1
	AirdropKindEvmos
)

func (k AirdropKind) String() string {
	switch k {
	case AirdropKindATOM:
		return "ATOM"
	case AirdropKindEvmos:
		return "Evmos"
	default:
		return "unknown"
	}
}

// Amount is the airdrop allocation in el1: 11 L1 for ATOM, 5 L1 for Evmos.
func (k AirdropKind) Amount() sdkmath.Int {
	switch k {
	case AirdropKindATOM:
		return sdkmath.NewIntWithDecimal(11, 18)
	case AirdropKindEvmos:
		return sdkmath.NewIntWithDecimal(5, 18)
	default:
		return sdkmath.ZeroInt()
	}
}

// AirdropEntry is one row of the recovery list.
type AirdropEntry struct {
	Address sdk.AccAddress
	Bech32  string
	Kind    AirdropKind
}

// AirdropRecoveryOutcome is what the recovery did with one listed account.
type AirdropRecoveryOutcome uint8

const (
	// AirdropRecovered the account qualified and its el1 balance moved to the
	// community pool.
	AirdropRecovered AirdropRecoveryOutcome = iota + 1
	// AirdropSkippedNoAccount no account exists at the address.
	AirdropSkippedNoAccount
	// AirdropSkippedSigned the account has signed a transaction (sequence > 0
	// or a public key on chain).
	AirdropSkippedSigned
	// AirdropSkippedNotPlain a module, vesting or contract account.
	AirdropSkippedNotPlain
	// AirdropSkippedBelowAirdrop the el1 balance is below the airdrop amount.
	AirdropSkippedBelowAirdrop
	// AirdropSkippedExtraBalance the el1 balance is 1 L1 or more above the
	// airdrop amount, so someone else sent L1 to the account.
	AirdropSkippedExtraBalance
)

func (o AirdropRecoveryOutcome) String() string {
	switch o {
	case AirdropRecovered:
		return "recovered"
	case AirdropSkippedNoAccount:
		return "no_account"
	case AirdropSkippedSigned:
		return "signed"
	case AirdropSkippedNotPlain:
		return "not_plain_account"
	case AirdropSkippedBelowAirdrop:
		return "below_airdrop"
	case AirdropSkippedExtraBalance:
		return "extra_balance"
	default:
		return "unknown"
	}
}

// AirdropRecoverySummary reports the result of one recovery run. Amounts are
// in el1.
type AirdropRecoverySummary struct {
	Height              int64
	Listed              int
	Recovered           int
	RecoveredATOM       int
	RecoveredEvmos      int
	SkippedNoAccount    int
	SkippedSigned       int
	SkippedNotPlain     int
	SkippedBelowAirdrop int
	SkippedExtraBalance int
	RecoveredAmount     sdkmath.Int
	CommunityPoolBefore sdkmath.LegacyDec
	CommunityPoolAfter  sdkmath.LegacyDec
	TotalSupply         sdkmath.Int
}

func (s *AirdropRecoverySummary) record(e AirdropEntry, o AirdropRecoveryOutcome, amount sdkmath.Int) {
	switch o {
	case AirdropRecovered:
		s.Recovered++
		s.RecoveredAmount = s.RecoveredAmount.Add(amount)
		if e.Kind == AirdropKindATOM {
			s.RecoveredATOM++
		} else {
			s.RecoveredEvmos++
		}
	case AirdropSkippedNoAccount:
		s.SkippedNoAccount++
	case AirdropSkippedSigned:
		s.SkippedSigned++
	case AirdropSkippedNotPlain:
		s.SkippedNotPlain++
	case AirdropSkippedBelowAirdrop:
		s.SkippedBelowAirdrop++
	case AirdropSkippedExtraBalance:
		s.SkippedExtraBalance++
	}
}

func (s AirdropRecoverySummary) skipped() int {
	return s.SkippedNoAccount + s.SkippedSigned + s.SkippedNotPlain + s.SkippedBelowAirdrop + s.SkippedExtraBalance
}

// AirdropRecoveryObserver receives every listed account with its outcome and
// its el1 balance before the recovery. The upgrade handler passes nil; the
// simulate command uses it to write a per-account report.
type AirdropRecoveryObserver func(entry AirdropEntry, outcome AirdropRecoveryOutcome, balance sdkmath.Int)

// LoadAirdropRecoveryList verifies and decodes the embedded list.
func LoadAirdropRecoveryList() ([]AirdropEntry, error) {
	return ParseAirdropRecoveryList(
		airdropRecoveryListGz, AirdropRecoveryListSHA256,
		airdropRecoveryListedATOM, airdropRecoveryListedEvmos,
	)
}

// ParseAirdropRecoveryList verifies and decodes a gzip-compressed list in the
// published format (address,airdrop,airdrop_amount_l1,l1_at_h). It rejects a
// wrong SHA-256 digest, a wrong header, malformed rows, unknown airdrop kinds or
// amounts, addresses that are not canonical 20-byte genesis1 bech32, rows that
// are not in strictly ascending order (which also rules out duplicates) and
// per-kind counts that differ from the expected ones. The l1_at_h column is
// informational and not used.
func ParseAirdropRecoveryList(gz []byte, wantSHA256 string, wantATOM, wantEvmos int) ([]AirdropEntry, error) {
	sum := sha256.Sum256(gz)
	if got := hex.EncodeToString(sum[:]); got != wantSHA256 {
		return nil, fmt.Errorf("airdrop recovery list: sha256 %s, want %s", got, wantSHA256)
	}

	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, fmt.Errorf("airdrop recovery list: %w", err)
	}
	defer zr.Close()

	scanner := bufio.NewScanner(zr)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("airdrop recovery list: %w", err)
		}
		return nil, fmt.Errorf("airdrop recovery list: empty")
	}
	if header := scanner.Text(); header != airdropRecoveryCSVHeader {
		return nil, fmt.Errorf("airdrop recovery list: header %q, want %q", header, airdropRecoveryCSVHeader)
	}

	entries := make([]AirdropEntry, 0, wantATOM+wantEvmos)
	var nATOM, nEvmos int
	prev := ""
	line := 1
	for scanner.Scan() {
		line++
		fields := strings.Split(scanner.Text(), ",")
		if len(fields) != 4 {
			return nil, fmt.Errorf("airdrop recovery list line %d: %d fields, want 4", line, len(fields))
		}

		addr := fields[0]
		if addr <= prev {
			return nil, fmt.Errorf("airdrop recovery list line %d: %s is not in strictly ascending order", line, addr)
		}
		prev = addr

		hrp, bz, err := bech32.DecodeAndConvert(addr)
		if err != nil {
			return nil, fmt.Errorf("airdrop recovery list line %d: %w", line, err)
		}
		if hrp != airdropRecoveryHRP || len(bz) != 20 {
			return nil, fmt.Errorf("airdrop recovery list line %d: %s is not a 20-byte %s address", line, addr, airdropRecoveryHRP)
		}
		if canonical, err := bech32.ConvertAndEncode(airdropRecoveryHRP, bz); err != nil || canonical != addr {
			return nil, fmt.Errorf("airdrop recovery list line %d: %s is not canonical bech32", line, addr)
		}

		var kind AirdropKind
		switch {
		case fields[1] == "ATOM" && fields[2] == "11":
			kind = AirdropKindATOM
			nATOM++
		case fields[1] == "Evmos" && fields[2] == "5":
			kind = AirdropKindEvmos
			nEvmos++
		default:
			return nil, fmt.Errorf("airdrop recovery list line %d: unknown airdrop %q with amount %q", line, fields[1], fields[2])
		}

		entries = append(entries, AirdropEntry{Address: sdk.AccAddress(bz), Bech32: addr, Kind: kind})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("airdrop recovery list: %w", err)
	}
	if nATOM != wantATOM || nEvmos != wantEvmos {
		return nil, fmt.Errorf("airdrop recovery list: %d ATOM and %d Evmos accounts, want %d and %d", nATOM, nEvmos, wantATOM, wantEvmos)
	}
	return entries, nil
}

// emptyCodeHash is the code hash of an account that holds no EVM code.
var emptyCodeHash = crypto.Keccak256Hash(nil)

// isPlainAccount reports whether acc is an ordinary user account: not a module
// account, not a vesting account and not an EVM contract.
//
// An EthAccount counts as a contract only when it carries a real code hash.
// EthAccount.Type() would also call an account with an empty or zero code-hash
// field a contract, and accounts written by older chain versions or by genesis
// tooling can carry such a field, so it is not used here. (A contract deployed
// through the EVM also has nonce 1, so the sequence check already excludes it.)
func isPlainAccount(acc sdk.AccountI) bool {
	if _, ok := acc.(sdk.ModuleAccountI); ok {
		return false
	}
	if _, ok := acc.(vestingexported.VestingAccount); ok {
		return false
	}
	if ethAcc, ok := acc.(ethermint.EthAccountI); ok {
		if h := ethAcc.GetCodeHash(); h != (common.Hash{}) && h != emptyCodeHash {
			return false
		}
	}
	return true
}

// classifyAirdropAccount applies the rule of the signal proposal to one listed
// account at the current height. balance is the account's el1 balance.
func classifyAirdropAccount(acc sdk.AccountI, kind AirdropKind, balance sdkmath.Int) AirdropRecoveryOutcome {
	airdrop := kind.Amount()
	switch {
	case acc == nil:
		return AirdropSkippedNoAccount
	case acc.GetSequence() != 0 || acc.GetPubKey() != nil:
		return AirdropSkippedSigned
	case !isPlainAccount(acc):
		return AirdropSkippedNotPlain
	case balance.LT(airdrop):
		return AirdropSkippedBelowAirdrop
	case balance.GTE(airdrop.Add(oneL1)):
		return AirdropSkippedExtraBalance
	default:
		return AirdropRecovered
	}
}

// RunAirdropRecovery moves the el1 balance of every listed account that still
// qualifies to the community pool. It is the body of the v1.6.2.1 upgrade
// handler and of the `genesisd airdrop-recovery simulate` command.
//
// An account qualifies only if, at this height, it exists, has never signed a
// transaction (sequence 0 and no public key), is a plain user account (not a
// module, vesting or contract account), and holds at least its airdrop amount
// and less than 1 L1 above it. Accounts that fail any condition are left
// untouched. A qualifying account's whole el1 balance moves, which is its
// airdrop plus, for some ATOM accounts, the 0.001-0.002 L1 the original
// delivery added.
//
// These checks are the four scope conditions of the signal proposal. Its
// other two requirements, that nothing has ever left the account and that
// nothing is staked or unbonding, follow from "never signed" on this chain:
// every send, IBC transfer, EVM transaction and delegation is a transaction
// signed by the account, which sets its sequence above 0.
//
// Per-transfer bank events are discarded: four events per transfer would put
// about 2.3 million events into the upgrade block's results and could break
// RPC and indexers. A single airdrop_recovery event with the totals is emitted
// instead; the published list and this command let anyone reproduce the
// per-account detail.
//
// Before returning it checks that total supply is unchanged and that the
// community pool and the distribution module balance each grew by exactly the
// recovered amount. Any error aborts the upgrade, and nothing is committed.
func (app *App) RunAirdropRecovery(ctx sdk.Context, observe AirdropRecoveryObserver) (AirdropRecoverySummary, error) {
	denom := AirdropRecoveryDenom
	summary := AirdropRecoverySummary{
		Height:          ctx.BlockHeight(),
		RecoveredAmount: sdkmath.ZeroInt(),
	}
	logger := ctx.Logger().With("module", "airdrop-recovery")

	entries, err := LoadAirdropRecoveryList()
	if err != nil {
		return summary, err
	}
	summary.Listed = len(entries)

	if app.AccountKeeper.GetModuleAccount(ctx, distrtypes.ModuleName) == nil {
		return summary, fmt.Errorf("airdrop recovery: distribution module account not found")
	}
	distrAddr := app.AccountKeeper.GetModuleAddress(distrtypes.ModuleName)

	feePool, err := app.DistrKeeper.FeePool.Get(ctx)
	if err != nil {
		return summary, fmt.Errorf("airdrop recovery: read fee pool: %w", err)
	}
	summary.CommunityPoolBefore = feePool.CommunityPool.AmountOf(denom)
	supplyBefore := app.BankKeeper.GetSupply(ctx, denom).Amount
	distrBefore := app.BankKeeper.GetBalance(ctx, distrAddr, denom).Amount

	// No gas limit may interrupt the loop. Writes go to ctx's store.
	work := ctx.WithGasMeter(storetypes.NewInfiniteGasMeter())

	logger.Info("airdrop recovery started",
		"listed", summary.Listed,
		"list_sha256", AirdropRecoveryListSHA256,
		"list_height", AirdropRecoveryListHeight,
	)

	for i, e := range entries {
		// A fresh event manager per account drops that account's transfer
		// events at once; one shared manager would hold millions of them in
		// memory until the end of the loop.
		actx := work.WithEventManager(sdk.NewEventManager())
		acc := app.AccountKeeper.GetAccount(actx, e.Address)
		balance := app.BankKeeper.GetBalance(actx, e.Address, denom).Amount
		outcome := classifyAirdropAccount(acc, e.Kind, balance)

		if outcome == AirdropRecovered {
			coins := sdk.NewCoins(sdk.NewCoin(denom, balance))
			if err := app.BankKeeper.SendCoinsFromAccountToModule(actx, e.Address, distrtypes.ModuleName, coins); err != nil {
				// The checks above make this unreachable. SendCoins may already
				// have debited the sender when it fails, so skipping the
				// account here could lose coins: abort instead.
				return summary, fmt.Errorf("airdrop recovery: transfer from %s: %w", e.Bech32, err)
			}
		}

		summary.record(e, outcome, balance)
		if observe != nil {
			observe(e, outcome, balance)
		}

		if n := i + 1; n%airdropRecoveryLogEvery == 0 || n == len(entries) {
			logger.Info("airdrop recovery progress",
				"processed", n,
				"listed", summary.Listed,
				"recovered", summary.Recovered,
				"recovered_el1", summary.RecoveredAmount.String(),
			)
		}
	}

	if summary.RecoveredAmount.IsPositive() {
		// Same bookkeeping as distribution's FundCommunityPool, done once for the
		// total instead of once per account: the coins are already in the
		// distribution module account, so credit them to the community pool.
		feePool, err := app.DistrKeeper.FeePool.Get(work)
		if err != nil {
			return summary, fmt.Errorf("airdrop recovery: read fee pool: %w", err)
		}
		feePool.CommunityPool = feePool.CommunityPool.Add(
			sdk.NewDecCoinsFromCoins(sdk.NewCoin(denom, summary.RecoveredAmount))...,
		)
		if err := app.DistrKeeper.FeePool.Set(work, feePool); err != nil {
			return summary, fmt.Errorf("airdrop recovery: update community pool: %w", err)
		}
	}

	// Post-conditions. A failure here aborts the upgrade.
	feePoolAfter, err := app.DistrKeeper.FeePool.Get(ctx)
	if err != nil {
		return summary, fmt.Errorf("airdrop recovery: read fee pool: %w", err)
	}
	summary.CommunityPoolAfter = feePoolAfter.CommunityPool.AmountOf(denom)
	summary.TotalSupply = app.BankKeeper.GetSupply(ctx, denom).Amount
	distrAfter := app.BankKeeper.GetBalance(ctx, distrAddr, denom).Amount

	if summary.Recovered+summary.skipped() != summary.Listed {
		return summary, fmt.Errorf("airdrop recovery: %d recovered + %d skipped != %d listed",
			summary.Recovered, summary.skipped(), summary.Listed)
	}
	if !summary.TotalSupply.Equal(supplyBefore) {
		return summary, fmt.Errorf("airdrop recovery: total supply changed from %s to %s", supplyBefore, summary.TotalSupply)
	}
	if got, want := summary.CommunityPoolAfter.Sub(summary.CommunityPoolBefore), sdkmath.LegacyNewDecFromInt(summary.RecoveredAmount); !got.Equal(want) {
		return summary, fmt.Errorf("airdrop recovery: community pool grew by %s, want %s", got, want)
	}
	if got := distrAfter.Sub(distrBefore); !got.Equal(summary.RecoveredAmount) {
		return summary, fmt.Errorf("airdrop recovery: distribution module balance grew by %s, want %s", got, summary.RecoveredAmount)
	}

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		AirdropRecoveryEventType,
		sdk.NewAttribute("list_sha256", AirdropRecoveryListSHA256),
		sdk.NewAttribute("list_height", strconv.FormatInt(AirdropRecoveryListHeight, 10)),
		sdk.NewAttribute("listed", strconv.Itoa(summary.Listed)),
		sdk.NewAttribute("recovered_accounts", strconv.Itoa(summary.Recovered)),
		sdk.NewAttribute("recovered_atom_accounts", strconv.Itoa(summary.RecoveredATOM)),
		sdk.NewAttribute("recovered_evmos_accounts", strconv.Itoa(summary.RecoveredEvmos)),
		sdk.NewAttribute(sdk.AttributeKeyAmount, sdk.NewCoin(denom, summary.RecoveredAmount).String()),
		sdk.NewAttribute("skipped_no_account", strconv.Itoa(summary.SkippedNoAccount)),
		sdk.NewAttribute("skipped_signed", strconv.Itoa(summary.SkippedSigned)),
		sdk.NewAttribute("skipped_not_plain_account", strconv.Itoa(summary.SkippedNotPlain)),
		sdk.NewAttribute("skipped_below_airdrop", strconv.Itoa(summary.SkippedBelowAirdrop)),
		sdk.NewAttribute("skipped_extra_balance", strconv.Itoa(summary.SkippedExtraBalance)),
		sdk.NewAttribute("community_pool", summary.CommunityPoolAfter.String()+denom),
	))

	logger.Info("airdrop recovery finished",
		"listed", summary.Listed,
		"recovered", summary.Recovered,
		"recovered_atom", summary.RecoveredATOM,
		"recovered_evmos", summary.RecoveredEvmos,
		"recovered_el1", summary.RecoveredAmount.String(),
		"skipped_no_account", summary.SkippedNoAccount,
		"skipped_signed", summary.SkippedSigned,
		"skipped_not_plain_account", summary.SkippedNotPlain,
		"skipped_below_airdrop", summary.SkippedBelowAirdrop,
		"skipped_extra_balance", summary.SkippedExtraBalance,
		"community_pool_el1", summary.CommunityPoolAfter.String(),
	)

	return summary, nil
}
