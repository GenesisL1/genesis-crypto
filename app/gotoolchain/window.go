// Package gotoolchain pins the Go toolchains that genesisd may be built with:
// Go 1.24, 1.25 and 1.26, any patch release. Every other toolchain is refused.
//
// genesisd is consensus-critical: every validator has to compute the same
// state from the same blocks. The Go toolchain is part of the program, since
// it supplies the runtime, the garbage collector and the standard library
// (encoding/json, the unicode tables, compress/flate, crypto). A toolchain the
// network has not validated produces an untested binary. Releases are built
// with Recommended. Just above the window, Go 1.27 switched encoding/json to
// the v2 implementation, changed the compress/flate encoder and moved to
// Unicode 17.
//
// The rule lives in Go source, not in the Makefile, so it holds for every way
// of building: make, go build/install/run/test, Nix, IDEs and CI.
//
// # Three layers
//
// All three rest on one fact: a Go 1.N toolchain sets the release tags
// go1.1 … go1.N itself, and -tags can add a tag but never remove one.
//
// Allowlist. versiongo_1.24.go, versiongo_1.25.go and versiongo_1.26.go are
// each compiled only by their own release (//go:build go1.24 && !go1.25, …),
// and each declares Release, the release compiling the binary. The app
// package and genesisd's main package depend on gotoolchain.Release, so with
// any other toolchain they fail with "undefined: gotoolchain.Release". This
// layer fails closed: deleting a versiongo file breaks the build for that
// release instead of letting a toolchain through, and adding one is the
// visible act of validating a new release.
//
// Messages. toolchain_too_old.go (//go:build !go1.24) and toolchain_too_new.go
// (//go:build go1.27) are compiled only outside the window and hold
// deliberately invalid statements. The go command compiles this package before
// its dependents, so the build stops here first, with the reason and the fix:
//
//	app/gotoolchain/toolchain_too_new.go:13:2: "genesisd needs Go 1.24 through 1.26: this Go toolchain is too new" (untyped string constant) is not used
//	app/gotoolchain/toolchain_too_new.go:14:2: "newer Go releases are not yet validated for this network" (untyped string constant) is not used
//	app/gotoolchain/toolchain_too_new.go:15:2: "fix: GOTOOLCHAIN=go1.24.13 make install" (untyped string constant) is not used
//
// Start-up. init checks runtime.Version, the version string the linker
// records in the binary, against MinMinor and MaxMinor, and panics outside
// the window. Forging a release tag does not get this far: "go build -tags
// go1.24" on Go 1.23 selects versiongo_1.24.go, whose //go:build line makes it
// a Go 1.24 file, and since Go 1.23 the compiler refuses files newer than
// itself ("file requires newer Go version go1.24"). This layer is the last net
// for guard files that were edited, and for compilers that set release tags
// differently.
//
// # What this package does not do
//
// The go directive in go.mod is deliberately left out of this window. It
// selects language semantics and the default GODEBUG settings (from go 1.24
// on, for example, randseednop makes math/rand.Seed a no-op), and changing
// those is a consensus decision of its own.
//
// Build constraints cannot see patch releases. Go 1.24.0 itself fails at link
// time on bytedance/sonic/loader (golang/go#71672), so use 1.24.1 or newer.
//
// # Moving the window
//
// Add or remove a versiongo_1.N.go file, then change MinMinor, MaxMinor and the
// //go:build lines and messages of the two toolchain_too_*.go files to match.
// window_test.go fails if they disagree.
package gotoolchain

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
)

const (
	// MinMinor is the oldest Go 1.x minor release genesisd may be built with.
	MinMinor = 24
	// MaxMinor is the newest Go 1.x minor release genesisd may be built with.
	MaxMinor = 26
	// Recommended is the reference toolchain for release builds.
	Recommended = "go1.24.13"
)

// Window names the supported toolchains in words, as the guard messages do.
var Window = fmt.Sprintf("Go 1.%d through 1.%d", MinMinor, MaxMinor)

func init() {
	if err := Check(runtime.Version()); err != nil {
		panic(err)
	}
}

// Check returns an error unless version, in the format of runtime.Version
// ("go1.24.13", "go1.26rc1", "go1.25.4 X:greenteagc", "devel go1.27-abc…"),
// names a toolchain inside the window. Unrecognised strings are refused.
func Check(version string) error {
	minor, ok := minorOf(version)
	if !ok {
		return fmt.Errorf("genesisd: cannot tell which Go release built this binary (runtime.Version() = %q); it must be %s",
			version, Window)
	}
	if minor < MinMinor || minor > MaxMinor {
		return fmt.Errorf("genesisd: built with %s, but it must be built with %s; rebuild with: GOTOOLCHAIN=%s make install",
			version, Window, Recommended)
	}
	return nil
}

// minorOf returns N from the first "go1.N" in version.
func minorOf(version string) (int, bool) {
	_, rest, found := strings.Cut(version, "go1.")
	if !found {
		return 0, false
	}
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	minor, err := strconv.Atoi(rest[:end])
	return minor, err == nil
}
