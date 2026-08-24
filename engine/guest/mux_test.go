package guest_test

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// slowMat holds every request until `want` of them have arrived, so overlap is
// a fact rather than an inference from the clock.
//
// It used to sleep a fixed 100ms and let the test time the total. That is *a
// test asserting a duration when it is asserting an ordering* - and the bar,
// 300ms for four 100ms requests, was three times the parallel answer and
// three-quarters of the serial one, which leaves a loaded machine somewhere in
// between (E482).
//
// A request that arrives when the others are already waiting releases them all.
// A mux that serialises never gets there, and the test says so with a deadline
// generous enough that only a genuine serialisation crosses it.
type slowMat struct {
	want int
	// giveUp bounds the wait, so a serialised client fails this test rather
	// than hanging it.
	giveUp time.Duration

	mu   sync.Mutex
	peak int
	now  int
	all  chan struct{}
}

func newSlowMat(want int) *slowMat {
	// Two seconds, not twenty: a healthy client waits none of it - the four
	// requests release each other - and a serialised one pays it once per
	// request, so the test reports in eight seconds rather than running out
	// somebody else's clock.
	return &slowMat{want: want, giveUp: 2 * time.Second, all: make(chan struct{})}
}

func (m *slowMat) Materialise(ctx context.Context, stack []ir.NodeID) (core.Handle, error) {
	m.mu.Lock()
	m.now++

	if m.now > m.peak {
		m.peak = m.now
	}

	reached := m.now >= m.want
	m.mu.Unlock()

	if reached {
		// Once, and by whichever request is last: closing twice panics, and
		// the count only reaches `want` on the way up.
		select {
		case <-m.all:
		default:
			close(m.all)
		}
	}

	// The barrier gives up rather than holding forever.
	//
	// A client that serialises its exchanges never lets the second request
	// arrive, so nothing releases this one - and the caller's context does not
	// reach here, because this is the *server* side and has a context of its
	// own. Without the timer the test hung for the harness's whole budget and
	// reported a stack dump, which is a worse answer than a wrong one: **a hang
	// is not a report** (E482).
	select {
	case <-m.all:
	case <-ctx.Done():
	case <-time.After(m.giveUp):
	}

	m.mu.Lock()
	m.now--
	m.mu.Unlock()

	return slowHandle{id: strconv.Itoa(len(stack))}, nil
}

// waited reports the peak concurrency seen.
func (m *slowMat) waited() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.peak
}

type slowHandle struct{ id string }

func (h slowHandle) Root() string   { return "/sim/" + h.id }
func (h slowHandle) Delta() string  { return "/sim/" + h.id }
func (h slowHandle) Release() error { return nil }
func (h slowHandle) Observations() core.Observation {
	return core.Observation{Reads: map[string]ir.NodeID{}, Listings: map[string]ir.NodeID{}}
}

// Concurrent requests must actually overlap.
//
// The scheduler evaluates independent steps at once and every one of them goes
// through this connection. A client holding one lock across a whole exchange
// turns a parallel build back into a serial one - measured at 7.2s for two
// independent three-second steps that should have taken four.
func TestConcurrentRequestsOverlap(t *testing.T) {
	t.Parallel()

	const requests = 4

	mat := newSlowMat(requests)
	c := pairWith(t, &guest.Server{Mat: mat})

	// A hang detector rather than a measurement, and it has to be one: a client
	// that holds its lock across an exchange *deadlocks* rather than
	// serialising - the reader goroutine needs the same lock to deliver the
	// reply - so nothing on the server side can release it and only the
	// caller's own context gets this test its answer.
	//
	// Three seconds against a healthy path measured in microseconds. The four
	// requests release each other the moment the last arrives, so a working
	// client waits none of this; a broken one reports in twelve seconds
	// instead of running out the harness's clock and printing a stack dump
	// (E482).
	ctx, done := context.WithTimeout(context.Background(), 3*time.Second)
	defer done()

	var wg sync.WaitGroup

	for range requests {
		wg.Go(func() {
			_, err := c.Materialise(ctx, nil)
			if err != nil {
				t.Error(err)
			}
		})
	}

	wg.Wait()

	if got := mat.waited(); got != requests {
		t.Errorf("at most %d of %d requests were in flight together"+
			"\n  each one waits for the others, so anything short of all of"+
			" them is a connection serialising what the scheduler sent in"+
			" parallel", got, requests)
	}
}

