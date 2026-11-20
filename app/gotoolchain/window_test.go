package gotoolchain

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

const (
	tooOldFile = "toolchain_too_old.go"
	tooNewFile = "toolchain_too_new.go"

	// maxQuoted is the length above which go/constant shortens a quoted
	// string. Longer guard lines would be echoed a second time, truncated,
	// in the compiler error.
	maxQuoted = 72
)

// releaseTags returns the release tags that Go 1.minor sets. They are
// cumulative: go1.1, go1.2, …, go1.minor.
func releaseTags(minor int) []string {
	tags := make([]string, 0, minor)
	for i := 1; i <= minor; i++ {
		tags = append(tags, fmt.Sprintf("go1.%d", i))
	}
	return tags
}

// TestBuildConstraintsMatchWindow replays the compile-time layers for every
// Go release from 1.0 to 1.40, asking go/build which files of this package
// that toolchain would compile. Inside [MinMinor, MaxMinor], exactly one
// versiongo file must be compiled and declare Release = "go1.N", and no guard
// file. Outside, no versiongo file and the matching guard file.
func TestBuildConstraintsMatchWindow(t *testing.T) {
	versionFiles, err := filepath.Glob("versiongo_*.go")
	if err != nil {
		t.Fatal(err)
	}
	for minor := 0; minor <= 40; minor++ {
		ctxt := build.Default
		ctxt.ReleaseTags = releaseTags(minor)
		ctxt.BuildTags = nil

		var got, want []string
		for _, name := range versionFiles {
			if matchFile(t, &ctxt, name) {
				got = append(got, releaseIn(t, name))
			}
		}
		if minor >= MinMinor && minor <= MaxMinor {
			want = []string{fmt.Sprintf("go1.%d", minor)}
		}
		if !slices.Equal(got, want) {
			t.Errorf("Go 1.%d: compiled Release declarations %q, want %q", minor, got, want)
		}

		gotOld := matchFile(t, &ctxt, tooOldFile)
		gotNew := matchFile(t, &ctxt, tooNewFile)
		wantOld, wantNew := minor < MinMinor, minor > MaxMinor
		if gotOld != wantOld || gotNew != wantNew {
			t.Errorf("Go 1.%d: %s selected=%v (want %v), %s selected=%v (want %v)",
				minor, tooOldFile, gotOld, wantOld, tooNewFile, gotNew, wantNew)
		}
		if !matchFile(t, &ctxt, "window.go") {
			t.Errorf("Go 1.%d: window.go must be compiled by every toolchain", minor)
		}
	}
}

func matchFile(t *testing.T, ctxt *build.Context, name string) bool {
	t.Helper()
	ok, err := ctxt.MatchFile(".", name)
	if err != nil {
		t.Fatalf("MatchFile(%s): %v", name, err)
	}
	return ok
}

// releaseIn returns the value of the string constant Release declared in a
// versiongo file.
func releaseIn(t *testing.T, name string) string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, ident := range vs.Names {
				if ident.Name != "Release" || i >= len(vs.Values) {
					continue
				}
				if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if s, err := strconv.Unquote(lit.Value); err == nil {
						return s
					}
				}
			}
		}
	}
	t.Fatalf("%s does not declare Release as a string constant", name)
	return ""
}

// TestReleaseMatchesRunningToolchain checks that the compile-time answer
// (Release, chosen by release tags) and the run-time answer (runtime.Version)
// agree for the toolchain running the tests.
func TestReleaseMatchesRunningToolchain(t *testing.T) {
	compiled, ok1 := minorOf(Release)
	running, ok2 := minorOf(runtime.Version())
	if !ok1 || !ok2 || compiled != running {
		t.Fatalf("Release = %q, but runtime.Version() = %q", Release, runtime.Version())
	}
	t.Logf("Release = %q, runtime.Version() = %q", Release, runtime.Version())
}

// TestGuardMessagesMatchWindow checks the compiler messages, which only
// toolchains outside the window ever compile. They must name the window and
// the reference toolchain, and each line must be short enough for the
// compiler to print it once.
func TestGuardMessagesMatchWindow(t *testing.T) {
	for _, name := range []string{tooOldFile, tooNewFile} {
		text := strings.Join(guardLines(t, name), "\n")
		for _, want := range []string{Window, "GOTOOLCHAIN=" + Recommended} {
			if !strings.Contains(text, want) {
				t.Errorf("%s does not mention %q", name, want)
			}
		}
	}
}

// guardLines returns the string statements in the init function of a guard
// file. Each statement must be a plain string literal that the compiler
// prints exactly as written.
func guardLines(t *testing.T, name string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "init" {
			continue
		}
		for _, stmt := range fn.Body.List {
			es, ok := stmt.(*ast.ExprStmt)
			if !ok {
				t.Errorf("%s: init may only contain string statements", name)
				continue
			}
			lit, ok := es.X.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				t.Errorf("%s: init may only contain string statements", name)
				continue
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil || strconv.Quote(s) != lit.Value || utf8.RuneCountInString(lit.Value) > maxQuoted {
				t.Errorf("%s: %s must be a plain string of at most %d characters", name, lit.Value, maxQuoted-2)
			}
			lines = append(lines, s)
		}
	}
	if len(lines) == 0 {
		t.Errorf("%s: no guard message found", name)
	}
	return lines
}

func TestCheck(t *testing.T) {
	for _, tc := range []struct {
		version string
		ok      bool
	}{
		{"go1.21.13", false},
		{"go1.23.12", false},
		{"go1.24", true},
		{"go1.24rc1", true},
		{"go1.24.13", true},
		{"go1.25.4 X:greenteagc", true},
		{"go1.26.3", true},
		{"devel go1.26-0123abcd Tue Jan 6 10:00:00 2026 +0000", true},
		{"go1.27.0", false},
		{"go1.27.1 X:nojsonv2", false},
		{"devel go1.28-4567cdef Mon Aug 3 10:00:00 2026 +0000", false},
		{"go1.", false},
		{"go2.0", false},
		{"", false},
	} {
		if err := Check(tc.version); (err == nil) != tc.ok {
			t.Errorf("Check(%q) = %v, want ok=%v", tc.version, err, tc.ok)
		}
	}
}

func TestRecommendedIsInsideWindow(t *testing.T) {
	if err := Check(Recommended); err != nil {
		t.Fatal(err)
	}
}
