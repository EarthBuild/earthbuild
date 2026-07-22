package statsstreamparser

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestParser_Parse(t *testing.T) {
	t.Parallel()

	makePacket := func(version uint8, data string) []byte {
		n := len(data)
		if n < 0 || int64(n) > math.MaxUint32 {
			panic("data too large")
		}

		buf := make([]byte, 1+4+n)
		buf[0] = version
		binary.LittleEndian.PutUint32(buf[1:5], uint32(n))
		copy(buf[5:], data)

		return buf
	}

	t.Run("valid stream", func(t *testing.T) {
		t.Parallel()

		parser := New()

		// Serialize two stats payloads
		payload1 := `{"cpu":{"usage":{"total":100}}}`
		payload2 := `{"cpu":{"usage":{"total":200}}}`

		packet1 := makePacket(1, payload1)
		packet2 := makePacket(1, payload2)

		// Parse the first packet
		stats, err := parser.Parse(packet1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(stats) != 1 {
			t.Fatalf("expected 1 stat, got %d", len(stats))
		}

		// Parse the second packet
		stats, err = parser.Parse(packet2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(stats) != 1 {
			t.Fatalf("expected 1 stat, got %d", len(stats))
		}
	})

	t.Run("underflow and recovery", func(t *testing.T) {
		t.Parallel()

		parser := New()

		payload := `{"cpu":{"usage":{"total":100}}}`
		packet := makePacket(1, payload)

		// Write in chunks to simulate network/stream chunking
		// Chunk 1: protocol version (1 byte)
		stats, err := parser.Parse(packet[:1])
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(stats) != 0 {
			t.Fatalf("expected 0 stats on underflow, got %d", len(stats))
		}

		// Chunk 2: length prefix (4 bytes)
		stats, err = parser.Parse(packet[1:5])
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(stats) != 0 {
			t.Fatalf("expected 0 stats on underflow, got %d", len(stats))
		}

		// Chunk 3: partial payload (half of the payload)
		mid := 5 + len(payload)/2

		stats, err = parser.Parse(packet[5:mid])
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(stats) != 0 {
			t.Fatalf("expected 0 stats on underflow, got %d", len(stats))
		}

		// Chunk 4: rest of payload
		stats, err = parser.Parse(packet[mid:])
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(stats) != 1 {
			t.Fatalf("expected 1 stat after complete read, got %d", len(stats))
		}
	})

	t.Run("invalid protocol version", func(t *testing.T) {
		t.Parallel()

		parser := New()
		packet := makePacket(2, `{"cpu":{"usage":{"total":100}}}`)

		_, err := parser.Parse(packet)
		if err == nil {
			t.Fatal("expected error for invalid protocol version, got nil")
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		t.Parallel()

		parser := New()
		packet := makePacket(1, `invalid-json`)

		_, err := parser.Parse(packet)
		if err == nil {
			t.Fatal("expected error for invalid json, got nil")
		}
	})

	t.Run("overly large payload size", func(t *testing.T) {
		t.Parallel()

		parser := New()
		// Construct a packet with version 1, and length prefix of 11 MB (over the 10 MB limit)
		packet := make([]byte, 5)
		packet[0] = 1
		binary.LittleEndian.PutUint32(packet[1:5], uint32(11*1024*1024))

		_, err := parser.Parse(packet)
		if err == nil {
			t.Fatal("expected error for overly large payload size, got nil")
		}
	})
}

func BenchmarkParser_Parse(b *testing.B) {
	makePacket := func(version uint8, data string) []byte {
		length := len(data)
		if length < 0 || int64(length) > math.MaxUint32 {
			panic("data too large")
		}

		buf := make([]byte, 1+4+length)
		buf[0] = version
		binary.LittleEndian.PutUint32(buf[1:5], uint32(length))
		copy(buf[5:], data)

		return buf
	}

	parser := New()
	payload := `{"cpu":{"usage":{"total":100}},"memory":{"usage":{"limit":4000}}}`
	packet := makePacket(1, payload)

	for b.Loop() {
		_, err := parser.Parse(packet)
		if err != nil {
			b.Fatal(err)
		}
	}
}
