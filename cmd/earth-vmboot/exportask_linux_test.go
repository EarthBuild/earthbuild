//go:build linux

package main

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/cmd/earth-vmboot/vmboot"
)

// One channel carries two questions, and telling them apart is a prefix.
//
// A staged path is absolute, so it can never be mistaken for a layer request;
// the point of sharing the channel is that the export device is serialised
// already, and a second device would be a second allocator to get wrong.
func TestAnExportAskNamesEitherALayerOrAStagedPath(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		asked string
		layer string
		is    bool
	}{
		{
			name:  "a layer",
			asked: vmboot.LayerAsk + "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
			layer: "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
			is:    true,
		},
		{name: "a staged artifact", asked: "/store/staged/out.tar"},
		// The shape that would collide if the prefix were not excluded by an
		// absolute path: a directory that happens to be called "layer:".
		{name: "a path that reads like one", asked: "/layer:7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			id, is := layerAsked(tc.asked)
			if is != tc.is {
				t.Fatalf("%q read as layer=%v, wanted %v", tc.asked, is, tc.is)
			}

			if id != tc.layer {
				t.Errorf("%q gave id %q, wanted %q", tc.asked, id, tc.layer)
			}
		})
	}
}

// The count the guest reports is what the host reads, and no more.
//
// The bytes sit on a block device that is larger than any of them, so the host
// has nothing else to tell it where the blob stops: a count one short truncates
// a layer, and one too long appends whatever the previous export left behind.
// Both produce an image that is wrong rather than absent.
func TestWhatIsCountedIsWhatWasWritten(t *testing.T) {
	t.Parallel()

	var sink strings.Builder

	counted := &countedWrites{to: &sink}

	for _, part := range []string{"one", "", "three-ish", strings.Repeat("x", 4096)} {
		n, err := counted.Write([]byte(part))
		if err != nil {
			t.Fatalf("write: %v", err)
		}

		if n != len(part) {
			t.Errorf("reported %d bytes written for %d", n, len(part))
		}
	}

	if counted.n != int64(sink.Len()) {
		t.Errorf("counted %d, wrote %d", counted.n, sink.Len())
	}
}
