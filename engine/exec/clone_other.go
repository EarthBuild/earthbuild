//go:build !darwin

package exec

import "errors"

// errNoClone says this platform has no whole-tree clone.
//
// Linux has `FICLONE`, which reflinks one file on btrfs or XFS and has no
// directory form, so there is nothing here to be gained over hard links - which
// on a Linux host are already cheap, the store not being reached through a
// virtiofs share.
var errNoClone = errors.New("this platform has no directory clone")

func cloneTree(_, _ string) error { return errNoClone }
