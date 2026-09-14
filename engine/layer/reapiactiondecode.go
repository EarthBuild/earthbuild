package layer

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Reading the messages a client sends, which is the half R2b did not need.
//
// **Encoding an Action derives a key; decoding one runs it.** Until now this
// engine only ever wrote these - Κₜ is ℋ over an Action it built itself, so
// nothing had to read one back. A client that sends an Action is asking for it
// to be executed, and that means understanding every field rather than
// reproducing it.
//
// The decoder is deliberately strict where the encoder is terse: a field this
// does not understand is skipped, but one it does understand and cannot parse
// is refused. An action half understood still runs, still produces something,
// and is still filed under the key of the action that was *sent* - so the wrong
// answer is cached and nothing anywhere says why.

// CommandIn reads a Command message.
func CommandIn(b []byte) (Command, error) {
	var (
		c   Command
		old []string
	)

	err := eachField(b, func(field, wire int, v []byte) error {
		if wire != wireBytes {
			return nil
		}

		switch field {
		case fieldArguments:
			c.Arguments = append(c.Arguments, string(v))
		case fieldOutputFilesOld, fieldOutputDirsOld:
			// **Deprecated since v2.1 and still sent.** A client that believes
			// it is talking to an older service puts its outputs here, and one
			// reading only `output_paths` loses everything it asked for.
			// Precedence is REAPI's own: where `output_paths` is present these
			// are ignored, which is settled after the walk.
			old = append(old, string(v))
		case fieldCommandPlatform:
			// **The older home for a platform, and ignoring it is unsafe.** An
			// action naming a container-image here would otherwise run in
			// whatever base was to hand and be filed under the image it named,
			// which is the false hit I3 forbids - refusing it needs it read.
			return eachField(v, func(pf, pw int, pv []byte) error {
				if pf != fieldPlatformProps || pw != wireBytes {
					return nil
				}

				name, value, err := propertyIn(pv)
				if err != nil {
					return fmt.Errorf("a platform property: %w", err)
				}

				c.Platform = append(c.Platform, Property{Name: name, Value: value})

				return nil
			})
		case fieldEnv:
			name, value, err := propertyIn(v)
			if err != nil {
				return fmt.Errorf("an environment variable: %w", err)
			}

			c.Env = append(c.Env, Property{Name: name, Value: value})
		case fieldWorkingDir:
			c.WorkingDirectory = string(v)
		case fieldOutputs:
			c.OutputPaths = append(c.OutputPaths, string(v))
		}

		return nil
	})
	if err != nil {
		return Command{}, err
	}

	// REAPI's rule: "If output_paths is used, output_files and
	// output_directories will be ignored."
	if len(c.OutputPaths) == 0 {
		c.OutputPaths = old
	}

	return c, nil
}

// ActionIn reads an Action message.
//
// Absent and present-but-empty are kept apart throughout, because they are
// different bytes and so different keys: a salt that was not sent must not read
// back as an empty one, or this engine would name the action something its
// sender cannot reproduce.
func ActionIn(b []byte) (Action, error) {
	var a Action

	err := eachField(b, func(field, wire int, v []byte) error {
		switch {
		case field == fieldCommandDigest && wire == wireBytes:
			id, size, err := digestIn(v)
			if err != nil {
				return fmt.Errorf("the command digest: %w", err)
			}

			a.Command, a.CommandSize = id, size
		case field == fieldInputRoot && wire == wireBytes:
			id, size, err := digestIn(v)
			if err != nil {
				return fmt.Errorf("the input root digest: %w", err)
			}

			a.InputRoot, a.InputSize = id, size
		case field == fieldDoNotCache && wire == wireVarint:
			n, read := binary.Uvarint(v)
			if read <= 0 {
				return errors.New("do_not_cache is not a varint")
			}

			a.DoNotCache = n != 0
		case field == fieldSalt && wire == wireBytes:
			// Copied: `v` points into the caller's buffer, and a salt kept as a
			// view of it changes when that buffer is reused. This one reaches a
			// key.
			a.Salt = append([]byte(nil), v...)
		case field == fieldPlatform && wire == wireBytes:
			return eachField(v, func(pf, pw int, pv []byte) error {
				if pf != fieldPlatformProps || pw != wireBytes {
					return nil
				}

				name, value, err := propertyIn(pv)
				if err != nil {
					return fmt.Errorf("a platform property: %w", err)
				}

				a.Platform = append(a.Platform, Property{Name: name, Value: value})

				return nil
			})
		}

		return nil
	})
	if err != nil {
		return Action{}, err
	}

	if a.Command == (ir.NodeID{}) {
		return Action{}, errors.New(
			"an Action names no command, so there is nothing to run" +
				"\n  send the Command as a blob and put its digest in command_digest")
	}

	return a, nil
}

