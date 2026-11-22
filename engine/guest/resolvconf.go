package guest

import (
	"net"
	"os"
	"strings"
)

// systemdResolvConf holds the real upstream servers where /etc/resolv.conf holds
// only the stub.
//
// systemd-resolved writes both: `/etc/resolv.conf` points at its own listener on
// 127.0.0.53, and this one names the servers that listener forwards to. Docker
// reaches for it in the same situation and for the same reason - a container
// with its own namespace cannot use the stub.
const systemdResolvConf = "/run/systemd/resolve/resolv.conf"

// ReachableNameservers is the nameservers in a resolv.conf that a step in its
// own network namespace could actually reach.
//
// Exported because a microVM asks the same question from outside: the host
// picks one resolver to hand its guest on the kernel command line, and the rule
// for which are usable is this one. Two copies of it would be two answers to
// "is 127.0.0.53 any use to something over there".
//
// Loopback is dropped, and that is the entire point: 127.0.0.53 names a
// listener in the *guest's* namespace, and a step given one of its own has an
// empty loopback there. Keeping it produces a file that looks right, resolves
// nothing, and fails as `apk add ... exited 1, and printed nothing` (E931).
//
// Only `nameserver` lines are read. `search` and `options` describe how to ask
// rather than whom, and carrying them would mean deciding what a step's search
// domains should be - which is the image's business and not this engine's.
func ReachableNameservers(conf string) []string {
	var out []string

	for line := range strings.SplitSeq(conf, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}

		ip := net.ParseIP(fields[1])
		if ip == nil || ip.IsLoopback() {
			continue
		}

		out = append(out, fields[1])
	}

	return out
}

// hostNameservers is what this machine can resolve with, from the guest's view.
//
// The systemd file first, because where both exist the other one is the stub.
// Where neither yields anything reachable the answer is none, and the caller
// decides what that means - which is a step without a network of its own rather
// than a step with a network and no way to name anything.
func hostNameservers() []string {
	for _, at := range []string{systemdResolvConf, "/etc/resolv.conf"} {
		b, err := os.ReadFile(at) //nolint:gosec // two fixed paths
		if err != nil {
			continue
		}

		if ns := ReachableNameservers(string(b)); len(ns) > 0 {
			return ns
		}
	}

	return nil
}

// resolvMount is the `/etc/resolv.conf` a step with its own network gets.
//
// Carries its contents rather than an id, the same shape `hostsMount` uses and
// for the same reason: there is nothing in any store to point at.
//
// Only for a step that has its own namespace. A step sharing the guest's can
// reach whatever the guest reaches, including a loopback stub, so rewriting the
// file there would replace something that works with something else that does.
func resolvMount(nameservers []string) []Mount {
	if len(nameservers) == 0 {
		return nil
	}

	var b strings.Builder

	for _, ns := range nameservers {
		b.WriteString("nameserver " + ns + "\n")
	}

	// **A resolver is not a secret, and the mode is the difference.** Carried as
	// one because a secret mount is the shape that holds its own contents - the
	// alternative is a file in a store, and there is nothing in any store to
	// point at - but a secret is staged `0400` for the excellent reason that a
	// credential is, and a resolver at `0400` is a step that cannot resolve a
	// name unless it runs as root.
	//
	// Which most steps do and many do not. `apt` drops to the `_apt` user for
	// network access and reports `Temporary failure resolving`; so does any
	// image with a `USER` in it. The namespace backend binds the host's own
	// file at `0444` and never had this, so it read as one sandbox having no
	// network rather than as one file having no mode.
	return []Mount{{Target: "/etc/resolv.conf", Secret: b.String(), Mode: 0o644}}
}

// daemonResolver is the `/etc/resolv.conf` a daemon in the step's own network
// namespace gets, or nothing when it needs none.
//
// The daemon runs beside the step and is not chrooted, so it reads the *guest's*
// resolver. On a machine running systemd-resolved that is the stub -
// `nameserver 127.0.0.53` - which answers in the guest's namespace, where
// systemd-resolved is listening, and nowhere else. Moved into the step's
// namespace with that file, the daemon resolves nothing (E967).
//
// The same nameservers `resolvMount` gives the step, for the same reason, and
// under the same condition: only where the namespace is the step's own. A daemon
// sharing the guest's reaches whatever the guest reaches, loopback stub
// included, and rewriting it there would replace something that works.
func daemonResolver(netns string, nameservers []string) []byte {
	if netns == "" || len(nameservers) == 0 {
		return nil
	}

	var b strings.Builder

	for _, ns := range nameservers {
		b.WriteString("nameserver " + ns + "\n")
	}

	return []byte(b.String())
}
