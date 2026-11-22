package exec

import "testing"

// What a WITH DOCKER step is actually given, in the decided polarity (E381).
//
// One function, three modes, and the interesting property is that the mode is
// decided by the block *and the surroundings together*: a bare block shares when
// there is something to share and starts its own when there is not - nesting by
// not nesting (E377).
func TestWhatAWithDockerStepIsGiven(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		isolate  bool
		cache    string
		inside   bool
		socket   bool
		allowed  bool
		ownIt    bool // it gets a daemon of its own
		mounted  int
		saysNone string
	}{
		{
			// The nesting case, and the default: there is a daemon around this
			// build, so the block uses it.
			name: "bare, inside a step with a daemon", inside: true, socket: true,
		},
		{
			// Nothing to share, so it starts its own. Same Earthfile, different
			// surroundings, and the author wrote no flag for either.
			name: "bare, nothing around it", ownIt: true, mounted: 1,
		},
		{
			// Asked for its own, and gets it whatever is around. Its storage is
			// mounted from a directory made for this step, which is what keeps
			// it out of the image and out of the next step (E398).
			name: "isolated, inside a step with a daemon", isolate: true,
			inside: true, socket: true,
			ownIt: true, mounted: 1,
		},
		{
			// Its own daemon, storage in the named cache: one mount, and the
			// only mode where a daemon's storage outlives the step.
			name: "a named cache", cache: "layers", ownIt: true, mounted: 1,
		},
		{
			// A socket on a machine this build is not containerised on. That is
			// the machine's own daemon, root on it (E145), so it is not shared -
			// and the block gets one of its own instead, which is strictly
			// better than E354's refusal and needs nobody's permission.
			name: "a socket, but the machine's own daemon", socket: true, ownIt: true,
			mounted: 1,
		},
		{
			// The same socket with the operator's say-so, which is what that
			// permission has always meant.
			name: "the machine's daemon, allowed", socket: true, allowed: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := dockerPlanFor(tc.isolate, tc.cache, "", tc.inside, tc.socket, tc.allowed)

			if got.Own != tc.ownIt {
				t.Errorf("own daemon = %v, want %v", got.Own, tc.ownIt)
			}

			if len(got.Mounts) != tc.mounted {
				t.Errorf("%d mount(s), want %d: %v", len(got.Mounts), tc.mounted, got.Mounts)
			}

			// A step is never given both: an inherited socket and a daemon of
			// its own are two answers to one question, and a step holding both
			// would use whichever the client looked at first.
			if got.Own && got.Inherit {
				t.Error("the step was given a daemon of its own and an inherited one")
			}
		})
	}
}

// A block that says nothing and finds nothing gets a daemon rather than a
// refusal.
//
// The E354 refusal - a bare block on Linux is refused because the only daemon
// available is the machine's - is what this replaces. There is a third answer
// now, and it is the right one: start one.
func TestABareBlockWithNothingAroundItIsNotRefused(t *testing.T) {
	t.Parallel()

	got := dockerPlanFor(false, "", "", false, false, false)

	if !got.Own {
		t.Error("nothing was arranged for a block that has no daemon to share")
	}

	// Mounted, and ephemeral: out of the image, and gone with the step (E398).
	if len(got.Mounts) != 1 || !got.Mounts[0].Ephemeral {
		t.Errorf("a block with no cache did not get a storage mount that is"+
			" thrown away, so its daemon's files would enter the image: %v",
			got.Mounts)
	}
}
