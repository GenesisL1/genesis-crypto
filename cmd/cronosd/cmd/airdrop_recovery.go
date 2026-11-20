package cmd

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	sdkmath "cosmossdk.io/math"
	tmproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/crypto-org-chain/cronos/v2/app"
	"github.com/crypto-org-chain/cronos/v2/cmd/cronosd/opendb"
	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/server"
)

const flagAccountsOut = "accounts-out"

// AirdropRecoveryCmd groups the tools for the v1.6.2.1 untouched-airdrop
// recovery upgrade.
func AirdropRecoveryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "airdrop-recovery",
		Short: "Check the v1.6.2.1 untouched-airdrop recovery before the upgrade",
	}
	cmd.AddCommand(airdropRecoveryVerifyListCmd(), airdropRecoverySimulateCmd())
	return cmd
}

func airdropRecoveryVerifyListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify-list",
		Short: "Check the list embedded in this binary: SHA-256, format, order and counts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			entries, err := app.LoadAirdropRecoveryList()
			if err != nil {
				return err
			}
			var atom, evmos int
			for _, e := range entries {
				if e.Kind == app.AirdropKindATOM {
					atom++
				} else {
					evmos++
				}
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(),
				"list OK: sha256 %s, block %d, %d accounts (%d ATOM, %d Evmos)\n",
				app.AirdropRecoveryListSHA256, app.AirdropRecoveryListHeight, len(entries), atom, evmos)
			return err
		},
	}
}

type airdropRecoveryReport struct {
	Height                 int64          `json:"height"`
	ListSHA256             string         `json:"list_sha256"`
	ListHeight             int64          `json:"list_height"`
	Listed                 int            `json:"listed"`
	RecoveredAccounts      int            `json:"recovered_accounts"`
	RecoveredATOMAccounts  int            `json:"recovered_atom_accounts"`
	RecoveredEvmosAccounts int            `json:"recovered_evmos_accounts"`
	RecoveredEl1           string         `json:"recovered_el1"`
	RecoveredL1            string         `json:"recovered_l1"`
	Skipped                map[string]int `json:"skipped"`
	CommunityPoolBeforeL1  string         `json:"community_pool_before_l1"`
	CommunityPoolAfterL1   string         `json:"community_pool_after_l1"`
	TotalSupplyL1          string         `json:"total_supply_l1"`
	Elapsed                string         `json:"elapsed"`
}

func el1ToL1(amount sdkmath.Int) string {
	return sdkmath.LegacyNewDecFromIntWithPrec(amount, 18).String()
}

func decEl1ToL1(amount sdkmath.LegacyDec) string {
	return amount.QuoInt(sdkmath.NewIntWithDecimal(1, 18)).String()
}

func airdropRecoverySimulateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "simulate",
		Short: "Run the recovery on this node's latest state and save nothing",
		Long: `Loads the application state at the node's latest height, runs exactly the
code the v1.6.2.1 upgrade handler runs, prints a JSON summary on standard
output and discards every change: nothing is committed. Logs go to standard
error.

Stop the node first, because its database cannot be opened twice, or point
--home at a copy of the node's home directory. With --accounts-out, it also
writes one CSV row per listed account: address, airdrop, outcome and el1
balance before the recovery.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) (err error) {
			serverCtx := server.GetServerContextFromCmd(cmd)
			home := serverCtx.Viper.GetString(flags.FlagHome)

			// Logs go to stderr so that stdout carries only the JSON report.
			logger, err := server.CreateSDKLogger(serverCtx, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			// The mempool-only airdrop block in app.toml plays no part in the
			// recovery, and loading it would log one line per address.
			serverCtx.Viper.Set(app.FlagBlockedAddresses, []string{})

			db, err := opendb.OpenDB(serverCtx.Viper, home, server.GetAppDBBackend(serverCtx.Viper))
			if err != nil {
				return err
			}
			defer func() {
				if cerr := db.Close(); err == nil {
					err = cerr
				}
			}()

			cronosApp := app.New(logger, db, nil, true, serverCtx.Viper)
			height := cronosApp.LastBlockHeight()
			if height == 0 {
				return fmt.Errorf("no committed state in %s", home)
			}

			// A cache context on top of the latest state: RunAirdropRecovery's
			// writes stay in this cache, which is never written back.
			ctx, _ := cronosApp.NewUncachedContext(false, tmproto.Header{Height: height}).CacheContext()

			var observe app.AirdropRecoveryObserver
			if path, _ := cmd.Flags().GetString(flagAccountsOut); path != "" {
				f, ferr := os.Create(path)
				if ferr != nil {
					return ferr
				}
				w := bufio.NewWriter(f)
				var writeErr error
				defer func() {
					// Report the first write, flush or close error.
					if ferr := errors.Join(writeErr, w.Flush(), f.Close()); err == nil {
						err = ferr
					}
				}()
				_, writeErr = fmt.Fprintln(w, "address,airdrop,outcome,balance_el1")
				observe = func(e app.AirdropEntry, o app.AirdropRecoveryOutcome, balance sdkmath.Int) {
					if writeErr == nil {
						_, writeErr = fmt.Fprintf(w, "%s,%s,%s,%s\n", e.Bech32, e.Kind, o, balance)
					}
				}
			}

			start := time.Now()
			summary, err := cronosApp.RunAirdropRecovery(ctx, observe)
			if err != nil {
				return fmt.Errorf("recovery failed at height %d: %w", height, err)
			}

			report := airdropRecoveryReport{
				Height:                 height,
				ListSHA256:             app.AirdropRecoveryListSHA256,
				ListHeight:             app.AirdropRecoveryListHeight,
				Listed:                 summary.Listed,
				RecoveredAccounts:      summary.Recovered,
				RecoveredATOMAccounts:  summary.RecoveredATOM,
				RecoveredEvmosAccounts: summary.RecoveredEvmos,
				RecoveredEl1:           summary.RecoveredAmount.String(),
				RecoveredL1:            el1ToL1(summary.RecoveredAmount),
				Skipped: map[string]int{
					app.AirdropSkippedNoAccount.String():    summary.SkippedNoAccount,
					app.AirdropSkippedSigned.String():       summary.SkippedSigned,
					app.AirdropSkippedNotPlain.String():     summary.SkippedNotPlain,
					app.AirdropSkippedBelowAirdrop.String(): summary.SkippedBelowAirdrop,
					app.AirdropSkippedExtraBalance.String(): summary.SkippedExtraBalance,
				},
				CommunityPoolBeforeL1: decEl1ToL1(summary.CommunityPoolBefore),
				CommunityPoolAfterL1:  decEl1ToL1(summary.CommunityPoolAfter),
				TotalSupplyL1:         el1ToL1(summary.TotalSupply),
				Elapsed:               time.Since(start).Round(time.Millisecond).String(),
			}

			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(report)
		},
	}
	cmd.Flags().String(flagAccountsOut, "", "write one CSV row per listed account to this file")
	return cmd
}
