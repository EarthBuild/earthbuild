package exec

import (
	"debug/elf"
	"fmt"
	"os"
	osexec "os/exec"

	"github.com/EarthBuild/earthbuild/engine/guest"
)

// envAllowHostDocker lets a build reach the daemon on this machine.
//
// Named once because it appears in the refusal and in the check, and those two
// must be the same string - a message telling somebody to set a variable the
// code does not read is worse than no message.
const envAllowHostDocker = "EARTH_ALLOW_HOST_DOCKER"

// hostDockerMounts gives a WITH DOCKER step the daemon on this machine, if that
// has been allowed.
//
// The three mounts are the same ones the VM backend provides - client, plugins,
// socket - and the difference is whose daemon is on the other end. In a VM it
// is a machine that is destroyed when the build ends. Here it is this one.
//
// **A build step with the host's docker socket has root on the host.** It can
// start a container with `/` bind-mounted and write anywhere, whatever user the
// step itself runs as, and no namespace the engine sets up constrains that -
// the daemon is outside all of them. That is a different trust domain (green
// paper A5) rather than a different path, so it is opt-in and the refusal says
// what it would cost.
//
// `look` is a parameter so the decision can be tested without a docker on the
// machine running the tests.
func hostDockerMounts(look func(string) (string, bool), allowed bool) ([]guest.Mount, string, error) {
	// **The gate first, before anything is offered.** A step holding this
	// machine's docker socket has root on this machine, and no namespace the
	// engine sets up constrains that.
	//
	// Rewriting this function to make an unusable client non-fatal dropped this
	// check entirely, because the refusal it replaced happened to sit after the
	// client lookup and the rewrite reorganised around the client. **A trust
	// decision positioned by accident is a refactoring casualty**, and the test
	// that caught it is the one that asks for the refusal by name (E145).
	if !allowed {
		return nil, "", fmt.Errorf(
			"a WITH DOCKER step would be given this machine's docker daemon, which is"+
				"\n  root on this machine: a step can start a container with / mounted"+
				"\n  and write anywhere, whatever user the step runs as"+
				"\n  the macOS backend hands over a throwaway VM's daemon instead, which is"+
				"\n  why this is refused here and not there"+
				"\n  set %s=1 to allow it", envAllowHostDocker)
	}

	// The socket is the only thing the host must provide; the client is a
	// convenience. An image can carry its own - alpine packages `docker-cli` -
	// and the daemon is what no image can supply.
	//
	// So an unusable or absent client is *omitted*, not fatal. Refusing the
	// whole build for it (as this did) is right about the client and too strong
	// about the step: it declines a feature that would have worked on every
	// image carrying its own client (E145).
	cm, note := clientMounts(look)

	mounts := make([]guest.Mount, 0, 1+len(cm))
	mounts = append(mounts, guest.Mount{Sandbox: dockerSocketPath, Target: dockerSocketPath})

	return append(mounts, cm...), note, nil
}

// clientMounts offers this machine's docker client to a step, and says why it
// could not where it could not.
//
// Split out because both kinds of step want it: one that inherits a daemon and
// one that starts its own both need something to talk to it with, and the daemon
// is the part no image can supply (E145).
//
// Never fatal. An image often carries its own client - alpine packages
// `docker-cli` - so an absent or unusable one is omitted with a note rather than
// failing a build that would have worked.
func clientMounts(look func(string) (string, bool)) ([]guest.Mount, string) {
	client, ok := look("docker")
	if !ok {
		return nil, "this machine has no docker client installed"
	}

	// A binary that cannot run inside the step is worse than no binary. The
	// host's client is usually linked against the distribution's libc, the
	// step's image is usually alpine, and neither the interpreter nor the
	// library is there - so execve fails on the *interpreter* and the kernel
	// says ENOENT, which the shell prints as `docker: not found` about a file
	// that is demonstrably present (E117).
	dynamic, elfErr := needsAnInterpreter(client)
	if elfErr != nil {
		// Reported as a note rather than an error: not knowing whether the
		// client will run is a reason to warn, and refusing the build over it
		// would turn an unreadable ELF header into a failed build.
		return nil, "cannot tell whether " + client + " will run inside a step"
	}

	if dynamic {
		return nil, client + " is dynamically linked, so a step could not run it"
	}

	// Mounted *at* the image's expected path rather than at its own: a step's
	// PATH comes from the image it runs, so a client at
	// /home/somebody/.nix-profile/bin would not be found by `docker build`.
	return []guest.Mount{
		{Sandbox: client, Target: dockerClientPath, ReadOnly: true},
	}, ""
}

// lookHostDocker finds the docker client on this machine.
func lookHostDocker(name string) (string, bool) {
	p, err := osexec.LookPath(name)

	return p, err == nil
}

// hostDockerAllowed reports whether the operator said yes.
func hostDockerAllowed() bool {
	v := os.Getenv(envAllowHostDocker)

	return v != "" && v != "0" && v != "false"
}

// needsAnInterpreter reports whether an executable is dynamically linked.
//
// PT_INTERP, not the ELF type: a static-PIE binary is ET_DYN and runs perfectly
// well without an interpreter, so keying on the type would refuse a client that
// works. The interpreter is the thing that has to exist in the step's image,
// so the interpreter is what is asked about.
//
// An error is *not* "static". A file this cannot parse is one whose behaviour
// is unknown, and reporting unknown as a permissive answer is how the store's
// case-sensitivity probe read "could not tell" as "case-insensitive" (E97).
func needsAnInterpreter(path string) (bool, error) {
	f, err := elf.Open(path)
	if err != nil {
		return false, fmt.Errorf("read %s as an executable: %w", path, err)
	}

	defer f.Close()

	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP {
			return true, nil
		}
	}

	return false, nil
}
