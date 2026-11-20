package app

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	coreheader "cosmossdk.io/core/header"
	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	upgradetypes "cosmossdk.io/x/upgrade/types"
	abci "github.com/cometbft/cometbft/abci/types"
	tmproto "github.com/cometbft/cometbft/proto/tendermint/types"
	tmtypes "github.com/cometbft/cometbft/types"
	dbm "github.com/cosmos/cosmos-db"
	cronostypes "github.com/crypto-org-chain/cronos/v2/x/cronos/types"
	"github.com/ethereum/go-ethereum/common"
	ethermint "github.com/evmos/ethermint/types"
	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	"github.com/cosmos/cosmos-sdk/server"
	"github.com/cosmos/cosmos-sdk/testutil/mock"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	vestingtypes "github.com/cosmos/cosmos-sdk/x/auth/vesting/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	distrtypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	minttypes "github.com/cosmos/cosmos-sdk/x/mint/types"
)

// el1 parses an amount in el1 (1 L1 = 10^18 el1).
func el1(t *testing.T, s string) sdkmath.Int {
	t.Helper()
	i, ok := sdkmath.NewIntFromString(s)
	require.True(t, ok, s)
	return i
}

func gzipList(t *testing.T, header string, rows ...string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err := zw.Write([]byte(header + "\n" + strings.Join(rows, "\n") + "\n"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

func bech32Addr(t *testing.T, hrp string, b byte) string {
	t.Helper()
	s, err := bech32.ConvertAndEncode(hrp, bytes.Repeat([]byte{b}, 20))
	require.NoError(t, err)
	return s
}

func TestAirdropRecoveryEmbeddedList(t *testing.T) {
	sum := sha256.Sum256(airdropRecoveryListGz)
	require.Equal(t, AirdropRecoveryListSHA256, hex.EncodeToString(sum[:]),
		"app/migration_data/untouched.csv.gz must be byte-identical to the published file")

	entries, err := LoadAirdropRecoveryList()
	require.NoError(t, err)
	require.Len(t, entries, 579_281)

	var atom, evmos int
	for _, e := range entries {
		switch e.Kind {
		case AirdropKindATOM:
			atom++
		case AirdropKindEvmos:
			evmos++
		}
	}
	require.Equal(t, 379_167, atom)
	require.Equal(t, 200_114, evmos)

	require.Equal(t, "genesis100000aeu2lh0jrrnmn2npc88typ25u7tnv6kze", entries[0].Bech32)
	require.Equal(t, AirdropKindATOM, entries[0].Kind)
	require.Equal(t, "genesis1zzzwlxzt4vwdkgllfh6h4pfyz4x949em4ndn9k", entries[len(entries)-1].Bech32)
	require.Len(t, entries[0].Address, 20)

	require.Equal(t, "11000000000000000000", AirdropKindATOM.Amount().String())
	require.Equal(t, "5000000000000000000", AirdropKindEvmos.Amount().String())
}

func TestParseAirdropRecoveryListRejectsBadInput(t *testing.T) {
	a := bech32Addr(t, "genesis", 0x01)
	b := bech32Addr(t, "genesis", 0x02)
	lo, hi := a, b
	if hi < lo {
		lo, hi = hi, lo
	}
	header := airdropRecoveryCSVHeader

	// A valid two-row list parses.
	gz, digest := gzipList(t, header, lo+",ATOM,11,11", hi+",Evmos,5,5")
	entries, err := ParseAirdropRecoveryList(gz, digest, 1, 1)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.Equal(t, AirdropKindATOM, entries[0].Kind)
	require.Equal(t, AirdropKindEvmos, entries[1].Kind)

	// Wrong digest.
	_, err = ParseAirdropRecoveryList(gz, strings.Repeat("0", 64), 1, 1)
	require.ErrorContains(t, err, "sha256")

	cases := []struct {
		name   string
		header string
		rows   []string
		atom   int
		evmos  int
		errMsg string
	}{
		{"wrong header", "address,airdrop", []string{lo + ",ATOM,11,11"}, 1, 0, "header"},
		{"wrong field count", header, []string{lo + ",ATOM,11"}, 1, 0, "fields"},
		{"unsorted", header, []string{hi + ",ATOM,11,11", lo + ",ATOM,11,11"}, 2, 0, "ascending"},
		{"duplicate", header, []string{lo + ",ATOM,11,11", lo + ",ATOM,11,11"}, 2, 0, "ascending"},
		{"other prefix", header, []string{bech32Addr(t, "cosmos", 0x01) + ",ATOM,11,11"}, 1, 0, "genesis"},
		{"not bech32", header, []string{"genesis1notanaddress,ATOM,11,11"}, 1, 0, "line 2"},
		{"wrong amount for kind", header, []string{lo + ",ATOM,5,5"}, 1, 0, "unknown airdrop"},
		{"unknown kind", header, []string{lo + ",OSMO,11,11"}, 1, 0, "unknown airdrop"},
		{"wrong counts", header, []string{lo + ",ATOM,11,11"}, 2, 0, "want 2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gz, digest := gzipList(t, tc.header, tc.rows...)
			_, err := ParseAirdropRecoveryList(gz, digest, tc.atom, tc.evmos)
			require.ErrorContains(t, err, tc.errMsg)
		})
	}
}

func TestClassifyAirdropAccount(t *testing.T) {
	addr := sdk.AccAddress(bytes.Repeat([]byte{0x07}, 20))
	plain := authtypes.NewBaseAccountWithAddress(addr)
	eoa := ethermint.ProtoAccount().(*ethermint.EthAccount) // code hash of empty code
	eoa.BaseAccount = authtypes.NewBaseAccountWithAddress(addr)
	noCodeHash := &ethermint.EthAccount{BaseAccount: authtypes.NewBaseAccountWithAddress(addr)}
	zeroCodeHash := &ethermint.EthAccount{BaseAccount: authtypes.NewBaseAccountWithAddress(addr), CodeHash: common.Hash{}.Hex()}
	contract := &ethermint.EthAccount{BaseAccount: authtypes.NewBaseAccountWithAddress(addr), CodeHash: common.HexToHash("0x01").Hex()}

	signed := authtypes.NewBaseAccountWithAddress(addr)
	require.NoError(t, signed.SetSequence(1))
	withKey := authtypes.NewBaseAccountWithAddress(addr)
	require.NoError(t, withKey.SetPubKey(secp256k1.GenPrivKey().PubKey()))

	vesting, err := vestingtypes.NewContinuousVestingAccount(
		authtypes.NewBaseAccountWithAddress(addr),
		sdk.NewCoins(sdk.NewCoin(AirdropRecoveryDenom, AirdropKindATOM.Amount())), 0, 1,
	)
	require.NoError(t, err)
	module := authtypes.NewEmptyModuleAccount("test")

	atom := AirdropKindATOM.Amount()
	cases := []struct {
		name    string
		acc     sdk.AccountI
		kind    AirdropKind
		balance sdkmath.Int
		want    AirdropRecoveryOutcome
	}{
		{"no account", nil, AirdropKindATOM, atom, AirdropSkippedNoAccount},
		{"exact airdrop", plain, AirdropKindATOM, atom, AirdropRecovered},
		{"eth EOA account", eoa, AirdropKindATOM, atom, AirdropRecovered},
		{"eth account, empty code-hash field", noCodeHash, AirdropKindATOM, atom, AirdropRecovered},
		{"eth account, zero code hash", zeroCodeHash, AirdropKindATOM, atom, AirdropRecovered},
		{"airdrop plus delivery dust", plain, AirdropKindATOM, el1(t, "11002000000000000000"), AirdropRecovered},
		{"just under 1 L1 extra", plain, AirdropKindEvmos, el1(t, "5999999999999999999"), AirdropRecovered},
		{"exactly 1 L1 extra", plain, AirdropKindEvmos, el1(t, "6000000000000000000"), AirdropSkippedExtraBalance},
		{"below airdrop", plain, AirdropKindATOM, el1(t, "10999999999999999999"), AirdropSkippedBelowAirdrop},
		{"zero balance", plain, AirdropKindEvmos, sdkmath.ZeroInt(), AirdropSkippedBelowAirdrop},
		{"sequence > 0", signed, AirdropKindATOM, atom, AirdropSkippedSigned},
		{"public key set", withKey, AirdropKindATOM, atom, AirdropSkippedSigned},
		{"vesting account", vesting, AirdropKindATOM, atom, AirdropSkippedNotPlain},
		{"module account", module, AirdropKindATOM, atom, AirdropSkippedNotPlain},
		{"contract account", contract, AirdropKindATOM, atom, AirdropSkippedNotPlain},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, classifyAirdropAccount(tc.acc, tc.kind, tc.balance))
		})
	}
}

