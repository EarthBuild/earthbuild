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

// reachableNameservers is the nameservers in a resolv.conf that a step in its
// own network namespace could actually reach.
//
// Loopback is dropped, and that is the entire point: 127.0.0.53 names a
// listener in the *guest's* namespace, and a step given one of its own has an
// empty loopback there. Keeping it produces a file that looks right, resolves
// nothing, and fails as `apk add ... exited 1, and printed nothing` (E931).
//
// Only `nameserver` lines are read. `search` and `options` describe how to ask
// rather than whom, and carrying them would mean deciding what a step's search
// domains should be - which is the image's business and not this engine's.
func reachableNameservers(conf string) []string {
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

		if ns := reachableNameservers(string(b)); len(ns) > 0 {
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

	return []Mount{{Target: "/etc/resolv.conf", Secret: b.String()}}
}
