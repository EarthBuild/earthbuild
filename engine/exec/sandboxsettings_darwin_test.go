//go:build darwin

package exec

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/image"
)

// TestASettingTheGuestReadsAtStartNamesTheSandbox.
//
// **A machine is found and reused by name.** Anything the guest reads once, at
// start, is therefore fixed for the life of that machine - so a build asking for
// one arrangement and a build asking for the other have to be asking about two
// machines, or the second silently gets whatever the first said (E549).
//
// The failure this prevents is not a wrong build, it is a measurement that
// cannot be taken: an A/B where both arms reuse the first arm's VM reports that
// the switch does nothing, which is indistinguishable from a switch that does
// nothing.
func TestASettingTheGuestReadsAtStartNamesTheSandbox(t *testing.T) {
	plain := SandboxName("an-image", "/guest", "/store")

	for _, s := range []struct {
		name, env string
	}{
		{"pinning a traced step", guest.EnvTracePin},
		{"hashing on the way in", image.EnvNoKnownDigests},
	} {
		t.Run(s.name, func(t *testing.T) {
			t.Setenv(s.env, "1")

			if got := SandboxName("an-image", "/guest", "/store"); got == plain {
				t.Errorf("%s does not change the sandbox's name (%s),"+
					"\n  so a machine started without it answers a build that asked"+
					"\n  for it - and the two arms of any comparison are one arm twice",
					s.env, got)
			}
		})
	}
}
