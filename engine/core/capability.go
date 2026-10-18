package core

import (
	"fmt"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Capabilities is what an engine can evaluate.
//
// It exists because the native engine is built incrementally and will, for a
// long time, be unable to build most real Earthfiles. Green paper I10 requires
// that it say so: an engine that cannot evaluate a construct refuses it, naming
// the construct and the alternative, and never approximates.
//
// Silently doing approximately the right thing is the failure mode a
// half-built engine invites, and it is worse than refusing because the result
// looks like a build.
type Capabilities struct {
	// Ops is the set of operations this engine can evaluate. A nil map means
	// no restriction, which is what the simulator and the tests use.
	Ops map[ir.OpKind]bool
	// Milestone names the release this engine corresponds to, so a refusal can
	// say when the missing construct arrives.
	Milestone string
}

// Supports reports whether an operation can be evaluated.
func (c *Capabilities) Supports(k ir.OpKind) bool {
	if c == nil || c.Ops == nil {
		return true
	}

	return c.Ops[k]
}

// arrival names the milestone at which a construct becomes available, so a
// refusal can tell the user when rather than only that.
var arrival = map[ir.OpKind]string{
	ir.OpImage: "M1",
	ir.OpExec:  "M1",
	ir.OpFile:  "M3",
	ir.OpMerge: "M4",
	ir.OpBuild: "M4",
	ir.OpLocal: "M7",
	ir.OpHost:  "M9",
}

// earthfileConstruct maps an operation back to the Earthfile the user wrote,
// because "OpHost is unsupported" is not a sentence anyone can act on.
var earthfileConstruct = map[ir.OpKind]string{
	ir.OpImage: "FROM",
	ir.OpExec:  "RUN",
	ir.OpFile:  "COPY",
	ir.OpMerge: "a merged target",
	ir.OpBuild: "BUILD",
	ir.OpLocal: "a local build context",
	ir.OpHost:  "LOCALLY",
}

// UnsupportedError reports a construct this engine cannot evaluate.
//
// Modelled on rustc's diagnostics rather than a bare "unsupported": it says
// what failed, where, what was expected, and how to proceed. A user who reads
// it should not have to ask a second question.
type UnsupportedError struct {
	Op        ir.OpKind
	Construct string
	Source    string
	Milestone string
	Current   string
}

func (e *UnsupportedError) Error() string {
	var b strings.Builder

	fmt.Fprintf(&b, "%s is not supported by the native engine", e.Construct)

	if e.Source != "" {
		fmt.Fprintf(&b, " (%s)", e.Source)
	}

	if e.Current != "" {
		fmt.Fprintf(&b, "\n  this engine implements %s", e.Current)
	}

	if e.Milestone != "" {
		fmt.Fprintf(&b, "; %s arrives at %s", e.Construct, e.Milestone)
	}

	fmt.Fprintf(&b, "\n  to build this now, use --engine=buildkit")

	return b.String()
}

// Check refuses a graph containing anything this engine cannot evaluate.
//
// It walks the whole graph *before* any step runs. Refusing late would leave a
// half-built tree and a user wondering which half is real; refusing first means
// nothing was started that cannot be finished.
func (c *Capabilities) Check(g *ir.Graph) error {
	if c == nil || c.Ops == nil {
		return nil
	}

	for _, n := range g.Nodes() {
		if c.Supports(n.Op.Kind) {
			continue
		}

		construct := earthfileConstruct[n.Op.Kind]
		if construct == "" {
			construct = n.Op.Kind.String()
		}

		return &UnsupportedError{
			Op:        n.Op.Kind,
			Construct: construct,
			Source:    n.Meta.Source,
			Milestone: arrival[n.Op.Kind],
			Current:   c.Milestone,
		}
	}

	return nil
}
