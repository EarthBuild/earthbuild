package exec

const (
	// testPlatform is the platform a fixture builds for.
	testPlatform = "linux/arm64"
	// testOtherPlatform is a different one, where only the difference matters.
	testOtherPlatform = "linux/amd64"
	// testArch is this fixture's architecture half.
	testArch = "arm64"
	// testTwiceFile is written by two steps, to prove which one wins.
	testTwiceFile = "twice.txt"
)
