package image

import (
	"encoding/json"
	"strings"
	"testing"
)

// An index entry is chosen by its variant as well as its architecture.
//
// `indexEntry.Platform` read `os` and `architecture` and dropped `variant`, so
// every 32-bit ARM entry in an index looked like `linux/arm` - two of them, in
// alpine's case, and neither matching a step that wants `linux/arm/v7`. The
// refusal named the platform and listed what the image provides, and the list
// said `linux/arm, linux/arm` (E946).
//
// It was behind E942: placement refused to emulate `linux/arm/v7` at all, so the
// build never reached the pull that could not find it.
func TestAnIndexEntryIsChosenByItsVariant(t *testing.T) {
	t.Parallel()

	const index = `{"manifests":[
	  {"digest":"sha256:amd64","platform":{"os":"linux","architecture":"amd64"}},
	  {"digest":"sha256:armv6","platform":{"os":"linux","architecture":"arm","variant":"v6"}},
	  {"digest":"sha256:armv7","platform":{"os":"linux","architecture":"arm","variant":"v7"}},
	  {"digest":"sha256:arm64","platform":{"os":"linux","architecture":"arm64","variant":"v8"}}
	]}`

	var m manifest

	err := json.Unmarshal([]byte(index), &m)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ want, digest string }{
		{"linux/arm/v7", "sha256:armv7"},
		{"linux/arm/v6", "sha256:armv6"},
		// No variant asked for, and the entry has one: `linux/arm64` is how
		// every Earthfile in this corpus spells the platform an index calls
		// `linux/arm64/v8`.
		{"linux/arm64", "sha256:arm64"},
		// Neither side has one.
		{"linux/amd64", "sha256:amd64"},
	} {
		got, selErr := selectPlatform(m, tc.want)
		if selErr != nil {
			t.Errorf("%s: %v", tc.want, selErr)

			continue
		}

		if got != tc.digest {
			t.Errorf("%s selected %s, want %s", tc.want, got, tc.digest)
		}
	}

	// A variant the index does not carry is still a refusal, and the refusal
	// has to distinguish the entries or it reads as the same one twice.
	_, err = selectPlatform(m, "linux/arm/v5")
	if err == nil {
		t.Fatal("a variant no entry provides was accepted")
	}

	if !strings.Contains(err.Error(), "linux/arm/v7") {
		t.Errorf("the refusal does not say which variants exist:\n%v", err)
	}
}

// A single-manifest image is checked by its variant too, or not at all.
//
// `checkArchitecture` compared `os/arch` against a platform that carries a
// variant, so a `linux/arm` configuration refused a `linux/arm/v7` build - the
// same mistake as the index selection above, one layer further down and reached
// only once that one was fixed (E951).
//
// An image that states a variant is still held to it: `linux/arm/v6` is not
// `linux/arm/v7`, and the whole reason this function exists is that running the
// wrong one fails as `exec format error` with nothing to connect it to an image.
func TestASingleManifestImageIsCheckedLooselyOnItsVariant(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name              string
		os, arch, variant string
		want              string
		refused           bool
	}{{
		name: "a configuration with no variant serves a build that wants one",
		os:   "linux", arch: "arm", want: "linux/arm/v7",
	}, {
		name: "a configuration with a variant serves a build that asks for none",
		os:   "linux", arch: "arm64", variant: "v8", want: "linux/arm64",
	}, {
		name: "the same variant on both sides",
		os:   "linux", arch: "arm", variant: "v7", want: "linux/arm/v7",
	}, {
		name: "a different variant is still refused",
		os:   "linux", arch: "arm", variant: "v6", want: "linux/arm/v7",
		refused: true,
	}, {
		name: "a different architecture is still refused",
		os:   "linux", arch: "amd64", want: "linux/arm64",
		refused: true,
	}, {
		name: "an image that says nothing is trusted",
		want: "linux/arm/v7",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := checkArchitecture(tc.os, tc.arch, tc.variant, tc.want)
			if (err != nil) != tc.refused {
				t.Errorf("%s/%s/%s against %s gave %v, refused=%v",
					tc.os, tc.arch, tc.variant, tc.want, err, tc.refused)
			}
		})
	}
}
