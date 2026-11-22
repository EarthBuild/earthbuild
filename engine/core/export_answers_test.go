package core

import "github.com/EarthBuild/earthbuild/engine/ir"

// AnswersForTest exposes the rule deciding whether an entry can serve a step.
//
// In a file of its own rather than beside the rule, so that what is exported
// for a test is visible as such.
func AnswersForTest(n *ir.Node, e Entry) bool { return answersFor(n, e) }
