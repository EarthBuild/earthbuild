package guest

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// chownIDs resolves a `--chown` specification against the destination image.
//
// **Against the image, never this machine.** `COPY --chown=testuser:testgroup`
// means the user that image has. Resolving it against the guest's own passwd
// file would give a different machine's answer - usually a different number,
// sometimes no user at all - and produce an image whose files belong to somebody
// who does not exist in it. A3 says a step cannot reach the guest's filesystem,
// and a lookup made on its behalf may not either.
//
// `user`, `user:group`, and either part numeric. A user alone takes that user's
// own group, which is what `chown` does and what the author of `--chown=www-data`
// means.
func chownIDs(root, spec string) (int, int, error) {
	user, group, hasGroup := strings.Cut(spec, ":")

	uid, primary, err := lookUpUser(root, user)
	if err != nil {
		return 0, 0, err
	}

	if !hasGroup || group == "" {
		return uid, primary, nil
	}

	gid, err := lookUpGroup(root, group)
	if err != nil {
		return 0, 0, err
	}

	return uid, gid, nil
}

// lookUpUser finds a user's id and primary group in the image's passwd file.
func lookUpUser(root, user string) (int, int, error) {
	if n, err := strconv.Atoi(user); err == nil {
		// A number is an id, and the group is the same number unless one was
		// given - `chown 1000 file` behaves this way and a build that meant
		// otherwise can say so.
		return n, n, nil
	}

	at := filepath.Join(root, "etc", "passwd")

	fields, err := findEntry(at, user, 7)
	if err != nil {
		return 0, 0, fmt.Errorf("--chown=%s: %w", user, err)
	}

	uid, err := strconv.Atoi(fields[2])
	if err != nil {
		return 0, 0, fmt.Errorf("--chown=%s: /etc/passwd gives it the id %q, which is not a number",
			user, fields[2])
	}

	gid, err := strconv.Atoi(fields[3])
	if err != nil {
		return 0, 0, fmt.Errorf("--chown=%s: /etc/passwd gives it the group %q, which is not a number",
			user, fields[3])
	}

	return uid, gid, nil
}

// lookUpGroup finds a group's id in the image's group file.
func lookUpGroup(root, group string) (int, error) {
	if n, err := strconv.Atoi(group); err == nil {
		return n, nil
	}

	at := filepath.Join(root, "etc", "group")

	fields, err := findEntry(at, group, 4)
	if err != nil {
		return 0, fmt.Errorf("--chown=:%s: %w", group, err)
	}

	gid, err := strconv.Atoi(fields[2])
	if err != nil {
		return 0, fmt.Errorf("--chown=:%s: /etc/group gives it the id %q, which is not a number",
			group, fields[2])
	}

	return gid, nil
}

// findEntry reads a colon-separated database and returns the named row.
//
// The file that was read is named in every failure, because the answer is
// usually "the base image does not have that user" and nothing in the Earthfile
// says which image that is.
func findEntry(at, name string, want int) ([]string, error) {
	b, err := os.ReadFile(at) //nolint:gosec // a path inside the step's own root
	if err != nil {
		return nil, fmt.Errorf("this image has no %s to look %q up in: %w",
			strings.TrimPrefix(at, filepath.Dir(filepath.Dir(at))), name, err)
	}

	for line := range strings.SplitSeq(string(b), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < want || fields[0] != name {
			continue
		}

		return fields, nil
	}

	return nil, fmt.Errorf("this image's %s has no %q",
		strings.TrimPrefix(at, filepath.Dir(filepath.Dir(at))), name)
}