// recoveryFixture creates accounts at listed addresses covering every outcome,
// plus one unlisted account, and returns what the recovery should do.
type recoveryFixture struct {
	ctx      sdk.Context
	want     map[string]AirdropRecoveryOutcome
	balances map[string]sdkmath.Int // el1 balance before the recovery
	outsider sdk.AccAddress
	expected sdkmath.Int // el1 the recovery should move
}

// blockContext gives ctx the header the chain would give a block at height,
// including the header info that the upgrade keeper reads.
func blockContext(ctx sdk.Context, height int64) sdk.Context {
	blockTime := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC).Add(time.Duration(height) * 14 * time.Second)
	return ctx.
		WithBlockHeader(tmproto.Header{Height: height, ChainID: TestAppChainID, Time: blockTime}).
		WithHeaderInfo(coreheader.Info{Height: height, ChainID: TestAppChainID, Time: blockTime})
}

func newRecoveryFixture(t *testing.T, a *App, ctx sdk.Context) recoveryFixture {
	t.Helper()

	entries, err := LoadAirdropRecoveryList()
	require.NoError(t, err)
	var atom, evmos []AirdropEntry
	for _, e := range entries {
		if e.Kind == AirdropKindATOM && len(atom) < 6 {
			atom = append(atom, e)
		} else if e.Kind == AirdropKindEvmos && len(evmos) < 4 {
			evmos = append(evmos, e)
		}
		if len(atom) == 6 && len(evmos) == 4 {
			break
		}
	}

	f := recoveryFixture{
		ctx:      ctx,
		want:     map[string]AirdropRecoveryOutcome{},
		balances: map[string]sdkmath.Int{},
		expected: sdkmath.ZeroInt(),
	}
	fund := func(addr sdk.AccAddress, amt sdkmath.Int) {
		require.NoError(t, FundAccount(a.BankKeeper, ctx, addr, sdk.NewCoins(sdk.NewCoin(AirdropRecoveryDenom, amt))))
	}
	plain := func(e AirdropEntry, amt string, want AirdropRecoveryOutcome, edit func(sdk.AccountI)) {
		acc := a.AccountKeeper.NewAccountWithAddress(ctx, e.Address)
		if edit != nil {
			edit(acc)
		}
		a.AccountKeeper.SetAccount(ctx, acc)
		fund(e.Address, el1(t, amt))
		f.want[e.Bech32] = want
		f.balances[e.Bech32] = el1(t, amt)
		if want == AirdropRecovered {
			f.expected = f.expected.Add(el1(t, amt))
		}
	}

	plain(atom[0], "11000000000000000000", AirdropRecovered, nil)
	plain(atom[1], "11002000000000000000", AirdropRecovered, nil)
	plain(atom[2], "12000000000000000000", AirdropSkippedExtraBalance, nil)
	plain(atom[3], "11000000000000000000", AirdropSkippedSigned, func(acc sdk.AccountI) {
		require.NoError(t, acc.SetSequence(1))
	})
	plain(evmos[0], "5000000000000000000", AirdropRecovered, nil)
	plain(evmos[1], "5999999999999999999", AirdropRecovered, nil)
	plain(evmos[2], "4900000000000000000", AirdropSkippedBelowAirdrop, nil)
	plain(evmos[3], "5000000000000000000", AirdropSkippedSigned, func(acc sdk.AccountI) {
		require.NoError(t, acc.SetPubKey(secp256k1.GenPrivKey().PubKey()))
	})

	// A vesting account at a listed address.
	base := a.AccountKeeper.NewAccount(ctx, authtypes.NewBaseAccountWithAddress(atom[4].Address)).(*authtypes.BaseAccount)
	vacc, err := vestingtypes.NewContinuousVestingAccount(base,
		sdk.NewCoins(sdk.NewCoin(AirdropRecoveryDenom, AirdropKindATOM.Amount())),
		ctx.BlockTime().Unix(), ctx.BlockTime().Unix()+1_000_000)
	require.NoError(t, err)
	a.AccountKeeper.SetAccount(ctx, vacc)
	fund(atom[4].Address, AirdropKindATOM.Amount())
	f.want[atom[4].Bech32] = AirdropSkippedNotPlain
	f.balances[atom[4].Bech32] = AirdropKindATOM.Amount()

	// A contract account at a listed address.
	base = a.AccountKeeper.NewAccount(ctx, authtypes.NewBaseAccountWithAddress(atom[5].Address)).(*authtypes.BaseAccount)
	a.AccountKeeper.SetAccount(ctx, &ethermint.EthAccount{BaseAccount: base, CodeHash: common.HexToHash("0x01").Hex()})
	fund(atom[5].Address, AirdropKindATOM.Amount())
	f.want[atom[5].Bech32] = AirdropSkippedNotPlain
	f.balances[atom[5].Bech32] = AirdropKindATOM.Amount()

	// An account that is not on the list.
	f.outsider = sdk.AccAddress(bytes.Repeat([]byte{0xee}, 20))
	fund(f.outsider, AirdropKindATOM.Amount())

	return f
}

