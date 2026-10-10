package main

import (
	"os"
	"strings"
	"testing"
)

// NAME=VALUE is taken as written.
func TestABuildArgumentIsTakenAsWritten(t *testing.T) {
	t.Parallel()

	var args buildArgs

	err := args.Set("TAG=v1")
	if err != nil {
		t.Fatal(err)
	}

	// An `=` in the value is part of the value: a key that is itself an
	// assignment is ordinary, and cutting at the last one would corrupt it.
	err = args.Set("QUERY=a=b")
	if err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]string{"TAG": "v1", "QUERY": "a=b"} {
		if args[name] != want {
			t.Errorf("%s = %q, want %q", name, args[name], want)
		}
	}
}

// A bare name takes its value from the environment.
//
// The same spelling `earthly --build-arg NAME` accepts, and for the same reason:
// the two front ends put the same argument in front of the same engine, and a
// flag one takes and the other refuses is a script that works until somebody
// changes which binary they call. What this used to refuse was *guessing an
// empty value*, and looking the name up does not guess.
func TestABareBuildArgumentComesFromTheEnvironment(t *testing.T) {
	// Not parallel: t.Setenv.
	t.Setenv("TAG_SUFFIX", "from-the-environment")

	var args buildArgs

	err := args.Set("TAG_SUFFIX")
	if err != nil {
		t.Fatalf("a bare name was refused: %v", err)
	}

	if args["TAG_SUFFIX"] != "from-the-environment" {
		t.Errorf("TAG_SUFFIX = %q, want the environment's value", args["TAG_SUFFIX"])
	}
}

// A bare name with nothing behind it is still refused.
//
// The half of the old refusal that was right: an empty string is a value a build
// can legitimately be given, so a name nobody exported must not quietly become
// one.
func TestABareBuildArgumentWithNothingBehindItIsRefused(t *testing.T) {
	// Not parallel: t.Setenv.
	t.Setenv("NOT_EXPORTED", "")
	os.Unsetenv("NOT_EXPORTED")

	var args buildArgs

	err := args.Set("NOT_EXPORTED")
	if err == nil {
		t.Fatal("a name with nothing behind it was accepted")
	}

	if !strings.Contains(err.Error(), "NOT_EXPORTED") {
		t.Errorf("the refusal never names the argument: %v", err)
	}
}

// An empty name is refused whichever way it is written.
func TestANamelessBuildArgumentIsRefused(t *testing.T) {
	t.Parallel()

	var args buildArgs

	for _, in := range []string{"", "=v"} {
		err := args.Set(in)
		if err == nil {
			t.Errorf("%q was accepted as a build argument", in)
		}
	}
}
