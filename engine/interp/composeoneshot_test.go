package interp_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// fakeCompose stands in for `docker` and answers as compose v5 does when the
// file has a one-shot service beside a long-running one: `up -d --wait` over
// every service fails the moment the one-shot exits, even with status 0.
//
// FAKE_RUNNING and FAKE_EXITED are what `ps` reports running and exited.
const fakeCompose = `#!/bin/sh
echo "docker $*" >> "$LOG"
case "$*" in
*" up -d --wait") echo "container default-oneshot-1 exited (0)" >&2; exit 1 ;;
*" up -d --wait "*) exit 0 ;;
*" ps --status running --services") [ -z "$FAKE_RUNNING" ] || echo "$FAKE_RUNNING" ;;
*" ps -a --status exited --format "*) [ -z "$FAKE_EXITED" ] || echo "$FAKE_EXITED" ;;
esac
`

// A compose service that runs once and exits does not fail the block.
//
// `up -d --wait` treats any container that stops as a failure, exit 0 included,
// so a file with a one-shot service - a migration, a seed, an `echo` - failed
// every build that used it, three attempts out of three. The reference brings
// services up with a bare `up -d` and never looks at how they ended.
//
// The wait is kept for what it is for: a service that stays up must be ready
// before the body connects to it, so after the one-shot ends the services still
// running are waited for again - and only those, because waiting on all of them
// would start the one-shot again and fail the same way.
func TestAOneShotComposeServiceDoesNotFailTheBlock(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    WITH DOCKER --compose docker-compose.yml
        RUN run-the-tests
    END
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	var script string

	for _, n := range p.Graph.Nodes() {
		if n.Meta.Description == "RUN run-the-tests" {
			script = n.Op.Args[len(n.Op.Args)-1]
		}
	}

	if script == "" {
		t.Fatalf("no body step:\n%s", describe(p.Graph.Nodes()))
	}

	for _, tc := range []struct{ name, running, exited, rewait string }{
		{"beside a long-running service", "db", "oneshot 0", "up -d --wait db"},
		{"with nothing left running", "", "oneshot 0", ""},
		{"that failed, which is reported", "db", "oneshot 3", "up -d --wait db"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			bin := t.TempDir()
			log := filepath.Join(bin, "log")

			for name, body := range map[string]string{
				"docker":        fakeCompose,
				"run-the-tests": "#!/bin/sh\necho ran >> \"$LOG\"\n",
			} {
				err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o700) //nolint:gosec // an executable is the point
				if err != nil {
					t.Fatal(err)
				}
			}

			cmd := exec.CommandContext(t.Context(), "/bin/sh", "-c", script)
			fake := []string{"FAKE_RUNNING=" + tc.running, "FAKE_EXITED=" + tc.exited}
			cmd.Env = append([]string{"PATH=" + bin + ":/usr/bin:/bin", "LOG=" + log}, fake...)

			out, err := cmd.CombinedOutput()
			calls, _ := os.ReadFile(log)

			if err != nil {
				t.Fatalf("the block failed because a one-shot service exited 0: %v\n%s\ncalls:\n%s",
					err, out, calls)
			}

			if !strings.Contains(string(calls), "\nran\n") {
				t.Errorf("the body never ran:\n%s", calls)
			}

			waits := strings.Count(string(calls), "up -d --wait")
			if tc.rewait == "" && waits != 1 {
				t.Errorf("waited again with nothing running, which starts the one-shot again:\n%s", calls)
			}

			if tc.rewait != "" && !strings.Contains(string(calls), tc.rewait+"\n") {
				t.Errorf("the service still running was not waited for (want %q):\n%s", tc.rewait, calls)
			}

			if tc.exited == "oneshot 3" && !strings.Contains(string(out), "compose service oneshot exited 3") {
				t.Errorf("a service that failed went unreported:\n%s", out)
			}
		})
	}
}

// A wait that failed with nothing exited failed for its own reason - a pull, an
// unhealthy service - and must still fail the block rather than run the body.
func TestAComposeWaitThatFailedForItsOwnReasonStillFails(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    WITH DOCKER --compose docker-compose.yml
        RUN run-the-tests
    END
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	var script string

	for _, n := range p.Graph.Nodes() {
		if n.Meta.Description == "RUN run-the-tests" {
			script = n.Op.Args[len(n.Op.Args)-1]
		}
	}

	bin := t.TempDir()
	log := filepath.Join(bin, "log")

	docker := filepath.Join(bin, "docker")

	err = os.WriteFile(docker, []byte(fakeCompose), 0o700) //nolint:gosec // an executable is the point
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.CommandContext(t.Context(), "/bin/sh", "-c", script)
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "LOG=" + log, "FAKE_RUNNING=db"}

	out, err := cmd.CombinedOutput()
	if err == nil {
		calls, _ := os.ReadFile(log)
		t.Errorf("the block ran although its services never came up:\n%s\ncalls:\n%s", out, calls)
	}
}
