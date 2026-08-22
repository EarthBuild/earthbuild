package exec

import (
	"github.com/EarthBuild/earthbuild/engine/decl"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// stackDeclaration is what a step's base declares, composed from the stack.
//
// Every element the store holds a declaration for, applied in order. Elements it
// holds a layer for contribute nothing here, and an element it holds neither way
// is the materialiser's business to refuse (I18) rather than this function's to
// diagnose - it is asking a question about declarations, and "there is none" is
// a complete answer to that question.
//
// **BaseEnv is not sent to the guest any more.** The guest materialises the same
// stack and folds the same declarations, so it derives this rather than being
// told - which is what makes a delegate an engine (C.3) instead of a machine
// that trusts whatever the driver says its base declared.
func stackDeclaration(store string, base []ir.NodeID) decl.Declaration {
	var found []decl.Declaration

	for _, id := range base {
		d, held, err := decl.Read(store, id)
		if err != nil || !held {
			continue
		}

		found = append(found, d)
	}

	return decl.Compose(found...)
}
