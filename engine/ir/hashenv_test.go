package ir_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// The digest function is read from one name, and unknown values are refused.
//
// **A typo must not silently build a BLAKE3 store.** Someone setting
// EARTH_DIGEST=sha-256 and getting BLAKE3 has a store that no remote execution
// service will read, and nothing anywhere said so - the build simply gets no
// hits and nobody knows why.
func TestTheDigestFunctionIsReadFromTheEnvironment(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		set  string
		want ir.HashFunc
		err  string
	}{
		{set: "", want: ir.HashBLAKE3},
		{set: "blake3", want: ir.HashBLAKE3},
		{set: "BLAKE3", want: ir.HashBLAKE3},
		{set: "sha256", want: ir.HashSHA256},
		{set: "SHA-256", want: ir.HashSHA256},
		{set: "sha_256", want: ir.HashSHA256},
		{set: "sha1", err: "sha1"},
		{set: "md5", err: "md5"},
		{set: "yes", err: "yes"},
	} {
		got, err := ir.HashFromEnv(tc.set)

		switch {
		case tc.err != "":
			if err == nil {
				t.Errorf("%q was accepted as a digest function", tc.set)

				continue
			}

			if !strings.Contains(err.Error(), tc.err) || !strings.Contains(err.Error(), "sha256") {
				t.Errorf("refusing %q does not name what was set and what is"+
					" available:\n  %v", tc.set, err)
			}

		case err != nil:
			t.Errorf("%q was refused: %v", tc.set, err)

		case got != tc.want:
			t.Errorf("%q selected %v, want %v", tc.set, got, tc.want)
		}
	}
}
