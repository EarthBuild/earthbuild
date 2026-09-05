//go:build linux

package guest

import (
	"fmt"
	"strconv"
)

// EnvStepNet selects how a step reaches the network. See docs/native/settings.md.
//
// `shared` is the default and is what every build has had until now: steps run
// in the guest's own network namespace. `private` gives each step a namespace of
// its own with a way out, which is the fix for E923 and is behind a setting
// until it has run against the corpus - the same shape `EARTH_STEP_SHIM` used
// for the shim, and for the same reason.
const EnvStepNet = "EARTH_STEP_NET"

// The two values EnvStepNet takes.
const (
	NetShared  = "shared"
	NetPrivate = "private"
)

// stepNetSpace is where a step's networks are addressed from.
//
// **Not 172.30.0.0/16**, which is buildkit's - `buildkitd/cni-conf.json.template`
// hands its steps addresses out of it. Both engines run on one machine while the
// comparison this branch exists for is being made, so taking that range would
// collide with the thing being replaced, on exactly the machine where it matters.
const stepNetSpace = 10<<24 | 201<<16 // 10.201.0.0/16

// netPlan is the addressing for one step's network namespace.
//
// A /30 per step: four addresses, of which the usable pair is `.1` for the
// guest's end of the veth and `.2` for the step's. Wasteful in the abstract and
// exactly right here - a veth is a point-to-point link with two ends, and a
// larger block would only make the arithmetic longer.
type netPlan struct {
	// Name is the namespace as `ip netns` names it, which is also the file
	// under /var/run/netns that holds it open. A namespace with no process in
	// it and no bind mount is collected, so this file is what makes the
	// namespace outlive the command that created it.
	Name string
	// HostLink and StepLink are the two ends of the veth pair.
	HostLink string
	StepLink string
	// HostAddr is the step's default gateway; StepAddr is the step itself.
	HostAddr string
	StepAddr string
	// Subnet is the /30 both ends sit in, and what NAT is written against.
	Subnet string
}

// stepNetPlan derives one step's addressing from a number.
//
// Pure, so the arithmetic can be tested without a kernel: the interesting
// failures here are two steps given one address, and a name a byte too long for
// `IFNAMSIZ`, and neither needs a namespace to demonstrate.
//
// 16384 blocks, wrapping. A build with more concurrent steps than that would
// reuse an address while the first holder still had it, which is worth knowing
// rather than worth guarding: the guest's own concurrency is bounded far below
// it, and a guard would be untested code standing in front of an impossibility.
func stepNetPlan(i int) netPlan {
	block := i & 0x3fff

	base := stepNetSpace | block<<2
	quad := func(n int) string {
		return fmt.Sprintf("%d.%d.%d.%d", n>>24&0xff, n>>16&0xff, n>>8&0xff, n&0xff)
	}

	id := strconv.Itoa(i)

	return netPlan{
		// Short because `IFNAMSIZ` is 15 and the kernel refuses a longer name
		// rather than truncating it. `eh`/`es` leaves thirteen digits, which is
		// eight more than the counter can reach.
		Name:     "earth-s" + id,
		HostLink: "eh" + id,
		StepLink: "es" + id,
		HostAddr: quad(base + 1),
		StepAddr: quad(base + 2),
		Subnet:   quad(base) + "/30",
	}
}

// stepNetUp is what has to run for a step to have a network of its own.
//
// Returned as data rather than executed here so the sequence can be asserted:
// the order matters - the peer moves into the namespace before it is addressed,
// and the route needs the link up - and an ordering mistake shows as a build
// that has no network rather than as an error anybody reads.
//
// `ip` and `iptables` rather than netlink. The guest already reaches for
// external binaries this way - `dockerd` and Apple's `container` are both found
// with `LookPath` - and the alternative is three dependencies, one of which
// shells out to `iptables` anyway.
func stepNetUp(p netPlan) [][]string {
	return [][]string{
		{"ip", "netns", "add", p.Name},
		{"ip", "link", "add", p.HostLink, "type", "veth", "peer", "name", p.StepLink},
		{"ip", "link", "set", p.StepLink, "netns", p.Name},
		{"ip", "addr", "add", p.HostAddr + "/30", "dev", p.HostLink},
		{"ip", "link", "set", p.HostLink, "up"},
		{"ip", "-n", p.Name, "addr", "add", p.StepAddr + "/30", "dev", p.StepLink},
		{"ip", "-n", p.Name, "link", "set", p.StepLink, "up"},

		// Loopback is down in a fresh namespace, and a step that talks to
		// itself on 127.0.0.1 - which is most of what a nested daemon does -
		// fails in a way that reads as the daemon's fault.
		{"ip", "-n", p.Name, "link", "set", "lo", "up"},
		{"ip", "-n", p.Name, "route", "add", "default", "via", p.HostAddr},

		// The way out. Without this the namespace is isolation and nothing
		// else, which is the option the current comment in `isolate_linux.go`
		// weighs against sharing - and rejects, correctly, because a build that
		// cannot fetch a dependency is no use.
		{"iptables", "-t", "nat", "-A", "POSTROUTING", "-s", p.Subnet, "-j", "MASQUERADE"},

		// **And permission to be forwarded at all, which MASQUERADE is not.**
		// That rule rewrites a packet's source; whether it is forwarded is
		// filter/FORWARD's decision, and Docker sets that chain's policy to
		// DROP on every machine it is installed on. A GitHub runner has Docker,
		// so a step got an address, a route, a resolver and no connectivity -
		// `apk add ... exited 8, and printed nothing` - while the same setup
		// worked in a container whose policy was ACCEPT (E931b).
		//
		// Both directions, because the reply is a separate packet and the chain
		// sees it too. Inserted at the head rather than appended: Docker's own
		// rules are in this chain, and a rule after a DROP is a rule that never
		// runs.
		{"iptables", "-I", "FORWARD", "1", "-s", p.Subnet, "-j", "ACCEPT"},
		{"iptables", "-I", "FORWARD", "1", "-d", p.Subnet, "-j", "ACCEPT"},
	}
}

// stepNetDown undoes it, and is expected to be run best-effort.
//
// Deleting the namespace takes the step's end of the veth with it, and a veth
// dies with its peer, so the guest's end needs no separate removal. The NAT rule
// does: it lives in the guest's tables, which outlive every namespace here.
func stepNetDown(p netPlan) [][]string {
	return [][]string{
		{"iptables", "-t", "nat", "-D", "POSTROUTING", "-s", p.Subnet, "-j", "MASQUERADE"},
		{"iptables", "-D", "FORWARD", "-s", p.Subnet, "-j", "ACCEPT"},
		{"iptables", "-D", "FORWARD", "-d", p.Subnet, "-j", "ACCEPT"},
		{"ip", "netns", "del", p.Name},
	}
}
