package core

import (
	"errors"
	"fmt"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// ErrInputMissing marks a step that could not be given something it stands on.
var ErrInputMissing = errors.New("an input could not be obtained")

// MissingInputError says which layer an executor could not get hold of.
//
// **Not the same as a layer that does not exist.** A worker went behind a
// firewall, a machine left the fleet, a network went away - and the step that
// produced the layer is still in the graph and can be run again. Every other
// source in this engine degrades rather than fails (I6, I11), and until this the
// fleet was the one that did not: a driver that could not fetch a delegated
// result failed the build (E278).
//
// An executor returns it instead of a failure, and the scheduler answers by
// rebuilding whatever made the layer, here.
type MissingInputError struct {
	// Layer is what could not be obtained.
	Layer ir.NodeID
	// Path is the file the step wanted and was not given, when the executor
	// knows which one.
	//
	// **The difference between a wrong prediction costing a file and costing a
	// base.** A worker fetching part of a layer can be wrong about which part,
	// and answering that by fetching the whole layer turns the cheap
	// configuration into the expensive one in a single hop - measured at four
	// workers, 63.6 MiB against 1.1 (E328).
	//
	// Empty when the executor does not know, which is every path that fails
	// before a step reads anything.
	Path string
	// Where is optional context - which machine was asked, and why it did not
	// answer.
	Where string
}

func (m MissingInputError) Error() string {
	at := ""
	if m.Path != "" {
		at = " at " + m.Path
	}

	if m.Where == "" {
		return fmt.Sprintf("%v: layer %v%s", ErrInputMissing, m.Layer, at)
	}

	return fmt.Sprintf("%v: layer %v%s from %s", ErrInputMissing, m.Layer, at, m.Where)
}

// Is makes errors.Is(err, ErrInputMissing) true for this.
func (m MissingInputError) Is(target error) bool { return target == ErrInputMissing }

// producerOf is the node whose result is this layer.
//
// The scheduler is the only party that knows. An executor holds a digest it
// cannot obtain and nothing about how it was made, which is why the answer to an
// unobtainable input has to be given here rather than there.
func (s *Scheduler) producerOf(id ir.NodeID) (*ir.Node, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for nid, res := range s.done {
		if res.Layer != id {
			continue
		}

		if n, ok := s.nodes[nid]; ok {
			return n, true
		}
	}

	return nil, false
}
