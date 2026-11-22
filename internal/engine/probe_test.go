package engine

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/conslogging"
)

// A Docker engine that starts up asks the daemon once, not three times.
//
// `docker info` talks to the daemon and costs about a tenth of a second each
// time. Three of them ran before any command was dispatched, so every
// invocation paid roughly 0.2s for answers it usually never used - a native
// build, which touches Docker for nothing, spent most of its fixed cost there.
//
// The bare `info` those replaced was there to keep a docker-cli panic off the
// user's terminal when the daemon is down, and it still is: the combined
// question is tried first, its output is discarded if it fails, and the
// original sequence runs to produce the same diagnosis it always did.
func TestAStartingDockerEngineAsksTheDaemonOnce(t *testing.T) {
	dir := t.TempDir()

	log := filepath.Join(dir, "calls")

	fake := "#!/bin/sh\n" +
		"echo \"$*\" >> " + log + "\n" +
		"case \"$*\" in\n" +
		"  *SecurityOptions*DockerRootDir*) echo '[name=seccomp name=userns]|/var/lib/docker' ;;\n" +
		"  *SecurityOptions*) echo '[name=seccomp name=userns]' ;;\n" +
		"  *DockerRootDir*) echo '/var/lib/docker' ;;\n" +
		"  *) echo 'Server Version: 27.0' ;;\n" +
		"esac\n"

	// Executable because PATH lookup has to find something it can run (G306).
	//nolint:gosec // a fixture in a directory this test made
	err := os.WriteFile(filepath.Join(dir, "docker"), []byte(fake), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", dir)

	e, err := newDockerEngine(t.Context(), &Config{
		ContainerName: "test", DefaultPort: DefaultBuildkitPort, Log: quietLogger(),
	})
	if err != nil {
		t.Fatalf("a healthy daemon must give an engine: %v", err)
	}

	body, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}

	calls := strings.Count(strings.TrimSpace(string(body)), "\n") + 1
	if calls != 1 {
		t.Errorf("startup ran `docker info` %d times, want 1"+
			"\n  each one talks to the daemon, and they run before any command"+
			" is dispatched:\n%s", calls, body)
	}

	// The one answer still has to be read correctly, or the saving is a
	// regression wearing a stopwatch: a user-namespaced daemon needs its run
	// arguments, and only the security half of the answer says so.
	if want := []string{"--userns", "host"}; !slices.Equal(e.RunArgs, want) {
		t.Errorf("RunArgs = %q, want %q - the security options were not read", e.RunArgs, want)
	}
}

// quietLogger is a console logger whose output nobody reads.
func quietLogger() *conslogging.ConsoleLogger {
	var swallowed strings.Builder

	return conslogging.Current(conslogging.DefaultPadding, conslogging.Info, false).
		WithWriter(&swallowed)
}
