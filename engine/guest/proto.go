// Package guest is the protocol between the engine and an agent running inside
// a VM, and the two halves that speak it.
//
// It exists because of experiment E1b: `container exec` accepts no mount
// options, so a running VM cannot have a filesystem attached from outside. The
// host CAS is therefore shared in at boot over virtiofs, and layer assembly -
// overlay mounts, rootfs construction, per-step snapshots - happens *inside*
// the guest. That agent is earth-guestd.
//
// The protocol is deliberately poorer than the IR, for the same reasons the
// fleet's step assignment is (green paper C.3): the wire's constraints -
// versioning, forward compatibility, canonical bytes - stay off the IR, and a
// request type that cannot express a host operation cannot be used to ask for
// one.
package guest

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Version is the protocol version. Host and guest may be updated separately -
// the guest ships inside a VM image - so the version is checked on the first
// exchange rather than assumed.
//
// **Bump this for any change to the wire's semantics, not only its shape.**
// Version 2 added request ids: the frames still parse under version 1, so an old
// guest accepts them, answers without an id, and the new host waits forever for
// a reply that will never be matched. The symptom is a build that hangs, which
// is worse than one that refuses - and the version check exists precisely to
// turn the first into the second.
// Version 3 added mounts. Bumped rather than added quietly: an older guest
// would ignore an unknown field and run the step *without* its mount, which is
// a step that cannot see its cache reporting success - the silent-wrong failure
// this protocol is versioned to prevent.
// Version 8 added cancel: an older guest would ignore the request and keep
// running the step, so the host would report a build as interrupted while the
// sandbox carried on writing - the silent-disagreement failure this version
// check exists to turn into a refusal.
// Version 16 added the step's resource usage. An older guest reports zero, and
// a build asked for `--exec-stats` then says a step used no CPU at all - which
// is a number, and wrong, where absence would have been readable.
// Version 15 added the cache sharing mode: an older guest would ignore
// `Exclusive` and queue nothing, so two steps declaring `--sharing=locked` would
// use one directory at once - the promise this engine had just started keeping,
// silently dropped again.
// Version 14 added `COPY --chown`: an older guest would ignore it and leave the
// files owned by whoever the copy ran as, producing an image whose files belong
// to the wrong user - a failure that surfaces at runtime, in a container, a long
// way from the build.
// Version 13 added host entries: an older guest would ignore them and resolve
// every name by whatever the image shipped, so a step would reach the real
// `api.test` instead of the address the Earthfile named - a build that talks to
// the wrong machine and reports success.
// Version 12 added ephemeral mounts: an older guest would read one as a mount of
// its store's root directory - no id, so `filepath.Join(store, "")` - and bind
// the whole layer store into the step at the daemon's path. Not a degradation,
// a different build entirely.
// Version 11 added a daemon of the step's own: an older guest would ignore the
// request and run the body with nothing behind the socket, so the step's first
// `docker` command would fail saying it cannot reach a daemon - a confusing
// message about a request that was silently declined.
const Version = 16

// Kind identifies a request.
type Kind string

// The requests. Deliberately few: every addition is a thing that can go wrong
// across a version skew.
const (
	KindHello       Kind = "hello"
	KindMaterialise Kind = "materialise"
	KindRelease     Kind = "release"
	KindObserve     Kind = "observe"
	KindExec        Kind = "exec"
	KindCapture     Kind = "capture"
	KindExport      Kind = "export"
	KindCopy        Kind = "copy"
	// KindCancel abandons a request that is still running, by id.
	//
	// The only request that refers to another one. It exists because a step is
	// the one thing here that can take minutes, and a build that cannot be
	// interrupted while a step runs is a build nobody can stop: Ctrl-C during a
	// five-minute compile was a five-minute wait (E56).
	KindCancel Kind = "cancel"
)

