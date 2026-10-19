package formatter

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/domain"
	"github.com/EarthBuild/earthbuild/logbus"
	"github.com/EarthBuild/earthbuild/logstream"
)

const repeatMarker = "Repeating the failure error..."

// consoleCapture collects the formatted console output of a bus.
type consoleCapture struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func (cc *consoleCapture) Write(delta *logstream.Delta) {
	dfl := delta.GetDeltaFormattedLog()
	if dfl == nil || dfl.GetTargetId() != "_full" {
		return
	}

	cc.mu.Lock()
	defer cc.mu.Unlock()

	cc.buf.Write(dfl.GetData())
}

func (cc *consoleCapture) String() string {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	return cc.buf.String()
}

type harness struct {
	t       *testing.T
	bus     *logbus.Bus
	console *consoleCapture
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	b := logbus.New()
	f := New(context.Background(), b, false, false, false, true, nil, false)
	console := &consoleCapture{}

	b.AddRawSubscriber(f)
	b.AddFormattedSubscriber(console)
	b.Run().SetStart(time.Now())

	t.Cleanup(func() {
		err := f.Close()
		if err != nil {
			t.Errorf("formatter close: %v", err)
		}
	})

	return &harness{t: t, bus: b, console: console}
}

func (h *harness) command(targetID, targetName, commandID, name string) *logbus.Command {
	h.t.Helper()

	if _, ok := h.bus.Run().Target(targetID); !ok {
		_, err := h.bus.Run().NewTarget(targetID, domain.Target{Target: targetName}, nil, "", "")
		if err != nil {
			h.t.Fatalf("new target: %v", err)
		}
	}

	cp, err := h.bus.Run().NewCommand(commandID, name, targetID, "", "", false, false, false, nil, "", "", "")
	if err != nil {
		h.t.Fatalf("new command: %v", err)
	}

	cp.SetStart(time.Now())

	return cp
}

func write(t *testing.T, cp *logbus.Command, s string) {
	t.Helper()

	_, err := cp.Write([]byte(s), time.Now(), 1)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
}

func fail(h *harness, targetID, commandID string, cp *logbus.Command) {
	errMsg := "ERROR Earthfile:10:5 the command did not complete successfully"
	cp.SetEnd(time.Now(), logstream.RunStatus_RUN_STATUS_FAILURE, errMsg)
	h.bus.Run().SetFatalError(
		time.Now(), targetID, commandID, logstream.FailureType_FAILURE_TYPE_NONZERO_EXIT, "", errMsg,
	)
}

func TestBuildFailureNotRepeatedWhenContiguous(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	cp := h.command("t1", "fail", "c1", "RUN ./fail.sh")
	write(t, cp, "unique-failure-output\n")
	fail(h, "t1", "c1", cp)

	got := h.console.String()

	if n := strings.Count(got, "unique-failure-output"); n != 1 {
		t.Errorf("want failed command output printed once, got %d times:\n%s", n, got)
	}

	if strings.Contains(got, repeatMarker) {
		t.Errorf("want no %q in output:\n%s", repeatMarker, got)
	}

	if !strings.Contains(got, "FAILURE") {
		t.Errorf("want failure banner in output:\n%s", got)
	}

	if n := strings.Count(got, "did not complete successfully"); n < 1 {
		t.Errorf("want error message in output:\n%s", got)
	}
}

func TestBuildFailureRepeatedWhenInterleaved(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	failing := h.command("t1", "fail", "c1", "RUN ./fail.sh")
	other := h.command("t2", "other", "c2", "RUN ./other.sh")

	write(t, failing, "unique-failure-output\n")
	write(t, other, "some parallel output\n")
	write(t, failing, "more failure output\n")
	fail(h, "t1", "c1", failing)

	got := h.console.String()

	if n := strings.Count(got, "unique-failure-output"); n != 2 {
		t.Errorf("want failed command output printed twice, got %d times:\n%s", n, got)
	}

	if !strings.Contains(got, repeatMarker) {
		t.Errorf("want %q in output:\n%s", repeatMarker, got)
	}
}

func TestBuildFailureRepeatedWhenFollowedByOtherOutput(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	failing := h.command("t1", "fail", "c1", "RUN ./fail.sh")
	write(t, failing, "unique-failure-output\n")
	failing.SetEnd(time.Now(), logstream.RunStatus_RUN_STATUS_FAILURE, "boom")

	other := h.command("t2", "other", "c2", "RUN ./other.sh")
	write(t, other, "some parallel output\n")

	h.bus.Run().SetFatalError(
		time.Now(), "t1", "c1", logstream.FailureType_FAILURE_TYPE_NONZERO_EXIT, "", "boom",
	)

	got := h.console.String()

	if !strings.Contains(got, repeatMarker) {
		t.Errorf("want %q in output:\n%s", repeatMarker, got)
	}
}
