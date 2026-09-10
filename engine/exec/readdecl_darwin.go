//go:build darwin

package exec

import (
	"bytes"
	"context"
	"fmt"
	osexec "os/exec"
	"path/filepath"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// ReadDeclaration hands over what a stack element declares, from a store this
// host cannot open.
//
// **The half that was missing.** This backend could already pack a layer
// (PackLayer) and could not read a declaration, so an image it wrote inherited
// no environment at all: `FROM rust` produced an image with no PATH, and the
// build that used it as a base failed at `cargo: not found`, nowhere near the
// cause. The microVM had the same gap and the same shape of fix.
//
// A second exec and a pipe, for the reason PackLayer gives: the protocol holds
// the only stdio pair `container exec` offers.
//
// **No output is an answer.** Most stack elements are trees and declare
// nothing, so the guest writes nothing and exits clean rather than failing -
// the caller asks about every element and keeps the few that reply.
func (a *Apple) ReadDeclaration(ctx context.Context, id ir.NodeID) ([]byte, bool, error) {
	guestBin, err := a.guestBinary()
	if err != nil {
		return nil, false, fmt.Errorf("read what %s declares: %w", id, err)
	}

	cmd := osexec.CommandContext(ctx, "container", "exec", "-i", //nolint:gosec // fixed argv
		"-e", "EARTH_GUEST_ROOT="+guestStore,
		a.name, "/earth/"+filepath.Base(guestBin), "--decl", id.String())

	var (
		out       bytes.Buffer
		complaint strings.Builder
	)

	cmd.Stdout = &out
	cmd.Stderr = &complaint

	err = cmd.Run()
	if err != nil {
		if said := strings.TrimSpace(complaint.String()); said != "" {
			return nil, false, fmt.Errorf("read what %s declares in %s: %w\n  %s",
				id, a.name, err, said)
		}

		return nil, false, fmt.Errorf("read what %s declares in %s: %w", id, a.name, err)
	}

	if out.Len() == 0 {
		return nil, false, nil
	}

	return out.Bytes(), true, nil
}
