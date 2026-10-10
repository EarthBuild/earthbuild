package check_test

// Directories these checks never descend into.
//
// `testdata` for two reasons, and the second is the one that found it: it holds
// no source to cite, and it is *built while these run*. A 20,000-entry fixture
// is assembled under a temporary name and renamed by another package's test,
// and a walker inside it at that moment fails on a path that existed when it
// was listed.
const (
	skipGit      = ".git"
	skipModules  = "node_modules"
	skipTestdata = "testdata"
)
