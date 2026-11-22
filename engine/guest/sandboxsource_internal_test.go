package guest

import "testing"

// A sandbox path naming the store is resolved against the store this guest has.
//
// The packed-image mount names its archive at the path the store has inside a
// VM, and it has to: that path is in the loading step's argv and therefore in
// its key, so a host path there would key one build differently on every
// machine. Where the sandbox is this machine's own filesystem the store is not
// at that path, and the fixed prefix has to be rebased onto wherever it is
// (E750).
func TestASandboxPathNamingTheStoreIsResolvedAgainstIt(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name, path, layers, want string
	}{{
		name:   "rebased where the store is elsewhere",
		path:   StorePath + "/images/abc.tar",
		layers: "/home/b/.cache/earthbuild",
		want:   "/home/b/.cache/earthbuild/images/abc.tar",
	}, {
		name:   "unchanged where the store is already there",
		path:   StorePath + "/images/abc.tar",
		layers: StorePath,
		want:   StorePath + "/images/abc.tar",
	}, {
		// The docker socket is the machine's own and names nothing in a store.
		name:   "a path outside the store is left alone",
		path:   "/var/run/docker.sock",
		layers: "/home/b/.cache/earthbuild",
		want:   "/var/run/docker.sock",
	}, {
		// Prefix matching on a string would take this one, and it is a
		// different directory.
		name:   "a sibling directory sharing the prefix is not the store",
		path:   StorePath + "-docker/images/abc.tar",
		layers: "/home/b/.cache/earthbuild",
		want:   StorePath + "-docker/images/abc.tar",
	}, {
		name:   "unchanged where this guest has no store",
		path:   StorePath + "/images/abc.tar",
		layers: "",
		want:   StorePath + "/images/abc.tar",
	}} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := sandboxSource(c.path, c.layers)
			if got != c.want {
				t.Errorf("sandboxSource(%q, %q) = %q, want %q", c.path, c.layers, got, c.want)
			}
		})
	}
}