func communityPool(t *testing.T, a *App, ctx sdk.Context) sdkmath.LegacyDec {
	t.Helper()
	fp, err := a.DistrKeeper.FeePool.Get(ctx)
	require.NoError(t, err)
	return fp.CommunityPool.AmountOf(AirdropRecoveryDenom)
}

func TestRunAirdropRecovery(t *testing.T) {
	a := Setup(t, "")
	f := newRecoveryFixture(t, a, blockContext(a.NewContextLegacy(false, tmproto.Header{}), 2))
	ctx := f.ctx.WithEventManager(sdk.NewEventManager())
	denom := AirdropRecoveryDenom
	distrAddr := a.AccountKeeper.GetModuleAddress(distrtypes.ModuleName)

	poolBefore := communityPool(t, a, ctx)
	supplyBefore := a.BankKeeper.GetSupply(ctx, denom).Amount
	distrBefore := a.BankKeeper.GetBalance(ctx, distrAddr, denom).Amount

	seen := map[string]AirdropRecoveryOutcome{}
	summary, err := a.RunAirdropRecovery(ctx, func(e AirdropEntry, o AirdropRecoveryOutcome, _ sdkmath.Int) {
		if _, ok := f.want[e.Bech32]; ok {
			seen[e.Bech32] = o
		}
	})
	require.NoError(t, err)

	// Every fixture account got the outcome the proposal's rule requires.
	require.Equal(t, f.want, seen)

	// Summary counts and amount.
	require.Equal(t, 579_281, summary.Listed)
	require.Equal(t, 4, summary.Recovered)
	require.Equal(t, 2, summary.RecoveredATOM)
	require.Equal(t, 2, summary.RecoveredEvmos)
	require.Equal(t, 2, summary.SkippedSigned)
	require.Equal(t, 2, summary.SkippedNotPlain)
	require.Equal(t, 1, summary.SkippedBelowAirdrop)
	require.Equal(t, 1, summary.SkippedExtraBalance)
	require.Equal(t, 579_281-10, summary.SkippedNoAccount)
	require.True(t, f.expected.Equal(summary.RecoveredAmount), "recovered %s, want %s", summary.RecoveredAmount, f.expected)

	// Balances: recovered accounts are empty, everything else is unchanged.
	addrs := make([]string, 0, len(f.want))
	for addr := range f.want {
		addrs = append(addrs, addr)
	}
	sort.Strings(addrs)
	for _, addr := range addrs {
		got := a.BankKeeper.GetBalance(ctx, sdk.MustAccAddressFromBech32(bech32ToSDK(t, addr)), denom).Amount
		if f.want[addr] == AirdropRecovered {
			require.True(t, got.IsZero(), "%s still holds %s", addr, got)
		} else {
			require.True(t, got.Equal(f.balances[addr]), "%s holds %s, want %s", addr, got, f.balances[addr])
		}
	}
	require.True(t, a.BankKeeper.GetBalance(ctx, f.outsider, denom).Amount.Equal(AirdropKindATOM.Amount()))

	// Accounting: supply unchanged, pool and distribution module up by exactly the amount.
	require.True(t, a.BankKeeper.GetSupply(ctx, denom).Amount.Equal(supplyBefore))
	require.True(t, communityPool(t, a, ctx).Sub(poolBefore).Equal(sdkmath.LegacyNewDecFromInt(f.expected)))
	require.True(t, a.BankKeeper.GetBalance(ctx, distrAddr, denom).Amount.Sub(distrBefore).Equal(f.expected))
	require.True(t, summary.CommunityPoolAfter.Equal(communityPool(t, a, ctx)))

	// Exactly one event, the summary; no per-transfer events.
	events := ctx.EventManager().Events()
	require.Len(t, events, 1)
	require.Equal(t, AirdropRecoveryEventType, events[0].Type)
	attrs := map[string]string{}
	for _, attr := range events[0].Attributes {
		attrs[attr.Key] = attr.Value
	}
	require.Equal(t, sdk.NewCoin(denom, f.expected).String(), attrs[sdk.AttributeKeyAmount])
	require.Equal(t, "4", attrs["recovered_accounts"])
	require.Equal(t, AirdropRecoveryListSHA256, attrs["list_sha256"])

	// Running it again moves nothing: emptied accounts no longer qualify.
	again, err := a.RunAirdropRecovery(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, 0, again.Recovered)
	require.True(t, again.RecoveredAmount.IsZero())
	require.True(t, communityPool(t, a, ctx).Sub(poolBefore).Equal(sdkmath.LegacyNewDecFromInt(f.expected)))
}

