//go:build linux

package exec

import (
	"bufio"
	"fmt"
	"os"
	osexec "os/exec"
	"strconv"
	"strings"
	"syscall"
)

// subRange is a block of ids the machine has delegated to this user.
type subRange struct {
	first int
	count int
}

// subIDs reads the range allocated to a user in /etc/subuid or /etc/subgid.
//
// The format is `name:first:count`, one line per allocation. Only the first is
// taken: a second is legal and rare, and one range keeps `/proc/pid/uid_map`
// something a person can read.
func subIDs(file, user string, id int) (subRange, bool) {
	f, err := os.Open(file) //nolint:gosec // a path this function is given
	if err != nil {
		return subRange{}, false
	}

	defer f.Close()

	me := strconv.Itoa(id)

	s := bufio.NewScanner(f)
	for s.Scan() {
		parts := strings.Split(strings.TrimSpace(s.Text()), ":")
		if len(parts) != 3 || (parts[0] != user && parts[0] != me) {
			continue
		}

		first, err1 := strconv.Atoi(parts[1])
		count, err2 := strconv.Atoi(parts[2])

		if err1 != nil || err2 != nil || count < 1 {
			continue
		}

		return subRange{first: first, count: count}, true
	}

	return subRange{}, false
}

// idMapper finds the setuid helper that writes a multi-id mapping.
//
// An unprivileged process may write only *one* entry to `/proc/pid/uid_map` -
// itself - which is what Go's UidMappings does and why a step could not become
// any other user. `newuidmap` is the shipped setuid program that writes a whole
// delegated range, and is how every rootless container runtime maps more than
// one id.
func idMapper(name string) (string, bool) {
	// NixOS puts its setuid wrappers first; the rest are where a distribution
	// installs `shadow`.
	for _, dir := range []string{"/run/wrappers/bin", "/usr/bin", "/bin", "/usr/local/bin"} {
		fi, err := os.Stat(dir + "/" + name)
		if err == nil && !fi.IsDir() {
			return dir + "/" + name, true
		}
	}

	p, err := osexec.LookPath(name)

	return p, err == nil
}

// delegatedIDs reports the ranges this user may map, and the helpers to do it.
func delegatedIDs() (uids, gids subRange, ok bool) {
	name := os.Getenv("USER")
	if name == "" {
		name = os.Getenv("LOGNAME")
	}

	uids, uok := subIDs("/etc/subuid", name, os.Geteuid())
	gids, gok := subIDs("/etc/subgid", name, os.Getegid())

	_, hasU := idMapper("newuidmap")
	_, hasG := idMapper("newgidmap")

	return uids, gids, uok && gok && hasU && hasG
}

// mapIDs gives a child the whole range this user has been delegated.
//
// Two entries: the invoking user becomes 0, and the delegated block becomes
// 1..count. A step can then be any of those ids - which `apt` needs, dropping to
// `_apt` to download, and which six of eleven corpus examples fail without.
func mapIDs(pid int, uids, gids subRange) error {
	for _, m := range []struct {
		tool  string
		host  int
		block subRange
	}{
		{"newuidmap", os.Geteuid(), uids},
		{"newgidmap", os.Getegid(), gids},
	} {
		bin, ok := idMapper(m.tool)
		if !ok {
			return fmt.Errorf("%s is not installed", m.tool)
		}

		out, err := osexec.Command(bin, strconv.Itoa(pid), //nolint:gosec // a path from a fixed list
			"0", strconv.Itoa(m.host), "1",
			"1", strconv.Itoa(m.block.first), strconv.Itoa(m.block.count),
		).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s: %w: %s", m.tool, err, strings.TrimSpace(string(out)))
		}
	}

	return nil
}

// rangedNamespace asks for a namespace whose ids the helper will map.
//
// No mappings here, deliberately: Go writes `/proc/pid/uid_map` itself between
// clone and exec, and an unprivileged process may write only one entry there.
// The range is written afterwards by `newuidmap`, and the guest re-executes so
// that its capabilities are computed with the mapping in place (E105).
func rangedNamespace() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS | syscall.CLONE_NEWPID,
	}
}
