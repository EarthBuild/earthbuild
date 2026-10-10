package image_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/image"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// A healthcheck reaches the image on disk.
//
// The OCI configuration has no field for one - a health check is Docker's
// extension, not OCI's - and an image config is a JSON object that both read.
// So it is written *beside* the standard fields, which is what every other
// builder does and what a daemon looks for (E486).
//
// Written rather than converted: the plan carries Docker's shape already, so
// this path is a copy. E44 is the reason - two hand-written copies of these
// fields disagreed, and the fix was one converter - and a healthcheck that
// needed reshaping here would be a second place for them to disagree again.
func TestAHealthcheckIsWrittenIntoTheImageConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := image.WriteLayout(dir, image.Spec{
		Ref:      "thing:latest",
		Platform: ocispec.Platform{OS: "linux", Architecture: "arm64"},
		Config:   ocispec.ImageConfig{Cmd: []string{"/bin/sh"}},
		Healthcheck: &image.Healthcheck{
			Test:        []string{"CMD-SHELL", "curl -f localhost || exit 1"},
			Interval:    30 * time.Second,
			Retries:     3,
			StartPeriod: 5 * time.Second,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var cfg struct {
		Config struct {
			Cmd         []string `json:"Cmd"`
			Healthcheck *struct {
				Test        []string `json:"Test"`
				Interval    int64    `json:"Interval"`
				Retries     int      `json:"Retries"`
				StartPeriod int64    `json:"StartPeriod"`
			} `json:"Healthcheck"`
		} `json:"config"`
	}

	readConfigBlob(t, dir, &cfg)

	if cfg.Config.Healthcheck == nil {
		t.Fatal("the image config carries no Healthcheck")
	}

	if got := strings.Join(cfg.Config.Healthcheck.Test, " "); got !=
		"CMD-SHELL curl -f localhost || exit 1" {
		t.Errorf("the test is %q", got)
	}

	// Nanoseconds, which is how Docker's own config writes a duration.
	if got := cfg.Config.Healthcheck.Interval; got != int64(30*time.Second) {
		t.Errorf("the interval is %d, want %d", got, int64(30*time.Second))
	}

	if got := cfg.Config.Healthcheck.Retries; got != 3 {
		t.Errorf("retries is %d, want 3", got)
	}

	// And the standard fields are still there: a config written by hand beside
	// the extension is one that can drop them.
	if len(cfg.Config.Cmd) != 1 || cfg.Config.Cmd[0] != "/bin/sh" {
		t.Errorf("the command is %q, and the OCI fields must survive the"+
			" extension", cfg.Config.Cmd)
	}
}

// An image with nothing to say about its health says nothing.
//
// `omitempty` rather than a null: a config carrying `"Healthcheck": null` is
// one every reader has to have an opinion about, and this engine has no reason
// to make them.
func TestAnImageWithNoHealthcheckWritesNoField(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := image.WriteLayout(dir, image.Spec{
		Ref:      "thing:latest",
		Platform: ocispec.Platform{OS: "linux", Architecture: "arm64"},
		Config:   ocispec.ImageConfig{Cmd: []string{"/bin/sh"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any

	readConfigBlob(t, dir, &raw)

	config, _ := raw["config"].(map[string]any)
	if _, present := config["Healthcheck"]; present {
		t.Errorf("the config carries a Healthcheck key for an image that"+
			" declares none: %v", config)
	}
}

// readConfigBlob finds the image config in a layout and decodes it.
func readConfigBlob(t *testing.T, dir string, into any) {
	t.Helper()

	// The config is the one blob that parses as an object with a rootfs: the
	// manifest names it, and following the manifest here would be reimplementing
	// the reader this package is the writer for.
	blobs := filepath.Join(dir, "blobs", "sha256")

	entries, err := os.ReadDir(blobs)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(blobs, e.Name()))
		if err != nil {
			continue
		}

		var probe map[string]any
		if json.Unmarshal(b, &probe) != nil {
			continue
		}

		if _, isConfig := probe["rootfs"]; !isConfig {
			continue
		}

		err = json.Unmarshal(b, into)
		if err != nil {
			t.Fatalf("decoding the config: %v", err)
		}

		return
	}

	t.Fatal("no image config blob in the layout")
}
