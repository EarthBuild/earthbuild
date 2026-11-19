package guest

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAMountTargetIsResolvedInsideTheStep.
//
// **A mount lands where the step would look, and never outside it.** The
// target was `root + /var/run/docker.sock` with nothing followed, so the kernel
// followed it instead - on the guest, outside the chroot. Debian's
// `/var/run -> /run` is absolute, so the socket was bound onto the guest's own
// /run, the step found nothing at /var/run/docker.sock, and `docker load` said
// `Cannot connect to the Docker daemon`. Alpine's `../run` only worked because
// `root/var/..` happens to be root.
//
// The same walk is what keeps an image from choosing where a mount goes: a
// `/var/run -> /etc` in a base image is the step's /etc, not the guest's.
func TestAMountTargetIsResolvedInsideTheStep(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		links map[string]string // path in the step -> link text
		dirs  []string
		at    string
		want  string // relative to root
	}{
		{
			"absolute link",
			map[string]string{"var/run": "/run"},
			[]string{"run", "var"},
			"/var/run/docker.sock", "run/docker.sock",
		},
		{
			"relative link",
			map[string]string{"var/run": "../run"},
			[]string{"run", "var"},
			"/var/run/docker.sock", "run/docker.sock",
		},
		{
			"a link to a link",
			map[string]string{"var/run": "/a/run", "a": "/b"},
			[]string{"b/run", "var"},
			"/var/run/docker.sock", "b/run/docker.sock",
		},
		{
			"climbing out is clamped",
			map[string]string{"var/run": "../../../../etc"},
			[]string{"etc", "var"},
			"/var/run/x", "etc/x",
		},
		{"a missing tail", nil, []string{"var"}, "/var/new/../../../x", "x"},
		{"nothing to follow", nil, []string{"usr/bin"}, "/usr/bin/docker", "usr/bin/docker"},
		{
			"the final element",
			map[string]string{"etc/hosts": "/run/hosts"},
			[]string{"etc", "run"},
			"/etc/hosts", "run/hosts",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()

			for _, d := range tc.dirs {
				err := os.MkdirAll(filepath.Join(root, d), 0o750)
				if err != nil {
					t.Fatal(err)
				}
			}

			for at, to := range tc.links {
				err := os.Symlink(to, filepath.Join(root, at))
				if err != nil {
					t.Fatal(err)
				}
			}

			got, err := mountTargetIn(root, tc.at)
			if err != nil {
				t.Fatal(err)
			}

			if want := filepath.Join(root, tc.want); got != want {
				t.Errorf("mountTargetIn(%q) = %q, want %q", tc.at, got, want)
			}
		})
	}
}

// A loop is refused rather than followed for ever.
func TestAMountTargetInALoopIsRefused(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := os.Symlink("/b", filepath.Join(root, "a"))
	if err == nil {
		err = os.Symlink("/a", filepath.Join(root, "b"))
	}

	if err != nil {
		t.Fatal(err)
	}

	_, err = mountTargetIn(root, "/a/x")
	if err == nil {
		t.Fatal("a symlink loop resolved")
	}
}
