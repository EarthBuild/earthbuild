// Command vsockcheck reads the counting stream a microVM sends and says where,
// if anywhere, the transport stopped telling the truth.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

func main() {
	uds := flag.String("uds", "", "firecracker's vsock unix socket")
	port := flag.Int("port", 1234, "the guest port")
	slow := flag.Duration("slow", 0, "pause this long every -every bytes, to hold the transport up")
	every := flag.Int("every", 1<<20, "how often to pause")
	flag.Parse()

	err := check(*uds, *port, *slow, *every)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vsockcheck: %v\n", err)
		os.Exit(1)
	}
}

func check(uds string, port int, slow time.Duration, every int) error {
	c, err := net.Dial("unix", uds)
	if err != nil {
		return fmt.Errorf("dial %s: %w", uds, err)
	}

	defer func() { _ = c.Close() }()

	_, err = fmt.Fprintf(c, "CONNECT %d\n", port)
	if err != nil {
		return fmt.Errorf("CONNECT: %w", err)
	}

	// A byte at a time: anything read past the newline is the guest's stream.
	var line []byte

	for {
		var b [1]byte

		_, err = io.ReadFull(c, b[:])
		if err != nil {
			return fmt.Errorf("greeting: %w", err)
		}

		if b[0] == '\n' {
			break
		}

		line = append(line, b[0])
	}

	if len(line) < 2 || string(line[:2]) != "OK" {
		return fmt.Errorf("firecracker said %q", line)
	}

	buf := make([]byte, 256<<10)

	var (
		at     int
		faults int
		since  int
	)

	for {
		n, readErr := c.Read(buf)

		for i := 0; i+8 <= n; i += 8 {
			// Only whole counters at their own alignment are checked, so the
			// read boundary does not matter.
			if (at+i)%8 != 0 {
				continue
			}

			want := uint64(at+i) / 8
			got := binary.LittleEndian.Uint64(buf[i : i+8])

			if got != want {
				faults++
				fmt.Printf("FAULT at byte %d: counter %d, expected %d"+
					"  (stream is %+d bytes off)\n",
					at+i, got, want, (int64(got)-int64(want))*8)

				if faults > 8 {
					return fmt.Errorf("%d faults; stopping", faults)
				}

				// Re-base so one displacement is not reported for every
				// remaining counter in the stream.
				at = int(got)*8 - i
			}
		}

		at += n
		since += n

		if slow > 0 && since >= every {
			since = 0

			time.Sleep(slow)
		}

		if readErr != nil {
			if readErr == io.EOF {
				fmt.Printf("clean: %d bytes, %d faults\n", at, faults)

				return nil
			}

			return fmt.Errorf("read at %d: %w", at, readErr)
		}
	}
}
