//go:build darwin

package exec

import (
	"context"
	"fmt"
	"io"
	osexec "os/exec"
	"path/filepath"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// PackLayer writes one layer of this sandbox's store as an OCI blob.
//
// **A second exec, because the first one is busy.** The protocol holds the only
// stdio pair `container exec` gives, and this carries a layer rather than a
// message - so it goes the way faults already go: another exec, the guest
// binary in a mode that does one thing, and a pipe (E556, and the prior art in
// applefill_darwin.go).
//
// Nothing comes back but bytes. The blob's name is the digest of its contents,
// so the caller hashes what it copies and there is no reply to parse - which is
// what lets this be a pipe instead of a protocol.
//
// stderr is collected rather than passed through: a failure here is reported to
// whoever asked for the image, and a guest complaining on the terminal in the
// middle of a build's output names nothing the reader can act on.
func (a *Apple) PackLayer(ctx context.Context, id ir.NodeID, w io.Writer) error {
	guestBin, err := a.guestBinary()
	if err != nil {
		return fmt.Errorf("pack layer %s: %w", id, err)
	}

	cmd := osexec.CommandContext(ctx, "container", "exec", "-i", //nolint:gosec // fixed argv
		"-e", "EARTH_GUEST_ROOT="+guestStore,
		a.name, "/earth/"+filepath.Base(guestBin), "--pack", id.String())

	var complaint strings.Builder

	cmd.Stdout = w
	cmd.Stderr = &complaint

	err = cmd.Run()
	if err != nil {
		if said := strings.TrimSpace(complaint.String()); said != "" {
			return fmt.Errorf("pack layer %s in %s: %w\n  %s", id, a.name, err, said)
		}

		return fmt.Errorf("pack layer %s in %s: %w", id, a.name, err)
	}

	return nil
}
