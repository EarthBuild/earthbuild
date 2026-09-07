//go:build unix

// Package fdpass moves an open file between processes.
//
// A descriptor is not a number that can be sent as one: it indexes a table
// private to a process, so the kernel has to be asked to install the same open
// file in another process's table. `SCM_RIGHTS` over an `AF_UNIX` socket is that
// request.
//
// Three callers now, which is why this is its own package rather than a file in
// the one that needed it first: a terminal reaching a step (E190), the guest's
// own channel, and a seccomp listener created *inside* a process that is about
// to exec - the last of which cannot hand it back any other way, because the
// process that owns the descriptor is gone by the time the step is running.
package fdpass

import (
	"errors"
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// ErrNoDescriptorChannel is what a connection that cannot carry a descriptor
// says.
//
// Named rather than left as a type assertion failure, because the caller's next
// question is always "then what can I do" and the answer depends on which
// connection this is.
var ErrNoDescriptorChannel = errors.New("this connection carries bytes, not descriptors")

// SocketPair is a connected pair that can carry descriptors.
//
// `net.Pipe` is in-process and an OS pipe is bytes; neither can carry an open
// file. A unix socketpair can, through SCM_RIGHTS, and that is what lets a step
// hold *the* terminal rather than a relay of one - job control, window size,
// raw mode and `isatty` all come from the descriptor and none of them survive
// being copied through a byte stream.
//
// It also cannot cross a machine, which is why `RUN --interactive` is accepted
// only when the driver and the workers are on one host: the restriction is the
// mechanism, not a policy laid over it.
func SocketPair() (here, there *net.UnixConn, err error) {
	// SOCK_CLOEXEC is not a socketpair flag on darwin, so close-on-exec is set
	// afterwards rather than asked for: portable, and the window between the
	// two is this function.
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("socketpair: %w", err)
	}

	for _, fd := range fds {
		unix.CloseOnExec(fd)
	}

	conns := make([]*net.UnixConn, 0, 2)

	for _, fd := range fds {
		f := os.NewFile(uintptr(fd), "socketpair")

		c, err := net.FileConn(f)

		// FileConn dups, so this end is finished with either way.
		_ = f.Close()

		if err != nil {
			for _, made := range conns {
				_ = made.Close()
			}

			return nil, nil, fmt.Errorf("socketpair as a connection: %w", err)
		}

		uc, ok := c.(*net.UnixConn)
		if !ok {
			_ = c.Close()

			return nil, nil, ErrNoDescriptorChannel
		}

		conns = append(conns, uc)
	}

	return conns[0], conns[1], nil
}

// SendFile hands an open file to the other end.
//
// One byte of ordinary payload travels with it, because a control message with
// no data is permitted to be dropped: the byte is what guarantees the recvmsg
// on the other side has something to return.
func SendFile(c net.Conn, f *os.File) error {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("send a descriptor: %w", ErrNoDescriptorChannel)
	}

	rights := unix.UnixRights(int(f.Fd()))

	_, _, err := uc.WriteMsgUnix([]byte{0}, rights, nil)
	if err != nil {
		return fmt.Errorf("send a descriptor: %w", err)
	}

	return nil
}

// RecvFile takes a descriptor sent by SendFile.
//
// The returned file is this process's own: the kernel installs a new descriptor
// referring to the same open file, so closing it here does not close the
// sender's.
func RecvFile(c net.Conn) (*os.File, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return nil, fmt.Errorf("receive a descriptor: %w", ErrNoDescriptorChannel)
	}

	// Room for one right, and no more: a message carrying several is not
	// something this protocol sends, and accepting one would leak every
	// descriptor after the first.
	oob := make([]byte, unix.CmsgSpace(4))
	buf := make([]byte, 1)

	_, oobn, _, _, err := uc.ReadMsgUnix(buf, oob)
	if err != nil {
		return nil, fmt.Errorf("receive a descriptor: %w", err)
	}

	msgs, err := unix.ParseSocketControlMessage(oob[:oobn])
	if err != nil {
		return nil, fmt.Errorf("parse the control message: %w", err)
	}

	if len(msgs) != 1 {
		return nil, fmt.Errorf("expected one control message, found %d: %w",
			len(msgs), ErrNoDescriptorChannel)
	}

	fds, err := unix.ParseUnixRights(&msgs[0])
	if err != nil {
		return nil, fmt.Errorf("parse the descriptor: %w", err)
	}

	if len(fds) != 1 {
		for _, fd := range fds {
			_ = unix.Close(fd)
		}

		return nil, fmt.Errorf("expected one descriptor, found %d: %w",
			len(fds), ErrNoDescriptorChannel)
	}

	return os.NewFile(uintptr(fds[0]), "passed"), nil
}

// ConnFromFD turns an inherited descriptor back into a connection.
//
// The guest is a separate program and receives its channel the way it receives
// the id gate: an extra descriptor on a known number, inherited across exec.
// This is the other side of that - the number, back to something that can carry
// a terminal.
//
// The file is closed here rather than returned: `net.FileConn` duplicates the
// descriptor, so keeping the original open would leave the guest holding two
// references to one socket and the far end waiting for a close that never
// comes.
func ConnFromFD(fd int) (*net.UnixConn, error) {
	f := os.NewFile(uintptr(fd), "descriptor channel")
	if f == nil {
		return nil, fmt.Errorf("fd %d is not open: %w", fd, ErrNoDescriptorChannel)
	}

	c, err := net.FileConn(f)

	_ = f.Close()

	if err != nil {
		return nil, fmt.Errorf("fd %d as a connection: %w", fd, err)
	}

	uc, ok := c.(*net.UnixConn)
	if !ok {
		_ = c.Close()

		return nil, fmt.Errorf("fd %d is not a unix socket: %w", fd, ErrNoDescriptorChannel)
	}

	return uc, nil
}
