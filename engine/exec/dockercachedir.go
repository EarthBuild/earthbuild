package exec

import (
	"errors"
	"fmt"
	"path/filepath"
)

// cacheNameLimit is how long a shared daemon cache's name may be.
//
// The same figure the interpreter enforces, and deliberately a second copy
// rather than an import: these two checks answer to different parties, and a
// shared constant would invite somebody to relax one for the other's reason.
const cacheNameLimit = 64

// dockerCacheDir is where a shared daemon keeps what it is asked to keep.
//
// **The name is a peer's claim here, whatever the interpreter did with it.** A
// step assignment arrives from a driver this worker did not write (A5, C.3), and
// `DockerCache` crosses that wire - so by the time a path is composed from it,
// `../../..` is a directory outside the store with a daemon writing into it
// (E360).
//
// The interpreter's check is not this one and does not replace it: that one
// protects the author, naming the file and the flag at the line that wrote them,
// and runs on a machine that trusts the Earthfile. This one runs where the input
// came from somebody else.
//
// Under the store, because that is what the operator gave this engine to fill,
// and beside the layers for the same reason the rate is (E351): a cache belongs
// to the machine that holds the layers it was built against.
func dockerCacheDir(store, name string) (string, error) {
	err := checkCacheName(name)
	if err != nil {
		return "", err
	}

	return filepath.Join(store, "docker-cache", name), nil
}

// checkCacheName is whether this engine will make a directory of what a peer
// sent.
//
// Split from the path so the **boundary** can use it: a worker refuses an
// assignment naming a cache it would not make a directory of, at the point it
// accepts the assignment, rather than when something later composes a path
// (A5, E360).
func checkCacheName(name string) error {
	if name == "" {
		return errors.New("a shared daemon cache needs a name")
	}

	if len(name) > cacheNameLimit {
		return fmt.Errorf("a daemon cache name of %d characters, and %d is"+
			" the most this engine will make a directory of", len(name),
			cacheNameLimit)
	}

	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-':
		default:
			return fmt.Errorf("%q is not allowed in a daemon cache name,"+
				" which becomes a directory under this machine's store", r)
		}
	}

	// `.` and `..` pass the loop above - every character in them is allowed -
	// and both name a directory rather than a cache. Checked after rather than
	// woven into it, because a rule about a *whole* name is not a rule about its
	// characters and merging the two is how one of them gets edited away.
	if name == "." || name == ".." {
		return fmt.Errorf("%q names a directory rather than a cache", name)
	}

	return nil
}
