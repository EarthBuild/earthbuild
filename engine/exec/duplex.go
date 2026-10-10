package exec

import "io"

// duplex joins a reader and a writer into one stream.
type duplex struct {
	r io.ReadCloser
	w io.WriteCloser
}

func (d *duplex) Read(p []byte) (int, error)  { return d.r.Read(p) }  //nolint:wrapcheck // io passthrough
func (d *duplex) Write(p []byte) (int, error) { return d.w.Write(p) } //nolint:wrapcheck // io passthrough

func (d *duplex) Close() error {
	err := d.w.Close()

	rerr := d.r.Close()
	if err == nil {
		err = rerr
	}

	return err
}
