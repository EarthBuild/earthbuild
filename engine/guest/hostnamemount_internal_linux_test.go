package guest

import "testing"

// Both answers to "what is this machine called" are the same answer.
//
// E758 set the hostname in the step's UTS namespace and left `/etc/hostname`
// as whatever the image shipped - `localhost`, in alpine's case - so `hostname`
// and `cat /etc/hostname` disagreed. Which one a tool believes is then a
// property of the tool, and both are widely read: shells and `uname -n` ask the
// kernel, while init scripts, JVM startup and a good deal of packaging read the
// file (E765).
func TestTheTwoAnswersForTheMachineNameAgree(t *testing.T) {
	t.Parallel()

	mounts := hostnameMount()
	if len(mounts) != 1 {
		t.Fatalf("a step gets %d /etc/hostname mounts, want 1", len(mounts))
	}

	if mounts[0].Target != "/etc/hostname" {
		t.Errorf("the mount lands at %s, want /etc/hostname", mounts[0].Target)
	}

	// The file holds a name and a newline, as every /etc/hostname does: a
	// reader that does not trim is common enough that the newline matters more
	// than it looks.
	if got, want := mounts[0].Secret, SandboxHost+"\n"; got != want {
		t.Errorf("/etc/hostname holds %q, want %q", got, want)
	}

	// Readable by whoever the step runs as. A step running as a non-root user
	// that cannot read its own machine name is a stranger failure than not
	// having one.
	if mounts[0].Mode != 0o644 {
		t.Errorf("/etc/hostname is mode %#o, want %#o", mounts[0].Mode, 0o644)
	}
}
