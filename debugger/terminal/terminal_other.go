//go:build !windows

package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/term"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/debugger/common"
)

func handlePtyData(data []byte) error {
	_, err := os.Stdout.Write(data)
	if err != nil {
		return fmt.Errorf("failed to write data to stdout: %w", err)
	}

	return nil
}

func getWindowSizePayload() ([]byte, error) {
	size, err := pty.GetsizeFull(os.Stdin)
	if err != nil {
		return nil, err
	}

	b, err := json.Marshal(size)
	if err != nil {
		return nil, err
	}

	return common.SerializeDataPacket(common.WinSizeData, b)
}

// ConnectTerm presents a terminal to the shell repeater.
func ConnectTerm(ctx context.Context, conn io.ReadWriteCloser, log *conslogging.ConsoleLogger) error {
	sigs := make(chan os.Signal, 10)
	signal.Notify(sigs, syscall.SIGWINCH)

	writeCh := make(chan []byte, 10)

	ctx, cancel := context.WithCancel(ctx)

	ts := &termState{}

	go func() {
	outer:
		for {
			connDataType, data, err := common.ReadDataPacket(conn)
			if err != nil {
				if !errors.Is(err, io.EOF) {
					log.VerbosePrintf("ReadDataPacket failed: %s\n", err.Error())
				}

				break
			}

			switch connDataType {
			case common.StartShellSession:
				log.VerbosePrintf("starting new interactive shell pseudo terminal\n")

				err := ts.makeRaw()
				if err != nil {
					log.VerbosePrintf("makeRaw failed: %s\n", err.Error())
					break outer
				}

				sigs <- syscall.SIGWINCH
			case common.EndShellSession:
				err := ts.restore()
				if err != nil {
					log.VerbosePrintf("restore failed: %s\n", err.Error())
					break outer
				}
			case common.PtyData:
				err := handlePtyData(data)
				if err != nil {
					log.VerbosePrintf("handlePtyData failed: %s\n", err.Error())
					break outer
				}
			default:
				log.VerbosePrintf("unhandled terminal data type: %d\n", connDataType)
				break outer
			}
		}

		cancel()
	}()

	go func() {
		for range sigs {
			if len(sigs) > 0 {
				continue
			}

			data, err := getWindowSizePayload()
			if err != nil {
				log.VerbosePrintf("failed to get window size payload: %s\n", err.Error())
				break
			}

			writeCh <- data
		}

		cancel()
	}()

	go func() {
		for {
			buf := <-writeCh

			_, err := conn.Write(buf)
			if err != nil {
				log.VerbosePrintf("failed to send term data to shell: %s\n", err.Error())
				break
			}
		}

		cancel()
	}()
	go func() {
		for {
			buf := make([]byte, 100)

			n, err := os.Stdin.Read(buf)
			if err != nil {
				log.VerbosePrintf("failed to read from stdin: %s\n", err.Error())
				break
			}

			buf = buf[:n]

			buf2, err := common.SerializeDataPacket(common.PtyData, buf)
			if err != nil {
				log.VerbosePrintf("failed to serialize data: %s\n", err.Error())
				break
			}

			writeCh <- buf2
		}

		cancel()
	}()

	<-ctx.Done()

	log.VerbosePrintf("exiting interactive debugger shell\n")

	err := ts.restore()
	if err != nil {
		return err
	}

	return nil
}

type termState struct {
	oldState *term.State
	mu       sync.Mutex
}

func (ts *termState) makeRaw() error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.oldState == nil {
		var err error

		// #nosec G115 - Fd() returns a small int
		ts.oldState, err = term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			return fmt.Errorf("failed to initialize terminal in raw mode: %w", err)
		}
	}

	return nil
}

func (ts *termState) restore() error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.oldState != nil {
		// #nosec G115 - Fd() returns a small int
		err := term.Restore(int(os.Stdin.Fd()), ts.oldState)
		if err != nil {
			return fmt.Errorf("failed to restore terminal mode: %w", err)
		}

		ts.oldState = nil
	}

	return nil
}
