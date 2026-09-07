package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A mutant is registered for putting back *before* it is written, not after.
//
// `os.WriteFile` opens with O_TRUNC, so a write that fails part of the way
// through leaves the file damaged rather than untouched. Registering afterwards
// means that failure returns with a mutated file on disk and nothing that knows
// how to restore it - and the signal handler, which is the other half of the
// promise the tool makes, is blind for the same window.
//
// The symptom is not a broken sweep but a lying one: the next sweep finds the
// leftover mutant, reports "0 matches, want exactly 1", and sends the reader to
// the catalogue to fix an entry that was always correct. Two verdicts were lost
// to exactly that before this test existed.
//
//nolint:paralleltest // swaps package state
func TestAMutantIsRegisteredBeforeItIsWritten(t *testing.T) {
	dir := t.TempDir()

	path := filepath.Join(dir, "source.go")
	err := os.WriteFile(path, []byte("package p\n\nconst guard = true\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	registered := false

	writeSource = func(string, []byte, fs.FileMode) error {
		registered = held.Load() != nil

		return errors.New("the disk filled up half way through the write")
	}

	t.Cleanup(func() { writeSource = os.WriteFile; held.Store(nil) })

	run(dir, Mutant{
		File:        "source.go",
		Anchor:      "true",
		Replacement: "false",
		Package:     "./",
	}, time.Minute, true)

	if !registered {
		t.Error("the mutant was written before anything knew how to put it back" +
			"\n  a write that fails, or a signal, in that window leaves the mutant on disk" +
			"\n  and the next sweep blames the catalogue for it")
	}
}
