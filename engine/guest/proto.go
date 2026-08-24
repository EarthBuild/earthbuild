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
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/EarthBuild/earthbuild/engine/decl"
	"github.com/EarthBuild/earthbuild/engine/image"

	"github.com/EarthBuild/earthbuild/engine/ir"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
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
	// KindPackImage writes a loadable image archive into the store.
	//
	// `WITH DOCKER --load` needs the image as a tar the daemon in the sandbox
	// can read, and it is built from layers in the store. The host built it and
	// left it where the guest would find it, which is two parties sharing one
	// directory - and neither half of that survives the store becoming a disk
	// the guest owns (E558).
	//
	// Produced and consumed on the same side once this moves: nothing on the
	// host ever reads the archive.
	KindPackImage Kind = "pack-image"

	// KindSquash merges a range of the stack into one layer, in the store.
	//
	// Φ (green paper 4.8) replaces a run of layers with a single identity so
	// what remains can be mounted, and that identity is a tree somebody has to
	// build by reading every layer in the range. On a store the host shares it
	// did that itself; on a disk the guest owns, only the guest can (E557).
	KindSquash Kind = "squash"

	// KindStoreHas asks which of a set of layer ids the store holds.
	//
	// The first request that treats the guest's store as the store rather than
	// as a directory the host can also see. Under a shared mount the host
	// answered this with `os.Stat` and the question never crossed the wire; once
	// the store is a disk the guest owns, only the guest can answer it (E541).
	//
	// A set rather than one id, because the scheduler asks about a whole stack
	// at once and a round trip per layer is the cost this move exists to avoid.
	KindStoreHas Kind = "store-has"
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

	// FromEntry is the entry an observe reply should start at.
	//
	// Observe-only. A large observation does not fit in one frame - this
	// repository's own `+unit-test` produces 19.58 MB against a 16 MiB limit -
	// and a step whose observation cannot be delivered loses the second cache
	// tier entirely (E620). Paging it costs a round trip per page and keeps the
	// tier for exactly the steps that are most expensive to rerun.
	//
	// Absent means zero, which is what an older host sends and what a first page
	// asks for, so the two are the same request.
	FromEntry int `json:"fromEntry,omitempty"`

	Version int      `json:"version,omitempty"`
	Stack   []string `json:"stack,omitempty"`  // layer ids, hex, oldest first
	Handle  string   `json:"handle,omitempty"` // returned by materialise
	Argv    []string `json:"argv,omitempty"`   // exec only
	Path    string   `json:"path,omitempty"`   // export only: what to take
	Dest    string   `json:"dest,omitempty"`   // export and copy: the destination
	// Image is what a packed image declares: its name, its configuration, the
	// platform it is for. Pack-image only, with the layers in Stack and the
	// name to file it under in Into.
	//
	// The layers are ids rather than paths, because the host and the guest see
	// the store at different ones and a path from the wrong side names nothing.
	Image *ImageSpec `json:"image,omitempty"`

	// Into is the identity a squash's range collapses to, or the identity a
	// packed image is filed under. With the range or the layers in Stack.
	//
	// The caller's to decide: Φ derives it from the range (green paper 4.8), so
	// the guest is told what the result is called rather than choosing, and two
	// machines flattening the same range agree without consulting each other.
	Into string   `json:"into,omitempty"`
	From []string `json:"from,omitempty"` // copy only: the layers to copy out of, oldest first
	// Interactive says a terminal is being sent on the descriptor channel and
	// this step is to run on it. Exec only.
	Interactive bool `json:"interactive,omitempty"`
	// Clamp is the timestamp everything this operation writes should carry.
	//
	// Unix seconds, and nil for "keep what the file has", which is what a build
	// that has not asked for reproducible timestamps wants: an incremental
	// compiler downstream reads mtimes and a pinned one tells it nothing
	// changed.
	//
	// **In the request rather than in the guest's environment.** The guest read
	// `SOURCE_DATE_EPOCH` for itself and was duly given it at boot, and the
	// value still never reached a step's captured delta - the only place it
	// was consulted was `COPY`. Boot is the wrong place for it besides: a
	// sandbox is named by its image, store and memory, so it outlives the build
	// that started it and answers the next one with the last one's instruction
	// (E549).
	Clamp *int64 `json:"clamp,omitempty"`

	// MayShare permits an export to answer with a path in the store instead of
	// the bytes. See Response.Shared.
	//
	// Asked per request rather than forwarded to the sandbox, for the reason
	// `SOURCE_DATE_EPOCH` no longer is: a machine is named by its image, store
	// and memory, so a per-build decision left on one is answered from whatever
	// the first build wanted (E555). Off by default, so a host that has not
	// heard of sharing is served the bytes.
	MayShare bool `json:"mayshare,omitempty"`

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
	// Layer names a layer in the layer store, bound read-only: a bound view of
	// something this build already made (green paper §3.3d).
	//
	// Separate from ID, which the guest resolves against the *cache* store.
	// The two stores are different directories and a bound view resolved
	// against the wrong one is a step reading an empty directory rather than
	// the object it asked for.
	Layer string `json:"layer,omitempty"`
	// Sub is the subtree of Layer that appears at Target, or empty for all of
	// it. 𝑢 of green paper §3.3d.
	Sub string `json:"sub,omitempty"`
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

	// More says this observe reply is a page and further entries remain.
	//
	// Absent from an older guest, which is exactly right: it answers with
	// everything it has and there is nothing further to ask for.
	More bool `json:"more,omitempty"`

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
	// Declares is what the materialised stack says about how a step should run.
	//
	// **Sent back with the handle, because a declaration and a tree are a pair**
	// (green paper §3.2a). The host used to read it out of the store instead,
	// from the `.decl` files beside the base's layers - a read the disk cannot
	// serve, and one the guest had already done to build the mount (E554).
	//
	// Absent for a materialiser that has nothing to say, which is every
	// backend's answer for a stack of plain layers.
	Declares *decl.Declaration `json:"declares,omitempty"`

	// Shared is where an export's bytes already sit in the store, relative to
	// its root - so the host takes them off its own disk instead of having the
	// guest write them back over virtiofs.
	//
	// **The store is a disk both sides can read**, and an export that ignores
	// that ships 45 MB out of the VM to a host that already had it: 0.21s to
	// 0.28s of a 1.16s build, its single largest item (E568). Empty whenever
	// the guest cannot prove the merged file is the store's file unchanged, in
	// which case the bytes follow the ordinary way.
	Shared string `json:"shared,omitempty"`

	// Held is the subset of a store-has request's ids the store holds.
	//
	// The subset rather than a parallel array of booleans: absent means absent,
	// and a shorter list cannot be misread the way a truncated one could.
	Held []string `json:"held,omitempty"`

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
// errTooLarge marks a message that will not fit in a frame.
//
// Recognisable on purpose: a caller has to tell "this reply is impossible" from
// "the connection is gone", because the first is answerable and the second is
// not. Conflating them is what hung a build (E617).
var errTooLarge = errors.New("message exceeds the frame limit")

