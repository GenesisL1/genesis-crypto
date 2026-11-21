package app

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"unicode"
)

// ConsensusUnicodeVersion is the version of the Unicode tables every GenesisL1
// node must be built with.
//
// Go compiles its Unicode tables into each binary, and state-machine code
// passes user input to functions that read them. For example, ibc-go stores
// strings.ToUpper of the base denom of every new IBC token as the token's
// symbol, and whoever sends the token chooses the base denom. Go 1.21 to 1.26
// ship Unicode 15.0.0; Go 1.27 ships Unicode 17.0.0, in which 55 characters
// change case and more than 10,000 become printable. Nodes built on either side
// of that line can compute a different state for the same transaction, so a
// single transaction could split the chain.
//
// golang.org/x/text picks its own tables by Go version in the same way (its
// Unicode 15.0.0 tables for Go 1.21 to 1.26), so this check covers it too.
//
// Change this value only in a coordinated upgrade that moves every node to the
// new Go release at the same height.
const ConsensusUnicodeVersion = "15.0.0"

// CheckToolchain returns an error unless this binary was built the way every
// GenesisL1 node must be: with Unicode tables that behave like
// ConsensusUnicodeVersion (Go 1.21 to 1.26), and with the classic encoding/json
// rather than the one GOEXPERIMENT=jsonv2 builds on encoding/json/v2 (opt-in in
// Go 1.25 and 1.26, the default from Go 1.27). Both are compiled into the
// binary and change what some transactions do. genesisd runs this check before
// any command, and New runs it again for other programs that embed the app.
func CheckToolchain() error {
	if unicode.Version != ConsensusUnicodeVersion {
		return toolchainError("Unicode " + unicode.Version + " tables")
	}
	if failed := unicodeCanaryFailures(); len(failed) > 0 {
		return toolchainError("Unicode tables that differ from " + ConsensusUnicodeVersion + " (" + strings.Join(failed, "; ") + ")")
	}
	if info, ok := debug.ReadBuildInfo(); ok && buildSettingHas(info.Settings, "GOEXPERIMENT", "jsonv2") {
		return toolchainError("the jsonv2 encoding/json (GOEXPERIMENT=jsonv2)")
	}
	return nil
}

// unicodeCanaryFailures checks characters whose behaviour differs between
// Unicode 15.0.0 and 17.0.0, so that tables changed without a new
// unicode.Version are caught too.
func unicodeCanaryFailures() []string {
	var failed []string
	expect := func(ok bool, what string) {
		if !ok {
			failed = append(failed, what)
		}
	}
	// U+0264 gained the upper case U+A7CB in Unicode 16: the ibc-go symbol case.
	expect(strings.ToUpper("ɤ") == "ɤ", "strings.ToUpper(U+0264) is not U+0264")
	expect(strings.ToLower("Ɤ") == "Ɤ", "strings.ToLower(U+A7CB) is not U+A7CB")
	// U+1C89 and U+1C8A, Cyrillic TJE, were added in Unicode 16.
	expect(!unicode.IsPrint('Ᲊ'), "unicode.IsPrint(U+1C89) is true")
	expect(!strings.EqualFold("Ᲊ", "ᲊ"), "strings.EqualFold(U+1C89, U+1C8A) is true")
	return failed
}

// buildSettingHas reports whether the comma-separated build setting key lists
// item, as GOEXPERIMENT does.
func buildSettingHas(settings []debug.BuildSetting, key, item string) bool {
	for _, s := range settings {
		if s.Key != key {
			continue
		}
		for _, v := range strings.Split(s.Value, ",") {
			if strings.TrimSpace(v) == item {
				return true
			}
		}
	}
	return false
}

func toolchainError(found string) error {
	return fmt.Errorf(`genesisd will not run: it was built with %s, which has %s.
GenesisL1 nodes need Unicode %s tables and the classic encoding/json (Go 1.21 to 1.26,
no GOEXPERIMENT=jsonv2). Nodes built differently can compute a different state for the same
transaction, which would split the chain.
Rebuild with Go 1.23.12 to 1.25.x, the versions this release builds with. If a newer Go is
installed, run:
    GOTOOLCHAIN=go1.23.12 make install    (or make build)`,
		runtime.Version(), found, ConsensusUnicodeVersion)
}