// Request is what the host asks of the guest.
//
// Note what cannot be expressed: there is no field that names a host operation.
// A guest is not the invoking machine and can never satisfy host locality
// (green paper §4.7.1), so the request type simply has no way to ask - a
// property of the type rather than a check that could be forgotten.
type Request struct {
	// ID correlates a reply with the request that asked for it.
	//
	// Required, because replies arrive out of order: the scheduler runs
	// independent steps at once, and a slow materialise must not hold up a fast
	// exec behind it. Matching replies by arrival instead would hand one step
	// another step's filesystem, which is a wrong build that reports success.
	ID uint64 `json:"id"`

	// Cancel names the request this one abandons. Cancel-only.
	//
	// A separate field from ID, because a cancel is itself a request with its
	// own id and its own reply: conflating the two would leave the caller
	// unable to tell "the cancel arrived" from "the step it named finished".
	Cancel uint64 `json:"cancel,omitempty"`

	Kind Kind `json:"kind"`

	// Prepared is a base somebody has already assembled, used as it is.
	//
	// **A materialisation strategy arriving as a fact.** Every base until now was
	// a stack of layers the guest assembles; a lazily materialised one is a
	// directory primed with the paths a step was predicted to read (E292), and
	// it is not a layer - a fragment never is (E281). Passing it as a layer id
	// would be passing a lie the cache would key on, so it is said explicitly.
	//
	// Materialise-only, and never together with Stack: the two say different
	// things about where a step's filesystem comes from, and a caller that sent
	// both does not know which it wants (E300).
	Prepared string `json:"prepared,omitempty"`

	Version int      `json:"version,omitempty"`
	Stack   []string `json:"stack,omitempty"`  // layer ids, hex, oldest first
	Handle  string   `json:"handle,omitempty"` // returned by materialise
	Argv    []string `json:"argv,omitempty"`   // exec only
	Path    string   `json:"path,omitempty"`   // export only: what to take
	Dest    string   `json:"dest,omitempty"`   // export and copy: the destination
	From    []string `json:"from,omitempty"`   // copy only: the layers to copy out of, oldest first
	// Interactive says a terminal is being sent on the descriptor channel and
	// this step is to run on it. Exec only.
	Interactive bool `json:"interactive,omitempty"`
	// Trace asks for the step's reads to be observed.
	//
	// Off by default, and that is a cost decision rather than a safety one: a
	// traced step pays a round trip through the engine for every path it opens
	// or asks about, and `cat` alone names fifty-five (E210). What it buys is
	// the only observation source a RUN has, so a step that is not traced can be
	// built and cached and can never be reused against a different base.
	Trace bool `json:"trace,omitempty"`
	// NoNet is `RUN --network=none`: run this step in an empty network
	// namespace. Exec only.
	NoNet bool `json:"noNet,omitempty"`
	// DirCopy is `COPY --dir`: the directory itself rather than its contents.
	// Without it a directory source contributes what is in it, which is the rule
	// everywhere else and one a trailing separator cannot express.
	DirCopy bool `json:"dirCopy,omitempty"`
	// NoFollow is `COPY --symlink-no-follow`: a symlink the copy names arrives
	// as a link rather than as what it points at.
	//
	// Additive, and the version bump beside it is why it can be: a guest that
	// did not know this field would ignore it and dereference where the author
	// asked for a link - a wrong build reported as a success. The handshake
	// refuses the pairing instead.
	NoFollow bool `json:"noFollow,omitempty"`
	// KeepOwn is `COPY --keep-own`: uid and gid travel with the copy.
	KeepOwn bool `json:"keepOwn,omitempty"`
	// Chown is `COPY --chown=user[:group]`: what the copied files belong to.
	//
	// The specification rather than a pair of numbers, because the names are
	// resolved against the *destination image* and only the guest has it (A3).
	Chown string `json:"chown,omitempty"`
	// Stream asks for the step's output as it appears, rather than only at the
	// end. Requested by the host so the guest does not pay for framing nobody is
	// listening to.
	Stream bool `json:"stream,omitempty"`
	// Dir is the working directory inside the step's filesystem: WORKDIR.
	Dir string   `json:"dir,omitempty"`
	Env []string `json:"env,omitempty"` // exec only, "K=V"; ε, and only ε
	// BaseEnv is what the base image declared, under ε.
	//
	// Not ambient state: it comes from the image this step stands on, which is
	// an input and therefore already in the step's key. Without it a build on
	// `node:20-alpine` saw NODE_VERSION empty and any image with an unusual
	// PATH could not find its own tools.
	BaseEnv []string `json:"baseEnv,omitempty"`
	// Mounts are directories made visible inside the step's filesystem.
	//
	// Not layers: a layer is stacked and becomes part of what the step produces,
	// while a mount is a hole in that filesystem onto something that outlives
	// it. That is what a cache mount is for, and it is why a mounted directory
	// is deliberately *not* captured when the step's delta is taken.
	Mounts []Mount `json:"mounts,omitempty"`
	// Daemon asks the guest to run a container daemon inside this step, for the
	// duration of this step. Exec only, and nil for every step that is not
	// inside a WITH DOCKER.
	//
	// A pointer so that "no daemon" is absent from the wire rather than present
	// and empty: the guest can then tell a step that wants none from one that
	// asked for one and filled nothing in, which is a caller bug and is refused
	// rather than defaulted.
	Daemon *Daemon `json:"daemon,omitempty"`
	// Hosts are name-to-address entries the step resolves by, as "name address".
	//
	// `HOST api.test 10.0.0.1`. They become an `/etc/hosts` bound into the step,
	// rather than written into its filesystem: what a step writes into its own
	// root is captured, and a resolver file is the engine's doing rather than
	// the step's output (E398 learned this about a daemon's storage).
	Hosts []string `json:"hosts,omitempty"`
}

