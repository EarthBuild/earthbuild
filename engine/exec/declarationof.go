package exec

import (
	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/decl"
)

// declarationOf is what a materialised base declares.
//
// Asked of the handle, which is the party that assembled the stack and read its
// declarations to do it. The alternative - reading them again from the store -
// is a second answer to a question that already has one, and it is an answer
// only a host that can open the store is able to give (E554).
//
// A materialiser with nothing to say yields the zero declaration, which is what
// a stack of plain layers declares and what every caller handled before any of
// this existed.
func declarationOf(h core.Handle) decl.Declaration {
	said, ok := h.(interface{ Declaration() decl.Declaration })
	if !ok {
		return decl.Declaration{}
	}

	return said.Declaration()
}
