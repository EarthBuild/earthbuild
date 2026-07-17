package statsstreamparser

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/alexcb/binarystream"
	"github.com/containerd/go-runc"
)

// Parser parses stream data containing execution statistics.
type Parser struct {
	buf                 *bytes.Buffer
	bsr                 *binarystream.BinaryStream
	readProtocolVersion bool
}

// New creates a new parser instance.
func New() *Parser {
	buf := bytes.NewBuffer(nil)

	return &Parser{
		buf: buf,
		bsr: binarystream.NewReader(buf, binary.LittleEndian),
	}
}

// Reset discards any buffered partial frame and returns the parser to its
// initial state. Used to recover from a desynced or malformed stats stream
// (e.g. when the daemon's runc stats collector hits EOF and emits a partial
// or raw frame) without treating the decode failure as fatal.
func (ssp *Parser) Reset() {
	ssp.buf.Reset()
	ssp.bsr = binarystream.NewReader(ssp.buf, binary.LittleEndian)
	ssp.readProtocolVersion = false
}

// Parse parses stream data containing execution statistics.
func (ssp *Parser) Parse(b []byte) ([]*runc.Stats, error) {
	_, err := ssp.buf.Write(b)
	if err != nil {
		return nil, err
	}

	var stats []*runc.Stats

	for {
		if !ssp.readProtocolVersion {
			protocolVersion, err := ssp.bsr.ReadUint8()
			if err != nil {
				if errors.Is(err, binarystream.ErrBufferUnderflow) {
					break
				}

				return nil, err
			}

			if protocolVersion != 1 {
				return nil, fmt.Errorf("unexpected stats stream protocol version %d", protocolVersion)
			}

			ssp.readProtocolVersion = true
		}

		statsStreamJSON, err := ssp.bsr.ReadUint32PrefixedString()
		if err != nil {
			if errors.Is(err, binarystream.ErrBufferUnderflow) {
				break
			}

			return nil, err
		}

		var runcStat runc.Stats

		err = json.Unmarshal([]byte(statsStreamJSON), &runcStat)
		if err != nil {
			return nil, err
		}

		stats = append(stats, &runcStat)
		ssp.readProtocolVersion = false
	}

	return stats, nil
}
