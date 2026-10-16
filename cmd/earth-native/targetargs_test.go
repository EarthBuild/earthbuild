package main

import (
	"reflect"
	"testing"
)

// TestArgumentsMayFollowTheTarget.
//
// **`earth +target --ARG=value` is how the language passes a build argument.**
// It is the form the documentation uses, the form a person types, and the form
// this repository's own corpus uses - wrapped inside `--target="+create-files
// --with_docker_ignore=\"true\""` in a dozen places. The engine took build
// arguments only through `-build-arg NAME=value` before the target, so every one
// of those printed a usage message.
//
// Go's flag package stops at the first non-flag word, so everything after the
// target arrives untouched and this is where it is read.
func TestArgumentsMayFollowTheTarget(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name string
		in   []string
		want map[string]string
		bad  bool
	}{
		{"none", nil, map[string]string{}, false},
		{"one", []string{"--FOO=bar"}, map[string]string{"FOO": "bar"}, false},
		{
			"several",
			[]string{"--FOO=bar", "--BAZ=qux"},
			map[string]string{"FOO": "bar", "BAZ": "qux"},
			false,
		},
		// A value may hold anything, including the separator it was split on.
		{"value with =", []string{"--K=a=b"}, map[string]string{"K": "a=b"}, false},
		{"empty value", []string{"--K="}, map[string]string{"K": ""}, false},
		// Quoted by a shell that did not strip them, which is how the corpus
		// writes it: `--with_docker_ignore=\"true\"`.
		{"quoted", []string{`--K="true"`}, map[string]string{"K": "true"}, false},

		// Refused rather than guessed. A bare word after the target is not a
		// second target and is not an argument either.
		{"bare word", []string{"stray"}, nil, true},
		{"no value", []string{"--FOO"}, nil, true},
		{"no name", []string{"--=x"}, nil, true},
	} {
		got, err := argsAfterTarget(c.in)
		if c.bad {
			if err == nil {
				t.Errorf("%s: accepted %v, want a refusal", c.name, c.in)
			}

			continue
		}

		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}

		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
