package guest

import "path/filepath"

// daemonArgs is how this engine starts a docker daemon inside a step.
//
// **Measured rather than copied.** A daemon was started by hand in a plain user
// namespace on the machine this project uses, and every flag here is one the
// attempt needed - each was added because the daemon said so, and the reason it
// said is recorded next to it (E364):
//
//   - `--group=` : the socket is chowned to the `docker` group by default, and a
//     namespace mapping one id has no such group. `chown: invalid argument`.
//   - `--pidfile` : the default is `/var/run/docker.pid`, which the *host's*
//     daemon holds. Without this a step refuses to start because a process it
//     cannot see is running.
//   - `--data-root`, `--exec-root` : the defaults are shared with the host.
//   - `--storage-driver=vfs` : overlay on overlay needs kernel support a step
//     cannot assume, and vfs works anywhere. Slow and correct beats fast and
//     conditional for the first daemon this engine runs.
//   - `--iptables=false`, `--bridge=none` : a bridge wants netlink permissions a
//     user namespace does not have, and a step's network is the sandbox's.
//     **Both halves are conditional and `ownNet` is when they stop holding**:
//     the daemon is only put in a user namespace when the guest is not root
//     (namespacedAs), and a step now has a network namespace of its own. Keeping
//     them there costs port publishing entirely - without iptables there are no
//     DNAT rules, only the userland proxy, and a published `127.0.0.1:5432` never
//     appeared for the step to connect to (E967).
//
// Not `rootlesskit` and not `slirp4netns`: a step is already inside a namespace
// this engine made, which is the whole reason those are unnecessary (E363).
// execRoot is where the daemon keeps its runtime sockets.
//
// **Not under the step**, and the reason is a hard kernel limit rather than
// taste: `sun_path` is 108 bytes and containerd refuses over 104, while a step's
// root is a store plus a handle plus a root. The first real run started the
// daemon, reached containerd, and timed out waiting for a socket that could not
// bind (E375).
//
// Safe to be a fixed path because the shim mounts a private tmpfs at `/run`
// before the daemon starts (E373): each daemon has its own, no other daemon or
// process on the machine can see it, and it is gone when the namespace is.
const execRoot = "/run/earthbuild-docker"

// ownNet says the daemon has a network namespace of its own to manage. In the
// guest's shared one it must not: editing iptables there is editing the
// *machine's* firewall, which is what these flags were right to prevent.
func daemonArgs(root, sock string, ownNet bool) []string {
	args := []string{
		"--group=",
		"--storage-driver=vfs",
		"--host=unix://" + sock,
		"--data-root=" + filepath.Join(root, "data"),
		"--exec-root=" + execRoot,
		"--pidfile=" + filepath.Join(root, "docker.pid"),
	}

	if ownNet {
		return args
	}

	return append(args, "--iptables=false", "--bridge=none")
}
