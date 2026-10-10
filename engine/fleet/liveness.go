package fleet

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// Frames on the reply path, so that a worker which is busy can say so.
//
// **Liveness and completion used to share one clock.** The driver set its reach
// deadline on the stream and then read the result off it, which makes a step
// longer than the reach indistinguishable from a machine that has gone - and a
// cold worker fetching a large base is exactly that step. A worker dropped
// mid-fetch keeps an empty store, so the next assignment is just as expensive:
// an absorbing state rather than a slow path (E-F1).
//
// Raising the reach is not the fix. It was ten seconds because a corpse in the
// fleet otherwise costs a reach *per step* (E256), and that argument is as good
// as it ever was. What was wrong is which interval it bounded.
const (
	// noteAlive is a worker saying it is still working. No body.
	noteAlive = byte('k')
	// noteReply precedes the framed reply.
	noteReply = byte('r')
)

// beatEvery is how often a busy worker says it is still there.
//
// A third of the reach, so two beats may be lost before a live worker is called
// dead. Cheap: one byte on a stream that is already open.
const beatEvery = defaultReach / 3

// replyRunning runs a step, saying so at intervals, and then answers.
//
// The beats are what the driver's deadline is extended by, so `run` may take as
// long as the step takes. A worker that stops beating has stopped, which is the
// thing the bound was always trying to detect.
func replyRunning(
	ctx context.Context, s io.Writer, every time.Duration,
	run func() (Reply, error),
) error {
	// One writer at a time: a beat that interleaved with the reply would put a
	// stray byte inside the length prefix, and the driver would read a size the
	// worker never named.
	var mu sync.Mutex

	beating, stop := context.WithCancel(ctx)
	defer stop()

	done := make(chan struct{})

	go func() {
		defer close(done)

		t := time.NewTicker(every)
		defer t.Stop()

		for {
			select {
			case <-beating.Done():
				return

			case <-t.C:
				mu.Lock()
				_, err := s.Write([]byte{noteAlive})
				mu.Unlock()

				if err != nil {
					// The driver has gone or the stream is closed. The step
					// carries on - it may still be worth having - and the reply
					// below reports the same failure with somewhere to put it.
					return
				}
			}
		}
	}()

	r, runErr := run()

	stop()
	<-done

	// **A refusal is a reply.** The driver is reading frames on this stream and
	// nothing else will arrive on it, so a worker that said nothing when a step
	// failed would be read as one that died - which is the confusion this whole
	// split exists to remove.
	if runErr != nil {
		r = Reply{Version: Version, Refused: runErr.Error()}
	}

	mu.Lock()
	defer mu.Unlock()

	return sendReply(s, r)
}

// sendReply writes one tagged reply.
func sendReply(s io.Writer, r Reply) error {
	body, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encode a reply: %w", err)
	}

	_, err = s.Write([]byte{noteReply})
	if err != nil {
		return fmt.Errorf("say a reply is coming: %w", err)
	}

	return WriteMessage(s, body)
}

// readReply reads beats until a reply arrives, giving the worker `reach`
// between frames.
//
// `extend` is how the bound is applied - the stream's deadline, in production -
// and is called before every frame is waited for. Nil means the caller is
// bounding it some other way, which is what a test with a buffer does.
func readReply(s io.Reader, extend func(time.Time), reach time.Duration) (Reply, error) {
	for {
		if extend != nil {
			extend(time.Now().Add(reach))
		}

		var tag [1]byte

		_, err := io.ReadFull(s, tag[:])
		if err != nil {
			return Reply{}, fmt.Errorf("%w: no reply: %w", ErrMalformed, err)
		}

		switch tag[0] {
		case noteAlive:
			continue

		case noteReply:
			return decodeReply(s)

		default:
			// **A worker built before this engine wrote a bare framed message.**
			// Its first byte is the top of an eight-byte length, so it is zero
			// for every message this engine will send - `maxMessage` is far
			// under 2^56. Reading it as a tag would turn a version skew into a
			// decode error with nothing in it to suggest the cause.
			return decodeReplyAfter(tag[0], s)
		}
	}
}

// decodeReply reads one framed reply.
func decodeReply(s io.Reader) (Reply, error) {
	body, err := ReadMessage(s)
	if err != nil {
		return Reply{}, err
	}

	return unmarshalReply(body)
}

// decodeReplyAfter reads a framed reply whose first length byte has been eaten.
func decodeReplyAfter(first byte, s io.Reader) (Reply, error) {
	var n [8]byte

	n[0] = first

	_, err := io.ReadFull(s, n[1:])
	if err != nil {
		return Reply{}, fmt.Errorf("%w: no length: %w", ErrMalformed, err)
	}

	size := binary.BigEndian.Uint64(n[:])
	if size > uint64(maxMessage) {
		return Reply{}, fmt.Errorf("%w: a peer asked this engine to allocate %d"+
			" bytes, and %d is the most it will", ErrMalformed, size, maxMessage)
	}

	body := make([]byte, size)

	_, err = io.ReadFull(s, body)
	if err != nil {
		return Reply{}, fmt.Errorf("%w: %d bytes promised and fewer arrived: %w",
			ErrMalformed, size, err)
	}

	return unmarshalReply(body)
}

func unmarshalReply(body []byte) (Reply, error) {
	var r Reply

	err := json.Unmarshal(body, &r)
	if err != nil {
		return Reply{}, fmt.Errorf("%w: a reply that is not JSON: %w", ErrMalformed, err)
	}

	return r, nil
}
