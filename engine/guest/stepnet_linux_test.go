//go:build linux

package guest

import (
	"strings"
	"testing"
)

// Two steps never get the same address, which is the whole point of the thing.
//
// Parallel steps share the guest's network namespace, so two of them binding one
// fixed port collide: an inner buildkitd wants 8371 and 8372, and the second
// dies with `bind: address already in use` (E923). A namespace each fixes it
// only if the addresses differ, so this is the property worth asserting.
func TestEachStepNetGetsItsOwnAddresses(t *testing.T) {
	t.Parallel()

	seen := map[string]int{}

	for i := range 64 {
		p := stepNetPlan(i)

		for _, addr := range []string{p.HostAddr, p.StepAddr, p.Subnet, p.Name, p.HostLink} {
			if prev, ok := seen[addr]; ok {
				t.Errorf("step %d reuses %q from step %d", i, addr, prev)
			}

			seen[addr] = i
		}
	}
}

// The two ends of a veth are in the same /30 and the step's route points at the
// guest's end, or the namespace is isolated in the sense that nothing works.
func TestAStepNetPlanIsRoutable(t *testing.T) {
	t.Parallel()

	p := stepNetPlan(0)

	if !strings.HasSuffix(p.Subnet, "/30") {
		t.Errorf("a veth pair wants a /30, got %q", p.Subnet)
	}

	hostOct := p.HostAddr[strings.LastIndex(p.HostAddr, ".")+1:]
	stepOct := p.StepAddr[strings.LastIndex(p.StepAddr, ".")+1:]

	if hostOct == stepOct {
		t.Errorf("both ends of the veth got %q", hostOct)
	}

	// A /30 holds four addresses: network, two hosts, broadcast. The usable
	// pair is .1 and .2 of the block, and anything else is not addressable.
	if hostOct != "1" || stepOct != "2" {
		t.Errorf("a /30's usable pair is .1 and .2, got .%s and .%s", hostOct, stepOct)
	}
}

// An interface name is at most IFNAMSIZ-1, and the kernel refuses a longer one
// rather than truncating it - so a name built from a counter must stay short at
// the counter's largest value, not merely at zero.
func TestStepNetNamesFitTheKernelsLimit(t *testing.T) {
	t.Parallel()

	const ifnamsiz = 15

	for _, i := range []int{0, 1, 999, 16383, 65535} {
		p := stepNetPlan(i)

		if len(p.HostLink) > ifnamsiz {
			t.Errorf("step %d: host link %q is %d characters, limit is %d",
				i, p.HostLink, len(p.HostLink), ifnamsiz)
		}

		if len(p.StepLink) > ifnamsiz {
			t.Errorf("step %d: step link %q is %d characters, limit is %d",
				i, p.StepLink, len(p.StepLink), ifnamsiz)
		}
	}
}