// Every reply must reach the request that asked for it.
//
// This is the property that makes multiplexing worth doing rather than
// dangerous. With replies arriving out of order, a client that matched them by
// arrival would hand one step another step's filesystem - a wrong build that
// reports success.
func TestRepliesGoToTheRightRequest(t *testing.T) {
	t.Parallel()

	c := pairWith(t, &guest.Server{Mat: &jitterMat{}})

	var wg sync.WaitGroup

	for i := range 64 {
		wg.Go(func() {
			// The stack length is echoed back in the root, so a crossed reply is
			// visible rather than merely possible.
			stack := make([]ir.NodeID, i%8)
			for j := range stack {
				stack[j] = ir.NodeID{byte(j + 1)}
			}

			h, err := c.Materialise(context.Background(), stack)
			if err != nil {
				t.Error(err)

				return
			}

			if want := fmt.Sprintf("/sim/%d", len(stack)); h.Root() != want {
				t.Errorf("a request for %d layers got the reply for %s", len(stack), h.Root())
			}
		})
	}

	wg.Wait()
}

// jitterMat replies at varying speeds, so replies arrive out of order.
type jitterMat struct{}

func (m *jitterMat) Materialise(_ context.Context, stack []ir.NodeID) (core.Handle, error) {
	time.Sleep(time.Duration(7-len(stack)%7) * time.Millisecond)

	return slowHandle{id: strconv.Itoa(len(stack))}, nil
}

// A connection that dies must fail everything outstanding, not leave callers
// waiting for a reply that can never arrive.
func TestBrokenConnectionFailsOutstandingRequests(t *testing.T) {
	t.Parallel()

	host, guestSide := net.Pipe()

	// A materialise that never returns: this test needs a request outstanding
	// when the connection dies, and a barrier waiting for a second request that
	// never comes is one that holds for as long as the test needs rather than
	// for a second somebody guessed at.
	srv := &guest.Server{Mat: newSlowMat(2)}
	go func() { _ = srv.Serve(context.Background(), guestSide) }()

	c, err := guest.Dial(host)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)

	go func() {
		_, err := c.Materialise(context.Background(), nil)
		done <- err
	}()

	time.Sleep(50 * time.Millisecond)
	guestSide.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Error("a request outstanding on a broken connection reported success")
		}
	case <-time.After(2 * time.Second):
		t.Error("a request outstanding on a broken connection never returned")
	}
}

// A guest speaking an older protocol must be *refused*, promptly.
//
// This is the regression that matters, not the mismatch itself: version 2 added
// request ids, and a version-1 guest replies without one. Negotiating through
// the multiplexed path meant the host waited forever for a reply it could never
// match, so the check could not run and the build hung. A handshake that depends
// on the newest feature cannot negotiate; it stays in the oldest dialect.
func TestAnOldGuestIsRefusedRatherThanHanging(t *testing.T) {
	t.Parallel()

	host, guestSide := net.Pipe()

	// A guest from before request ids: it answers the handshake with a version
	// and no id, exactly as version 1 did.
	go func() {
		c := struct {
			r *bufio.Reader
			w io.Writer
		}{bufio.NewReader(guestSide), guestSide}

		var hdr [4]byte
		_, err := io.ReadFull(c.r, hdr[:])
		if err != nil {
			return
		}

		body := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		_, err = io.ReadFull(c.r, body)
		if err != nil {
			return
		}

		reply, _ := json.Marshal(map[string]any{"version": 1})

		binary.BigEndian.PutUint32(hdr[:], uint32(len(reply)))
		_, _ = c.w.Write(hdr[:])
		_, _ = c.w.Write(reply)
	}()

	done := make(chan error, 1)

	go func() {
		_, err := guest.Dial(host)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a guest speaking protocol 1 was accepted")
		}

		// Both numbers, taken from the constant rather than written here: a
		// version bump should not need this test edited, and one that does
		// invites someone to edit the assertion instead of thinking about the
		// bump.
		if !strings.Contains(err.Error(), "1") ||
			!strings.Contains(err.Error(), strconv.Itoa(guest.Version)) {
			t.Errorf("the refusal does not name both versions:\n%s", err)
		}

	case <-time.After(2 * time.Second):
		t.Error("dialling an old guest hung instead of refusing")
	}
}