// TestAirdropRecoveryUpgradeBlock runs the upgrade the way the chain will: the
// plan is scheduled, and block 2 is finalized and committed through BaseApp, so
// the upgrade module's PreBlocker applies the plan and the summary event has to
// reach the block results.
func TestAirdropRecoveryUpgradeBlock(t *testing.T) {
	a := Setup(t, "")
	require.True(t, a.UpgradeKeeper.HasHandler(AirdropRecoveryUpgradeName))
	require.False(t, a.RegisterUpgradeHandlers(a.appCodec, 0), "v1.6.2.1 must keep the default store loader")

	// Block 1: the fixture accounts are created.
	ctx := blockContext(a.GetContextForFinalizeBlock(nil), 1)
	f := newRecoveryFixture(t, a, ctx)
	_, err := a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: 1, Time: ctx.BlockTime()})
	require.NoError(t, err)
	_, err = a.Commit()
	require.NoError(t, err)

	state := func(height int64) sdk.Context {
		return blockContext(a.NewUncachedContext(false, tmproto.Header{}), height)
	}

	// The passed proposal's plan, for block 2. It is written after block 1 is
	// committed: had block 1 seen a pending plan, this binary, which has the
	// handler, would have refused to run it (BINARY UPDATED BEFORE TRIGGER),
	// which is what stops validators who switch binaries too early.
	require.NoError(t, a.UpgradeKeeper.ScheduleUpgrade(state(1), upgradetypes.Plan{Name: AirdropRecoveryUpgradeName, Height: 2}))
	poolBefore := communityPool(t, a, state(1))
	supplyBefore := a.BankKeeper.GetSupply(state(1), AirdropRecoveryDenom).Amount

	// Block 2: the upgrade height.
	rsp, err := a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: 2, Time: state(2).BlockTime()})
	require.NoError(t, err)
	_, err = a.Commit()
	require.NoError(t, err)
	after := state(2)

	// One summary event in the block results; no transfer from a listed account.
	listed := map[string]bool{}
	for addr := range f.want {
		listed[bech32ToSDK(t, addr)] = true
	}
	var summaries []abci.Event
	for _, ev := range rsp.Events {
		if ev.Type == AirdropRecoveryEventType {
			summaries = append(summaries, ev)
		}
		for _, attr := range ev.Attributes {
			if (attr.Key == "sender" || attr.Key == "spender") && listed[attr.Value] {
				t.Fatalf("block events include a %s event from listed account %s", ev.Type, attr.Value)
			}
		}
	}
	require.Len(t, summaries, 1)
	attrs := map[string]string{}
	for _, attr := range summaries[0].Attributes {
		attrs[attr.Key] = attr.Value
	}
	require.Equal(t, sdk.NewCoin(AirdropRecoveryDenom, f.expected).String(), attrs[sdk.AttributeKeyAmount])
	require.Equal(t, "4", attrs["recovered_accounts"])
	require.Equal(t, "579281", attrs["listed"])
	// The recovery runs before BeginBlock, so the pool it reports is the pool
	// at the start of the block plus exactly the recovered amount.
	require.Equal(t, poolBefore.Add(sdkmath.LegacyNewDecFromInt(f.expected)).String()+AirdropRecoveryDenom, attrs["community_pool"])

	// Committed state: recovered accounts empty, the others untouched.
	for addr, want := range f.want {
		got := a.BankKeeper.GetBalance(after, sdk.MustAccAddressFromBech32(bech32ToSDK(t, addr)), AirdropRecoveryDenom).Amount
		if want == AirdropRecovered {
			require.True(t, got.IsZero(), "%s still holds %s", addr, got)
		} else {
			require.True(t, got.Equal(f.balances[addr]), "%s holds %s, want %s", addr, got, f.balances[addr])
		}
	}
	require.True(t, a.BankKeeper.GetBalance(after, f.outsider, AirdropRecoveryDenom).Amount.Equal(AirdropKindATOM.Amount()))
	require.True(t, a.BankKeeper.GetSupply(after, AirdropRecoveryDenom).Amount.Equal(supplyBefore))
	require.True(t, communityPool(t, a, after).Sub(poolBefore).GTE(sdkmath.LegacyNewDecFromInt(f.expected)))

	// The plan is done at height 2 and no longer pending.
	name, height, err := a.UpgradeKeeper.GetLastCompletedUpgrade(after)
	require.NoError(t, err)
	require.Equal(t, AirdropRecoveryUpgradeName, name)
	require.Equal(t, int64(2), height)
	_, err = a.UpgradeKeeper.GetUpgradePlan(after)
	require.ErrorIs(t, err, upgradetypes.ErrNoUpgradePlanFound)
}