// Daemon is a container daemon a step asked to have running inside it.
//
// It lives and dies with the step. That is not a simplification: a daemon
// outliving its step is a daemon holding the step's overlay open, and the layer
// the capture then takes is of a filesystem still being written to.
type Daemon struct {
	// Root is where it keeps everything, inside the step's filesystem.
	//
	// What is at that path decides whether the storage survives: a mount puts it
	// on a cache directory that outlives the step, and no mount leaves it in the
	// step's own overlay, which is thrown away. The executor decides that (E365)
	// and the guest is not told which it is - the daemon's behaviour is
	// identical either way, and a guest that knew would be a second place the
	// rule is written.
	Root string `json:"root"`
	// Socket is where it listens, inside the step's filesystem.
	//
	// Said rather than derived at both ends. A host that computes this path and
	// a guest that computes it again are two implementations of one rule, and
	// the day they disagree the daemon listens where the client does not look -
	// which presents as a step whose first `docker` command cannot reach a
	// daemon that is running perfectly well.
	Socket string `json:"socket"`
}

// Mount is a directory bound into a step's filesystem.
type Mount struct {
	// ID names the shared directory, which the *guest* resolves against its own
	// store.
	//
	// Not a path. The host and the guest are different machines - a VM on macOS
	// - and the store the host can see at one path is mounted somewhere else
	// inside the guest. Sending a host path made the guest create that path in
	// its own filesystem, so the first build's cache was written somewhere that
	// vanished with the VM and the second build found nothing.
	ID string `json:"id"`
	// Target is where it appears inside the step's filesystem, absolute.
	Target string `json:"target"`
	// ReadOnly binds it so the step cannot write through it.
	ReadOnly bool `json:"readOnly,omitempty"`
	// Persist copies the directory in and out instead of binding it.
	//
	// A bind is invisible to the capture - what a step writes into it goes to
	// the bound source and never reaches the overlay's upper layer, which is
	// what makes an ordinary cache stay out of the image. `--persist` asks for
	// the contents to be *in* the image, so they have to be written into the
	// step's own root, which means copying.
	Persist bool `json:"persist,omitempty"`
	// Exclusive asks for `--sharing=locked`: one step in this directory at a
	// time. The alternative is `shared`, where several use it at once and the
	// tools inside cope with their own locks (E432).
	Exclusive bool `json:"exclusive,omitempty"`
	// Ephemeral asks the guest to make a directory for this step and remove it
	// when the step is over.
	//
	// A mount with nowhere to come from and nowhere to go. It exists because
	// "discarded with the step" and "not captured from the step" are different
	// properties: a step's overlay is what the capture turns into a layer, so
	// leaving something in it discards nothing - it ships it. A mount is a hole
	// in the step's filesystem and is therefore invisible to the capture, and an
	// ephemeral one is a hole onto a directory nothing else will ever see (E398).
	//
	// The daemon a WITH DOCKER step starts for itself is what needs it: its
	// storage must be out of the image and must not outlive the step, and a
	// named cache differs only in the second.
	Ephemeral bool `json:"ephemeral,omitempty"`
	// Secret is the credential's value, present only for a secret mount.
	//
	// It travels on the wire and never reaches a layer: the guest writes it to
	// a private file outside the step's filesystem and binds that in, so what
	// the step reads is a mount rather than a file the overlay would capture.
	// Writing it into the step's root would put the credential in the image.
	//
	// Kept out of the graph entirely - the IR carries the secret's id and
	// nothing else - so there is no key it could change and no record of it in
	// a plan.
	Secret string `json:"secret,omitempty"`

	// Sandbox names a path in the sandbox's own filesystem to bind, rather than
	// something in the layer store.
	//
	// WITH DOCKER is what needs it: the daemon runs in the VM, and a step is
	// given the client and a socket to reach it. Neither is a layer - they
	// belong to the machine and outlive the step - so they arrive the way a
	// cache does, as a hole in the step's filesystem, and the mount machinery
	// already knows how to make one.
	Sandbox string `json:"sandbox,omitempty"`

	// Mode is the permission the mount point is created with, when one has to
	// be created. Zero means the default, which is right for a secret and wrong
	// for a device: a step that cannot open /dev/null has no /dev/null.
	Mode uint32 `json:"mode,omitempty"`
}

