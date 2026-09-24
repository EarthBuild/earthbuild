package guest

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/EarthBuild/earthbuild/engine/decl"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// WriteDeclaration hands over what a stack element declares, and says whether
// the store held one.
//
// **The other half of PackLayer.** A stack element is a tree or a declaration
// (green paper 3.2a) and an image needs both: the layers give it a filesystem,
// the declarations give it the environment, working directory and user its base
// established. A host that can read neither writes an image built `FROM rust`
// with no PATH, and the failure surfaces as `cargo: not found` in whatever later
// build uses it as a base.
//
// **Absent is an answer.** Most elements are trees and declare nothing, so the
// caller asks about all of them and keeps the few that do; reporting that as an
// error would make the ordinary case look like a fault.
//
// The bytes as they lie, because the host decodes them with `decl.Decode` - the
// same reader the store uses. Re-encoding here would be a second encoder to
// disagree with the first.
func WriteDeclaration(root string, id ir.NodeID, w io.Writer) (int64, bool, error) {
	body, err := os.ReadFile(decl.Path(root, id))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, false, nil
	}

	if err != nil {
		return 0, false, fmt.Errorf("read what %s declares: %w", id, err)
	}

	n, err := w.Write(body)
	if err != nil {
		return 0, false, fmt.Errorf("hand over what %s declares: %w", id, err)
	}

	return int64(n), true, nil
}
