package cli

import "github.com/EarthBuild/earthbuild/engine/exec"

// sandbox picks the backend for this platform: the guest as a child process,
// confined with namespaces and cgroups.
func sandbox(_ string) (exec.Sandbox, error) {
	dir, err := storeDir()
	if err != nil {
		return nil, err
	}

	sb := exec.NewNative()
	sb.Root = dir

	err = sb.Available()
	if err != nil {
		return nil, err
	}

	return sb, nil
}
