//go:build linux

package trace

import "testing"

// A tracer with no listener treats every notification as outstanding.
//
// The zero value is how a synthetic sighting is fed in and how every test here
// builds one, so its descriptor is 0 - which is stdin, and not negative. A guard
// written as `fd < 0` misses it, asks the kernel about a notification stdin
// never issued, is told "gone", and silently discards every path: three tests
// failed at once, all of them saying the tracer had not attempted the path.
func TestATracerWithNoListenerDoubtsNothing(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		tr   *Tracer
	}{
		{name: "the zero value", tr: &Tracer{}},
		{name: "explicitly absent", tr: &Tracer{fd: -1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if !tc.tr.stillOutstanding(1) {
				t.Error("a notification was doubted by a tracer with nothing to ask")
			}
		})
	}
}
