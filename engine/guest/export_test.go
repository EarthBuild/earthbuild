package guest

import (
	"context"
	"io"
)

// NewTestConn exposes the framing to tests in this package's external test
// package, so protocol-level misbehaviour - version skew, malformed ids - can
// be provoked without a well-behaved Client in the way.
func NewTestConn(rw io.ReadWriter) *TestConn { return &TestConn{newConn(rw)} }

// TestConn is a raw framed connection, for tests only.
type TestConn struct{ c *conn }

// Send writes one framed message.
func (t *TestConn) Send(v any) error { return t.c.send(v) }

// Recv reads one framed message.
func (t *TestConn) Recv(v any) error { return t.c.recv(v) }

// RunWithDaemonForTest runs a real daemon around body, for the external test
// package.
//
// Here rather than in the package proper because it exists only to let a test
// stand a daemon up the way a step does - the same launch, the same wait, the
// same shutdown - and put something else inside it. A production caller has
// `execRequest`, which is the only path that should ever start one.
func RunWithDaemonForTest(ctx context.Context, stepRoot string, d *Daemon, body func() error) error {
	return withDaemon(ctx, stepRoot, d, launchDockerd, publishSocket, body)
}
