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
	var c Command

	err := eachField(b, func(field, wire int, v []byte) error {
		if wire != wireBytes {
			return nil
		}

		switch field {
		case fieldArguments:
			c.Arguments = append(c.Arguments, string(v))
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
