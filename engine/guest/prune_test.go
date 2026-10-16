package guest

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A guest collects its own store when asked.
//
// **Because `earth prune` cannot reach it.** Prune collects the host's store
// directory, which is right where the guest and the host share a filesystem
// and wrong for a microVM: there the store is a fixed-size image the guest has
// mounted and the host has never opened, so the command collected something
// else and reported success. The only remedy left was remaking the device,
// which discards every layer - a purge where a prune was wanted.
func TestAGuestCollectsItsStoreWhenAsked(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	layers := filepath.Join(root, "layers")

	for _, name := range []string{
		"1111111111111111111111111111111111111111111111111111111111111111",
		"2222222222222222222222222222222222222222222222222222222222222222",
	} {
		if err := os.MkdirAll(filepath.Join(layers, name), 0o750); err != nil {
			t.Fatal(err)
		}
	}

	host, guestSide := net.Pipe()

	s := &Server{LayerDir: root}
	go func() { _ = s.Serve(context.Background(), guestSide) }()

	c, err := dialWithin(host, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	// Keep nothing: the purge case, which is what a person asking to reclaim a
	// device-backed store means.
	said, err := c.Prune(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(said, "removed 2 layers") {
		t.Errorf("the guest did not report what it collected: %q", said)
	}

	left, err := os.ReadDir(layers)
	if err != nil {
		t.Fatal(err)
	}

	if len(left) != 0 {
		t.Errorf("%d layers left after a prune that was asked to keep nothing", len(left))
	}
}
