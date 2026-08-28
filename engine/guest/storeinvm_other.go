//go:build !darwin

package guest

// storeInVMByDefault is false where the sandbox is not a virtual machine.
//
// On Linux the store is a directory on the machine that is building, reached
// without crossing anything, so there is no device to move it to and nothing to
// gain by pretending otherwise. See the darwin file for what this decides there.
const storeInVMByDefault = false
