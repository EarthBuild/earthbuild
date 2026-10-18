// Command probe is a step body for the VM tests: a static binary small enough
// to place in a layer, which reports whether it can see outside its chroot.
package main

import (
	"fmt"
	"os"
)

func main() {
	// A second question, asked with an argument so the confinement probe above
	// stays the default and unchanged.
	//
	// Opening /dev/tty succeeds only for a process with a *controlling*
	// terminal, which is what the path means. A step whose streams merely point
	// at a pty passes `test -t 0` and has none, and that is the difference an
	// interactive construct is built on.
	if len(os.Args) > 1 && os.Args[1] == "ctty" {
		f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			fmt.Println("NO-CTTY")
			os.Exit(0)
		}

		_ = f.Close()

		fmt.Println("HAS-CTTY")

		return
	}

	// /etc/passwd exists on any real host and in the sandbox image, but must NOT
	// exist inside a layer stack that does not contain it. Seeing it means the
	// chroot did not take, which means A3 is false and no result here is
	// cacheable.
	_, err := os.Stat("/etc/passwd")
	if err == nil {
		fmt.Println("ESCAPED")
		os.Exit(2)
	}

	err = os.WriteFile("/produced", []byte("by the step"), 0o644)
	if err != nil {
		fmt.Println("WRITE FAILED:", err)
		os.Exit(3)
	}

	fmt.Println("CONFINED")
}
