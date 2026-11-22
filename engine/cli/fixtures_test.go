package cli_test

// Names for the strings these end-to-end fixtures repeat.
//
// A build test writes an Earthfile, builds a target and looks at what came out,
// so the target's name, the artefact's name and the base image appear in every
// one of them. Naming them is what makes a change to any of the three a single
// edit - the base image alone was eighteen occurrences across two packages
// before E175.
const (
	// testTarget is the target these fixtures build.
	testTarget = "build"
	// testArtefact is the file a fixture target saves.
	testArtefact = "out.txt"
	// testBaseImage is the image they stand on. Pulled for real by the tests
	// behind EARTH_TEST_NETWORK, so a bump here is a bump in what CI fetches.
	testBaseImage = "alpine:3.22"
	// testShell is the shell a step runs. busybox's, spelled in full because the
	// image's `/bin/sh` is a symlink to it and naming the link has hidden a
	// missing binary before.
	testShell = "/bin/busybox sh"
)

const (
	// testEarthfile is the file a target is read from, as a diagnostic names it.
	testEarthfile = "Earthfile"
	// testLocPrefix is that name with the separator a location adds.
	testLocPrefix = "Earthfile:"
	// testCacheDirEnv names where the store lives.
	testCacheDirEnv = "EARTH_CACHE_DIR"
	// testProbe is a condition a prediction is recorded against.
	testProbe = "probe"
)
