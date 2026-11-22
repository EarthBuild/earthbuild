package vmboot_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/cmd/earth-vmboot/vmboot"
)

// What the host encodes, the guest reads back, values and all.
//
// **A setting that does not cross is a setting that is silently ignored.** The
// guest reads fifteen of them - tracing, the step shim, the idle timeout,
// hash-on-unpack - and a microVM gave it none, because a guest's environment
// comes from its kernel and not from the process that started the machine. The
// symptom is not a failure: it is an A/B whose two arms are the same arm.
func TestSettingsCrossIntoTheGuest(t *testing.T) {
	t.Parallel()

	want := []string{"EARTH_TRACE_PIN=1", "EARTH_IDLE=5m", "EARTH_CLONE_LAYERS=0"}

	got := vmboot.ParseEnv("console=ttyS0 " + vmboot.EncodeEnv(want) + " panic=1")

	if !slices.Equal(got, want) {
		t.Errorf("the guest reads %v, the host sent %v", got, want)
	}
}

// **A value with a space survives**, which is why this is encoded rather than
// written out. The kernel command line is space-separated, so a setting written
// plainly would arrive as two parameters and the second would look like a typo.
func TestAValueWithSpacesSurvives(t *testing.T) {
	t.Parallel()

	want := []string{"EARTH_SOMETHING=two words", "EARTH_OTHER=a=b"}

	got := vmboot.ParseEnv(vmboot.EncodeEnv(want))
	if !slices.Equal(got, want) {
		t.Errorf("the guest reads %v, the host sent %v", got, want)
	}
}

// Nothing to carry is no parameter at all, rather than an empty one.
func TestNoSettingsIsNoParameter(t *testing.T) {
	t.Parallel()

	if got := vmboot.EncodeEnv(nil); got != "" {
		t.Errorf("an empty set was encoded as %q", got)
	}

	if got := vmboot.ParseEnv("console=ttyS0"); len(got) != 0 {
		t.Errorf("a command line with no settings yielded %v", got)
	}
}

// Rubbish is nothing, not a panic: this runs in PID 1 of a guest that has
// already booted, and a malformed parameter must leave a machine with no
// settings rather than one that will not start.
func TestRubbishIsNoSettings(t *testing.T) {
	t.Parallel()

	if got := vmboot.ParseEnv("earth.env=not-base64!!"); len(got) != 0 {
		t.Errorf("rubbish decoded to %v", got)
	}
}

// The command line is a fixed-size buffer, so what will not fit is refused here
// rather than truncated by the kernel into something that parses.
func TestTooMuchIsRefused(t *testing.T) {
	t.Parallel()

	huge := []string{"EARTH_BIG=" + strings.Repeat("x", 8192)}

	if got := vmboot.EncodeEnv(huge); got != "" {
		t.Errorf("%d bytes of settings were encoded rather than refused", len(got))
	}
}
