package guest

import (
	"reflect"
	"testing"
)

// A step with a network of its own needs nameservers it can actually reach.
//
// **A loopback nameserver is the whole problem.** Ubuntu points
// `/etc/resolv.conf` at `127.0.0.53`, where systemd-resolved listens *in the
// guest's namespace*. A step given a namespace of its own inherits the file and
// not the listener, so `127.0.0.53` is its own empty loopback: every lookup
// fails, and the build reports `apk add --no-cache git exited 1, and printed
// nothing` (E931).
//
// Docker and buildkit both rewrite the file for a container with its own
// namespace, for exactly this reason. This is that rewrite.
func TestOnlyReachableNameserversSurvive(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name string
		in   string
		want []string
	}{
		{"ordinary", "nameserver 9.9.9.9\nnameserver 1.1.1.1\n", []string{"9.9.9.9", "1.1.1.1"}},

		// The case this exists for.
		{"systemd stub only", "nameserver 127.0.0.53\n", nil},
		{"loopback v6", "nameserver ::1\n", nil},
		{"mixed", "nameserver 127.0.0.53\nnameserver 9.9.9.9\n", []string{"9.9.9.9"}},

		// Anything that is not a nameserver line is not this function's
		// business: `search` and `options` describe how to ask, not whom, and a
		// step that keeps them asks the same questions of a reachable server.
		{"comments and options", "# a comment\noptions edns0\nnameserver 8.8.8.8\n", []string{"8.8.8.8"}},
		{"nothing at all", "", nil},
		{"malformed", "nameserver\n", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := reachableNameservers(c.in); !reflect.DeepEqual(got, c.want) {
				t.Errorf("reachableNameservers(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// The file a step gets names those servers and nothing else.
func TestTheResolvMountCarriesThem(t *testing.T) {
	t.Parallel()

	if m := resolvMount(nil); m != nil {
		t.Errorf("no reachable server should mean no mount, got %v", m)
	}

	m := resolvMount([]string{"9.9.9.9", "1.1.1.1"})

	if len(m) != 1 || m[0].Target != "/etc/resolv.conf" {
		t.Fatalf("expected one mount at /etc/resolv.conf, got %v", m)
	}

	want := "nameserver 9.9.9.9\nnameserver 1.1.1.1\n"
	if m[0].Secret != want {
		t.Errorf("resolv.conf is %q, want %q", m[0].Secret, want)
	}
}
