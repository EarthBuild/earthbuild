//go:build linux

package exec

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
	"testing"
)

// A full store device says what a full device needs, which is a bigger one.
//
// **The general advice is wrong here.** `store.FullHint` ends with "deleting the
// store reclaims all of it", which is the remedy on a host filesystem and not on
// a guest's: the device is a fixed-size image made once, so a build that filled
// it needs a larger one - and the number to change is in the environment rather
// than on the disk.
func TestAFullStoreDeviceSaysWhichSettingToChange(t *testing.T) {
	t.Parallel()

	got := vmFullHint(fmt.Errorf("capture: %w", syscall.ENOSPC), "/srv/store.img")

	for _, want := range []string{"/srv/store.img", EnvVMStore, "larger"} {
		if !strings.Contains(got, want) {
			t.Errorf("the hint does not mention %q:\n%s", want, got)
		}
	}
}

// Anything else is not a full device and gets no hint: advice about disk space
// attached to a permissions failure is the shape E491 exists to keep out.
func TestOnlyAFullDeviceGetsTheHint(t *testing.T) {
	t.Parallel()

	if got := vmFullHint(errors.New("something else"), "/srv/store.img"); got != "" {
		t.Errorf("an unrelated failure was given disk advice: %s", got)
	}

	if got := vmFullHint(syscall.ENOSPC, ""); got != "" {
		t.Errorf("a sandbox with no store device was given advice about one: %s", got)
	}
}

// A full store reported by the guest is recognised, though it arrives as text.
//
// **The hint could never fire for the case it was written for.** A microVM's
// ENOSPC always happens in the guest: the store is a device only the guest has
// mounted, and the host learns about it through the protocol, which carries a
// message and not a wrapped `syscall.Errno`. `errors.Is(err, syscall.ENOSPC)`
// is therefore false for every failure this hint exists to explain, and the
// mechanism looked present while explaining nothing.
//
// Observed: a build died with "write /store/layers/....partial/usr/libexec/gcc/
// .../cc1: no space left on device" and no hint at all, on a guest store of
// 44,015 layers with 5G free - which is exactly the situation the paragraph
// about remaking the image was written to describe.
func TestAFullStoreIsRecognisedWhenTheGuestReportsItAsText(t *testing.T) {
	t.Parallel()

	// The shape the protocol delivers: a message, no Errno underneath.
	fromGuest := errors.New(
		"copy /var/lib/earthbuild/scratch/mounts/h-42/upper/usr/libexec/gcc/cc1: " +
			"write /store/layers/.8cde.partial-256956288/usr/libexec/gcc/cc1: " +
			"no space left on device")

	got := vmFullHint(fromGuest, "/srv/store.img")
	if got == "" {
		t.Fatal("a guest reporting a full store got no hint, which is the only way it can report one")
	}

	if !strings.Contains(got, "/srv/store.img") {
		t.Errorf("the hint does not name the image to remake: %s", got)
	}
}

// Text that merely mentions space is not a full store.
//
// The reason the typed check was right to exist: a step whose own output says
// "no space left on device" - a test asserting that message, a log being
// echoed - is not this engine's store filling up, and advice about remaking a
// store device attached to somebody's passing test is worse than none.
func TestASentenceAboutSpaceIsNotAFullStore(t *testing.T) {
	t.Parallel()

	notOurs := errors.New(`exit status 1: echo "no space left on device"`)
	if got := vmFullHint(notOurs, "/srv/store.img"); got != "" {
		t.Errorf("a step quoting the message was treated as a full store: %s", got)
	}
}
