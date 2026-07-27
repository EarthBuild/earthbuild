package statsstreamparser

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/containerd/go-runc"
)

const maxPayloadSize = 10 * 1024 * 1024 // 10 MB

// Parser parses stream data containing execution statistics.
type Parser struct {
	// buf accumulates incoming stream data across multiple Parse calls.
	buf *bytes.Buffer
	// hasReadVersion is true if the protocol version byte for the current packet
	// has been read and validated, but the stats payload is still pending.
	hasReadVersion bool
}

// New creates a new parser instance.
func New() *Parser {
	return &Parser{
		buf: bytes.NewBuffer(nil),
	}
}

// Parse parses stream data containing execution statistics.
func (p *Parser) Parse(b []byte) ([]*runc.Stats, error) {
	errorf := func(format string, args ...any) (stats []*runc.Stats, err error) {
		return nil, fmt.Errorf("stats stream parser: "+format, args...)
	}

	_, err := p.buf.Write(b)
	if err != nil {
		return errorf("write to buf: ", err)
	}

	var stats []*runc.Stats

loop:
	for {
		if !p.hasReadVersion {
			var protocolVersion byte

			protocolVersion, err = p.buf.ReadByte()
			switch {
			case errors.Is(err, io.EOF):
				break loop
			case err != nil:
				return errorf("read protocol version: ", err)
			case protocolVersion != 1:
				return errorf("unexpected protocol version %d", protocolVersion)
			}

			p.hasReadVersion = true
		}

		lenBytes, err := p.buf.Peek(4)
		switch {
		case errors.Is(err, io.EOF):
			break loop
		case err != nil:
			return errorf("peek length: ", err)
		}

		n := int(binary.LittleEndian.Uint32(lenBytes))
		if n < 0 || n > maxPayloadSize {
			return errorf("payload length exceeds %d bytes: %d", maxPayloadSize, n)
		}

		statsBytes, err := p.buf.Peek(4 + n)
		switch {
		case errors.Is(err, io.EOF):
			break loop
		case err != nil:
			return errorf("peek payload: ", err)
		}

		p.buf.Next(4 + n)

		var runcStat runc.Stats

		err = json.Unmarshal(statsBytes[4:], &runcStat)
		if err != nil {
			return errorf("unmarshal stats: %w", err)
		}

		stats = append(stats, &runcStat)
		p.hasReadVersion = false
	}

	return stats, nil
}
