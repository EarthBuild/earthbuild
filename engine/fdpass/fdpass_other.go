//go:build !unix

// Package fdpass moves an open file between processes.
//
// Not here it does not. `SCM_RIGHTS` over an `AF_UNIX` socket is a POSIX
// mechanism, and the platforms without it have no equivalent this package could
// present under the same names - so every entry point refuses rather than
// pretending.
//
// **This file exists because the engine cross-compiles.** It did not, silently:
// `GOOS` never reached the toolchain, so every platform's binary was built for
// the machine doing the building and this package was never compiled anywhere it
// does not work. Fixing that produced `undefined: unix.Socketpair` from a
// windows build, which is this package's first honest word on the subject
// (E580, E581).
package fdpass

import (
	"errors"
	"net"
	"os"
)

// errUnsupported is the one answer this platform has.
//
// A sentence rather than a code, because the caller cannot fix it and the reader
// wants to know why a build that compiled has a feature that will not start.
var errUnsupported = errors.New(
	"passing an open file between processes needs SCM_RIGHTS over an AF_UNIX socket," +
		" which this platform does not have")

// ErrNoDescriptorChannel is what a connection that cannot carry a descriptor
// says. Here that is every connection.
//
// Declared on both sides of the tag because callers compare against it, and a
// sentinel that exists on one platform is a compile error on the other rather
// than a branch nobody takes.
var ErrNoDescriptorChannel = errUnsupported

// SocketPair is unsupported here.
func SocketPair() (here, there *net.UnixConn, err error) { return nil, nil, errUnsupported }

// SendFile is unsupported here.
func SendFile(net.Conn, *os.File) error { return errUnsupported }

// RecvFile is unsupported here.
func RecvFile(net.Conn) (*os.File, error) { return nil, errUnsupported }

// ConnFromFD is unsupported here.
func ConnFromFD(int) (*net.UnixConn, error) { return nil, errUnsupported }
