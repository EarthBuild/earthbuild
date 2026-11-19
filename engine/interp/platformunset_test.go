package interp_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// An undeclared name in a `--platform` value expands to nothing, which means
// "inherit".
//
// `FROM --platform=$NATIVEPLATFORM +b0` with no `ARG NATIVEPLATFORM` above it
// was refused as `"$NATIVEPLATFORM" is not a platform`: the value kept the
// unset name, as a tag or a label does. The reference expands every
// `--platform` with placeholders off (FROM, FROM DOCKERFILE, COPY, BUILD,
// WITH DOCKER), so the same line builds there, on the platform it inherits.
func TestAnUndeclaredNameInAPlatformFlagIsEmpty(t *testing.T) {
	t.Parallel()

	const b0 = "\nb0:\n    FROM alpine:3.22\n    RUN true\n    SAVE ARTIFACT /etc/hostname\n"

	for _, tc := range []struct{ name, main string }{
		{"FROM a target", "    FROM --platform=$NATIVEPLATFORM +b0\n"},
		{"FROM an image", "    FROM --platform=$NATIVEPLATFORM alpine:3.22\n"},
		{"COPY", "    FROM alpine:3.22\n    COPY --platform=$NATIVEPLATFORM +b0/hostname /h\n"},
		{"BUILD", "    FROM alpine:3.22\n    BUILD --platform=$NATIVEPLATFORM +b0\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := interp.Build(versioned+b0+"\nmain:\n"+tc.main+"    RUN true\n", testMain)
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
