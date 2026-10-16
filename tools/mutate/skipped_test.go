package main

import "testing"

// A survivor says whether anything was skipped, because the two readings are
// opposite.
//
// `go test` fails when a test notices a mutant, so "no failure" is read as "no
// test noticed". A skipped test does not fail either, and a package whose
// tests all skipped produces exactly the same silence as one whose tests all
// ran and shrugged.
//
// This is not hypothetical. `nstest.In` skips when the machine will not make a
// user namespace, which is the default in a container - and a sweep run there
// reported `guest: chrooting a confined step (A3, I10)` as unguarded when
// `TestAConfinedStepIsChrootedIntoItsOwnFilesystem` asserts on exactly that
// field and had simply not run.
func TestASurvivorCountsWhatWasSkipped(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		text  string
		count int
		first string
	}{
		"nothing skipped": {text: "ok  \tpkg\t0.1s\n"},

		"one": {
			text:  "=== RUN   TestA\n--- SKIP: TestA (0.00s)\n    x_test.go:9: no user namespace\nPASS\n",
			count: 1, first: "TestA",
		},

		"several, and the first is named": {
			text:  "--- SKIP: TestA (0.00s)\n--- SKIP: TestB (0.00s)\n--- PASS: TestC (0.00s)\n",
			count: 2, first: "TestA",
		},

		// A subtest skip is still a skip: the parent reports PASS, and the
		// mechanism the subtest covered was not exercised either way.
		"a subtest": {
			text:  "    --- SKIP: TestA/case (0.00s)\n--- PASS: TestA (0.00s)\n",
			count: 1, first: "TestA/case",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			count, first := skipped(tc.text)
			if count != tc.count {
				t.Errorf("counted %d skips, want %d", count, tc.count)
			}

			if first != tc.first {
				t.Errorf("named %q as the first skip, want %q", first, tc.first)
			}
		})
	}
}
