package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every directory of Go source reaches the image the repository lints and tests.
//
// `+code` copies a hand-written list of directories into /earthly, and `+lint`,
// `+unit-test` and every binary target build from that image. A directory
// missing from the list is not linted, not tested and not compiled by any of
// them - and nothing says so, because the targets pass. They are passing on a
// smaller repository than the one on disk.
//
// The `engine` tree - this whole effort - was absent from that list for its
// entire life. `+lint` found it the first time it was run for real, as three
// typecheck failures in `cmd/earth-guestd`, which is the only part of the
// engine the list did copy:
//
//	could not import github.com/EarthBuild/earthbuild/engine/core
//	(no required module provides package ...)
//
// A hand-written list is fine; a hand-written list nothing checks is a list
// that is wrong as soon as somebody adds a directory.
func TestEveryGoDirectoryReachesTheBuildImage(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..")

	src, err := os.ReadFile(filepath.Join(root, testEarthfile)) //nolint:gosec // a fixture this test wrote
	if err != nil {
		// The tree under test is not a checkout. That is the ordinary state
		// inside the build image this test is *about*: `+code` copies source
		// directories and not the Earthfile, so the repository's own
		// `+unit-test` runs these tests somewhere the question cannot be asked.
		//
		// Which is the same rule as everywhere else here - a check that cannot
		// run is not a check that failed - and it is a little funny that the
		// guard for "the image does not contain everything" was the last thing
		// to learn it (E52).
		t.Skipf("no Earthfile above this test, so there is no copy list to check: %v", err)
	}

	copied := copiedByCode(t, string(src))

	// Directories that hold Go source and are deliberately not in the image,
	// each with the reason. An exclusion nobody can explain is an omission.
	excused := map[string]string{
		"examples": "a corpus of other people's Earthfiles, built by their own targets",
		"scripts":  "shell, and the Go in it is a helper the build never compiles",
		"tests":    "integration tests, run against a built binary rather than compiled with it",
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}

	found := 0

	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}

		if !holdsGo(filepath.Join(root, e.Name())) {
			continue
		}

		found++

		if copied[e.Name()] {
			continue
		}

		if why, ok := excused[e.Name()]; ok {
			t.Logf("%s is deliberately not in the image: %s", e.Name(), why)

			continue
		}

		t.Errorf("%s holds Go source and no target copies it, so nothing lints, tests or"+
			" compiles it\n  add it to the COPY --dir list in +code", e.Name())
	}

	// A scan that found nothing would pass for the wrong reason - a moved
	// Earthfile, a renamed target, a walk that never started.
	if found < 10 {
		t.Errorf("only %d directories of Go source were found, so this check is not"+
			" reading the repository", found)
	}
}

// copiedByCode reads the names `+code` copies into the image.
//
// Text rather than the parser: the property is about what a person wrote in a
// list, and a check that resolved the Earthfile properly would still be reading
// the same line.
func copiedByCode(t *testing.T, src string) map[string]bool {
	t.Helper()

	out := map[string]bool{}

	inCode := false

	var continued string

	for line := range strings.SplitSeq(src, "\n") {
		trimmed := strings.TrimSpace(line)

		// Targets are unindented, so an unindented line that is not `code:` ends
		// the target.
		if line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			inCode = strings.HasPrefix(trimmed, "code:")

			continue
		}

		if !inCode {
			continue
		}

		if continued != "" {
			trimmed, continued = continued+" "+trimmed, ""
		}

		if strings.HasSuffix(trimmed, "\\") {
			continued = strings.TrimSuffix(trimmed, "\\")

			continue
		}

		if !strings.HasPrefix(trimmed, "COPY ") {
			continue
		}

		for _, f := range strings.Fields(trimmed) {
			// Flags, the command, and the destination are not sources.
			if strings.HasPrefix(f, "-") || f == "COPY" || f == "./" {
				continue
			}

			// `buildkitd/buildkitd.go` and `inputgraph/*.go` name a directory
			// by naming something inside it.
			out[strings.Split(f, "/")[0]] = true
		}
	}

	if len(out) == 0 {
		t.Fatal("no COPY sources were found in +code, so this check reads nothing")
	}

	return out
}

// holdsGo reports whether a directory contains Go source, at any depth worth
// looking at.
func holdsGo(dir string) bool {
	found := false

	_ = filepath.WalkDir(dir, func(p string, _ os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable corner is not the point
		}

		if strings.HasSuffix(p, ".go") {
			found = true

			return filepath.SkipAll
		}

		return nil
	})

	return found
}
