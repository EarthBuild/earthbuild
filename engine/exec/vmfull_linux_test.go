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
