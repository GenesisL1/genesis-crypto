//go:build !go1.24

package gotoolchain

// Only Go toolchains older than 1.24 compile this file. The statements in
// init are deliberately invalid Go: they stop the build here, and the
// compiler prints each one as a line of the error. Keep each string at 70
// characters or fewer so the compiler prints it only once (window_test.go
// checks this). See window.go for why the window exists.
//
// The go command fetches the reference toolchain on first use, or install
// Go 1.24.13 from https://go.dev/dl/.
func init() {
	"genesisd needs Go 1.24 through 1.26: this Go toolchain is too old"
	"fix: GOTOOLCHAIN=go1.24.13 make install"
}
