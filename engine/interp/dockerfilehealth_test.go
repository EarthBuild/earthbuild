package interp_test

import (
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A Dockerfile's HEALTHCHECK is the Earthfile's HEALTHCHECK.
//
// This engine models a healthcheck in an image's identity and implements the
// Earthfile spelling; the Dockerfile spelling was refused. That is the shape
// the mounts were in - the mechanism present, the translation not connected -
// and it turns an ordinary Dockerfile away over a construct the engine has.
func TestADockerfileHealthcheckIsAHealthcheck(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", `FROM alpine:3.22
HEALTHCHECK --interval=30s --timeout=5s --retries=3 CMD curl -f localhost
`)

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
    SAVE IMAGE app:latest
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatalf("HEALTHCHECK was refused: %v", err)
	}

	// A healthcheck is image *configuration*, not a step: it changes what the
	// image says about itself and produces no work, so the graph is the wrong
	// place to look for it.
	if len(p.Images) != 1 {
		t.Fatalf("the image was not declared: %+v", p.Images)
	}

	hc := p.Images[0].Config.Healthcheck
	if hc == nil {
		t.Fatal("the image has no healthcheck, so the Dockerfile's was dropped")
	}

	if got := strings.Join(hc.Test, " "); !strings.Contains(got, "curl -f localhost") {
		t.Errorf("the healthcheck runs %q", got)
	}

	if hc.Retries != 3 {
		t.Errorf("retries is %d, not the 3 the Dockerfile asked for", hc.Retries)
	}

	if hc.Interval != 30*time.Second {
		t.Errorf("the interval is %v, not 30s", hc.Interval)
	}
}

// And NONE, which turns off whatever the base declared.
func TestADockerfileHealthcheckNoneIsHonoured(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", `FROM alpine:3.22
HEALTHCHECK NONE
`)

	_, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatalf("HEALTHCHECK NONE was refused: %v", err)
	}
}

// MAINTAINER is a label, which is what Docker made it years ago.
//
// Deprecated since Docker 1.13 and defined as `LABEL maintainer=...` - so an
// engine with LABEL has MAINTAINER, and refusing it turns away an old
// Dockerfile over a construct that is a spelling rather than a feature.
func TestADockerfileMaintainerIsALabel(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", `FROM alpine:3.22
MAINTAINER someone@example.test
`)

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
    SAVE IMAGE app:latest
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatalf("MAINTAINER was refused: %v", err)
	}

	if len(p.Images) != 1 {
		t.Fatalf("the image was not declared: %+v", p.Images)
	}

	if got := p.Images[0].Config.Labels["maintainer"]; got != "someone@example.test" {
		t.Errorf("the maintainer label is %q", got)
	}
}
