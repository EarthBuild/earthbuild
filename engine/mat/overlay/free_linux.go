package overlay

import "golang.org/x/sys/unix"

// freeOn is the room left on the filesystem holding a path.
//
// A copy of what `engine/store` has, because that package imports this one and
// the dependency cannot run both ways. Two statfs calls are not worth a package
// to share.
func freeOn(path string) (uint64, error) {
	var st unix.Statfs_t

	err := unix.Statfs(path, &st)
	if err != nil {
		return 0, err
	}

	//nolint:unconvert // Bavail is uint64 on some arches and int64 on others
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
