package interp

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// `SAVE ARTIFACT ./*` publishes what the glob matches, and a consumer names one
// of them.
//
// The pattern is declared once and the files exist only after the step has run,
// so the artifact is recorded as the pattern. A reference to `one` then matched
// neither the recorded name (`*`) nor the recorded path (`/s/*`), fell through
// to the name as written, and asked the producing target for `/one` - which it
// does not have. "COPY /one: nothing in that target has it", reported one target
// away from the SAVE that was meant to publish it.
//
// Found by building `+all-binaries`: every per-platform target ends in `SAVE
// ARTIFACT ./*`, so nothing downstream could reach a single binary (E580).
func TestAGlobbedSaveCanBeNamedByAConsumer(t *testing.T) {
	t.Parallel()

	from := &ir.Node{}

	p := &Plan{Artifacts: []Artifact{
		{From: from, Name: "*", Path: "/s/*"},
	}}

	for _, c := range []struct {
		name, want string
	}{
		{"one", "/s/one"},
		{"/one", "/s/one"},
		{"two", "/s/two"},
		// A star does not cross a separator, here as everywhere else: `./*`
		// publishes what is beside it, not what is under it.
		{"nested/deep", "/nested/deep"},
	} {
		if got := p.savedAt(from, c.name); got != c.want {
			t.Errorf("savedAt(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

// A concrete save is unaffected: the pattern branch must not answer for names
// the target never published.
func TestAConcreteSaveStillResolvesExactly(t *testing.T) {
	t.Parallel()

	from := &ir.Node{}

	p := &Plan{Artifacts: []Artifact{
		{From: from, Name: "binary", Path: "/out/binary"},
	}}

	if got := p.savedAt(from, "binary"); got != "/out/binary" {
		t.Errorf("savedAt(binary) = %q, want /out/binary", got)
	}

	// Never saved, so the answer is the name as written - which is what makes
	// the failure name what it looked for.
	if got := p.savedAt(from, "absent"); got != "/absent" {
		t.Errorf("savedAt(absent) = %q, want /absent", got)
	}
}
