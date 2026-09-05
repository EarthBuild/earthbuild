package guest

import "testing"

// Every step gets a /run/secrets.
//
// Not because this engine puts anything in it. BuildKit creates the directory
// for every step unconditionally - measured on a bare `alpine:3.19` with no
// secret anywhere in the build - and Docker's own `--mount=type=secret` names
// the same path, so it is the convention a tool reaches for rather than one
// engine's detail.
//
// The symptom that led here was two removes away from any secret.
// `tests/autocompletion` completes a directory only when it has a subdirectory,
// `/run/secrets` was the only subdirectory `/run` had, and without it the
// completion omitted `../run/` - a diff of one line, in a test about tab
// completion, caused by a mount (E939).
//
// Ephemeral and a tmpfs, for the reason /dev/shm is both: what a step writes
// into its own root is captured, and a directory called secrets is the last one
// whose contents should reach a layer or a disk.
func TestEveryStepGetsARunSecrets(t *testing.T) {
	t.Parallel()

	var found *Mount

	for i, m := range stepMounts(Request{}, nil, false) {
		if m.Target == "/run/secrets" {
			found = &stepMounts(Request{}, nil, false)[i]
		}
	}

	if found == nil {
		t.Fatal("no /run/secrets among the mounts every step gets")
	}

	if !found.Ephemeral {
		t.Error("/run/secrets is not ephemeral, so what a step puts there is captured")
	}

	if !found.Tmpfs {
		t.Error("/run/secrets is not a tmpfs, so what a step puts there reaches a disk")
	}

	if found.Mode != 0o755 {
		t.Errorf("/run/secrets mode = %#o, want %#o", found.Mode, 0o755)
	}
}
