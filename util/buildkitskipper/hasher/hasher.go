// Package hasher implements deterministic hashing for build targets and their inputs to support cache keys.
package hasher

import (
	"bufio"
	"context"
	"crypto/sha1" // #nosec G505
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"strconv"
)

// Hasher provides deterministic hashing for build targets and inputs.
type Hasher struct {
	h hash.Hash
}

// New creates a new Hash instance.
func New() *Hasher {
	return &Hasher{
		h: sha1.New(), // #nosec G401
	}
}

// GetHash returns the computed hash.
func (h *Hasher) GetHash() []byte {
	if h == nil {
		return nil
	}

	return h.h.Sum(nil)
}

// HashInt hashes an integer.
func (h *Hasher) HashInt(i int) {
	h.HashBytes(fmt.Appendf(nil, "int:%d", i))
}

// HashJSONMarshalled hashes a JSON marshalled value.
func (h *Hasher) HashJSONMarshalled(v any) {
	dt, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("failed to hash command: %s", err)) // shouldn't happen
	}

	h.HashBytes(dt)
}

// HashBool hashes a boolean.
func (h *Hasher) HashBool(v bool) {
	h.HashBytes([]byte("bool:" + strconv.FormatBool(v)))
}

// HashString hashes a string.
func (h *Hasher) HashString(s string) {
	h.HashBytes([]byte("str:" + s))
}

// HashBytes hashes a byte slice.
func (h *Hasher) HashBytes(b []byte) {
	h.h.Write([]byte(strconv.Itoa(len(b))))
	h.h.Write(b)
}

// HashFile hashes a file.
func (h *Hasher) HashFile(ctx context.Context, src string) error {
	stat, err := os.Stat(src)
	if err != nil {
		return err
	}

	h.HashString(fmt.Sprintf("name: %s;", stat.Name()))
	h.HashString(fmt.Sprintf("size: %d;", stat.Size()))

	f, err := os.Open(src) // #nosec G304
	if err != nil {
		return err
	}
	defer f.Close()

	readCh := make(chan error)
	buf := make([]byte, 1024*1024)
	r := bufio.NewReader(f)

	for {
		var n int

		go func() {
			var err error

			n, err = r.Read(buf)
			readCh <- err
		}()

		select {
		case err := <-readCh:
			switch {
			case errors.Is(err, io.EOF):
				return nil
			case err != nil:
				return err
			default:
				h.h.Write(buf[:n])
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
