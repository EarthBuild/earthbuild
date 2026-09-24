package cli

import (
	"bytes"
	"strings"
	"testing"
)

// A build that could not give its steps their own networks says so.
//
// **Because it is the default now.** A step gets a network namespace of its own
// unless the guest has no `ip` or `iptables`, and on a guest that has neither
// every step runs the way it always did - sharing, and colliding on any fixed
// port two of them want. That is a correct build and a slower, flakier one, and
// the difference is invisible without this.
//
// E922 is the argument for saying it at all: the cgroup warning printed on every
// Native job for weeks and changed nobody's behaviour, including mine. A
// degradation nobody is told about is a degradation nobody fixes.
func TestASharedNetworkSaysSo(t *testing.T) {
	t.Parallel()

	t.Run("silent when every step got its own", func(t *testing.T) {
		t.Parallel()

		var b bytes.Buffer

		warnSharedNet(&b, "")

		if b.Len() != 0 {
			t.Errorf("a build whose steps were isolated printed a warning: %q", b.String())
		}
	})

	t.Run("names the reason and what it costs", func(t *testing.T) {
		t.Parallel()

		var b bytes.Buffer

		warnSharedNet(&b, "ip is not on the guest's PATH")

		out := b.String()

		if !strings.Contains(out, "ip is not on the guest's PATH") {
			t.Errorf("the warning does not carry the guest's reason: %q", out)
		}

		// What it costs, in the terms it is met in: two steps wanting one port.
		if !strings.Contains(out, "port") {
			t.Errorf("the warning does not say what breaks: %q", out)
		}

		// And how to be rid of the message where the sharing is deliberate.
		if !strings.Contains(out, "EARTH_STEP_NET") {
			t.Errorf("the warning does not name the setting: %q", out)
		}
	})

	t.Run("nil writer is not a crash", func(t *testing.T) {
		t.Parallel()

		warnSharedNet(nil, "something")
	})
}
