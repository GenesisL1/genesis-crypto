//go:build go1.24 && !go1.25

package gotoolchain

// Release is the Go release compiling genesisd, as the compiler's own release
// tags report it. It is declared once per validated release, in
// versiongo_1.N.go, so code that refers to it does not compile with any other
// toolchain.
const Release = "go1.24"
