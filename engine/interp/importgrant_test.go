package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// TestAnImportMayGrantPrivilegeToEveryReferenceThroughIt.
//
// `tests/allow-privileged-import.earth` grants once and uses the alias twice:
//
//	IMPORT --allow-privileged github.com/EarthBuild/test-remote/privileged:main
//	...
//	COPY privileged+privileged/proc-status .
//
// The flag is on the *import*, so every reference through that name inherits
// it - which is the point of naming a repository once. This engine honoured
// `--allow-privileged` written on a FROM, COPY or BUILD and dropped it here, so
// the alias resolved to a remote target with no grant and the privileged step
// inside it was refused.
//
// An import *without* the flag grants nothing, which is the half that makes the
// other half worth having.
func TestAnImportMayGrantPrivilegeToEveryReferenceThroughIt(t *testing.T) {
	t.Parallel()

	src := `
IMPORT %s github.com/org/repo:main AS priv

main:
    FROM alpine:3.22
    COPY priv+privileged/out .
`

	f := func() *fetcher {
		return &fetcher{dir: ctxWith(t, map[string]string{
			testEarthfile: versioned + "\nprivileged:\n    FROM alpine:3.22\n" +
				"    RUN --privileged privileged-step\n    SAVE ARTIFACT /out\n",
		})}
	}

	_, err := interp.Build(versioned+strings.Replace(src, "%s ", "", 1), testMain,
		interp.WithRemotes(f().fetch))
	if err == nil {
		t.Error("a plain import granted privilege to what it names; the flag is" +
			" the whole of what makes the grant deliberate")
	}

	_, err = interp.Build(versioned+strings.Replace(src, "%s", "--allow-privileged", 1),
		testMain, interp.WithRemotes(f().fetch))
	if err != nil {
		t.Fatalf("the import granted privilege and the reference through it was"+
			" still refused: %v", err)
	}
}
