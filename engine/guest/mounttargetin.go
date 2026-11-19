package guest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// maxPathLinks bounds the symlinks one resolution follows: Linux's own limit.
const maxPathLinks = 40

// mountTargetIn is where a mount for p lands, resolved the way the step sees it.
//
// **Every component, against root.** The kernel follows links in a mount
// target, and the guest is not chrooted when it binds, so a link the image
// holds is followed on the *guest*: Debian's absolute `/var/run -> /run` put
// the docker socket on the guest's /run, and an image could aim a mount at any
// directory of the machine (A3). resolveLast settles only the final component;
// a link anywhere in the path does the same damage.
//
// Each link's text is read against root and `..` is clamped at it, which is
// what a chrooted step sees. Components that do not exist yet are left as
// written, cleaned the same way, for bindMounts to make.
func mountTargetIn(root, p string) (string, error) {
	rest := strings.Split(p, "/")
	cur := root
	missing := false
	links := 0

	for len(rest) > 0 {
		part := rest[0]
		rest = rest[1:]

		switch part {
		case "", ".":
			continue
		case "..":
			if cur != root {
				cur = filepath.Dir(cur)
			}

			continue
		}

		next := filepath.Join(cur, part)
		if missing {
			cur = next

			continue
		}

		fi, err := os.Lstat(next)
		if errors.Is(err, fs.ErrNotExist) {
			missing = true
			cur = next

			continue
		}

		if err != nil {
			return "", fmt.Errorf("resolve the mount target %s: %w", p, err)
		}

		if fi.Mode()&os.ModeSymlink == 0 {
			cur = next

			continue
		}

		links++
		if links > maxPathLinks {
			return "", fmt.Errorf("the mount target %s passes more than %d symlinks, so it is a loop", p, maxPathLinks)
		}

		link, err := os.Readlink(next)
		if err != nil {
			return "", fmt.Errorf("resolve the mount target %s: %w", p, err)
		}

		if filepath.IsAbs(link) {
			cur = root
		}

		rest = append(strings.Split(link, "/"), rest...)
	}

	return cur, nil
}
