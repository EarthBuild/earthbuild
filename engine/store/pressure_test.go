package store

import (
	"strings"
	"testing"
	"time"
)

// A store running out of room says how fast it went, not merely that it did.
//
// **"no space left on device" is the end of a story nobody watched.** The
// failure names the file that could not be written and says nothing about
// whether the store was full for the last hour or emptied itself in the last
// forty seconds - and those want different responses: a bigger device, or a
// collector that is not keeping up.
//
// Cheap enough to be worth having. One statfs is 2.2us on the test machine,
// against 5.1 seconds to measure the store by walking it, so a reading a second
// costs nothing anybody can find.
func TestPressureSaysHowFastTheStoreIsFilling(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

	p := newPressure()
	// A gigabyte a second, for ten seconds.
	for i := range 10 {
		p.sample(at.Add(time.Duration(i)*time.Second), uint64(20-i)<<30)
	}

	note := p.note(at.Add(9 * time.Second))
	if note == "" {
		t.Fatal("a store losing a gigabyte a second said nothing")
	}

	for _, want := range []string{"1.0 GiB/s", "11.0 GiB left"} {
		if !strings.Contains(note, want) {
			t.Errorf("the note does not say %q:\n%s", want, note)
		}
	}
}

// A store that is not moving is not reported as filling.
//
// The note exists to explain a failure, and a rate invented from noise would
// send a reader after a collector that is working perfectly.
func TestASteadyStoreIsNotCalledFilling(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

	p := newPressure()
	for i := range 10 {
		p.sample(at.Add(time.Duration(i)*time.Second), 20<<30)
	}

	if note := p.note(at.Add(9 * time.Second)); note != "" {
		t.Errorf("a store that did not move was called filling: %s", note)
	}
}

// A store gaining room is not reported either: that is the collector working.
func TestAStoreBeingCollectedIsNotCalledFilling(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

	p := newPressure()
	for i := range 10 {
		p.sample(at.Add(time.Duration(i)*time.Second), uint64(10+i)<<30)
	}

	if note := p.note(at.Add(9 * time.Second)); note != "" {
		t.Errorf("a store gaining room was called filling: %s", note)
	}
}

// With too little history there is no rate to report.
func TestOneReadingIsNotATrend(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

	p := newPressure()
	p.sample(at, 5<<30)

	if note := p.note(at); note != "" {
		t.Errorf("a single reading produced a trend: %s", note)
	}
}
