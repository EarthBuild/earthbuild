package exec_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

// The container service reads as running from either CLI generation's report.
//
// **0.9.0 said so in a sentence; 0.12 onwards says it in a table.** The check
// looked for `apiserver is running`, which the rework of `system status` into a
// status/value table removed - so on any current CLI the Apple backend reported
// its service down on every build, with the service up. Both shapes are what
// `container system status` actually printed; the stopped ones are the refusals.
func TestTheContainerServiceReadsAsRunningFromEitherGeneration(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		out  string
		want bool
	}{
		{"0.9.0 running", "apiserver is running\napplication data root: /Users/x/Library/Application Support/com.apple.container/\n", true},
		{"1.x table, running", "FIELD              VALUE\nstatus             running\nappRoot            /Users/x/Library/Application Support/com.apple.container/\n", true},
		{"1.x table, no header", "status   running\ninstallRoot  /usr/local/\n", true},
		{"1.x table, not running", "FIELD   VALUE\nstatus  not running\n", false},
		{"1.x unregistered", "status  unregistered\n", false},
		{"0.9.0 not running", "apiserver is not running and not registered with launchd\n", false},
		{"a row that only mentions running", "containersRunning  3\n", false},
		{"empty", "", false},
	} {
		if got := exec.ContainerServiceRunning(tc.out); got != tc.want {
			t.Errorf("%s: ContainerServiceRunning = %v, want %v\n%s", tc.name, got, tc.want, tc.out)
		}
	}
}
