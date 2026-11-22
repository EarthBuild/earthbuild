//go:build darwin

package exec_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

// The sandbox says how much memory it needs rather than taking the default.
//
// `container run` gives a VM 1 GiB unless told otherwise, and a build VM at
// 1 GiB is not a build VM. The symptom is not a clean out-of-memory message: a
// step runs, its output is captured, and the *copy into the layer store* fails
// with `mkdir ...: cannot allocate memory` - an ENOMEM from a filesystem
// operation, which reads like a disk problem and is not one.
//
// It surfaced on `examples/next-js`, and only in the corpus suite, which is the
// part worth remembering: the same target built alone on a fresh VM. Ten builds
// earlier in the same VM had filled 724 MB of a 1034 MB guest with page cache
// from writes over virtiofs, so the eleventh had nowhere to put a directory.
// A test that only ever builds one thing cannot see this.
func TestTheSandboxAsksForEnoughMemory(t *testing.T) {
	t.Parallel()

	a := exec.NewApple()

	if a.Memory == "" {
		t.Fatal("the sandbox takes whatever container's default is")
	}

	if !strings.HasSuffix(a.Memory, "G") {
		t.Fatalf("memory is %q, which is not a size in gibibytes", a.Memory)
	}

	got := strings.TrimSuffix(a.Memory, "G")
	if got == "1" || got == "0" {
		t.Errorf("the sandbox asks for %sG, which is the default it needs to beat", got)
	}
}

// Two VMs of different sizes are different machines, and the name says so.
//
// The name is how a running VM is found and reused. Left out of it, raising the
// memory would change nothing until every existing sandbox happened to be
// removed - and the build that needed the memory would keep failing while the
// setting said it had been given some.
func TestMemoryIsPartOfTheSandboxName(t *testing.T) {
	t.Parallel()

	small := exec.SandboxNameWith("alpine:3.20", "/opt/earth", "/store", "1G", nil)
	large := exec.SandboxNameWith("alpine:3.20", "/opt/earth", "/store", "8G", nil)

	if small == large {
		t.Error("a VM with more memory reuses the one that ran out of it")
	}

	again := exec.SandboxNameWith("alpine:3.20", "/opt/earth", "/store", "8G", nil)
	if again != large {
		t.Error("the same size gave two names")
	}
}
