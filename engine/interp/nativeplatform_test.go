package interp_test

import (
	"runtime"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// `--platform=native` is the machine the build runs on.
//
// It resolves to a concrete platform rather than to "unset", and the difference
// matters: unset inherits, and the whole use of the word is to override an
// inherited foreign platform back to this machine. A build that wrote `native`
// and got the platform it was trying to escape would cross-compile silently.
func TestNativePlatformIsThisMachine(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, source string }{
		{"FROM", `
main:
    FROM --platform=native alpine:3.22
    RUN build
`},
		{testCmdCopy, `
producer:
    FROM alpine:3.22
    RUN compile > /out.bin
    SAVE ARTIFACT /out.bin

main:
    FROM --platform=linux/amd64 alpine:3.22
    COPY --platform=native +producer/out.bin /dst/
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p, err := interp.Build(versioned+tc.source, testMain)
			if err != nil {
				t.Fatal(err)
			}

			want := ir.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}

			var found bool

			for _, n := range p.Graph.Nodes() {
				if n.Op.Kind != ir.OpExec {
					continue
				}

				if n.Platform == want {
					found = true
				}
			}

			if !found {
				t.Errorf("nothing runs on %+v:\n%s", want, describe(p.Graph.Nodes()))
			}
		})
	}
}

// `native` is the only word; anything else that is not a platform is still
// refused, so a typo does not quietly become the host.
func TestOnlyNativeIsAWordRatherThanAPlatform(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
main:
    FROM --platform=host alpine:3.22
    RUN build
`, testMain)
	if err == nil {
		t.Fatal("`host` was accepted as a platform")
	}
}
