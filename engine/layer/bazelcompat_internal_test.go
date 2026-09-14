package layer

import (
	"testing"
)

// A client is told execution is available, or it will not ask.
//
// **Bazel checks `execution_capabilities.exec_enabled` before it sends
// anything.** A service that advertises only its cache is a service bazel uses
// only as a cache - it refuses remote execution outright and says the server
// does not support it, which is exactly what we were telling it.
func TestCapabilitiesSayExecutionIsAvailable(t *testing.T) {
	t.Parallel()

	got := EncodeCapabilities(DigestFunctionSHA256, 4<<20)

	caps, err := CapabilitiesIn(got)
	if err != nil {
		t.Fatal(err)
	}

	if !caps.ExecEnabled {
		t.Error("a client is not told this service can execute, so it will not ask")
	}

	if len(caps.ExecDigestFunctions) == 0 {
		t.Error("execution advertises no digest function")
	}

	// **v2.1 at the high end, because `output_paths` is new in v2.1.** A client
	// told 2.0 concludes the field does not exist and sends the deprecated
	// output_files and output_directories instead - which is a client asking
	// correctly and being ignored.
	if caps.HighMajor != 2 || caps.HighMinor < 1 {
		t.Errorf("this service advertises up to v%d.%d, and output_paths is new"+
			" in v2.1", caps.HighMajor, caps.HighMinor)
	}
}

// The outputs a client declares are read wherever it put them.
//
// `output_paths` supersedes `output_files` and `output_directories` and wins
// where both are present - REAPI says the older two are ignored then - but a
// client that believes it is talking to an older service sends the older
// fields, and reading only the new one loses everything it asked for.
func TestOutputsAreReadFromEitherPlace(t *testing.T) {
	t.Parallel()

	// The deprecated pair, as a pre-2.1 client sends them.
	old := appendString(nil, fieldOutputFilesOld, "out/a.txt")
	old = appendString(old, fieldOutputDirsOld, "out/sub")

	got, err := CommandIn(old)
	if err != nil {
		t.Fatal(err)
	}

	if len(got.OutputPaths) != 2 {
		t.Errorf("a client using the deprecated fields declared %v", got.OutputPaths)
	}

	// And where both are sent, the new one wins and the old are ignored.
	both := appendString(nil, fieldOutputFilesOld, "ignored")
	both = appendString(both, fieldOutputs, "out/a.txt")

	got, err = CommandIn(both)
	if err != nil {
		t.Fatal(err)
	}

	if len(got.OutputPaths) != 1 || got.OutputPaths[0] != "out/a.txt" {
		t.Errorf("output_paths was sent and %v came back", got.OutputPaths)
	}
}

// A platform sent in the older place is still read.
//
// **Otherwise it is silently ignored**, which is the one outcome that must not
// happen: an action naming a container-image this engine cannot provide would
// run in whatever base was to hand and be filed under the image it named (I3).
// Refusing needs the property to be seen first.
func TestAPlatformInTheDeprecatedPlaceIsRead(t *testing.T) {
	t.Parallel()

	props := appendMessage(nil, fieldPlatformProps,
		encodeProperty(Property{Name: "container-image", Value: "docker://x"}))

	got, err := CommandIn(appendMessage(nil, fieldCommandPlatform, props))
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Platform) != 1 || got.Platform[0].Name != "container-image" {
		t.Errorf("a platform in Command.platform came back as %v", got.Platform)
	}
}
