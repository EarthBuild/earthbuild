package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// backendPlatforms is the set of platforms this package has a sandbox for.
//
// One `sandbox_<goos>.go` per backend, plus `sandbox_other.go` which refuses.
// Reading the directory rather than listing them here is the point: a third
// backend arrives as a new file and this test notices without being told.
func backendPlatforms(t *testing.T) []string {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}

	var found []string

	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".go")

		goos, ok := strings.CutPrefix(name, "sandbox_")
		if !ok || strings.HasSuffix(name, "_test") || goos == "other" {
			continue
		}

		found = append(found, goos)
	}

	return found
}

// buildTag returns the `//go:build` constraint of a file in this package.
func buildTag(t *testing.T, name string) string {
	t.Helper()

	b, err := os.ReadFile(filepath.Clean(name))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}

	m := regexp.MustCompile(`(?m)^//go:build (.*)$`).FindSubmatch(b)
	if m == nil {
		return ""
	}

	return strings.TrimSpace(string(m[1]))
}

// The cross-backend suite runs on every platform that has a backend.
//
// `cli.Run` is portable: `sandbox_darwin.go` picks Apple's `container`,
// `sandbox_linux.go` picks the guest in namespaces, and the interpreter above
// them does not know which it got. The suite whose entire purpose is to prove
// those two agree is `//go:build darwin`.
//
// So the shared case table - thirty-odd constructs, one list, deliberately
// written once so both backends answer the same questions - has never been
// asked of the Linux backend at all. The native backend is the one this branch
// exists to build, and it was the one being tested least.
//
// **The failure class: the portable thing was made portable and its only
// consumer was not.** It is the same shape as `copyTree` implementing a subset
// of `layer.Take` (E87-E91) and as `Pack` serving two callers with one set of
// rules - a shared definition, and one side of the sharing left where it was.
// Each time the shared half looked finished, because it was.
//
// A source guard, and worth what source guards are worth (see
// `nonTestFilesContaining`): it proves the suite is compiled for a platform,
// never that a case passed there. Its behavioural pair is the suite itself.
func TestTheCrossBackendSuiteRunsOnEveryBackend(t *testing.T) {
	t.Parallel()

	platforms := backendPlatforms(t)
	if len(platforms) < 2 {
		t.Skipf("one backend (%v), so there is nothing to be differential about", platforms)
	}

	// Every file naming the shared case table is part of the suite.
	for _, name := range []string{"e2e_sandbox_test.go", "e2e_cases_test.go"} {
		tag := buildTag(t, name)

		for _, goos := range platforms {
			if tag != "" && !strings.Contains(tag, goos) {
				t.Errorf("%s is built for %q, so the shared cases never run on %s:"+
					"\n  this package has a sandbox backend for %v"+
					"\n  a differential suite that compiles for one of them is not differential"+
					"\n  the cases are portable; make the runner choose the backend instead of naming it",
					name, tag, goos, platforms)
			}
		}
	}
}
