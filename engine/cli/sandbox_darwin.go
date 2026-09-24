package cli

import "github.com/EarthBuild/earthbuild/engine/exec"

// sandbox picks the backend for this platform: a VM, via Apple's container CLI.
func sandbox(image string) (exec.Sandbox, error) {
	dir, err := storeDir()
	if err != nil {
		return nil, err
	}

	sb := exec.NewApple()
	sb.Store = dir

	// A build with a WITH DOCKER block needs an image with a daemon in it. The
	// VM is named after its image, so this separates the two machines without
	// any further arrangement - and a project that never mentions docker keeps
	// the small one.
	if image != "" {
		sb.Image = image
	}

	// The daemon runs with the containerd image store, which is what makes
	// `docker load` accept an OCI layout - the format this engine already
	// writes for SAVE IMAGE. Without it docker falls back to the legacy
	// docker-archive and fails looking for a `blobs/json` that an OCI layout
	// does not have.
	//
	// Passed as the container's command because the dind entrypoint forwards
	// arguments to dockerd; it is also part of the VM's name, so a machine
	// started without it is never mistaken for this one.
	if image == dockerSandboxImage {
		sb.Command = []string{"--feature", "containerd-snapshotter=true"}
	}

	err = sb.Available()
	if err != nil {
		return nil, err
	}

	return sb, nil
}
