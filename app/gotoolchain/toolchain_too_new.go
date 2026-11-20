//go:build go1.27

package gotoolchain

// Only Go 1.27 and newer compile this file. The statements in init are
// deliberately invalid Go: they stop the build here, and the compiler prints
// each one as a line of the error. Keep each string at 70 characters or fewer
// so the compiler prints it only once (window_test.go checks this). See
// window.go for why the window exists.
//
// The go command fetches the reference toolchain on first use.
func init() {
	"genesisd needs Go 1.24 through 1.26: this Go toolchain is too new"
	"newer Go releases are not yet validated for this network"
	"fix: GOTOOLCHAIN=go1.24.13 make install"
}
