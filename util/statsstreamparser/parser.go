package statsstreamparser

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"

	"github.com/containerd/go-runc"
)

// Parser parses stream data containing execution statistics.
type Parser struct {
	buf                 *bytes.Buffer
	readProtocolVersion bool
}

// New creates a new parser instance.
func New() *Parser {
	return &Parser{
		buf: bytes.NewBuffer(nil),
	}
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
			if ssp.buf.Len() < 1 {
				break
			}
			protocolVersion, _ := ssp.buf.ReadByte()
			if protocolVersion != 1 {
				return nil, fmt.Errorf("unexpected stats stream protocol version %d", protocolVersion)
			}

			ssp.readProtocolVersion = true
		}

		if ssp.buf.Len() < 4 {
			break
		}
		length := binary.LittleEndian.Uint32(ssp.buf.Bytes()[:4])
		if ssp.buf.Len() < 4+int(length) {
			break
		}

		ssp.buf.Next(4)
		statsStreamBytes := ssp.buf.Next(int(length))

		var runcStat runc.Stats

		err = json.Unmarshal(statsStreamBytes, &runcStat)
		if err != nil {
			return nil, err
		}

		stats = append(stats, &runcStat)
		ssp.readProtocolVersion = false
	}

	return stats, nil
}
