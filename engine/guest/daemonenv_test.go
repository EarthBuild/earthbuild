package guest

import (
	"slices"
	"testing"
)

// The daemon does not inherit a runtime directory belonging to the machine.
//
// `WITH DOCKER` starts a dockerd beside the step, and it inherited the whole
// environment of whatever invoked the engine. On a GitHub runner that includes
// `XDG_RUNTIME_DIR=/run/user/1001`, which exists on the runner and nowhere in a
// step - so `docker run -t` asked runc for a console socket, runc put it under
// that directory, and the daemon answered:
//
//	failed to create OCI runtime console socket:
//	  stat /run/user/1001: no such file or directory
//
// The step saw exit 127 and no output of its own, which is why it read as a
// missing `docker` binary for three CI rounds. It reproduces anywhere by
// exporting that one variable, and nowhere without it (E963).
//
// Everything else is kept. A proxy setting is how a build reaches a registry
// from a corporate network, and a daemon started without one fails in a way this
// engine cannot explain - so the rule is a named variable and not a policy of
// starting clean.
func TestTheDaemonDropsTheInvokersRuntimeDirectory(t *testing.T) {
	t.Parallel()

	got := daemonEnv([]string{
		"PATH=/usr/bin",
		"XDG_RUNTIME_DIR=/run/user/1001",
		"HOME=/root",
		"HTTPS_PROXY=http://proxy:3128",
	})

	want := []string{"PATH=/usr/bin", "HOME=/root", "HTTPS_PROXY=http://proxy:3128"}

	if !slices.Equal(got, want) {
		t.Errorf("the daemon is given %q, want %q", got, want)
	}

	// An environment without it is handed over unchanged, so the common case
	// costs nothing and cannot reorder anything.
	plain := []string{"PATH=/usr/bin", "HOME=/root"}
	if !slices.Equal(daemonEnv(plain), plain) {
		t.Errorf("an environment with no runtime directory was changed: %q", daemonEnv(plain))
	}
}

// The daemon joins the step's network namespace, so a published port is on the
// localhost the step will look at.
//
// `WITH DOCKER --compose` publishes `127.0.0.1:5432:5432` and the step then
// waits for `localhost:5432`. The daemon runs *beside* the step (daemonPaths),
// so when the step has a namespace of its own the two loopbacks are different
// interfaces and the port is on the wrong one. The corpus writes that wait
// unbounded, so the step did not fail - it span for the six hours GitHub allows
// a job (tests/with-docker-compose, E967).
//
// Only when there is one: where `ip netns` is unavailable the guest gives the
// step no namespace of its own and the daemon must stay where it is.
func TestTheDaemonJoinsTheStepsNetworkNamespace(t *testing.T) {
	t.Parallel()

	got := daemonEnvIn([]string{"PATH=/usr/bin"}, "/var/run/netns/earth-s1")

	want := []string{"PATH=/usr/bin", EnvStepNetNS + "=/var/run/netns/earth-s1"}
	if !slices.Equal(got, want) {
		t.Errorf("the daemon is given %q, want %q", got, want)
	}

	plain := []string{"PATH=/usr/bin"}
	if !slices.Equal(daemonEnvIn(plain, ""), plain) {
		t.Errorf("a step with no namespace changed the daemon's environment: %q",
			daemonEnvIn(plain, ""))
	}
}

// A daemon in the step's namespace needs a resolver reachable from inside it.
//
// `savedResolver` follows `/etc/resolv.conf`, which on a machine running
// systemd-resolved - every GitHub runner - is the *stub*: `nameserver
// 127.0.0.53`. That answers in the guest's namespace, where systemd-resolved is
// listening, and nowhere else. So a daemon moved into the step's namespace kept
// a resolver pointing at a loopback with nothing behind it, and every pull died
// as `dial tcp: lookup registry-1.docker.io` - which reads as a network outage
// and is a nameserver (E967).
//
// The reachable ones are what `hostNameservers` already finds for the step, and
// `resolvMount` already writes there. This is the same answer for the daemon.
//
// **Only when it has a namespace of its own.** A daemon sharing the guest's can
// reach whatever the guest reaches, loopback stub included, so rewriting it
// there would replace something that works with something else that does - the
// rule resolvMount states for steps, and the same one here.
func TestTheDaemonGetsAResolverItCanReach(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, netns string
		ns          []string
		want        string
	}{
		{"own namespace, reachable nameservers", "/var/run/netns/earth-s1",
			[]string{"10.0.0.2", "10.0.0.3"}, "nameserver 10.0.0.2\nnameserver 10.0.0.3\n"},
		{"sharing the guest's namespace", "", []string{"10.0.0.2"}, ""},
		{"own namespace, nothing reachable", "/var/run/netns/earth-s1", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := string(daemonResolver(tc.netns, tc.ns)); got != tc.want {
				t.Errorf("the daemon is given %q, want %q", got, tc.want)
			}
		})
	}
}

// A daemon in the step's own namespace manages its own network.
//
// `--iptables=false --bridge=none` was measured, and its recorded reason is
// conditional: *"a bridge wants netlink permissions a user namespace does not
// have, and a step's network is the sandbox's"*. Both halves have since stopped
// holding - the Native suite runs as root, so no user namespace is created, and
// a step now has a network namespace of its own.
//
// The cost of keeping them is that the daemon cannot publish a port at all:
// without iptables there are no DNAT rules and only the userland proxy is left,
// so `WITH DOCKER --compose` publishing `127.0.0.1:5432` produced nothing for the
// step to connect to (E967).
//
// Left exactly as it was otherwise. In the guest's shared namespace a daemon
// managing iptables would be editing the *machine's* firewall, which is the
// thing those flags were right to prevent.
func TestADaemonWithItsOwnNetworkManagesIt(t *testing.T) {
	t.Parallel()

	own := daemonArgs("/r", "/s", true)
	for _, unwanted := range []string{"--iptables=false", "--bridge=none"} {
		if slices.Contains(own, unwanted) {
			t.Errorf("a daemon with its own network is still given %s, so it"+
				" cannot publish a port", unwanted)
		}
	}

	shared := daemonArgs("/r", "/s", false)
	for _, wanted := range []string{"--iptables=false", "--bridge=none"} {
		if !slices.Contains(shared, wanted) {
			t.Errorf("a daemon sharing the guest's network lost %s, so it may"+
				" edit the machine's firewall", wanted)
		}
	}

	// The rest is identical either way: only the two network flags are in
	// question, and a silent change to a data root or a pidfile would be a
	// different bug wearing this one's clothes.
	for _, flag := range []string{"--group=", "--storage-driver=vfs", "--host=unix:///s"} {
		if !slices.Contains(own, flag) || !slices.Contains(shared, flag) {
			t.Errorf("%s did not survive in both shapes", flag)
		}
	}
}