// newAppOnDB opens an App on db, as a node process does when it starts.
func newAppOnDB(t *testing.T, db dbm.DB) *App {
	t.Helper()
	return New(log.NewNopLogger(), db, nil, true, simtestutil.AppOptionsMap{
		flags.FlagHome:            t.TempDir(),
		server.FlagInvCheckPeriod: uint(5),
	}, baseapp.SetChainID(TestAppChainID))
}

// initTestChain runs InitChain with the same genesis as Setup.
func initTestChain(t *testing.T, a *App) {
	t.Helper()
	pubKey, err := mock.NewPV().GetPubKey()
	require.NoError(t, err)
	valSet := tmtypes.NewValidatorSet([]*tmtypes.Validator{tmtypes.NewValidator(pubKey, 1)})
	key := secp256k1.GenPrivKey()
	acc := authtypes.NewBaseAccount(key.PubKey().Address().Bytes(), key.PubKey(), 0, 0)
	balance := banktypes.Balance{
		Address: acc.GetAddress().String(),
		Coins:   sdk.NewCoins(sdk.NewCoin(sdk.DefaultBondDenom, sdkmath.NewInt(100000000000000))),
	}
	genesis, err := simtestutil.GenesisStateWithValSet(a.AppCodec(), a.DefaultGenesis(), valSet, []authtypes.GenesisAccount{acc}, balance)
	require.NoError(t, err)
	cronosGen := cronostypes.DefaultGenesis()
	cronosGen.Params.EnableAutoDeployment = true
	genesis["cronos"] = a.cdc.MustMarshalJSON(cronosGen)
	genesis[authtypes.ModuleName] = a.AppCodec().MustMarshalJSON(authtypes.NewGenesisState(authtypes.DefaultParams(), []authtypes.GenesisAccount{acc}))
	state, err := json.Marshal(genesis)
	require.NoError(t, err)
	_, err = a.InitChain(&abci.RequestInitChain{
		ChainId:         TestAppChainID,
		ConsensusParams: DefaultConsensusParams,
		AppStateBytes:   state,
	})
	require.NoError(t, err)
}

