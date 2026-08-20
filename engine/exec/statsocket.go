package exec

import "os"

// statSocket reports whether anything is at a path.
//
// Deliberately not `exec.LookPath`, which is what `lookHostDocker` uses and what
// this was first written as. LookPath asks whether something is an *executable*
// on PATH; a unix socket is not executable, so it answers no for every socket
// that exists and a build would silently never inherit a daemon (E383).
//
// The two functions read almost identically at a call site, which is what let
// the wrong one through.
func statSocket(p string) bool {
	_, err := os.Stat(p)

	return err == nil
}
