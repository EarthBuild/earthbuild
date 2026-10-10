package cli

// Names for the strings the internal tests in this package repeat.
//
// Separate from fixtures_test.go because a test constant is visible only in its
// own package, and this package has tests in both (E199).
const (
	// testProbe is a condition a prediction is recorded against.
	testProbe = "probe"
	// testCacheDirEnv names where the store lives.
	testCacheDirEnv = "EARTH_CACHE_DIR"
	// testCommand is a step's command, where the command is beside the point.
	testCommand = "command"
	// testTarget is the target a fixture builds.
	testMainTarget = "main"
	// testEarthfile is the file a target is read from.
	testEarthfile = "Earthfile"
	// testBaseImage is the image a fixture stands on.
	testBaseImage = "alpine:3.22"
	// testTwoImages is that image and another, as a list is written.
	testTwoImages = "alpine:3.22,golang:1.26"
	// testManifest is a file whose presence decides a prediction.
	testManifest = "package.json"
	// testJar is a built artefact, named for a language that produces one.
	testJar = "app.jar"
	// testGoFile is a source file inside this repository, used where a real
	// path is needed.
	testGoFile = "core/schedule.go"
)

const (
	// testUnbuffer is the program the interactive tests need a terminal from.
	testUnbuffer = "unbuffer"
)
