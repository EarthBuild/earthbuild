//go:build linux && integration

package cli_test

import "testing"

// The gate picks the target a corpus file is meant to be built by.
//
// A rule in the harness rather than in the engine, and tested for the same
// reason the engine's rules are: it decides what the number means. Picking the
// first target had the gate build `subtest` - a helper that takes an argument
// and asserts what it holds - and report that the engine could not build the
// file (E445).
func TestTheEntryTargetOfACorpusFile(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, src, want string }{{
		name: "the first target, when there is no convention to follow",
		src:  "VERSION 0.8\nbuild:\n    FROM alpine\nother:\n    FROM alpine\n",
		want: "build",
	}, {
		name: "all, wherever it is declared",
		src:  "VERSION 0.8\nhelper:\n    FROM alpine\nall:\n    BUILD +helper\n",
		want: "all",
	}, {
		name: "test, when there is no all",
		src:  "VERSION 0.8\nsubtest:\n    FROM alpine\ntest:\n    BUILD +subtest\n",
		want: "test",
	}, {
		name: "all beats test",
		src:  "VERSION 0.8\ntest:\n    FROM alpine\nall:\n    BUILD +test\n",
		want: "all",
	}, {
		name: "nothing at all, from a file that declares none",
		src:  "VERSION 0.8\nFROM alpine\nARG x=1\n",
		want: "",
	}} {
		if got := entryTarget(tc.src); got != tc.want {
			t.Errorf("%s: chose %q, want %q", tc.name, got, tc.want)
		}
	}
}
