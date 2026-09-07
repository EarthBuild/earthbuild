package image_test

import (
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// TestThePinWindowIsReadFromTheEnvironment.
//
// Off unless asked, because a window is a period during which a moved tag is
// not noticed - which changes which image a build gets, and nobody should get
// that without having said so. Nonsense reads as off rather than as an error: a
// mistyped duration must not silently buy a staleness window.
//
//nolint:paralleltest // t.Setenv, which the runtime refuses in a parallel test
func TestThePinWindowIsReadFromTheEnvironment(t *testing.T) {
	for _, c := range []struct {
		set  string
		want time.Duration
	}{
		{"", 0},
		{"0", 0},
		{"5m", 5 * time.Minute},
		{"90s", 90 * time.Second},
		{"  2m  ", 2 * time.Minute},
		{"nonsense", 0},
		// A negative window is off, not a window into the past.
		{"-5m", 0},
	} {
		t.Setenv(image.EnvPinTTL, c.set)

		if got := image.PinTTLFromEnv(); got != c.want {
			t.Errorf("%q gave %v, want %v", c.set, got, c.want)
		}
	}
}
