package app

import (
	"context"
	"fmt"

	upgradetypes "cosmossdk.io/x/upgrade/types"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
)

// RegisterUpgradeHandlers registers the handler for the current upgrade plan and
// reports whether it installed its own store loader.
//
// v1.6.2.1 returns the untouched 2022 ATOM and Evmos airdrop allocations to the
// community pool (see airdrop_recovery.go). It adds, renames and deletes no
// stores, so it installs no store loader and returns false: app.New then keeps
// the default MaxVersionStoreLoader, which nodes running versiondb rely on, at
// the upgrade height as well.
//
// The handler for the previous plan, v1.6.2, is no longer registered. As with
// every upgrade, this binary must only be started at the v1.6.2.1 upgrade
// height (cosmovisor does this automatically); started earlier, the upgrade
// module stops it.
func (app *App) RegisterUpgradeHandlers(_ codec.BinaryCodec, _ int64) bool {
	app.UpgradeKeeper.SetUpgradeHandler(
		AirdropRecoveryUpgradeName,
		func(ctx context.Context, _ upgradetypes.Plan, fromVM module.VersionMap) (module.VersionMap, error) {
			m, err := app.ModuleManager.RunMigrations(ctx, app.configurator, fromVM)
			if err != nil {
				return m, err
			}

			if _, err := app.RunAirdropRecovery(sdk.UnwrapSDKContext(ctx), nil); err != nil {
				return m, fmt.Errorf("upgrade %s: %w", AirdropRecoveryUpgradeName, err)
			}

			return m, nil
		},
	)

	return false
}
