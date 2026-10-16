package exec

import (
	"context"
	"sync"
	"testing"
	"time"
)

// slowSandbox is a sandbox whose boot can be held open, which is the state the
// leak lives in: made, starting, not yet answering.
type slowSandbox struct {
	entered chan struct{}
	release chan struct{}

	mu      sync.Mutex
	stopped bool
}

func (s *slowSandbox) Start(context.Context) (Conn, error) {
	close(s.entered)
	<-s.release

	return ClosedConn(), nil
}

func (s *slowSandbox) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.stopped = true

	return nil
}

func (s *slowSandbox) StoreDir() string { return "" }
func (s *slowSandbox) Confines() bool   { return true }

func (s *slowSandbox) wasStopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.stopped
}

// Close stops a sandbox whose boot is still in flight.
//
// **Because a warm-up boots on another goroutine.** `warm()` returns at once
// and the machine comes up behind it, so a Close arriving in between found
// `running` false - the executor was made, the VM was not yet answering - and
// returned having stopped nothing. The boot then completed, claimed the store
// device and ran on, owned by nobody: the engine had already re-armed its Once
// and moved to a new sandbox.
//
// The stack that named it, from a corpus run refused 28 times:
//
//	claimStore <- Firecracker.Start <- Executor.connect <- client.func1
//	  <- Executor.Prewarm <- cli.(*engine).warm.func1
func TestCloseStopsASandboxThatIsStillStarting(t *testing.T) {
	t.Parallel()

	sb := &slowSandbox{entered: make(chan struct{}), release: make(chan struct{})}

	e, err := New(sb)
	if err != nil {
		t.Fatal(err)
	}

	// The warm-up: a boot on another goroutine, exactly as Prewarm does it.
	go func() { _, _ = e.client() }()

	select {
	case <-sb.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the sandbox never started")
	}

	closed := make(chan error, 1)

	go func() { closed <- e.Close() }()

	// Close must not have finished yet: the boot it has to wait for is held.
	select {
	case <-closed:
		t.Fatal("Close returned while the sandbox was still starting," +
			" so whatever that boot goes on to claim is owned by nobody")
	case <-time.After(200 * time.Millisecond):
	}

	close(sb.release)

	select {
	case err = <-closed:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return once the boot finished")
	}

	if !sb.wasStopped() {
		t.Error("the sandbox was left running: Close waited for the boot and" +
			" then did not stop what the boot had made")
	}
}
