package interp_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// What a caller declined to provide is not the same as what this engine cannot
// do, and the two must not be added together.
//
// The work list is read to decide what to build next, so a construct that is
// finished but unavailable to a plan-only caller has no business at the top of
// it. Probes were the first family of these; GIT CLONE is the second, and a
// build context fetched from another repository is the third.
func TestAWithheldCapabilityIsNotUnimplemented(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    GIT CLONE https://example.test/org/repo /src
`, testMain)
	if err == nil {
		t.Fatal("a repository was cloned by a caller that provided no way to clone one")
	}

	if !errors.Is(err, interp.ErrNotProvided) {
		t.Errorf("not reported as a capability the caller withheld:\n%s", err)
	}

	if !errors.Is(err, interp.ErrNoRunner) {
		t.Errorf("cloning needs something run, so it is a kind of ErrNoRunner:\n%s", err)
	}

	// The diagnosis still has to be useful to a person.
	if !strings.Contains(err.Error(), "GIT CLONE") {
		t.Errorf("the refusal does not name the construct:\n%s", err)
	}
}

// The probe family is the same kind of thing, and says so.
func TestAProbeIsAlsoAWithheldCapability(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    LET v = $(cat version)
    RUN echo $v
`, testMain)
	if err == nil {
		t.Fatal("a value was produced without running what produces it")
	}

	if !errors.Is(err, interp.ErrNotProvided) {
		t.Errorf("a probe is not reported as a withheld capability:\n%s", err)
	}
}

// An unimplemented construct is neither, which is the distinction that makes
// the bucket worth having.
func TestAnUnimplementedConstructIsNeither(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    WITH DOCKER
        RUN docker ps
    END
`, testMain)
	if err == nil {
		t.Skip("WITH DOCKER is implemented; pick another unimplemented construct here")
	}

	if errors.Is(err, interp.ErrNotProvided) {
		t.Errorf("an unimplemented construct was counted as a withheld capability:\n%s", err)
	}
}