// chainWaitingForUpgrade builds the state this binary finds on mainnet when the
// v1.6.2 binary halts: the last completed upgrade is v1.6.2, for which this
// binary has no handler; the fixture accounts exist; and v1.6.2.1 is scheduled
// for planHeight. The state is committed at height 2.
func chainWaitingForUpgrade(t *testing.T, planHeight int64) (dbm.DB, recoveryFixture) {
	t.Helper()
	db := dbm.NewMemDB()
	a := newAppOnDB(t, db)
	initTestChain(t, a)

	ctx := blockContext(a.GetContextForFinalizeBlock(nil), 1)
	f := newRecoveryFixture(t, a, ctx)
	_, err := a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: 1, Time: ctx.BlockTime()})
	require.NoError(t, err)
	_, err = a.Commit()
	require.NoError(t, err)

	// Height 2 stands for the old binary's blocks: it is written straight to
	// the store, because this binary refuses to run them (see below).
	u := blockContext(a.NewUncachedContext(false, tmproto.Header{}), 2)
	done := make([]byte, 9, 9+len("v1.6.2"))
	done[0] = upgradetypes.DoneByte
	binary.BigEndian.PutUint64(done[1:9], 1)
	u.KVStore(a.GetKey(upgradetypes.StoreKey)).Set(append(done, "v1.6.2"...), []byte{1})
	require.NoError(t, a.UpgradeKeeper.ScheduleUpgrade(u, upgradetypes.Plan{Name: AirdropRecoveryUpgradeName, Height: planHeight}))
	last, _, err := a.UpgradeKeeper.GetLastCompletedUpgrade(u)
	require.NoError(t, err)
	require.Equal(t, "v1.6.2", last)
	a.CommitMultiStore().Commit()
	return db, f
}

