package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A Dockerfile's STOPSIGNAL is the Earthfile's STOPSIGNAL.
//
// The mechanism is present and the Earthfile spelling reaches it; leaving the
// Dockerfile spelling refused turns an ordinary Dockerfile away over a
// construct this engine has.
func TestADockerfileStopSignalIsAStopSignal(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", "FROM alpine:3.22\nSTOPSIGNAL SIGQUIT\n")

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
    SAVE IMAGE app:latest
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatalf("STOPSIGNAL was refused: %v", err)
	}

	if len(p.Images) != 1 {
		t.Fatalf("the image was not declared: %+v", p.Images)
	}

	if got := p.Images[0].Config.StopSignal; got != "SIGQUIT" {
		t.Errorf("the image stops on %q, want SIGQUIT", got)
	}
}

// A Dockerfile's bad STOPSIGNAL is refused where it is written.
//
// The translation must not become a way in for a value the Earthfile spelling
// would have turned away: both reach the same check, so a Dockerfile cannot
// declare an image the daemon will reject at `docker run`.
func TestADockerfileStopSignalIsChecked(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", "FROM alpine:3.22\nSTOPSIGNAL SIGBANANA\n")

	_, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
    SAVE IMAGE app:latest
`, testMain, interp.WithContext(dir))
	if err == nil {
		t.Fatal("SIGBANANA was accepted")
	}

	if !strings.Contains(err.Error(), "STOPSIGNAL") {
		t.Errorf("the refusal does not name the command: %v", err)
	}
}
