package guest

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"time"
)

// sayStepListeners prints what is listening inside the step's network namespace.
//
// **Temporary, for E967.** `/proc/net/tcp` is per network namespace, so this
// needs no tool in the step and no tool on the guest beyond `ip` - which
// openStepNet has already established is present, since it made the namespace.
//
// The question it answers: with the daemon and the step provably in one
// namespace (sayNetNS), and the container reporting healthy, is the published
// port listening at all? A local address of `:1538` is 5432. Nothing there means
// the daemon never published; something there means the step cannot reach what
// it published. Remove with the diagnosis.
func sayStepListeners(netAt string) {
	if netAt == "" {
		return
	}

	// A short deadline of its own: a diagnostic that hangs would replace the
	// fault being diagnosed with itself.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// The namespace name is the guest's own - openStepNet made it - so nothing
	// here comes from an Earthfile.
	cmd := osexec.CommandContext(ctx, "ip", "netns", "exec", //nolint:gosec // the name is the guest's own, see above
		filepath.Base(netAt), "cat", "/proc/net/tcp")

	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "earthbuild netns[guest]: listeners in %s: %v\n", netAt, err)

		return
	}

	fmt.Fprintf(os.Stderr, "earthbuild netns[guest]: /proc/net/tcp in %s:\n%s\n",
		netAt, out)
}
