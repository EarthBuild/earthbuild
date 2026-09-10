//go:build linux

package exec

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

// sameFile reports whether two descriptors name one open file.
//
// Device and inode rather than the number: a descriptor that crossed a socket
// arrives under whatever number was free, and the number is the one thing about
// it that is guaranteed not to match.
func sameFile(t *testing.T, a, b *os.File) bool {
	t.Helper()

	fa, err := a.Stat()
	if err != nil {
		t.Fatal(err)
	}

	fb, err := b.Stat()
	if err != nil {
		t.Fatal(err)
	}

	return os.SameFile(fa, fb)
}

// dupOf is a factory in the shape of the real one: a *new* descriptor per
// call, naming the same open file.
//
// **The server closes what it hands over**, which it must - a copy kept here
// would outlive the build that asked and leak a socket per build - so a factory
// returning one file over and over would have it closed under the second
// caller. The real one makes a fresh packet socket each time; this makes a
// fresh descriptor.
func dupOf(t *testing.T, f *os.File) func() (*os.File, error) {
	t.Helper()

	return func() (*os.File, error) {
		fd, err := unix.Dup(int(f.Fd()))
		if err != nil {
			return nil, err
		}

		return os.NewFile(uintptr(fd), f.Name()), nil
	}
}

// listenAt starts a server on a fresh socket and returns where it is.
func listenAt(t *testing.T, make func() (*os.File, error)) string {
	t.Helper()

	// Short, because a unix socket path is 108 bytes and t.TempDir is not.
	at := filepath.Join(t.TempDir(), "fds")

	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: at, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer wg.Done()

		_ = serveNetFDs(ln, make)
	}()

	t.Cleanup(func() {
		_ = ln.Close()
		wg.Wait()
	})

	return at
}

// TestAMachineHandsOutASocketForItsTap.
//
// **The whole of why a machine can be rejoined.** A tap has no file outside its
// network namespace, so a later build cannot open one; what it can do is ask
// something already inside for a descriptor, because descriptors are not
// namespaced. That is the same crossing the engine already makes - the shim
// makes the packet socket and the engine serves it from the host's namespace -
// asked for a second time.
func TestAMachineHandsOutASocketForItsTap(t *testing.T) {
	t.Parallel()

	held, err := os.CreateTemp(t.TempDir(), "stand-in-*")
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = held.Close() }()

	at := listenAt(t, dupOf(t, held))

	got, err := DialNetFD(at)
	if err != nil {
		t.Fatalf("ask the machine for a socket on its tap: %v", err)
	}

	defer func() { _ = got.Close() }()

	if !sameFile(t, held, got) {
		t.Error("what came back is not the descriptor the machine holds")
	}
}

// TestEveryBuildGetsItsOwn. A machine outlives several builds and each of them
// serves the tap for as long as it runs, so one answer is not enough.
func TestEveryBuildGetsItsOwn(t *testing.T) {
	t.Parallel()

	held, err := os.CreateTemp(t.TempDir(), "stand-in-*")
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = held.Close() }()

	at := listenAt(t, dupOf(t, held))

	for i := range 3 {
		got, err := DialNetFD(at)
		if err != nil {
			t.Fatalf("build %d could not get a socket: %v", i+1, err)
		}

		if !sameFile(t, held, got) {
			t.Errorf("build %d was given something else", i+1)
		}

		_ = got.Close()
	}
}

// TestAMachineThatCannotMakeOneSaysSo.
//
// Reported rather than dropped, because the two look identical from the other
// end - a build that asked and got nothing cannot tell a machine that refused
// from one that has gone - and the remedy differs: the first is a defect and
// the second is a boot.
func TestAMachineThatCannotMakeOneSaysSo(t *testing.T) {
	t.Parallel()

	at := listenAt(t, func() (*os.File, error) {
		return nil, errors.New("no such device")
	})

	_, err := DialNetFD(at)
	if err == nil {
		t.Fatal("a machine that could not make a socket answered as though it had")
	}
}

// TestNoMachineThereIsAnError. Asking a socket nobody is listening on is the
// ordinary case of a machine that has stopped, and has to be an error the
// caller can act on by booting one.
func TestNoMachineThereIsAnError(t *testing.T) {
	t.Parallel()

	_, err := DialNetFD(filepath.Join(t.TempDir(), "nothing"))
	if err == nil {
		t.Fatal("dialling a machine that is not there succeeded")
	}
}
