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
