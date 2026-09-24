package exec

import "strings"

// rootlessProbe is how this engine asks a machine about itself.
//
// Parameters rather than direct calls so the answer can be tested on a machine
// that has none of them - which includes every darwin developer working on the
// Linux backend, and is the reason `hostDockerMounts` takes its lookup the same
// way (E145).
type rootlessProbe struct {
	// look finds a program on PATH.
	look func(string) (string, bool)
	// subid says whether this user has a range allocated in the named file.
	subid func(file string) (bool, error)
	// userns is how many user namespaces the kernel will allow.
	userns func() (int, error)
}

// Readiness is whether a rootless daemon could run here, and what is missing.
type Readiness struct {
	OK  bool
	Why string
}

// helperPrograms are the setuid tools that map a range of ids into a namespace.
//
// Both, not either: `newuidmap` writes the uid map and `newgidmap` the gid map,
// and a daemon that can map one is a daemon that cannot start.
//
// **Not `rootlesskit` or `slirp4netns`**, which are how Docker's own script
// makes a namespace and a network for a daemon started from a login shell. A
// step here is already in a namespace this engine made, so requiring them would
// refuse machines that can host a daemon perfectly well - including the one this
// project measures on, which has neither (E363).
var helperPrograms = []string{"newuidmap", "newgidmap"}

// rootlessReady reports whether this machine could host a daemon of its own.
//
// **`WITH DOCKER` refuses with "not built yet"** (E355), which is true of this
// engine and says nothing about the machine. Three of the requirements are the
// host's rather than this engine's, and they fail differently:
//
//   - the **helpers** are a package away;
//   - a **range of ids** is a `usermod` away;
//   - **user namespaces** may be switched off by a distribution, and that is the
//     one an operator most often cannot change.
//
// A machine missing all three cannot host one however much is built here. A
// machine missing one is a sentence worth reading. The check exists to tell them
// apart (I10, E361).
//
// Every missing piece is named, not the first: an operator who installs the
// helpers and is then told about `/etc/subuid` has been sent round twice for one
// answer.
func rootlessReady(p rootlessProbe) Readiness {
	var missing []string

	for _, prog := range helperPrograms {
		if _, ok := p.look(prog); !ok {
			missing = append(missing,
				prog+" is not on PATH - it maps a range of ids into the"+
					" namespace, and comes with the uidmap package")

			break
		}
	}

	for _, file := range []string{"/etc/subuid", "/etc/subgid"} {
		got, err := p.subid(file)
		if err != nil || !got {
			missing = append(missing,
				file+" has no range for this user - a daemon of its own needs"+
					" ids to map, and `usermod --add-subuids` allocates them")

			break
		}
	}

	// **A daemon to run.** E361 asked Docker's question rather than this
	// engine's: rootless docker ships a script that makes a namespace with
	// `rootlesskit` and a network with `slirp4netns`, and the first version of
	// this check was written from that list. A step here is already inside a
	// namespace this engine made, so those are not the prerequisites - and the
	// machine this project measures on has neither of them, has a `dockerd`,
	// and was reported ready while nothing had asked whether a daemon existed
	// at all (E363).
	if _, ok := p.look("dockerd"); !ok {
		missing = append(missing,
			"dockerd is not on PATH - a block asking for a daemon of its own"+
				" needs one to give it, and this engine does not ship a copy")
	}

	n, err := p.userns()
	if err != nil || n <= 0 {
		missing = append(missing,
			"this kernel allows no user namespace to be created unprivileged"+
				" (user.max_user_namespaces), which a distribution sets and a"+
				" build cannot work around")
	}

	if len(missing) == 0 {
		return Readiness{OK: true}
	}

	return Readiness{Why: strings.Join(missing, "\n  ")}
}