// TestNewBinaryRunsOnlyFromUpgradeHeight reproduces the switch on mainnet: a new
// process with this binary opens the database the v1.6.2 binary left behind.
func TestNewBinaryRunsOnlyFromUpgradeHeight(t *testing.T) {
	t.Run("started at the upgrade height, it applies the upgrade", func(t *testing.T) {
		db, f := chainWaitingForUpgrade(t, 3)
		a := newAppOnDB(t, db)
		require.Equal(t, int64(2), a.LastBlockHeight())

		rsp, err := a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: 3, Time: blockContext(f.ctx, 3).BlockTime()})
		require.NoError(t, err)
		_, err = a.Commit()
		require.NoError(t, err)

		var amount string
		for _, ev := range rsp.Events {
			if ev.Type == AirdropRecoveryEventType {
				for _, attr := range ev.Attributes {
					if attr.Key == sdk.AttributeKeyAmount {
						amount = attr.Value
					}
				}
			}
		}
		require.Equal(t, sdk.NewCoin(AirdropRecoveryDenom, f.expected).String(), amount)

		after := blockContext(a.NewUncachedContext(false, tmproto.Header{}), 3)
		name, height, err := a.UpgradeKeeper.GetLastCompletedUpgrade(after)
		require.NoError(t, err)
		require.Equal(t, AirdropRecoveryUpgradeName, name)
		require.Equal(t, int64(3), height)
	})

	t.Run("started before the upgrade height, it refuses to run", func(t *testing.T) {
		db, f := chainWaitingForUpgrade(t, 4)
		a := newAppOnDB(t, db)
		_, err := a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: 3, Time: blockContext(f.ctx, 3).BlockTime()})
		require.ErrorContains(t, err, "upgrade handler is missing for v1.6.2 upgrade plan")
	})
}