// maxMessage is the largest frame either side will read or write.
//
// One constant for both directions. It was a literal in `recv` alone, so the
// writer would happily emit a frame the reader was certain to reject - and the
// failure arrived as a dead connection several requests later (E617).
const maxMessage = 1 << 24

// kindOf names what is being sent, for the refusal above.
//
// Best effort: the two message types carry a kind, and anything else is
// described rather than guessed at.
func kindOf(v any) string {
	switch m := v.(type) {
	case Request:
		return string(m.Kind)
	case *Request:
		return string(m.Kind)
	default:
		return "response"
	}
}

// reply answers a request, or says why it cannot.
//
// A send that fails because the *reply* is too big leaves a healthy connection
// and a caller waiting for ever, so the reason goes back in a frame that fits.
// Any other failure is the connection itself, which the read loop discovers.
func reply(c *conn, kind Kind, resp Response) error {
	err := c.send(resp)
	if !errors.Is(err, errTooLarge) {
		return err
	}

	// **Named by the request it answers.** A response knows its size and not its
	// subject, so `kindOf` can only call it "response" - which is how a 19.5 MB
	// frame went three rounds without anybody being able to say what it held
	// (E617, E618). The request kind is the one thing that identifies it.
	return c.send(Response{
		ID:  resp.ID,
		Err: fmt.Sprintf("the reply to %s could not be sent: %v", kind, err),
	})
}

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

	// **Refused here, because the reader refuses it there.** `recv` gives up on
	// a frame this large, which kills the connection rather than the call - so
	// the error surfaced against whichever request came next and named neither
	// the sender nor what it was sending (E617). Nothing is written: a partial
	// frame leaves the reader mid-message and every later request is misread as
	// its continuation, which is why the symptom was a lost connection.
	if len(b) > maxMessage {
		return fmt.Errorf("%w: "+
			"a %s message of %d bytes exceeds the %d MiB the other side will read"+
			"\n  this is a limit of the protocol rather than of the build,"+
			" and the message that hit it is the thing to make smaller",
			errTooLarge, kindOf(v), len(b), maxMessage>>20)
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
	if n > maxMessage {
		return fmt.Errorf("message of %d bytes exceeds the %d MiB limit"+
			"\n  the sender refuses these too, so this is a version older than"+
			" that check on the other side of the connection", n, maxMessage>>20)
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

// ImageSpec is what a packed image declares, as it crosses the wire.
//
// **Not `image.Spec`**, which carries its layers as functions - a
// `LayerSource` is code that produces bytes, and code does not travel. Sending
// the whole thing worked only while every caller remembered to blank that field
// first, which is a rule the type did not enforce and the wire guard refused to
// accept (E558).
//
// So the wire has its own shape, holding exactly what a build knows and a guest
// cannot work out for itself. The layers are not here at all: they are ids in
// `Stack`, resolved against the store the guest can open.
type ImageSpec struct {
	// Healthcheck is how a running container reports its own health, nil when
	// the image declares none.
	Healthcheck *image.Healthcheck `json:"healthcheck,omitempty"`
	// Ref is what the image is called - `app:latest`.
	Ref string `json:"ref"`
	// Platform is what the image was built for. A runtime checks it before
	// starting anything, so an image without one loads and will not run.
	Platform ocispec.Platform `json:"platform"`
	// Config is what the target declared: entrypoint, environment, labels.
	Config ocispec.ImageConfig `json:"config"`
}

// Spec is this description as the image writer wants it, without layers.
//
// The layers are the guest's to resolve: it is the party that can open the
// store they are in.
func (i ImageSpec) Spec() image.Spec {
	return image.Spec{
		Ref:         i.Ref,
		Platform:    i.Platform,
		Config:      i.Config,
		Healthcheck: i.Healthcheck,
	}
}

// ImageSpecOf is a spec as it travels, with the layers left behind.
func ImageSpecOf(spec image.Spec) ImageSpec {
	return ImageSpec{
		Ref:         spec.Ref,
		Platform:    spec.Platform,
		Config:      spec.Config,
		Healthcheck: spec.Healthcheck,
	}
}
