package cacheshare

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/helper"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Two machines holding one cache write one map.
//
// **The map is a blob, so its bytes are its name.** A map written in map order
// would be a different blob every time it was written, so two workers holding
// identical caches would file two maps, dedupe neither, and ship both.
func TestOneCacheIsOneMap(t *testing.T) {
	t.Parallel()

	m := helper.Map{
		"zeta@v1":  ir.DigestOf([]byte("z")),
		"alpha@v1": ir.DigestOf([]byte("a")),
		"mid@v2":   ir.DigestOf([]byte("m")),
	}

	first := string(encodeMap(m))

	for range 8 {
		if got := string(encodeMap(m)); got != first {
			t.Fatalf("one map encoded two ways:\n%q\n%q", first, got)
		}
	}

	lines := strings.Split(strings.TrimSpace(first), "\n")
	if len(lines) != 3 {
		t.Fatalf("three units encoded as %d lines", len(lines))
	}

	for i, want := range []string{"alpha@v1", "mid@v2", "zeta@v1"} {
		if key, _, _ := strings.Cut(lines[i], "\t"); key != want {
			t.Errorf("line %d names %q, want %q - the map is not sorted", i, key, want)
		}
	}
}

// An empty cache has an empty map rather than a blob full of nothing.
func TestAnEmptyCacheEncodesToNothing(t *testing.T) {
	t.Parallel()

	if got := encodeMap(helper.Map{}); len(got) != 0 {
		t.Errorf("an empty map encoded to %q", got)
	}
}
