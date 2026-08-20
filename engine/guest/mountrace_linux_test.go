//go:build linux

package guest_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/nstest"
)

// Two steps on one root do not fight over the mounts they share.
//
// Every step binds the devices and `/etc/resolv.conf` into its root and pops
// them again when it ends. Two steps on **one** root bind the same targets, and
// `unmountAll`'s own comment says what that means: *"a bind mount is a stack,
// not a flag"*. If one step's teardown pops the stack while another is between
// its bind and the read-only remount, the remount names something that is no
// longer a mount point - and EINVAL is what the kernel answers.
//
// That is the standing hypothesis for E171a's flake:
//
//	make /etc/resolv.conf read-only: invalid argument
//
// once in four whole-tree runs, never in isolation. This is the attempt to make
// it happen on purpose, which is what has to come before a fix - E172 spent an
// iteration on a mechanism that explained everything and was not true.
func TestTwoStepsOnOneRootDoNotFightOverTheirMounts(t *testing.T) {
	if !nstest.In(t) {
		return
	}

	root := stepRoot(t)
	c := pairWith(t, &guest.Server{Mat: &fixedRootMat{root: root}, Unconfined: true})

	h, err := c.Materialise(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = h.Release() })

	const (
		rounds   = 200
		perRound = 6
	)

	var failures []string

	var mu sync.Mutex

	for range rounds {
		var wg sync.WaitGroup

		for range perRound {
			wg.Go(func() {
				_, _, err := c.Exec(context.Background(), h, []string{testTrue}, nil)
				if err != nil {
					mu.Lock()
					failures = append(failures, err.Error())
					mu.Unlock()
				}
			})
		}

		wg.Wait()
	}

	// The mount path has to have run, or this measures nothing. `ensureFile`
	// creates the target for the resolver bind and leaves it behind when the
	// mount is popped, so its presence is the evidence that binds happened.
	if _, err := os.Lstat(filepath.Join(root, "etc", "resolv.conf")); err != nil {
		t.Fatalf("no resolver mount point was ever made, so no bind ran and this"+
			" test says nothing about sharing them: %v", err)
	}

	if len(failures) != 0 {
		t.Errorf("%d of %d steps failed while sharing a root; first: %s",
			len(failures), rounds*perRound, strings.SplitN(failures[0], "\n", 2)[0])
	}
}
