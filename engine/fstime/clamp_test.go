package fstime

import (
	"testing"
	"time"
)

// SOURCE_DATE_EPOCH decides whether timestamps are pinned or true.
//
// Both directions have a good case - a build that must be byte-reproducible
// wants every timestamp fixed, a build feeding an incremental compiler wants
// them real - so this engine picks neither and takes the instruction. The
// reference clamps to a fixed date and offers `--keep-ts` to escape; this is
// the same choice spelled the way the rest of the reproducible-builds world
// spells it (E34).
//
// Unset means preserve, which is what this engine already did, so no output
// changes under anyone who has not asked.
func TestSourceDateEpochDecidesTheClamp(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  string
		want time.Time
		ok   bool
	}{
		{name: "unset", set: "", ok: false},
		{name: "an epoch", set: "981173106", want: time.Unix(981173106, 0), ok: true},
		{name: "zero is a time", set: "0", want: time.Unix(0, 0), ok: true},
		{
			// Refused rather than ignored: a misspelt SOURCE_DATE_EPOCH means
			// somebody wanted a reproducible build and is not getting one, and
			// silently preserving timestamps would look exactly like success.
			name: "not a number",
			set:  "yesterday",
			ok:   false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SOURCE_DATE_EPOCH", tc.set)

			got, ok := Clamp()
			if ok != tc.ok {
				t.Fatalf("clamp is %v, want %v", ok, tc.ok)
			}

			if ok && !got.Equal(tc.want) {
				t.Errorf("clamp is %s, want %s", got, tc.want)
			}
		})
	}
}
