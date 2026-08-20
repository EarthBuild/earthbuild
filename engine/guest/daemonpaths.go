package guest

// daemonPaths resolves a daemon request into the guest's own paths.
//
// The daemon runs *beside* the step, not in it: the guest's `dockerd`, writing
// into the step's filesystem at the guest path for what the step calls
// `/var/lib/earthbuild-docker`, and listening at the guest path for what the
// step calls `/var/run/docker.sock`. The step reaches it through the socket at
// the name it knows, and its image therefore needs a Docker *client* and not a
// daemon - which is what images using WITH DOCKER tend to have.
//
// No error, because there is nothing here to refuse. `checkDaemon` has already
// established both are absolute, and an absolute path joined onto a root cannot
// leave it once cleaned - `/../../etc` inside a chroot is `/etc`, which is what
// the step's own kernel would make of the same string. An error return that
// cannot fire is a claim the code does not support.
func daemonPaths(stepRoot string, d *Daemon) (root, sock string) {
	// within refuses nothing for an absolute path; the containment is in the
	// cleaning, and the guard against it silently changing is a test that feeds
	// it paths trying to climb out.
	root, _ = within(stepRoot, d.Root)
	sock, _ = within(stepRoot, d.Socket)

	return root, sock
}