// TestAirdropRecoveryFullScale measures the upgrade block in the worst case,
// when all 579,281 listed accounts qualify, on a disk-backed database. It is
// slow, so it runs only on request:
//
//	AIRDROP_FULL_SCALE=1 go test ./app -run FullScale -v -timeout 60m
func TestAirdropRecoveryFullScale(t *testing.T) {
	if os.Getenv("AIRDROP_FULL_SCALE") == "" {
		t.Skip("set AIRDROP_FULL_SCALE=1 to run")
	}
	entries, err := LoadAirdropRecoveryList()
	require.NoError(t, err)
	total := sdkmath.ZeroInt()
	for _, e := range entries {
		total = total.Add(e.Kind.Amount())
	}

	db, err := dbm.NewDB("application", dbm.GoLevelDBBackend, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	a := newAppOnDB(t, db)
	initTestChain(t, a)

	// Blocks 1 to n: every listed account is funded with exactly its airdrop,
	// 25,000 accounts per block.
	start := time.Now()
	height := int64(0)
	for i := 0; i < len(entries); i += 25_000 {
		height++
		var ctx sdk.Context
		if height == 1 {
			ctx = blockContext(a.GetContextForFinalizeBlock(nil), height)
		} else {
			ctx = blockContext(a.NewUncachedContext(false, tmproto.Header{}), height)
		}
		batch := entries[i:min(i+25_000, len(entries))]
		sum := sdkmath.ZeroInt()
		for _, e := range batch {
			sum = sum.Add(e.Kind.Amount())
		}
		require.NoError(t, a.BankKeeper.MintCoins(ctx, minttypes.ModuleName, sdk.NewCoins(sdk.NewCoin(AirdropRecoveryDenom, sum))))
		for _, e := range batch {
			c := ctx.WithEventManager(sdk.NewEventManager())
			amount := sdk.NewCoins(sdk.NewCoin(AirdropRecoveryDenom, e.Kind.Amount()))
			require.NoError(t, a.BankKeeper.SendCoinsFromModuleToAccount(c, minttypes.ModuleName, e.Address, amount))
		}
		_, err = a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: height, Time: ctx.BlockTime()})
		require.NoError(t, err)
		_, err = a.Commit()
		require.NoError(t, err)
	}
	t.Logf("funded %d accounts in %d blocks in %s", len(entries), height, time.Since(start).Round(time.Second))

	state := blockContext(a.NewUncachedContext(false, tmproto.Header{}), height)
	require.NoError(t, a.UpgradeKeeper.ScheduleUpgrade(state, upgradetypes.Plan{Name: AirdropRecoveryUpgradeName, Height: height + 1}))
	poolBefore := communityPool(t, a, state)

	// The upgrade block, with the heap sampled while it runs.
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	heapBefore, peak := ms.HeapAlloc, ms.HeapAlloc
	stop := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		for {
			select {
			case <-stop:
				return
			case <-time.After(250 * time.Millisecond):
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				peak = max(peak, m.HeapAlloc)
			}
		}
	}()
	start = time.Now()
	rsp, err := a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: height + 1, Time: blockContext(state, height+1).BlockTime()})
	require.NoError(t, err)
	finalize := time.Since(start)
	close(stop)
	<-sampled
	// The block's changes are still held in memory until Commit: measure what is live.
	runtime.GC()
	runtime.ReadMemStats(&ms)
	live := ms.HeapAlloc
	start = time.Now()
	_, err = a.Commit()
	require.NoError(t, err)
	commit := time.Since(start)
	t.Logf("upgrade block: FinalizeBlock %s, Commit %s; heap %d MiB before the block, %d MiB live at its end (+%d MiB), peak %d MiB with garbage",
		finalize.Round(time.Millisecond), commit.Round(time.Millisecond),
		heapBefore>>20, live>>20, (live-heapBefore)>>20, peak>>20)

	attrs := map[string]string{}
	for _, ev := range rsp.Events {
		if ev.Type == AirdropRecoveryEventType {
			for _, attr := range ev.Attributes {
				attrs[attr.Key] = attr.Value
			}
		}
	}
	require.Equal(t, "579281", attrs["recovered_accounts"])
	require.Equal(t, sdk.NewCoin(AirdropRecoveryDenom, total).String(), attrs[sdk.AttributeKeyAmount])
	require.Equal(t, poolBefore.Add(sdkmath.LegacyNewDecFromInt(total)).String()+AirdropRecoveryDenom, attrs["community_pool"])
	require.Less(t, len(rsp.Events), 100, "the upgrade block must not carry per-transfer events")
}

// bech32ToSDK re-encodes a genesis1 address with the SDK's configured account
// prefix, so that sdk.MustAccAddressFromBech32 accepts it whatever the test
// binary's global config is.
func bech32ToSDK(t *testing.T, addr string) string {
	t.Helper()
	_, bz, err := bech32.DecodeAndConvert(addr)
	require.NoError(t, err)
	return sdk.AccAddress(bz).String()
}
