package app

import (
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"unicode"
)

// These tests fail when run with a Go toolchain whose binaries genesisd would
// refuse to run: see CheckToolchain.

func TestToolchainMatchesConsensus(t *testing.T) {
	if err := CheckToolchain(); err != nil {
		t.Fatal(err)
	}
}

func TestUnicodeCanaries(t *testing.T) {
	if failed := unicodeCanaryFailures(); len(failed) > 0 {
		t.Fatalf("Unicode %s tables: %s", unicode.Version, strings.Join(failed, "; "))
	}
}

func TestBuildSettingHas(t *testing.T) {
	settings := []debug.BuildSetting{
		{Key: "-tags", Value: "netgo,jsonv2"},
		{Key: "GOEXPERIMENT", Value: "synctest, jsonv2"},
	}
	if !buildSettingHas(settings, "GOEXPERIMENT", "jsonv2") {
		t.Error("jsonv2 in GOEXPERIMENT not found")
	}
	for _, s := range [][]debug.BuildSetting{
		nil,
		{{Key: "-tags", Value: "jsonv2"}},
		{{Key: "GOEXPERIMENT", Value: "nojsonv2"}},
		{{Key: "GOEXPERIMENT", Value: "jsonv2x,synctest"}},
	} {
		if buildSettingHas(s, "GOEXPERIMENT", "jsonv2") {
			t.Errorf("false positive for %v", s)
		}
	}
}

func TestToolchainErrorExplainsFix(t *testing.T) {
	msg := toolchainError("Unicode 17.0.0 tables").Error()
	for _, want := range []string{
		runtime.Version(),
		"Unicode 17.0.0 tables",
		"Unicode " + ConsensusUnicodeVersion + " tables",
		"no GOEXPERIMENT=jsonv2",
		"Go 1.23.12 to 1.25.x",
		"GOTOOLCHAIN=go1.23.12 make install",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message lacks %q:\n%s", want, msg)
		}
	}
}
