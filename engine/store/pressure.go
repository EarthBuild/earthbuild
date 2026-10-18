package store

import (
	"fmt"
	"sync"
	"time"
)

// pressureWindow is how much history a rate is taken over.
//
// Long enough that one large capture does not read as a trend, short enough
// that the figure describes what is happening now rather than what the build
// was doing a minute ago.
const pressureWindow = 30 * time.Second

// pressureFloor is the rate below which a store is not described as filling.
//
// A build writes constantly and a store drifts; without a floor every failure
// would carry a rate, and a reader chasing a collector that is working
// perfectly is worse served than one told nothing.
const pressureFloor = 1 << 20 // 1 MiB/s

// reading is what the filesystem said, and when.
type reading struct {
	at   time.Time
	free uint64
}

// pressure watches a store's free space and can say how fast it is going.
//
// **Because "no space left on device" is the end of a story nobody watched.**
// The failure names the file that could not be written and says nothing about
// whether the store had been full for an hour or emptied itself in the last
// forty seconds - and those want opposite responses: a bigger device, or a
// collector that is not keeping up.
//
// Worth having because the question is nearly free. One statfs is 2.2us on the
// test machine, against 5.1 seconds to measure the store by walking it, so a
// reading a second costs nothing anybody can find - and a second is ample
// resolution for a figure describing minutes.
type pressure struct {
	mu   sync.Mutex
	seen []reading
}

func newPressure() *pressure { return &pressure{} }

// sample records a reading and forgets what has fallen out of the window.
func (p *pressure) sample(at time.Time, free uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.seen = append(p.seen, reading{at: at, free: free})

	cut := at.Add(-pressureWindow)
	for len(p.seen) > 0 && p.seen[0].at.Before(cut) {
		p.seen = p.seen[1:]
	}
}

// note describes a store that is filling, or nothing.
//
// Empty for a store that is steady, gaining, or has too little history: each of
// those would put a number in front of a reader that does not describe a
// problem, and a diagnostic that cries wolf is one that gets skipped when it
// matters.
func (p *pressure) note(now time.Time) string {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.seen) < 2 {
		return ""
	}

	first, last := p.seen[0], p.seen[len(p.seen)-1]

	over := last.at.Sub(first.at)
	if over <= 0 || last.free >= first.free {
		return ""
	}

	lost := first.free - last.free

	rate := uint64(float64(lost) / over.Seconds())
	if rate < pressureFloor {
		return ""
	}

	left := time.Duration(float64(last.free)/float64(rate)) * time.Second

	return fmt.Sprintf(
		"  the store lost %s in the last %s, %s/s, with %s left\n"+
			"  at that rate it runs out in about %s: the collector is not keeping up\n",
		human(lost), over.Round(time.Second), human(rate), human(last.free),
		left.Round(time.Second))
}

// Watch samples a store's free space until stop is closed, and returns a
// function describing what it saw.
//
// A second between readings. The measurement is 2.2us, so the interval is
// chosen for the resolution a person wants rather than for the cost.
func Watch(root string, stop <-chan struct{}) func() string {
	p := newPressure()

	go func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()

		for {
			select {
			case <-stop:
				return
			case at := <-tick.C:
				free, err := Free(root)
				if err != nil {
					continue
				}

				p.sample(at, free)
			}
		}
	}()

	return func() string { return p.note(time.Now()) }
}
