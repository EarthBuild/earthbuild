package store

// testTwiceFile is written by two layers, to prove which one wins.
const testTwiceFile = "twice.txt"

const (
	// testTool is a program a layer carries, relative as a tar entry is.
	testTool = "usr/tool"
	// testHeader is an included file, where the nesting is the point.
	testHeader = "inc/b.h"
)