// Response is what the guest returns.
type Response struct {
	// ID is the request this answers.
	ID uint64 `json:"id"`

	// Chunk is a piece of a running step's output.
	//
	// A frame carrying one is *not* the reply: it is progress from a request
	// still in flight, and the caller keeps waiting. Streaming rather than
	// returning everything at the end is what makes a four-minute step
	// distinguishable from a hung one.
	Chunk string `json:"chunk,omitempty"`
	// Streaming marks such a frame. A separate flag rather than "Chunk is
	// non-empty", because a step legitimately prints an empty line.
	Streaming bool `json:"streaming,omitempty"`

	Err      string            `json:"err,omitempty"`
	Version  int               `json:"version,omitempty"`
	Handle   string            `json:"handle,omitempty"`
	Root     string            `json:"root,omitempty"`
	Reads    map[string]string `json:"reads,omitempty"`
	Negative []string          `json:"negative,omitempty"`
	Listings map[string]string `json:"listings,omitempty"`
	// Incomplete is the guest admitting it missed something. Without it a
	// source that knows it is lossy has no way to say so, and the host decodes
	// a partial observation as a complete one - which is the false hit Κ₂
	// exists to prevent (green paper §3.4, I3).
	Incomplete bool `json:"incomplete,omitempty"`
	// Why names each distinct reason the guest missed something. Diagnostic,
	// and the thing that turns "this step never earns an L2 hit" from a mystery
	// into a sentence.
	Why []string `json:"why,omitempty"`
	// Degraded is why a step's resource limits were not applied.
	//
	// Carried per response rather than announced at shutdown: I11 is
	// degrade-and-say-so, and a build whose steps all ran without the ceiling
	// they asked for has to learn that while it can still act on it (E123).
	Degraded string `json:"degraded,omitempty"`

	// Layer and Content are a capture's two digests, hex. Layer is the identity
	// (timestamps included); Content excludes them, so determinism screening
	// judges a step on what it produced rather than on when it ran.
	Layer   string `json:"layer,omitempty"`
	Content string `json:"content,omitempty"`
	Bytes   int64  `json:"bytes,omitempty"`
	// CPUNanos and MaxRSS are what the step's process spent, for `--exec-stats`.
	//
	// Reported by the guest because the kernel reports usage to the parent at
	// wait, and by the time a result reaches the host the process is gone
	// (E467). Zero where the platform cannot state one honestly rather than
	// converted with a guess.
	CPUNanos int64  `json:"cpuNanos,omitempty"`
	MaxRSS   uint64 `json:"maxRSS,omitempty"`

	// Exit is the step's exit code. A non-zero exit is a *result*, not a
	// protocol error: the step ran and failed, which the engine records and
	// caches like any other outcome. Conflating the two would make a failing
	// build indistinguishable from a broken guest.
	Exit   int    `json:"exit"`
	Output string `json:"output,omitempty"`
}

