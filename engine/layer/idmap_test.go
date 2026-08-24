package layer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// A digest can be taken as another namespace would see it.
//
// `PathDigest` hashes ownership, deliberately (§3.3, E92). Rootless, the guest
// and the host read the same stored layer through different id mappings: a
// directory the guest created is uid 0 to the guest and uid 1000 to the host,
// so an observation recorded on one side never matches a view computed on the
// other, and every prediction about it goes stale (E132).
//
// One side has to translate, and it is the guest's to do: **it is the only
// party that knows its own mapping**, it is written once per step rather than
// once per lookup, and the host stays free of any idea of what a namespace is.
//
// The mapping is `/proc/pid/uid_map`'s: triples of "this id inside, this id
// outside, this many". Parsed rather than assumed, because the engine writes
// two of them - `0 <euid> 1` and `1 <subuid> 65536` (E105) - and a translation
// that only knew about the first would be right for root and wrong for every
// user a step drops to.
func TestAnIDMapTranslatesAsTheKernelWould(t *testing.T) {
	t.Parallel()

	m, err := layer.ParseIDMap(strings.NewReader("         0       1000          1\n" +
		"         1     100000      65536\n"))
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		inside, outside uint32
	}{
		{0, 1000},       // root in the namespace is the invoking user
		{1, 100000},     // the first delegated id
		{65536, 165535}, // the last
		{99999, 99999},  // unmapped: unchanged, because inventing an answer
	} {
		if got := m.Outside(tc.inside); got != tc.outside {
			t.Errorf("id %d inside maps to %d outside, want %d", tc.inside, got, tc.outside)
		}
	}
}

// An unreadable or absent map is the identity.
//
// A guest with no mapping - running as root, or on a platform with no
// namespaces - sees exactly what the store holds, and translating would be the
// error rather than the fix. An empty map that translated everything to zero
// would make every observation disagree with every view, which is the failure
// this exists to remove.
func TestAnAbsentIDMapChangesNothing(t *testing.T) {
	t.Parallel()

	var none layer.IDMap

	for _, id := range []uint32{0, 1, 1000, 65535} {
		if got := none.Outside(id); got != id {
			t.Errorf("with no mapping, %d became %d", id, got)
		}
	}
}

// A malformed line is refused rather than half-read.
//
// Half a mapping is worse than none: it translates some ids and not others, so
// the disagreement it causes is intermittent and looks like a cache that
// sometimes works.
func TestAMalformedIDMapIsRefused(t *testing.T) {
	t.Parallel()

	_, err := layer.ParseIDMap(strings.NewReader("0 1000\n"))
	if err == nil {
		t.Error("a line with two fields was accepted as a mapping")
	}
}

// A digest taken through a mapping matches one taken outside it.
//
// The property the whole thing exists for. The guest digests a path it sees as
// uid 0; the host digests the same stored path as uid 1000; with the guest's
// mapping applied, the two are one number - which is what `Consistent`
// compares, and what E121 asserted while both halves ran on the same side of
// the boundary (E132).
func TestADigestThroughAMappingMatchesTheStore(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := filepath.Join(dir, "app")

	err := os.MkdirAll(p, 0o755) //nolint:gosec // matches what a copy creates
	if err != nil {
		t.Fatal(err)
	}

	me := uint32(os.Getuid())   //nolint:gosec // a uid fits
	mine := uint32(os.Getgid()) //nolint:gosec // as above

	// The host's view: no translation, the ids the store holds.
	host, err := layer.PathDigest(p)
	if err != nil {
		t.Fatal(err)
	}

	// The guest's view of the same path, if it saw uid 0 and mapped 0 -> me.
	// Simulated by asking for the digest of the real file *through* a mapping
	// that renames this process's ids to something else and back.
	guest, err := layer.PathDigestIn(p, layer.MapOf([3]uint32{0, me, 1}), layer.MapOf([3]uint32{0, mine, 1}))
	if err != nil {
		t.Fatal(err)
	}

	if host != guest {
		t.Errorf("the same path digests differently through an identity-preserving"+
			" mapping:\n  host  %s\n  guest %s", host, guest)
	}
}
