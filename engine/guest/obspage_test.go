package guest

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

func bigObs(n int) core.Observation {
	obs := core.Observation{
		Reads:    make(map[string]ir.NodeID, n),
		Listings: map[string]ir.NodeID{},
	}

	for i := range n {
		p := "/a/very/long/directory/name/repeated/so/entries/cost/bytes/f" + itoa(i)
		obs.Reads[p] = ir.NodeID{byte(i), byte(i >> 8)}
		obs.Negative = append(obs.Negative, p+".absent")
	}

	obs.Listings["/a/very/long/directory/name/repeated/so/entries/cost/bytes"] = ir.NodeID{9}

	return obs
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}

	var b []byte

	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}

	return string(b)
}

// An observation larger than a frame is delivered in pages.
//
// **A step whose observation does not fit loses the second cache tier**, and it
// is the biggest steps that do not fit: this repository's `+unit-test` produces
// 19.58 MB against a 16 MiB frame (E620). Measured before building this, because
// restoring a tier that never hits would be worth nothing - a step with a 16 MB
// observation hits L2 and skips a twenty-second body, so it is worth something.
//
// Every entry arrives exactly once, and no page is too big to send.
func TestALargeObservationIsDeliveredInPages(t *testing.T) {
	t.Parallel()

	obs := bigObs(40000)

	got := core.Observation{
		Reads:    map[string]ir.NodeID{},
		Listings: map[string]ir.NodeID{},
	}

	pages, from := 0, 0

	for {
		resp, next, more := observationPage(obs, from)

		if n := estimate(resp); n > maxMessage {
			t.Fatalf("page %d is %d bytes, which cannot be sent", pages, n)
		}

		for p, d := range resp.Reads {
			if _, seen := got.Reads[p]; seen {
				t.Fatalf("%s arrived twice", p)
			}

			got.Reads[p] = mustID(t, d)
		}

		for p, d := range resp.Listings {
			got.Listings[p] = mustID(t, d)
		}

		got.Negative = append(got.Negative, resp.Negative...)

		pages++

		if !more {
			break
		}

		if next <= from {
			t.Fatalf("page %d did not advance: from %d to %d", pages, from, next)
		}

		from = next

		if pages > 200 {
			t.Fatal("pagination did not terminate")
		}
	}

	if pages < 2 {
		t.Errorf("an observation of %d reads fitted in %d page(s), so nothing was tested",
			len(obs.Reads), pages)
	}

	if len(got.Reads) != len(obs.Reads) {
		t.Errorf("%d reads arrived, want %d", len(got.Reads), len(obs.Reads))
	}

	if len(got.Negative) != len(obs.Negative) {
		t.Errorf("%d negatives arrived, want %d", len(got.Negative), len(obs.Negative))
	}

	if len(got.Listings) != len(obs.Listings) {
		t.Errorf("%d listings arrived, want %d", len(got.Listings), len(obs.Listings))
	}
}

// Paging is deterministic, or two runs of one build key differently.
//
// The same argument the observed key itself makes about map order (green paper
// 4.6): what crosses the wire must not depend on which bucket Go walked first.
func TestPagingAnObservationIsDeterministic(t *testing.T) {
	t.Parallel()

	obs := bigObs(5000)

	first, next1, more1 := observationPage(obs, 0)
	second, next2, more2 := observationPage(obs, 0)

	if next1 != next2 || more1 != more2 {
		t.Fatalf("two pages of the same observation ended differently: %d/%v and %d/%v",
			next1, more1, next2, more2)
	}

	if len(first.Reads) != len(second.Reads) || len(first.Negative) != len(second.Negative) {
		t.Error("the same page held different entries on a second call")
	}

	for p := range first.Reads {
		if _, ok := second.Reads[p]; !ok {
			t.Fatalf("%s was in one rendering of the page and not the other", p)
		}
	}
}

// A small observation is one page and says so, so the common case pays nothing.
func TestASmallObservationIsOnePage(t *testing.T) {
	t.Parallel()

	_, _, more := observationPage(bigObs(3), 0)
	if more {
		t.Error("three reads were split across pages")
	}
}

func mustID(t *testing.T, s string) ir.NodeID {
	t.Helper()

	ids, err := decodeStack([]string{s})
	if err != nil {
		t.Fatalf("undecodable digest %q: %v", s, err)
	}

	return ids[0]
}