// conn frames JSON messages with a u32 length prefix.
//
// Framing is explicit rather than newline-delimited because a path can contain
// anything, and a protocol that breaks on an unusual filename is a protocol
// that breaks in exactly the situations worth debugging.
type conn struct {
	r *bufio.Reader

	w  sync.Mutex
	wc io.Writer
}

func newConn(rw io.ReadWriter) *conn {
	return &conn{r: bufio.NewReader(rw), wc: rw}
}

// send is safe for concurrent use: the write lock covers header and body
// together, so two replies cannot interleave into one unreadable frame.
func (c *conn) send(v any) error {
	c.w.Lock()
	defer c.w.Unlock()

	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	var hdr [4]byte

	binary.BigEndian.PutUint32(hdr[:], uint32(len(b))) //nolint:gosec // bounded by message size

	_, err = c.wc.Write(hdr[:])
	if err != nil {
		return fmt.Errorf("write header: %w", err)
	}

	_, err = c.wc.Write(b)
	if err != nil {
		return fmt.Errorf("write body: %w", err)
	}

	return nil
}

func (c *conn) recv(v any) error {
	var hdr [4]byte

	_, err := io.ReadFull(c.r, hdr[:])
	if err != nil {
		return err //nolint:wrapcheck // io.EOF must stay recognisable to callers
	}

	n := binary.BigEndian.Uint32(hdr[:])
	if n > 1<<24 {
		return fmt.Errorf("message of %d bytes exceeds the limit", n)
	}

	b := make([]byte, n)
	_, err = io.ReadFull(c.r, b)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	err = json.Unmarshal(b, v)
	if err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}

	return nil
}

// encodeStack renders layer ids for the wire.
func encodeStack(stack []ir.NodeID) []string {
	out := make([]string, len(stack))
	for i, id := range stack {
		out[i] = id.String()
	}

	return out
}

// decodeStack parses layer ids from the wire, refusing anything malformed
// rather than silently producing a zero id - which would name the wrong layer.
func decodeStack(in []string) ([]ir.NodeID, error) {
	out := make([]ir.NodeID, len(in))

	for i, s := range in {
		if len(s) != ir.HashSize*2 {
			return nil, fmt.Errorf("layer id %d is %d hex chars, want %d", i, len(s), ir.HashSize*2)
		}

		for j := range ir.HashSize {
			var b byte

			for k := range 2 {
				c := s[j*2+k]

				switch {
				case c >= '0' && c <= '9':
					b = b<<4 | (c - '0')
				case c >= 'a' && c <= 'f':
					b = b<<4 | (c - 'a' + 10)
				default:
					return nil, fmt.Errorf("layer id %d contains %q", i, c)
				}
			}

			out[i][j] = b
		}
	}

	return out, nil
}
