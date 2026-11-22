package exec_test

import (
	"context"
	"errors"
	osexec "os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// countingSandbox stands in for a VM. It records how often it was booted, which
// is the only way to observe the property this package exists to guarantee.
type countingSandbox struct {
	mu       sync.Mutex
	boots    int
	stops    int
	fail     error
	confines bool
	store    string
}

func (c *countingSandbox) Confines() bool   { return c.confines }
func (c *countingSandbox) StoreDir() string { return c.store }

func (c *countingSandbox) Start(context.Context) (exec.Conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.fail != nil {
		return nil, c.fail
	}

	c.boots++

	return exec.LoopbackConn(), nil
}

func (c *countingSandbox) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.stops++

	return nil
}

func (c *countingSandbox) counts() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.boots, c.stops
}

// found resolves a command through PATH. Hard-coding /bin/true is a Linuxism:
// macOS ships it at /usr/bin/true and nowhere else.
func found(t *testing.T, name string) string {
	t.Helper()

	p, err := osexec.LookPath(name)
	if err != nil {
		t.Skipf("no %s on this machine", name)
	}

	return p
}

func step(t *testing.T, name, argv string) *ir.Node {
	t.Helper()

	return &ir.Node{
		Op:   ir.Op{Kind: ir.OpExec, Args: []string{found(t, argv)}},
		Meta: ir.Meta{Source: "./Earthfile:" + name},
	}
}

// TestOneSandboxServesEveryStep is the whole reason this layer exists.
//
// A VM per step is roughly 690ms of lifecycle each (experiment E1b), which for a
// fifty-step build is thirty-five seconds of pure boot. The sandbox is a
// property of the *run*, not of the step, so N steps must cost one boot.
func TestOneSandboxServesEveryStep(t *testing.T) {
	if !needsIsolation(t) {
		return
	}

	t.Parallel()

	sb := &countingSandbox{}

	e, err := exec.New(sb)
	if err != nil {
		t.Fatal(err)
	}

	defer e.Close()

	for _, name := range []string{"1", "2", "3", "4", "5"} {
		_, err := e.Run(context.Background(), step(t, name, "true"), core.Worker{ID: "w"}, nil, nil)
		if err != nil {
			t.Fatalf("step %s: %v", name, err)
		}
	}

	if boots, _ := sb.counts(); boots != 1 {
		t.Errorf("5 steps booted %d sandboxes, want 1", boots)
	}
}

// A sandbox that outlives its run leaks a VM, which on a laptop is noticed and
// on CI is not.
func TestCloseStopsTheSandbox(t *testing.T) {
	if !needsIsolation(t) {
		return
	}

	t.Parallel()

	sb := &countingSandbox{}

	e, err := exec.New(sb)
	if err != nil {
		t.Fatal(err)
	}

	_, err = e.Run(context.Background(), step(t, "1", "true"), core.Worker{ID: "w"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	err = e.Close()
	if err != nil {
		t.Fatal(err)
	}

	if _, stops := sb.counts(); stops != 1 {
		t.Errorf("sandbox stopped %d times, want 1", stops)
	}

	// Closing twice happens: a deferred Close plus an explicit one on the error
	// path. The second must not fail and mask the first error.
	err = e.Close()
	if err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// A sandbox that will not boot must say what was tried. "exec failed" sends the
// reader to the wrong layer entirely.
//
// Reported at the step that needed the sandbox rather than at construction,
// because the sandbox now starts on first use: a build whose every step is
// cached is entitled to succeed on a machine whose VM backend is broken. What
// the diagnosis has to contain is unchanged, and is the half of this test worth
// keeping.
func TestBootFailureNamesTheSandbox(t *testing.T) {
	t.Parallel()

	sb := &countingSandbox{fail: errors.New("container: no such image earthbuild/guest:1")}

	e, err := exec.New(sb)
	if err != nil {
		t.Fatalf("constructing an executor tried to boot the sandbox: %v", err)
	}

	err = e.Ping(context.Background())
	if err == nil {
		t.Fatal("a step ran against a sandbox that cannot boot")
	}

	for _, want := range []string{"no such image", "sandbox"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%s", want, err)
		}
	}
}

// A confining sandbox yields cacheable results; the capture reaches the
// scheduler rather than being discarded with it.
func TestConfinedResultsAreCaptured(t *testing.T) {
	if !needsIsolation(t) {
		return
	}

	t.Parallel()

	e, err := exec.New(&countingSandbox{confines: true})
	if err != nil {
		t.Fatal(err)
	}

	defer e.Close()

	res, err := e.Run(context.Background(), step(t, "1", "true"), core.Worker{ID: "w"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if !res.Captured {
		t.Error("a confined step produced an uncaptured result")
	}

	if res.Layer == (ir.NodeID{}) {
		t.Error("captured result carries no layer digest")
	}
}

// The same step through a sandbox that does not confine must NOT be captured,
// however well the capture itself worked. A3 fails, so ε does not bound what the
// step observed, so the key would be a false claim.
func TestUnconfinedResultsAreNotCaptured(t *testing.T) {
	if !needsIsolation(t) {
		return
	}

	t.Parallel()

	e, err := exec.New(&countingSandbox{confines: false})
	if err != nil {
		t.Fatal(err)
	}

	defer e.Close()

	res, err := e.Run(context.Background(), step(t, "1", "true"), core.Worker{ID: "w"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if res.Captured {
		t.Error("an unconfined step produced a cacheable result")
	}
}

// A step that exits non-zero is a *result*. Conflating it with a broken sandbox
// makes a failing build indistinguishable from a broken engine.
func TestFailingStepIsAResultNotAnError(t *testing.T) {
	if !needsIsolation(t) {
		return
	}

	t.Parallel()

	e, err := exec.New(&countingSandbox{})
	if err != nil {
		t.Fatal(err)
	}

	defer e.Close()

	n := step(t, "fail", "false")

	res, err := e.Run(context.Background(), n, core.Worker{ID: "w"}, nil, nil)
	if err != nil {
		t.Fatalf("a step that failed is not an executor error: %v", err)
	}

	if res.Exit == 0 {
		t.Error("a step running /bin/false reported success")
	}
}

// The default platform is the *sandbox's*, not the host's.
//
// Both backends run Linux - a VM on macOS, this kernel on Linux - so defaulting
// to runtime.GOOS asks a registry for a darwin image, which no base image
// provides. The failure is clear but arrives after a pull, and it is the first
// thing a macOS user would ever hit.
func TestDefaultPlatformIsTheGuests(t *testing.T) {
	t.Parallel()

	if got := exec.DefaultPlatform(); !strings.HasPrefix(got, "linux/") {
		t.Errorf("default platform is %q; the sandbox runs Linux whatever the host is", got)
	}

	if strings.HasSuffix(exec.DefaultPlatform(), "/") {
		t.Error("default platform names no architecture")
	}
}
