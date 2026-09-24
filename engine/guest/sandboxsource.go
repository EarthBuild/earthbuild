package guest

import (
	"path/filepath"
	"strings"
)

// StorePath is where the layer store appears inside a sandbox that has one of
// its own - a VM, whose kernel mounts it there.
//
// Fixed, and load-bearing for that reason. The step that loads a packed image
// runs `docker load -i <archive>`, so the archive's path is in its argv and
// therefore in its key; a host path there would give one build a different key
// on every machine, and the same build a different key after the cache moved.
const StorePath = "/var/lib/earthbuild/store"

// sandboxSource resolves a sandbox path against the store this guest has.
//
// **Two sandboxes, one contract.** Where the sandbox is a VM the store is
// mounted at StorePath and this is the identity. Where it is this machine's own
// filesystem the store is wherever the cache directory put it, nothing mounts
// it at StorePath, and a mount naming the archive there names nothing at all -
// which is what `WITH DOCKER --load` reported, as a sandbox with no
// `/var/lib/earthbuild/store/images` in it (E750).
//
// Only the store prefix moves. A sandbox path is otherwise the machine's own -
// the docker client and its socket - and rebasing one of those onto the store
// would be a different bug wearing this one's clothes.
func sandboxSource(path, layers string) string {
	if layers == "" || layers == StorePath {
		return path
	}

	rest, ok := strings.CutPrefix(path, StorePath+string(filepath.Separator))
	if !ok {
		return path
	}

	return filepath.Join(layers, rest)
}
