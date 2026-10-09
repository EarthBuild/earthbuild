package states

import "sync"

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
	// Outcome is settled by the exporter once it has exported the image (or
	// failed to).
	Outcome ExportOutcome

	exporter imageExporter
	mu       sync.Mutex
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
