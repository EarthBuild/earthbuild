// Command gocacheprobe is a GOCACHEPROG that measures a build's working set.
//
// **The number stage 2 is gated on.** Shipping a cache beats compiling only if a
// step uses enough of it, and nobody had measured how much. A directory walk
// cannot answer it - it says what a cache *holds*, never what a build *asks
// for* - and the reads never reach an observation, because a path inside a mount
// is filtered out before one is recorded (E498).
//
// Go 1.21 and later will hand its whole build cache to a program named by
// `GOCACHEPROG`, consulted once per action. That is the only place the question
// is asked out loud, so this answers it and writes down what it heard.
//
// It is also the stage-2 alternative in prototype. If a build's misses can be
// served per action, the working-set fraction is 1 by construction and the
// economics stop depending on it - and Go's OutputID is a SHA-256, so those
// objects are content-addressed and need none of the machinery a cache-mount
// transport would.
//
// Usage:
//
//	GOCACHEPROG="gocacheprobe -store DIR -log FILE" go build ./...
//
// The log is one line per action - `get <hit|miss> <actionID> <bytes>` - and a
// `#` summary at close.
package main

import (
	"bufio"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// request and response are `cmd/go/internal/cacheprog`'s types, restated rather
// than imported - that package is internal to the Go distribution.
//
// `Body` is not a field: the go command writes it as a **separate JSON value**
// after the request, a base64 string, and only when `BodySize` is positive.
// Decoding it as part of the request would leave the stream one value out of
// step and every later action would be answered with the wrong id.
type request struct {
	ID       int64
	Command  string
	ActionID []byte `json:",omitempty"`
	OutputID []byte `json:",omitempty"`
	BodySize int64  `json:",omitempty"`
}

type response struct {
	ID            int64
	Err           string     `json:",omitempty"`
	KnownCommands []string   `json:",omitempty"`
	Miss          bool       `json:",omitempty"`
	OutputID      []byte     `json:",omitempty"`
	Size          int64      `json:",omitempty"`
	Time          *time.Time `json:",omitempty"`
	DiskPath      string     `json:",omitempty"`
}

// entry is what this cache records against an action.
type entry struct {
	OutputID []byte
	Size     int64
	Time     time.Time
}

type probe struct {
	store string

	mu     sync.Mutex
	log    *bufio.Writer
	gets   int
	hits   int
	puts   int
	hitB   int64
	putB   int64
	seen   map[string]bool
	hitSet map[string]bool
}

func main() {
	store := flag.String("store", "", "directory holding the cache")
	logTo := flag.String("log", "", "where to write the action log")
	flag.Parse()

	if *store == "" {
		fatal(fmt.Errorf("-store is required"))
	}

	for _, d := range []string{"a", "o"} {
		if err := os.MkdirAll(filepath.Join(*store, d), 0o755); err != nil {
			fatal(err)
		}
	}

	p := &probe{store: *store, seen: map[string]bool{}, hitSet: map[string]bool{}}

	if *logTo != "" {
		f, err := os.Create(*logTo)
		if err != nil {
			fatal(err)
		}

		defer func() { _ = f.Close() }()

		p.log = bufio.NewWriter(f)
		defer func() { _ = p.log.Flush() }()
	}

	if err := p.serve(os.Stdin, os.Stdout); err != nil {
		fatal(err)
	}
}

func (p *probe) serve(in io.Reader, out io.Writer) error {
	dec := json.NewDecoder(bufio.NewReaderSize(in, 1<<20))
	w := bufio.NewWriterSize(out, 1<<20)
	enc := json.NewEncoder(w)

	// Capabilities first and unprompted - the go command waits for this before
	// it sends anything, so a program that answers only when asked deadlocks.
	if err := enc.Encode(response{KnownCommands: []string{"get", "put", "close"}}); err != nil {
		return err
	}

	if err := w.Flush(); err != nil {
		return err
	}

	for {
		var req request

		if err := dec.Decode(&req); err != nil {
			if err == io.EOF {
				return nil
			}

			return fmt.Errorf("decode request: %w", err)
		}

		// **Before answering**, because the body is the next value on the same
		// stream whether this program wants it or not.
		var body []byte

		if req.BodySize > 0 {
			var b64 string

			if err := dec.Decode(&b64); err != nil {
				return fmt.Errorf("decode body of %d: %w", req.ID, err)
			}

			var err error

			if body, err = base64.StdEncoding.DecodeString(b64); err != nil {
				return fmt.Errorf("decode body of %d: %w", req.ID, err)
			}
		}

		res := p.answer(req, body)
		res.ID = req.ID

		if err := enc.Encode(res); err != nil {
			return err
		}

		if err := w.Flush(); err != nil {
			return err
		}

		if req.Command == "close" {
			p.summarise()

			return nil
		}
	}
}

func (p *probe) answer(req request, body []byte) response {
	switch req.Command {
	case "get":
		return p.get(req)

	case "put":
		return p.put(req, body)

	case "close":
		return response{}

	default:
		return response{Err: "unknown command " + req.Command}
	}
}

func (p *probe) get(req request) response {
	id := hex.EncodeToString(req.ActionID)

	b, err := os.ReadFile(p.actionAt(id)) //nolint:gosec // a path built from a hex digest
	if err != nil {
		p.note("get miss "+id, false, 0)

		return response{Miss: true}
	}

	var e entry
	if json.Unmarshal(b, &e) != nil {
		p.note("get miss "+id, false, 0)

		return response{Miss: true}
	}

	at := p.outputAt(hex.EncodeToString(e.OutputID))
	if _, err := os.Stat(at); err != nil {
		// The index survived and the object did not. A miss, not an error: the
		// step recompiles, which is what an empty cache would have made it do.
		p.note("get miss "+id, false, 0)

		return response{Miss: true}
	}

	p.note("get hit "+id, true, e.Size)

	when := e.Time

	return response{OutputID: e.OutputID, Size: e.Size, Time: &when, DiskPath: at}
}

func (p *probe) put(req request, body []byte) response {
	out := hex.EncodeToString(req.OutputID)
	at := p.outputAt(out)

	if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
		return response{Err: err.Error()}
	}

	if err := os.WriteFile(at, body, 0o644); err != nil { //nolint:gosec // a cache object, not a secret
		return response{Err: err.Error()}
	}

	e := entry{OutputID: req.OutputID, Size: int64(len(body)), Time: time.Now()}

	b, err := json.Marshal(e)
	if err != nil {
		return response{Err: err.Error()}
	}

	id := hex.EncodeToString(req.ActionID)

	if err := os.MkdirAll(filepath.Dir(p.actionAt(id)), 0o755); err != nil {
		return response{Err: err.Error()}
	}

	if err := os.WriteFile(p.actionAt(id), b, 0o644); err != nil { //nolint:gosec // likewise
		return response{Err: err.Error()}
	}

	p.mu.Lock()
	p.puts++
	p.putB += e.Size
	p.mu.Unlock()

	p.note("put "+id, false, e.Size)

	return response{DiskPath: at}
}

func (p *probe) actionAt(id string) string {
	return filepath.Join(p.store, "a", id[:2], id)
}

func (p *probe) outputAt(id string) string {
	return filepath.Join(p.store, "o", id[:2], id)
}

func (p *probe) note(line string, hit bool, size int64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(line) > 8 && line[:3] == "get" {
		p.gets++
		// Distinct actions, because the same one asked twice is one thing the
		// build needed - a working set is a set.
		p.seen[line[len(line)-64:]] = true

		if hit {
			p.hits++
			p.hitB += size
			p.hitSet[line[len(line)-64:]] = true
		}
	}

	if p.log != nil {
		fmt.Fprintf(p.log, "%s %d\n", line, size)
	}
}

func (p *probe) summarise() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.log == nil {
		return
	}

	fmt.Fprintf(p.log, "# gets %d (distinct %d) hits %d (distinct %d) hit-bytes %d\n",
		p.gets, len(p.seen), p.hits, len(p.hitSet), p.hitB)
	fmt.Fprintf(p.log, "# puts %d put-bytes %d\n", p.puts, p.putB)

	_ = p.log.Flush()
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "gocacheprobe:", err)
	os.Exit(1)
}
