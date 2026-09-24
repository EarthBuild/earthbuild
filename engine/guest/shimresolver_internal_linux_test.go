package guest

import (
	"os"
	"path/filepath"
	"testing"
)

// The resolver survives the private /run.
//
// `prepareShim` mounts a tmpfs over `/run` so a daemon that dies badly leaves
// nothing behind. On a machine using systemd-resolved, `/etc/resolv.conf` is a
// symlink into `/run/systemd/resolve/`, so the mount hides the file it points
// at - and the daemon, finding no nameserver, falls back to localhost:
//
//	Get "https://registry-1.docker.io/v2/": dial tcp: lookup
//	  registry-1.docker.io on [::1]:53: read: connection refused
//
// which is what every `WITH DOCKER` that pulls reported on a GitHub runner
// (E777).
func TestTheResolverSurvivesThePrivateRun(t *testing.T) {
	t.Parallel()

	// The paths the mount does and does not hide, which is the whole decision.
	for at, want := range map[string]bool{
		"/run/systemd/resolve/stub-resolv.conf": true,
		"/run/resolvconf/resolv.conf":           true,
		"/etc/resolv.conf":                      false,
		"/nix/store/abc-etc-resolv.conf":        false,
		"":                                      false,
	} {
		if got := hiddenByPrivateRun(at); got != want {
			t.Errorf("hiddenByPrivateRun(%q) = %v, want %v", at, got, want)
		}
	}

	// And the writing, which has to make the directory the tmpfs took away.
	at := filepath.Join(t.TempDir(), "systemd", "resolve", "stub-resolv.conf")

	err := writeResolver(at, []byte("nameserver 9.9.9.9\n"))
	if err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(at)
	if err != nil {
		t.Fatalf("the resolver was not put back: %v", err)
	}

	if string(got) != "nameserver 9.9.9.9\n" {
		t.Errorf("the resolver reads %q", got)
	}
}

// A resolver that is not under /run is left alone.
//
// On a machine where `/etc/resolv.conf` is a real file, or points into
// `/nix/store` as it does on NixOS, the tmpfs hides nothing and writing a copy
// would be this engine inventing a resolver nobody asked it for.
func TestAResolverOutsideRunIsLeftAlone(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	at := filepath.Join(root, "etc", "resolv.conf")

	err := restoreResolver(resolver{at: at, data: []byte("nameserver 1.1.1.1\n")})
	if err != nil {
		t.Fatal(err)
	}

	if _, err = os.Stat(at); err == nil {
		t.Error("a resolver outside /run was written anyway")
	}
}
