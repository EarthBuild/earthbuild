package bulk_test

import (
	"archive/tar"
	"bytes"
)

// escapingTar is an archive naming a path outside whatever it is unpacked
// into. Built rather than checked in, so what it tests is legible.
func escapingTar() string {
	var buf bytes.Buffer

	tw := tar.NewWriter(&buf)

	_ = tw.WriteHeader(&tar.Header{
		Name: "../escaped", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg,
	})
	_, _ = tw.Write([]byte("x"))
	_ = tw.Close()

	return buf.String()
}
