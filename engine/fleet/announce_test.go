package fleet

import (
	"net"
	"testing"
)

// The driver corrects an address a worker could not know.
//
// A worker binds to everything and reports `[::]:50277`, which is a name for
// *this machine's sockets* rather than a place - a peer handed it dials its own
// loopback. The worker cannot do better: a machine with several interfaces has
// no way to know which one the driver can see, and behind NAT the answer is not
// one of them (E277).
//
// The driver can. It observed the connection, so it knows the address the worker
// appeared to come from, and it is the only party that does.
func TestTheDriverCorrectsAnAddressAWorkerCouldNotKnow(t *testing.T) {
	t.Parallel()

	seen := &net.UDPAddr{IP: net.IPv4(192, 168, 1, 137), Port: 41001}

	for _, tc := range []struct {
		name      string
		announced string
		want      string
	}{
		{
			name:      "bound to everything",
			announced: "abcd@[::]:50277",
			want:      "abcd@192.168.1.137:50277",
		},
		{
			name:      "bound to all v4",
			announced: "abcd@0.0.0.0:50277",
			want:      "abcd@192.168.1.137:50277",
		},
		{
			name: "a real address is left alone",
			// A worker that knows where it is, or was configured, is believed:
			// it may be reachable at an address the driver's view does not
			// mention, and the driver's view is a hint rather than the truth.
			announced: "abcd@10.0.0.5:50277",
			want:      "abcd@10.0.0.5:50277",
		},
	} {
		if got := correctHost(tc.announced, seen); got != tc.want {
			t.Errorf("%s: %q became %q, want %q",
				tc.name, tc.announced, got, tc.want)
		}
	}
}

// The port is the worker's, and only the host is corrected.
//
// The driver sees the *control* connection, whose port is an ephemeral one on a
// different socket from the one serving blobs. Taking the whole observed address
// would point every peer at a port that answers nothing.
//
// This is where the mechanism stops being general: a NAT that remaps ports
// breaks it, and that is what endpoint discovery and relays exist for. On a LAN,
// and on any network that only translates addresses, it is right.
func TestOnlyTheHostIsTakenFromTheConnection(t *testing.T) {
	t.Parallel()

	seen := &net.UDPAddr{IP: net.IPv4(192, 168, 1, 137), Port: 41001}

	got := correctHost("abcd@[::]:50277", seen)
	if got != "abcd@192.168.1.137:50277" {
		t.Errorf("got %q; the port must be the one the worker announced, which"+
			" is the socket serving blobs, not the one it dialled from", got)
	}
}

// Nonsense in, nonsense untouched.
//
// An address this cannot parse is passed through rather than mangled: it will
// fail to dial and be skipped, which is the same outcome as a corrected address
// that is wrong, and it does not turn a diagnosable failure into a confusing one.
func TestAnUnparseableAnnouncementIsLeftAlone(t *testing.T) {
	t.Parallel()

	seen := &net.UDPAddr{IP: net.IPv4(192, 168, 1, 137), Port: 41001}

	for _, s := range []string{"", "nonsense", "abcd@", "@1.2.3.4:5"} {
		if got := correctHost(s, seen); got != s {
			t.Errorf("%q became %q", s, got)
		}
	}
}

// With nothing observed, nothing changes.
func TestWithNoObservedAddressNothingIsCorrected(t *testing.T) {
	t.Parallel()

	const announced = "abcd@[::]:50277"

	if got := correctHost(announced, nil); got != announced {
		t.Errorf("%q became %q with no connection to learn from", announced, got)
	}
}

// A worker corrects the driver's own address, because it was told it.
//
// The mirror of the driver correcting a worker's. Neither machine guesses about
// itself: a worker's address is fixed by the driver, which observed the
// connection, and the driver's is fixed by the worker, which was told where to
// dial. A hint with an unspecified host can only have come from the machine that
// composed the hint.
func TestAWorkerCorrectsTheDriversOwnAddress(t *testing.T) {
	t.Parallel()

	at := AtDriver("192.168.1.91:41000")

	for _, tc := range []struct{ in, want string }{
		// The driver's own, bound to everything and served on another port.
		{"abcd@[::]:50277", "abcd@192.168.1.91:50277"},
		// A peer the driver already corrected. Left alone: it is somebody
		// else's address and the driver saw it.
		{"abcd@192.168.1.137:50277", "abcd@192.168.1.137:50277"},
		// Not something this understands.
		{"nonsense", "nonsense"},
	} {
		if got := at(tc.in); got != tc.want {
			t.Errorf("%q became %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A worker with no idea where its driver is corrects nothing.
func TestWithNoDriverAddressNothingIsCorrected(t *testing.T) {
	t.Parallel()

	const in = "abcd@[::]:50277"

	if got := AtDriver("")(in); got != in {
		t.Errorf("%q became %q", in, got)
	}
}

// The correction happens once, where every reply passes.
//
// It was in `note`, which the rendezvous uses to keep its own record - and
// `Delegating` keeps a *second* holder table, built from the raw reply, so the
// driver's own fetches used the address the worker announced and the placement
// used the corrected one. Two records of one fact, correcting only one of them
// (E279).
//
// **This does not assert that.** It calls `correctedForTest`, which looks the
// worker up and applies `correctHost` itself - so what it checks is that the
// correction works when performed by the helper, not that `Assign` performs it.
// Deleting the line in `Assign` leaves this test green, which a mutation sweep
// proved by deleting it and finding nothing noticed (E804).
//
// It is kept because the lookup it does exercise is real, and its claim is
// corrected rather than its body: the call site is guarded by
// `TestAReplysAddressIsCorrectedWhereEveryReplyPasses`, which reads the source,
// because on one machine an announced address and a seen address are the same
// string and the correction has nothing to do.
func TestAReplyIsCorrectedBeforeAnybodySeesIt(t *testing.T) {
	t.Parallel()

	r := &Rendezvous{}
	r.AddForTest()

	seen := &net.UDPAddr{IP: net.IPv4(192, 168, 1, 137), Port: 41001}

	r.mu.Lock()
	r.conns[0].from = seen
	r.mu.Unlock()

	got := r.correctedForTest(Reply{HeldAt: "abcd@[::]:50277"}, "fleet-0")
	if got != "abcd@192.168.1.137:50277" {
		t.Errorf("a reply carried %q past the one place that can fix it", got)
	}
}
