package subcmd

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/cmd/earth/base"
	"github.com/EarthBuild/earthbuild/conslogging"
)

// **The two flags must arrive.** That is what this whole file is about: the
// native path's recorded failure is a flag parsed into a global, never copied
// into the options, and the engine doing something the caller did not ask for
// (E611). A fingerprint path that lost its destination would write nothing and
// say it had.
func TestTheFingerprintPathsArrive(t *testing.T) {
	t.Parallel()

	got := nativeOptions(nativeInput{
		dir: ".", target: "test", emitInputs: "a.json", checkInputs: "b.json",
	})

	if got.EmitInputs != "a.json" || got.CheckInputs != "b.json" {
		t.Errorf("emit %q, check %q", got.EmitInputs, got.CheckInputs)
	}
}

// The commands exist under the names a workflow file would write.
func TestTheInputCommandsAreRegistered(t *testing.T) {
	t.Parallel()

	want := map[string]bool{"emit-inputs": false, "check-inputs": false}

	newCLI := base.NewCLI(
		new(conslogging.ConsoleLogger),
		base.WithVersion(""),
		base.WithGitSHA(""),
		base.WithBuiltBy(""),
		base.WithDefaultBuildkitdImage(""),
		base.WithDefaultInstallationName(""),
	)

	for _, cmd := range NewBuild(newCLI).Cmds() {
		if _, ok := want[cmd.Name]; ok {
			want[cmd.Name] = true

			if cmd.Usage == "" || strings.HasSuffix(cmd.Usage, ".") {
				t.Errorf("%s has a usage line of %q", cmd.Name, cmd.Usage)
			}
		}
	}

	for name, found := range want {
		if !found {
			t.Errorf("there is no %s command", name)
		}
	}
}
