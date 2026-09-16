package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// TestImmutableExceptIsPartOfTheCachesIdentity.
//
// **Two machines must agree about what may be shared.** The flag says every
// file under the mount is written once, apart from the paths named - and if one
// Earthfile names `tmp/**` and another names nothing, the two are not describing
// the same cache. Sharing them anyway means one machine fetching a path the
// other never promised was stable, and the failure is a corrupt cache rather
// than a refused step.
//
// The same reasoning as E433, where a step run without a mount it declared
// writes into its layer what it would have discarded: the declaration has to be
// identical at both ends, so it is in the key.
func TestImmutableExceptIsPartOfTheCachesIdentity(t *testing.T) {
	t.Parallel()

	const src = "VERSION 0.8\nmain:\n    FROM alpine:3.22\n" +
		"    RUN --mount type=cache,target=/c,id=k%s echo hi\n"

	plain := plan(t, strings.ReplaceAll(src, "%s", ""))
	excepting := plan(t, strings.ReplaceAll(src, "%s", ",immutable-except=tmp/**"))
	other := plan(t, strings.ReplaceAll(src, "%s", ",immutable-except=lock"))

	if plain == excepting {
		t.Error("a cache declared immutable keys the same as one that is not," +
			" so a build would share what the author never said was shareable")
	}

	if excepting == other {
		t.Error("two different exclusion sets key the same, so two machines" +
			" can disagree about which paths are stable and share anyway")
	}
}

// TestPersistAndImmutableExceptRefuseEachOther.
//
// `--persist` copies the cache's contents into the image, which makes them part
// of what the target produces. A cache whose contents are the result is not one
// another machine can supply, and an author asking for both has asked for two
// incompatible things - so they are told, rather than one of the two quietly
// winning.
func TestPersistAndImmutableExceptRefuseEachOther(t *testing.T) {
	t.Parallel()

	_, err := build(t, "VERSION 0.8\nmain:\n    FROM alpine:3.22\n"+
		"    CACHE --id k --persist --immutable-except 'tmp/**' /c\n")
	if err == nil {
		t.Fatal("a cache was both persisted into the image and offered to other" +
			" machines, which are two different answers to where its contents live")
	}

	if !strings.Contains(err.Error(), "persist") ||
		!strings.Contains(err.Error(), "immutable-except") {
		t.Errorf("the refusal does not name both flags: %v", err)
	}
}

// TestACacheCommandCarriesTheClaim. The flag has two spellings for one idea and
// both must reach the mount, or an author who used the command form gets a
// cache nothing will share.
func TestACacheCommandCarriesTheClaim(t *testing.T) {
	t.Parallel()

	plain := plan(t, "VERSION 0.8\nmain:\n    FROM alpine:3.22\n"+
		"    CACHE --id k /c\n    RUN echo hi\n")
	claimed := plan(t, "VERSION 0.8\nmain:\n    FROM alpine:3.22\n"+
		"    CACHE --id k --immutable-except 'tmp/**' /c\n    RUN echo hi\n")

	if plain == claimed {
		t.Error("CACHE --immutable-except changed nothing, so the command form" +
			" of the flag is accepted and ignored")
	}
}

// build is plan's sibling for the cases that are meant to fail.
func build(t *testing.T, src string) (any, error) {
	t.Helper()

	return interp.Build(src, testMain)
}

// TestAnEmptyExclusionListIsStillAClaim.
//
// `--immutable-except ”` is the author saying "every path under this mount is
// written once, with no exceptions" - which is the *strongest* form of the
// claim, and the recommended setting for seven of the caches in
// `docs/caching/sharing-caches.md`: Cargo's registry, pip's wheels, NuGet,
// RubyGems and both of Bazel's stores are content-addressed with no index and
// no bookkeeping beside the blobs.
//
// Stored as a bare string it is indistinguishable from the flag being absent,
// so the strongest claim an author can make reads as no claim at all - and the
// caches most worth sharing are the ones silently not shared. Absence and
// emptiness are different answers and the key has to tell them apart.
func TestAnEmptyExclusionListIsStillAClaim(t *testing.T) {
	t.Parallel()

	plain := plan(t, "VERSION 0.8\nmain:\n    FROM alpine:3.22\n"+
		"    CACHE --id k /c\n    RUN echo hi\n")
	nothingExcepted := plan(t, "VERSION 0.8\nmain:\n    FROM alpine:3.22\n"+
		"    CACHE --id k --immutable-except '' /c\n    RUN echo hi\n")

	if plain == nothingExcepted {
		t.Error("a cache claimed immutable with no exceptions keys the same as" +
			" one making no claim, so the strongest claim is the one ignored")
	}
}

// TestTheMountFormAlsoDistinguishesAnEmptyList. The same, through
// `RUN --mount`, where presence is a key in a field map rather than a flag.
func TestTheMountFormAlsoDistinguishesAnEmptyList(t *testing.T) {
	t.Parallel()

	const src = "VERSION 0.8\nmain:\n    FROM alpine:3.22\n" +
		"    RUN --mount type=cache,target=/c,id=k%s echo hi\n"

	plain := plan(t, strings.ReplaceAll(src, "%s", ""))
	empty := plan(t, strings.ReplaceAll(src, "%s", ",immutable-except="))

	if plain == empty {
		t.Error("immutable-except= in a mount keys the same as omitting it," +
			" so the mount form cannot express a cache with no exceptions")
	}
}
