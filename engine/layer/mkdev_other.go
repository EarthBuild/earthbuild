//go:build !unix

package layer

// mkdev has no device numbers to make where the platform has none.
//
// Zero rather than a guess: a platform that cannot create a device node also
// never walks one, so the only entry this could describe is one that is not
// there. Assumption A2 - the result stays correct and the engine must not
// pretend to have recorded something it cannot.
func mkdev(uint32, uint32) uint64 { return 0 }
