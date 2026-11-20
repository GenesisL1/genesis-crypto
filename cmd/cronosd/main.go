package main

import (
	"os"

	"github.com/crypto-org-chain/cronos/v2/app"
	"github.com/crypto-org-chain/cronos/v2/app/gotoolchain"
	"github.com/crypto-org-chain/cronos/v2/cmd/cronosd/cmd"

	svrcmd "github.com/cosmos/cosmos-sdk/server/cmd"
)

// genesisd compiles only with Go 1.24–1.26: gotoolchain.Release is declared
// only for those releases (see app/gotoolchain).
const _ = gotoolchain.Release

func main() {
	rootCmd := cmd.NewRootCmd()
	if err := svrcmd.Execute(rootCmd, cmd.EnvPrefix, app.DefaultNodeHome); err != nil {
		os.Exit(1)
	}
}
