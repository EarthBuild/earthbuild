package states

import (
	"context"
	"sync"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

// imageExporter is who exports one SAVE IMAGE.
type imageExporter int

const (
	// exporterUndecided means nobody has started exporting the image yet.
	exporterUndecided imageExporter = iota
	// exporterWaitBlock means a wait block exports the image.
	exporterWaitBlock
	// exporterBuilder means builder.go exports the image.
	exporterBuilder
)

// ImageExport is the one export of one SAVE IMAGE. Every copy of the
// SaveImage shares it, and so does its wait item. A target BUILT from more than
// one place has its wait items attached to more than one wait block, and its
// SAVE IMAGE may also be left to builder.go. ImageExport is how they agree that
// exactly one of them exports the image, and how the target is told, once,
// that the export is done.
//
// A nil *ImageExport has no one to coordinate with: whoever asks may export.
type ImageExport struct {
	// ref is the image's solved state, once Ref has solved it.
	ref gwclient.Reference
	// solving is held by the Ref call that is solving the image's state. It is
	// a channel, not a mutex, so that a caller waiting for it can give up when
	// its context is done.
	solving chan struct{}

	// Outcome is settled by the exporter once it has exported the image (or
	// failed to).
	Outcome ExportOutcome
	// Pushed is settled once a wait block has pushed the image (or failed to).
	Pushed ExportOutcome
	// ExportedLocally is settled once a wait block has exported the image to
	// the local container engine (or failed to).
	ExportedLocally ExportOutcome

	exporter imageExporter
	mu       sync.Mutex
	solved   bool
}

// TakeForWaitBlock makes a wait block the image's exporter, unless builder.go
// already is. It reports whether a wait block is now the exporter.
func (e *ImageExport) TakeForWaitBlock() bool {
	return e.take(exporterWaitBlock)
}

// TakeForBuilder makes builder.go the image's exporter, unless a wait block
// already is. It reports whether builder.go is now the exporter.
func (e *ImageExport) TakeForBuilder() bool {
	return e.take(exporterBuilder)
}

// TakenByWaitBlock reports whether a wait block has taken the image's export.
func (e *ImageExport) TakenByWaitBlock() bool {
	if e == nil {
		return false
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	return e.exporter == exporterWaitBlock
}

func (e *ImageExport) take(exporter imageExporter) bool {
	if e == nil {
		return true
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.exporter == exporterUndecided {
		e.exporter = exporter
	}

	return e.exporter == exporter
}

// Ref returns the image's solved state, calling solve for it only once: the
// same image can be exported more than once (pushed by one wait block, then
// pushed again by another as part of a manifest list), and the later exports
// reuse the ref the first one solved. Callers that come while solve is running
// wait for it. If solve fails, nothing is kept, and the next caller solves
// again.
func (e *ImageExport) Ref(
	ctx context.Context, solve func(context.Context) (gwclient.Reference, error),
) (gwclient.Reference, error) {
	if e == nil {
		return solve(ctx)
	}

	e.mu.Lock()

	if e.solving == nil {
		e.solving = make(chan struct{}, 1)
	}

	solving := e.solving
	e.mu.Unlock()

	select {
	case solving <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	defer func() { <-solving }()

	e.mu.Lock()
	ref, solved := e.ref, e.solved
	e.mu.Unlock()

	if solved {
		return ref, nil
	}

	ref, err := solve(ctx)
	if err != nil {
		return nil, err
	}

	e.mu.Lock()
	e.ref, e.solved = ref, true
	e.mu.Unlock()

	return ref, nil
}
