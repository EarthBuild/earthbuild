// Package circbuf provides an in-memory circular buffer implementation to bound the size of captured log streams.
package circbuf

import (
	"errors"
	"slices"
)

// ErrInvalidSize is returned when initializing a Buffer with a non-positive max size.
var ErrInvalidSize = errors.New("circbuf: size must be a positive int")

// Buffer represents a dynamic circular (ring) buffer of fixed maximum size.
type Buffer struct {
	buf []byte
	off int
}

// NewBuffer creates and returns a Buffer with a max size.
func NewBuffer(size int) (*Buffer, error) {
	if size <= 0 {
		return nil, ErrInvalidSize
	}

	return &Buffer{
		buf: make([]byte, 0, size),
	}, nil
}

// Write implements io.Writer.
func (b *Buffer) Write(p []byte) (int, error) {
	n := len(p)
	max := cap(b.buf)

	if max <= 0 {
		return n, nil
	}

	// 1. Fast-path: Write exceeds or equals total buffer size.
	if n >= max {
		b.buf = b.buf[:max]
		b.off = 0
		copy(b.buf, p[n-max:])

		return n, nil
	}

	// 2. Growing phase: Buffer has not yet reached capacity.
	if free := max - len(b.buf); free > 0 {
		if n <= free {
			m := len(b.buf)
			b.buf = b.buf[:m+n]
			copy(b.buf[m:], p)

			return n, nil
		}

		m := len(b.buf)
		b.buf = b.buf[:max]
		copy(b.buf[m:], p[:free])
		p = p[free:]
		b.off = 0
		// Buffer is now full; fall through to full ring phase for remaining bytes in p.
	}

	// 3. Full ring phase: Copy incoming data in circular fashion.
	rem := max - b.off
	copy(b.buf[b.off:], p)

	if len(p) > rem {
		copy(b.buf, p[rem:])
	}

	b.off += len(p)
	if b.off >= max {
		b.off -= max
	}

	return n, nil
}

// Bytes returns a copy of the buffer contents, with the oldest contents at the
// beginning.
func (b *Buffer) Bytes() []byte {
	max := cap(b.buf)
	if max == 0 || len(b.buf) < max {
		return slices.Clone(b.buf)
	}

	out := make([]byte, max)
	copy(out, b.buf[b.off:])
	copy(out[max-b.off:], b.buf[:b.off])

	return out
}


