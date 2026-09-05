package guest_test

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/coretest"
	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/sim"
)

// pair runs a server over an in-memory connection and returns a dialled client.
//
// No VM, no overlayfs, no Apple runtime: the protocol is exercised end to end
// on any machine, and the real guest differs only in which materialiser sits
// behind the server.
func pair(t *testing.T, mat core.Materialiser) *guest.Client {
	t.Helper()

	return pairWith(t, &guest.Server{Mat: mat, Unconfined: true})
}

// pairWith runs a specific server, for tests that care about confinement.
func pairWith(t *testing.T, srv *guest.Server) *guest.Client {
	t.Helper()

	hostSide, guestSide := net.Pipe()
	go func() { _ = srv.Serve(context.Background(), guestSide) }()

	t.Cleanup(func() { hostSide.Close(); guestSide.Close() })

	c, err := guest.Dial(hostSide)
	if err != nil {
		t.Fatal(err)
	}

	return c
}

// TestRemoteMaterialiserConforms is the claim: a materialiser reached over the
// guest protocol behaves identically to a local one.
//
// It is the same suite the simulator and the overlayfs implementation pass, so
// "we moved it into a VM" is a deployment change rather than a semantic one.
func TestRemoteMaterialiserConforms(t *testing.T) {
	t.Parallel()

	coretest.MaterialiserSuite(t, func(t *testing.T) (core.Materialiser, func()) {
		t.Helper()

		return pair(t, &sim.Materialiser{}), func() {}
	})
}

// TestVersionMismatchIsCaughtAtTheHandshake: the guest ships inside a VM image
// and is updated on a different cadence from the host, so a skew is likely. It
// must surface on the first exchange, not midway through a build.
func TestVersionMismatchIsCaughtAtTheHandshake(t *testing.T) {
	t.Parallel()

	hostSide, guestSide := net.Pipe()
	defer hostSide.Close()

	srv := &guest.Server{Mat: &sim.Materialiser{}}
	go func() { _ = srv.Serve(context.Background(), guestSide) }()

	// Speak a version the guest does not.
	bad := guest.Request{Kind: guest.KindHello, Version: guest.Version + 99}

	c := guest.NewTestConn(hostSide)
	err := c.Send(bad)
	if err != nil {
		t.Fatal(err)
	}

	var resp guest.Response
	err = c.Recv(&resp)
	if err != nil {
		t.Fatal(err)
	}

	if resp.Err == "" {
		t.Fatal("a version mismatch was accepted")
	}

	if !strings.Contains(resp.Err, "version mismatch") {
		t.Errorf("unhelpful mismatch error: %q", resp.Err)
	}
}

// TestMalformedLayerIdsAreRefused: a wire is attacker-reachable, and a
// half-parsed layer id would name the wrong layer rather than failing.
func TestMalformedLayerIdsAreRefused(t *testing.T) {
	t.Parallel()

	hostSide, guestSide := net.Pipe()
	defer hostSide.Close()

	srv := &guest.Server{Mat: &sim.Materialiser{}}
	go func() { _ = srv.Serve(context.Background(), guestSide) }()

	c := guest.NewTestConn(hostSide)
	err := c.Send(guest.Request{Kind: guest.KindHello, Version: guest.Version})
	if err != nil {
		t.Fatal(err)
	}

	var hello guest.Response
	err = c.Recv(&hello)
	if err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{"", "zz", strings.Repeat("g", 64), strings.Repeat("a", 63)} {
		err := c.Send(guest.Request{Kind: guest.KindMaterialise, Stack: []string{bad}})
		if err != nil {
			t.Fatal(err)
		}

		var resp guest.Response
		err = c.Recv(&resp)
		if err != nil {
			t.Fatal(err)
		}

		if resp.Err == "" {
			t.Errorf("malformed layer id %q was accepted", bad)
		}
	}
}

// TestReleasingAnUnknownHandleIsNotAnError: cleanup runs more than once, and a
// second release must not fail in a way that masks the first error.
func TestReleasingAnUnknownHandleIsNotAnError(t *testing.T) {
	t.Parallel()

	c := pair(t, &sim.Materialiser{})

	h, err := c.Materialise(context.Background(), []ir.NodeID{{1}})
	if err != nil {
		t.Fatal(err)
	}

	err = h.Release()
	if err != nil {
		t.Fatal(err)
	}

	err = h.Release()
	if err != nil {
		t.Errorf("second release failed: %v", err)
	}
}
