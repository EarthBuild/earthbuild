package exec

import "testing"

// Being inside a container is read from a marker, not inferred.
//
// Both markers, because the runtime that made the container is not this engine's
// business: Docker writes `/.dockerenv` and Podman writes `/run/.containerenv`,
// and a build inside either is equally inside an outer step.
//
// **Not from cgroups.** The old trick - looking for `docker` or `kubepods` in
// `/proc/self/cgroup` - stopped working with cgroup v2, where the path is often
// just `0::/` whether containerised or not. A probe that answers "no" on a
// modern host would send an inner build down the machine's-daemon path and get
// it refused for a reason that is not true.
func TestBeingInsideAContainerIsReadFromAMarker(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		there string
		want  bool
	}{
		{"docker", "/.dockerenv", true},
		{"podman", "/run/.containerenv", true},
		{"neither", "", false},
		{"a cgroup file is not a marker", "/proc/self/cgroup", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := inContainer(func(p string) bool { return tc.there != "" && p == tc.there })

			if got != tc.want {
				t.Errorf("inContainer = %v, want %v (with %s present)", got, tc.want, tc.there)
			}
		})
	}
}
