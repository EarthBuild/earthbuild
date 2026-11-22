//go:build linux

package guest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// stepNetShimFlag turns this binary into the helper that holds a step's network
// namespace open while the agent furnishes it.
const stepNetShimFlag = "--step-net-shim"

// **A child, because a thread that moves never reliably comes back.** An
// earlier version of this did the work in the agent: lock an OS thread,
// `unshare` or `setns` into the step's namespace, configure the interface, and
// return the thread with a deferred `setns`. Every part of that is a hazard.
// A failed return leaves a thread in the runtime's pool still inside a step's
// namespace, and every goroutine later scheduled on it does its networking
// there; and Go's fork/exec inherits the namespaces of whichever thread
// performs it, so a step could be launched into the wrong network without
// anything failing. Both are silent and intermittent.
//
// A child process has neither problem: its threads die with it. The agent
// never changes namespace at all - it addresses the child's namespace by pid
// (see macvlanMessage) and binds the child's own /proc entry to keep the
// namespace alive after it exits.
//
// This is the pattern the repository already uses three times over -
// NetShimCommand for the host's tap, daemonshim for dockerd, stepshim for a
// step - and this code should have followed it rather than inventing a more
// delicate one.

// stepNetReady and stepNetDone are the two words of the handshake, one each
// way, so neither side has to poll for the other's progress.
const (
	stepNetReady = "ready\n"
	stepNetDone  = "done\n"
)

// RunStepNetShimIfAsked turns this process into a step-network shim when its
// argv says so, and never returns if it does.
//
// Called first thing in main, like the other shims, and for the same reason:
// Go cannot run code between clone and exec, so the namespace is entered by
// re-executing this binary with the flag on SysProcAttr.
func RunStepNetShimIfAsked() {
	if len(os.Args) < 3 || os.Args[1] != stepNetShimFlag {
		return
	}

	err := stepNetShim(os.Args[2])
	if err != nil {
		fmt.Fprintf(os.Stderr, "earth-guestd %s: %v\n", stepNetShimFlag, err)
		os.Exit(1)
	}

	os.Exit(0)
}

// stepNetShim runs in the child, already inside a fresh network namespace.
//
// It says so, waits for the agent to put an interface in, configures it, and
// exits. The exit is not a loose end: the agent has by then bound this
// process's namespace to a file, which is what keeps it alive.
func stepNetShim(spec string) error {
	var n VMStepNet

	err := json.Unmarshal([]byte(spec), &n)
	if err != nil {
		return fmt.Errorf("read the step's network: %w", err)
	}

	// fd 3 is the pipe back to the agent. stdout is not used, deliberately:
	// in a guest the agent's stdout is the protocol channel, and a shim that
	// wrote there would corrupt it.
	back := os.NewFile(3, "handshake")
	if back == nil {
		return fmt.Errorf("no handshake pipe on fd 3")
	}

	_, err = io.WriteString(back, stepNetReady)
	if err != nil {
		return fmt.Errorf("say the namespace is ready: %w", err)
	}

	// The agent replies when the interface is in place. A read rather than a
	// sleep: the interface appears when it appears.
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return fmt.Errorf("wait for the agent to furnish the namespace: %w", err)
	}

	if line != stepNetDone {
		return fmt.Errorf("the agent said %q, which is not %q", line, stepNetDone)
	}

	// Already in the right namespace, so these are plain ioctls with nothing to
	// enter and nothing to leave.
	err = addressLink(n)
	if err != nil {
		return err
	}

	// A nested daemon mostly talks to itself, and without loopback it finds
	// nothing listening - which reads as the daemon never starting.
	err = bringUpByName("lo")
	if err != nil {
		return fmt.Errorf("bring loopback up: %w", err)
	}

	err = addDefaultRoute(n.Gateway, n.Link)
	if err != nil {
		return err
	}

	_, err = io.WriteString(back, stepNetDone)

	return err
}

// buildStepNet makes a step's network without this process ever changing
// namespace, and returns the path holding the namespace open.
func buildStepNet(n VMStepNet, parent, at string) (err error) {
	spec, err := json.Marshal(n)
	if err != nil {
		return fmt.Errorf("describe the step's network: %w", err)
	}

	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find this binary: %w", err)
	}

	theirs, mine, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("make a handshake pipe: %w", err)
	}

	defer func() { _ = theirs.Close() }()

	toChild, fromParent, err := os.Pipe()
	if err != nil {
		_ = mine.Close()

		return fmt.Errorf("make a handshake pipe: %w", err)
	}

	//nolint:gosec // this binary, with a flag it defines
	cmd := osexec.Command(self, stepNetShimFlag, string(spec))
	cmd.Stdin = toChild
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{mine}
	// The child is born in a network namespace of its own. Go cannot run code
	// between clone and exec, which is exactly why this is a separate process.
	cmd.SysProcAttr = &syscall.SysProcAttr{Unshareflags: unix.CLONE_NEWNET}

	err = cmd.Start()

	_ = mine.Close()
	_ = toChild.Close()

	if err != nil {
		_ = fromParent.Close()

		return fmt.Errorf("start the step-network shim: %w", err)
	}

	defer func() {
		_ = fromParent.Close()

		waitErr := cmd.Wait()
		if waitErr != nil && err == nil {
			err = fmt.Errorf("the step-network shim failed: %w", waitErr)
		}
	}()

	replies := bufio.NewReader(theirs)

	line, err := replies.ReadString('\n')
	if err != nil || line != stepNetReady {
		return fmt.Errorf("the shim did not reach its own namespace: %q %w", line, err)
	}

	// **Both of these happen here, in the agent, without entering anything.**
	// The interface is created directly into the child's namespace by pid, and
	// the child's own /proc entry is bound to a file so the namespace outlives
	// it.
	err = addMacvlan(n, parent, cmd.Process.Pid)
	if err != nil {
		return err
	}

	err = bindNetns(cmd.Process.Pid, at)
	if err != nil {
		return err
	}

	_, err = io.WriteString(fromParent, stepNetDone)
	if err != nil {
		return fmt.Errorf("tell the shim to configure: %w", err)
	}

	line, err = replies.ReadString('\n')
	if err != nil || line != stepNetDone {
		return fmt.Errorf("the shim did not configure the namespace: %q %w", line, err)
	}

	return nil
}

// bindNetns keeps a process's network namespace alive past its exit.
//
// The same trick `ip netns add` uses: a namespace lives while something holds
// it, and a bind mount is a something. Done from here rather than in the child
// because the child is the one that is about to exit.
func bindNetns(pid int, at string) error {
	err := os.MkdirAll(filepath.Dir(at), 0o755)
	if err != nil {
		return fmt.Errorf("make room for %s: %w", at, err)
	}

	f, err := os.OpenFile(at, os.O_RDONLY|os.O_CREATE|os.O_EXCL, 0o444)
	if err != nil {
		return fmt.Errorf("claim %s for a network namespace: %w", at, err)
	}

	_ = f.Close()

	err = unix.Mount(fmt.Sprintf("/proc/%d/ns/net", pid), at, "none", unix.MS_BIND, "")
	if err != nil {
		_ = os.Remove(at)

		return fmt.Errorf("keep the network namespace at %s: %w", at, err)
	}

	return nil
}
