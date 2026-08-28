package interp

import (
	"reflect"
	"testing"
)

// `EXPOSE 1234-1239` declares six ports, not one port called "1234-1239".
//
// Docker expands a range at parse time, so the image configuration carries an
// entry each. This engine appended the protocol and stored the range verbatim,
// which produced `{"1234-1239/tcp":{}}` - a value the daemon rejects outright:
// `docker load` of such an image fails with `invalid port '1234-1239': invalid
// syntax`, naming the port and not the image or the build (E842).
//
// The upper bound is inclusive, which is worth a case of its own: `1234-1235` is
// two ports, and the off-by-one that makes it one or three is the entire bug
// this replaces.
func TestAnExposedPortRangeBecomesOnePortEach(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		in   string
		want []string
	}{
		{"8080", []string{"8080/tcp"}},
		{"8080/tcp", []string{"8080/tcp"}},
		{"8080/udp", []string{"8080/udp"}},
		{"1234-1239", []string{
			"1234/tcp", "1235/tcp", "1236/tcp", "1237/tcp", "1238/tcp", "1239/tcp",
		}},
		{"1234-1235", []string{"1234/tcp", "1235/tcp"}},
		{"1234-1234", []string{"1234/tcp"}},
		{"1234-1236/udp", []string{"1234/udp", "1235/udp", "1236/udp"}},

		// **Left alone rather than rejected.** A range whose end precedes its
		// start, or whose halves are not numbers, is not this function's to
		// diagnose: the daemon's own message names the port and is better than
		// anything invented here. Passing it through unchanged keeps the
		// failure where it was, which is the behaviour every other malformed
		// EXPOSE already has.
		{"1239-1234", []string{"1239-1234/tcp"}},
		{"http-alt", []string{"http-alt/tcp"}},
		{"-1234", []string{"-1234/tcp"}},
		{"1234-", []string{"1234-/tcp"}},
	} {
		t.Run(c.in, func(t *testing.T) {
			t.Parallel()

			if got := expandPorts(c.in); !reflect.DeepEqual(got, c.want) {
				t.Errorf("expandPorts(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