// digestIn reads one Digest message: the hash it names and how big the blob is.
//
// Size matters here where it did not for a tree walk: an Action's digest fields
// are handed back in an ActionResult and quoted to a client, which compares
// them with what it sent.
func digestIn(b []byte) (ir.NodeID, int64, error) {
	var (
		hex  string
		size int64
	)

	err := eachField(b, func(field, wire int, v []byte) error {
		switch {
		case field == fieldDigestHash && wire == wireBytes:
			hex = string(v)
		case field == fieldDigestSize && wire == wireVarint:
			n, read := binary.Uvarint(v)
			if read <= 0 {
				return errors.New("size_bytes is not a varint")
			}

			size = int64(n) //nolint:gosec // a length, and a negative one is refused below
			if size < 0 {
				return fmt.Errorf("size_bytes is %d, and a blob is not that big", n)
			}
		}

		return nil
	})
	if err != nil {
		return ir.NodeID{}, 0, err
	}

	id, err := ir.ParseNodeID(hex)
	if err != nil {
		return ir.NodeID{}, 0, fmt.Errorf("%q is not a digest: %w", hex, err)
	}

	return id, size, nil
}

// ResultIn reads an ActionResult message.
//
// The reply half of the pair: a client that asked for an action to be executed
// reads this to find what it produced. Written for the tests that drive this
// engine through its own protocol, which is the only way to check the reply
// says what a peer would read rather than what the encoder happened to write.
func ResultIn(b []byte) (Result, error) {
	var r Result

	err := eachField(b, func(field, wire int, v []byte) error {
		switch {
		case field == fieldOutputDirs && wire == wireBytes:
			return eachField(v, func(df, dw int, dv []byte) error {
				switch {
				case df == fieldOutDirPath && dw == wireBytes:
					r.Path = string(dv)
				case df == fieldOutDirRoot && dw == wireBytes:
					id, size, err := digestIn(dv)
					if err != nil {
						return fmt.Errorf("an output directory's digest: %w", err)
					}

					r.Root, r.RootSize = id, size
				}

				return nil
			})
		case field == fieldExitCode && wire == wireVarint:
			n, read := binary.Uvarint(v)
			if read <= 0 {
				return errors.New("exit_code is not a varint")
			}

			r.ExitCode = int32(n) //nolint:gosec // a process exit status
		case field == fieldStdoutRaw && wire == wireBytes:
			// Copied, as the salt is: `v` points into the caller's buffer.
			r.Stdout = append([]byte(nil), v...)
		}

		return nil
	})
	if err != nil {
		return Result{}, err
	}

	return r, nil
}

// Capabilities is what a service told a client it can do.
type Capabilities struct {
	DigestFunctions     []uint64
	ExecDigestFunctions []uint64
	ExecEnabled         bool
	MaxBatchBytes       int64
	LowMajor, LowMinor  int64
	HighMajor           int64
	HighMinor           int64
}

// CapabilitiesIn reads a ServerCapabilities.
//
// The reply half, so a test can ask what a client would be told rather than
// what this engine meant to say. The two were different: field 3 is
// `deprecated_api_version` and this service was writing its low version there.
func CapabilitiesIn(b []byte) (Capabilities, error) {
	var out Capabilities

	err := eachField(b, func(field, wire int, v []byte) error {
		if wire != wireBytes {
			return nil
		}

		switch field {
		case fieldCacheCaps:
			return eachField(v, func(cf, cw int, cv []byte) error {
				switch {
				case cf == fieldDigestFuncs:
					out.DigestFunctions = append(out.DigestFunctions, varintsIn(cv, cw)...)
				case cf == fieldMaxBatchSize && cw == wireVarint:
					n, _ := binary.Uvarint(cv)
					out.MaxBatchBytes = int64(n) //nolint:gosec // a length
				}

				return nil
			})
		case fieldExecCaps:
			return eachField(v, func(ef, ew int, ev []byte) error {
				switch {
				case ef == fieldExecEnabled && ew == wireVarint:
					n, _ := binary.Uvarint(ev)
					out.ExecEnabled = n != 0
				case ef == fieldExecDigestFunc && ew == wireVarint, ef == fieldExecDigestFns:
					out.ExecDigestFunctions = append(out.ExecDigestFunctions, varintsIn(ev, ew)...)
				}

				return nil
			})
		case fieldLowAPI:
			out.LowMajor, out.LowMinor = semverIn(v)
		case fieldHighAPI:
			out.HighMajor, out.HighMinor = semverIn(v)
		}

		return nil
	})
	if err != nil {
		return Capabilities{}, err
	}

	return out, nil
}

// varintsIn reads a repeated scalar, packed or not.
//
// Both, because proto3 packs by default and a conforming writer may do either -
// a reader that understood only one form would be right about half the peers.
func varintsIn(v []byte, wire int) []uint64 {
	if wire == wireVarint {
		n, _ := binary.Uvarint(v)

		return []uint64{n}
	}

	var out []uint64

	for len(v) > 0 {
		n, read := binary.Uvarint(v)
		if read <= 0 {
			return out
		}

		out = append(out, n)
		v = v[read:]
	}

	return out
}

// semverIn reads the major and minor of a SemVer.
func semverIn(b []byte) (major, minor int64) {
	_ = eachField(b, func(field, wire int, v []byte) error {
		if wire != wireVarint {
			return nil
		}

		n, _ := binary.Uvarint(v)

		switch field {
		case fieldSemVerMajor:
			major = int64(n) //nolint:gosec // a version
		case fieldSemVerMinor:
			minor = int64(n) //nolint:gosec // a version
		}

		return nil
	})

	return major, minor
}
