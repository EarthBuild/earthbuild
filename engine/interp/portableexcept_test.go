package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// TestPortableExceptIsPartOfTheCachesIdentity.
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
func TestPortableExceptIsPartOfTheCachesIdentity(t *testing.T) {
	t.Parallel()

	const src = "VERSION 0.8\nmain:\n    FROM alpine:3.22\n" +
		"    RUN --mount type=cache,target=/c,id=k%s echo hi\n"

	plain := plan(t, strings.ReplaceAll(src, "%s", ""))
	excepting := plan(t, strings.ReplaceAll(src, "%s", ",portable-except=tmp/**"))
	other := plan(t, strings.ReplaceAll(src, "%s", ",portable-except=lock"))

	if plain == excepting {
		t.Error("a cache declared portable keys the same as one that is not," +
			" so a build would share what the author never said was shareable")
	}

	if excepting == other {
		t.Error("two different exclusion sets key the same, so two machines" +
			" can disagree about which paths are stable and share anyway")
	}
}

// TestPersistAndPortableExceptRefuseEachOther.
//
// `--persist` copies the cache's contents into the image, which makes them part
// of what the target produces. A cache whose contents are the result is not one
// another machine can supply, and an author asking for both has asked for two
// incompatible things - so they are told, rather than one of the two quietly
// winning.
func TestPersistAndPortableExceptRefuseEachOther(t *testing.T) {
	t.Parallel()

	_, err := build(t, "VERSION 0.8\nmain:\n    FROM alpine:3.22\n"+
		"    CACHE --id k --persist --portable-except 'tmp/**' /c\n")
	if err == nil {
		t.Fatal("a cache was both persisted into the image and offered to other" +
			" machines, which are two different answers to where its contents live")
	}

	if !strings.Contains(err.Error(), "persist") ||
		!strings.Contains(err.Error(), "portable-except") {
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
		"    CACHE --id k --portable-except 'tmp/**' /c\n    RUN echo hi\n")

	if plain == claimed {
		t.Error("CACHE --portable-except changed nothing, so the command form" +
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
// `--portable-except ”` is the author saying "every path under this mount is
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
		"    CACHE --id k --portable-except '' /c\n    RUN echo hi\n")

	if plain == nothingExcepted {
		t.Error("a cache claimed portable with no exceptions keys the same as" +
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
	empty := plan(t, strings.ReplaceAll(src, "%s", ",portable-except="))

	if plain == empty {
		t.Error("portable-except= in a mount keys the same as omitting it," +
			" so the mount form cannot express a cache with no exceptions")
	}
}

// TestTheHelperIsPartOfTheCachesIdentity.
//
// **The helper decides what a unit is.** It chooses the boundaries, the keys and
// the bytes inside each frame, so two machines running different helpers over
// one cache produce units that are not the same units - filed under digests that
// do not match, sharing nothing, and worse, importable into each other.
//
// So it is in the key for the reason `--portable-except` is, only more so: that
// flag says which paths may cross, this one says what crossing *means*.
func TestTheHelperIsPartOfTheCachesIdentity(t *testing.T) {
	t.Parallel()

	const src = "VERSION 0.8\nmain:\n    FROM alpine:3.22\n" +
		"    CACHE --id k --portable-except ''%s /c\n    RUN echo hi\n"

	plain := plan(t, strings.ReplaceAll(src, "%s", ""))
	helped := plan(t, strings.ReplaceAll(src, "%s", " --helper ./go-blob"))
	other := plan(t, strings.ReplaceAll(src, "%s", " --helper ./npm-blob"))

	if plain == helped {
		t.Error("a cache read by a helper keys the same as one read by nobody," +
			" so two machines can disagree about what a unit is and share anyway")
	}

	if helped == other {
		t.Error("two different helpers key the same, so one machine's units" +
			" are imported by a helper that did not make them")
	}
}

// TestTheMountFormTakesAHelperToo. Both spellings of a cache reach the same
// mount, or an author who used `RUN --mount` gets a cache nothing can read.
func TestTheMountFormTakesAHelperToo(t *testing.T) {
	t.Parallel()

	const src = "VERSION 0.8\nmain:\n    FROM alpine:3.22\n" +
		"    RUN --mount type=cache,target=/c,id=k,portable-except=%s echo hi\n"

	plain := plan(t, strings.ReplaceAll(src, "%s", ""))
	helped := plain

	if got := plan(t, strings.ReplaceAll(src, "%s", ",helper=./go-blob")); got != helped {
		helped = got
	}

	if plain == helped {
		t.Error("helper= in a mount changed nothing, so the mount form of the" +
			" flag is accepted and ignored")
	}
}
