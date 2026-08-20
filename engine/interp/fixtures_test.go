package interp_test

import "github.com/EarthBuild/earthbuild/internal/earthfile"

// Commands, spelled once, by the parser.
//
// `string(earthfile.CmdCopy)` is a constant expression, so these cost nothing
// and a rename of the language breaks the build here instead of quietly
// changing what a test believes it is asserting about.
const (
	testCmdCopy         = string(earthfile.CmdCopy)
	testCmdLet          = string(earthfile.CmdLet)
	testCmdSaveImage    = string(earthfile.CmdSaveImage)
	testCmdSaveArtifact = string(earthfile.CmdSaveArtifact)

	// testForcedArtifact is a SAVE ARTIFACT that overwrites its destination.
	testForcedArtifact = testCmdSaveArtifact + " --force"
	// testSSHFlag asks a RUN for the caller's agent.
	testSSHFlag = "--ssh"
	// testPrivilegedFlag asks a RUN for capabilities it does not get by default.
	testPrivilegedFlag = "--privileged"
)

// Names for the strings the fixtures in this package repeat.
//
// Only values a test *chooses* belong here. A command's spelling is not one:
// `COPY` and `SAVE IMAGE` are the language's, held by the parser as
// `earthfile.CmdCopy` and `earthfile.CmdSaveImage`, and a test asserting on one
// takes it from there - so that a rename is a compile error here rather than a
// silent disagreement about what the language is (E200).
const (
	// testEarthfile is the file a target is read from, as a diagnostic names it.
	testEarthfile = "Earthfile"
	// testLibEarthfile is a second Earthfile, referenced across directories.
	testLibEarthfile = "lib/Earthfile"

	// testArch is the architecture a fixture asks to build for.
	testArch = "amd64"
	// testShell is the interpreter a RUN command is handed to.
	testShell = "/bin/sh"
	// testTrue is the shell's cheapest success.
	testTrue = "true"

	// testSecret is the name a secret is mounted under.
	testSecret = "TOKEN"
	// testRepo is a remote target's repository.
	testRepo = "github.com/org/repo"

	// testSourceFile is the file a COPY moves.
	testSourceFile = "src.txt"
	// testSourcePath is the same file inside a source tree.
	testSourcePath = "src/main.go"
	// testSourceDir is the directory a COPY takes wholesale.
	testSourceDir = "src"
	// testFileA is a second file, where two are needed and neither is special.
	testFileA = "a.txt"
	// testSpacedFile has a space in it, which is where quoting goes wrong.
	testSpacedFile = "a file.txt"
	// testPresentFile is the file an --if-exists copy finds.
	testPresentFile = "present.txt"
	// testObject is a build output named by an absolute path.
	testObject = "/code/main.o"

	// testWorkdir is where a fixture's commands run.
	testWorkdir = "/app"
	// testOutDir is where a fixture saves what it produced.
	testOutDir = "/out"
	// testImageRef is the image a target saves.
	testImageRef = "app:latest"
	// testVersion is a version string, where the value matters only in being one.
	testVersion = "1.2.3"

	// testTakenMark is what the branch a condition selects prints.
	testTakenMark = "yes-branch"
	// testSkippedMark is what the branch it does not select would have printed.
	testSkippedMark = "no-branch"
	// testOS is the operating system half of a platform.
	testOS = "linux"
	// testFileB is a second file, where two are needed and neither is special.
	testFileB = "b.txt"

	// testGreeting is what a fixture prints when the content is beside the point.
	testGreeting = "hello"
	// testMain is a target name, and the name of a Go package in a fixture.
	testMain = "main"
	// testGoPackage is the first line of a Go file a fixture builds.
	testGoPackage = "package main"

	// testAfter is a RUN placed to prove ordering.
	testAfter = "RUN after"
	// testDockerImages is a RUN needing a daemon, used to test WITH DOCKER.
	testDockerImages = "RUN docker images"
	// testUnbufferProbe is the IF condition the interactive tests turn on.
	testUnbufferProbe = "command -v unbuffer"
)
