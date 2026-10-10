package image_test

// Names for the strings the fixtures in this package repeat.
//
// The OCI document field names are here rather than taken from a package: the
// specification's Go types carry them as struct tags, which is not something a
// test can reference. The media *types* are constants there and are used as
// such - see accept_test.go and E201.
const (
	// testMediaType, testDigest, testSize, testConfigField and testSchemaVersion
	// are field names in an OCI manifest, as a hand-built fixture spells them.
	testMediaType     = "mediaType"
	testDigest        = "digest"
	testSize          = "size"
	testConfigField   = "config"
	testSchemaVersion = "schemaVersion"

	// testPlatform is the platform a fixture's image declares.
	testPlatform = "linux/arm64"
	// testArch is that platform's architecture half.
	testArch = "arm64"
	// testRegistry is the registry a bare reference resolves to.
	testRegistry = "docker.io"
	// testImageRef is the image a fixture builds or pulls.
	testImageRef = "app:latest"

	// testBinary is the program an image's entrypoint names.
	testBinary = "/app/main"
	// testWorkdir is the directory that program runs in.
	testWorkdir = "/app"
	// testConfigPath and testLibPath are files a layer carries, relative as a
	// tar entry is.
	testConfigPath = "etc/config"
	testLibPath    = "usr/foo"
	// testFileA is a file where only its being one matters.
	testFileA = "a.txt"
	// testValue is an environment variable's value, chosen for being unremarkable.
	testValue = "value"
)

const (
	// testLayersField is the field an OCI manifest lists its layers under.
	testLayersField = "layers"
	// testOS is the operating system half of a platform.
	testOS = "linux"
	// testRepoPath is an image reference's repository, as a registry spells it.
	testRepoPath = "library/alpine"
)
