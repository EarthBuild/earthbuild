//go:build linux

// Command vsockblast is PID 1 of a microVM that says a known thing very fast.
//
// It writes a counting stream to a vsock connection: the little-endian uint64
// at every eight-byte offset is that offset divided by eight. Any byte the
// transport loses, repeats or reorders shows up as a counter that is not the
// one the reader was expecting, and the difference says by how much and in
// which direction.
package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

const (
	port  = 1234
	total = 64 << 20
)

// chunk is the size of a single Write, set at link time so one source builds
// every arm of the sweep: -ldflags "-X main.chunkText=32768".
var chunkText = "1048576"

func main() {
	err := blast()
	if err != nil {
		fmt.Fprintf(os.Stderr, "vsockblast: %v\n", err)
	}

	fmt.Fprintf(os.Stderr, "vsockblast: done\n")

	// PID 1 returning is a kernel panic, so stop the machine instead.
	_ = unix.Reboot(unix.LINUX_REBOOT_CMD_RESTART)
}

func blast() error {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return fmt.Errorf("vsock socket: %w", err)
	}

	err = unix.Bind(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_ANY, Port: port})
	if err != nil {
		return fmt.Errorf("bind port %d: %w", port, err)
	}

	err = unix.Listen(fd, 1)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	fmt.Fprintf(os.Stderr, "vsockblast: listening on %d\n", port)

	nfd, _, err := unix.Accept(fd)
	if err != nil {
		return fmt.Errorf("accept: %w", err)
	}

	c := os.NewFile(uintptr(nfd), "vsock")
	defer func() { _ = c.Close() }()

	chunk, err := strconv.Atoi(chunkText)
	if err != nil || chunk <= 0 || chunk%8 != 0 {
		return fmt.Errorf("chunk %q must be a positive multiple of eight", chunkText)
	}

	fmt.Fprintf(os.Stderr, "vsockblast: chunk %d\n", chunk)

	buf := make([]byte, chunk)

	// One Write per chunk, because the question is whether a single large write
	// survives: the engine sends a step's observation the same way.
	for at := 0; at < total; at += chunk {
		for i := 0; i < chunk; i += 8 {
			binary.LittleEndian.PutUint64(buf[i:], uint64(at+i)/8)
		}

		_, err = c.Write(buf)
		if err != nil {
			return fmt.Errorf("write at %d: %w", at, err)
		}
	}

	fmt.Fprintf(os.Stderr, "vsockblast: wrote %d bytes\n", total)

	return nil
}
