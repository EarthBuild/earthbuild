package ir_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Every field of an image's configuration reaches the OCI configuration.
//
// Three bugs in one week had the same shape: two paths that should agree, one
// of which was updated. `LABEL` survived a stage copy and `VOLUME` did not;
// `Entrypoint` reached a loaded image and `ExposedPorts` did not; a directory
// kept its mtime and a file did not. In each case the half that still worked is
// what made the other half invisible.
//
// The image configuration had two writers - the one that saves an image to disk
// and the one that packs it for `docker load` - each converting the same fields
// by hand, and one of them was missing two. There is one converter now, and this
// is the guard that makes adding a field to `ir.ImageConfig` without carrying it
// across a test failure rather than a silence.
//
// Reflective on purpose: a hand-written list of fields is the same kind of thing
// that went wrong, one indirection further out.
func TestEveryImageConfigFieldIsCarried(t *testing.T) {
	t.Parallel()

	// Every field set to something distinguishable from zero.
	full := &ir.ImageConfig{
		Entrypoint: []string{"/bin/entry"},
		Cmd:        []string{"arg"},
		Env:        []string{"K=V"},
		WorkingDir: "/w",
		User:       "nobody",
		Labels:     map[string]string{"role": "probe"},
		Exposed:    []string{"8080/tcp"},
		Volumes:    []string{"/data"},
		Healthcheck: &ir.Healthcheck{
			Test:     []string{"CMD-SHELL", "true"},
			Interval: time.Second,
			Retries:  1,
		},
		StopSignal: "SIGQUIT",
	}

	// Anything left zero here would be a field nobody thought about, and the
	// test would then be checking that it is carried while not carrying it.
	v := reflect.ValueOf(*full)
	for i := range v.NumField() {
		if v.Field(i).IsZero() {
			t.Fatalf("this test does not set %s, so it cannot say whether it is carried",
				v.Type().Field(i).Name)
		}
	}

	got := ir.OCIConfig(full)

	for _, tc := range []struct {
		field string
		zero  bool
	}{
		{field: "Entrypoint", zero: len(got.Entrypoint) == 0},
		{field: "Cmd", zero: len(got.Cmd) == 0},
		{field: "Env", zero: len(got.Env) == 0},
		{field: "WorkingDir", zero: got.WorkingDir == ""},
		{field: "User", zero: got.User == ""},
		{field: "Labels", zero: len(got.Labels) == 0},
		{field: "Exposed -> ExposedPorts", zero: len(got.ExposedPorts) == 0},
		{field: "Volumes", zero: len(got.Volumes) == 0},
		// Not through `OCIConfig`, and that is the finding rather than an
		// exception: `ocispec.ImageConfig` has no field for a health check, so
		// it travels beside the configuration through a converter of its own
		// (E486). Checked here anyway, because "carried across" is the property
		// and the guard should not care which of the two converters carries it.
		{field: "Healthcheck", zero: ir.OCIHealthcheck(full) == nil},
		{field: "StopSignal", zero: got.StopSignal == ""},
	} {
		if tc.zero {
			t.Errorf("%s was set and did not reach the OCI configuration", tc.field)
		}
	}
}

// A nil configuration is not an empty one.
//
// A node that writes no image has no configuration, and an image declaring
// `"Volumes": {}` is making a statement where one declaring nothing is not.
func TestNoImageConfigIsNotAnEmptyOne(t *testing.T) {
	t.Parallel()

	got := ir.OCIConfig(nil)

	if got.Volumes != nil || got.ExposedPorts != nil || got.Labels != nil {
		t.Errorf("a nil configuration produced empty sets: %+v", got)
	}
}
