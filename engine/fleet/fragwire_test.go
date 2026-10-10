package fleet_test

import (
	"bytes"
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"

	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// A fragment crosses the wire with the proof that it belongs.
//
// The last piece of lazy transfer's plumbing: a request that names paths, and an
// answer that carries the part of the layer they name **and** the manifest that
// authenticates it (E286).
//
// Both together on purpose. A protocol that fetched them separately would have a
// state in which a fragment is here and its proof is not, and the only safe
// thing to do in that state is throw the fragment away - so the two are one
// answer.
func TestAFragmentCrossesTheWireWithItsProof(t *testing.T) {
	t.Parallel()

	local := netip.AddrPortFrom(netip.IPv6Loopback(), 0)

	theirs := t.TempDir()
	id := aBiggerLayer(t, theirs)

	holder, err := iroh.Bind(t.Context(), iroh.WithBindAddr(local),
		iroh.WithALPNs(fleet.ALPNBlob))
	if err != nil {
		t.Skipf("no endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = holder.Shutdown(context.WithoutCancel(t.Context())) })

	go func() {
		_ = fleet.ServeBlobs(t.Context(), holder, &fleet.Layers{Root: theirs},
			func(err error) { t.Logf("holder: %v", err) })
	}()

	asker, err := iroh.Bind(t.Context(), iroh.WithBindAddr(local))
	if err != nil {
		t.Skipf("no second endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = asker.Shutdown(context.WithoutCancel(t.Context())) })

	src := &fleet.PeerSource{
		Endpoint: asker,
		Peer:     netaddr.NewEndpointAddr(holder.ID()).WithIP(holder.LocalAddr()),
		Label:    "the other machine",
	}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	want := []string{"etc/hosts"}

	manifest, packed, err := src.Fragment(ctx, id, want, true)
	if err != nil {
		t.Fatalf("asking for a fragment: %v", err)
	}

	mine := &fleet.Fragments{Root: t.TempDir()}

	err = mine.PutVerified(id, want, manifest, bytes.NewReader(packed))
	if err != nil {
		t.Fatalf("what crossed did not verify: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(mine.Dir(id, want), "etc", "hosts"))
	if err != nil {
		t.Fatalf("the path that was asked for is not there: %v", err)
	}

	if string(body) != "127.0.0.1 localhost\n" {
		t.Errorf("it arrived as %q", body)
	}

	// And far less than the layer: the point of the exercise.
	whole, err := (&fleet.Layers{Root: theirs}).Get(id)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("fragment %d bytes + manifest %d, whole layer %d",
		len(packed), len(manifest), len(whole))

	if len(packed) >= len(whole) {
		t.Errorf("the fragment is %d bytes and the layer is %d",
			len(packed), len(whole))
	}
}

// A holder that has no such layer says so, rather than inventing a fragment.
func TestAFragmentOfALayerNobodyHasIsRefused(t *testing.T) {
	t.Parallel()

	local := netip.AddrPortFrom(netip.IPv6Loopback(), 0)

	holder, err := iroh.Bind(t.Context(), iroh.WithBindAddr(local),
		iroh.WithALPNs(fleet.ALPNBlob))
	if err != nil {
		t.Skipf("no endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = holder.Shutdown(context.WithoutCancel(t.Context())) })

	go func() {
		_ = fleet.ServeBlobs(t.Context(), holder, &fleet.Layers{Root: t.TempDir()},
			func(error) {})
	}()

	asker, err := iroh.Bind(t.Context(), iroh.WithBindAddr(local))
	if err != nil {
		t.Skipf("no second endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = asker.Shutdown(context.WithoutCancel(t.Context())) })

	src := &fleet.PeerSource{
		Endpoint: asker,
		Peer:     netaddr.NewEndpointAddr(holder.ID()).WithIP(holder.LocalAddr()),
	}

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	_, _, err = src.Fragment(ctx, ir.NodeID{9}, []string{"etc/hosts"}, true)
	if err == nil {
		t.Error("a holder with nothing produced a fragment")
	}
}

// aBiggerLayer writes a layer with more in it than the one path being asked
// for, so that "a fragment is smaller than the layer" can mean something.
func aBiggerLayer(t *testing.T, root string) ir.NodeID {
	t.Helper()

	tmp := t.TempDir()

	must(t, os.MkdirAll(filepath.Join(tmp, "etc"), 0o750))
	must(t, os.MkdirAll(filepath.Join(tmp, "usr", "lib"), 0o750))
	must(t, os.WriteFile(filepath.Join(tmp, "etc", "hosts"),
		[]byte("127.0.0.1 localhost\n"), 0o600))

	// The rest of a base: the part nobody reads.
	for i := range 40 {
		must(t, os.WriteFile(
			filepath.Join(tmp, "usr", "lib", fmt.Sprintf("lib%d.so", i)),
			bytes.Repeat([]byte{byte(i)}, 4096), 0o600))
	}

	c, err := layer.Take(tmp)
	if err != nil {
		t.Fatal(err)
	}

	at := filepath.Join(root, "layers", c.ID.String())
	must(t, os.MkdirAll(filepath.Dir(at), 0o750))
	must(t, os.Rename(tmp, at))

	return c.ID
}

func must(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}
