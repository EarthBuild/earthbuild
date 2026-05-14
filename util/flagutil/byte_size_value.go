package flagutil

import (
	"github.com/dustin/go-humanize"
)

// ByteSizeValue represents a byte size that can be parsed from a flag.
type ByteSizeValue uint64

// Set implements the [cli.GenericFlag].Set method.
func (b *ByteSizeValue) Set(s string) error {
	v, err := humanize.ParseBytes(s)
	if err != nil {
		return err
	}

	*b = ByteSizeValue(v)

	return nil
}

// String implements [fmt.Stringer].
func (b *ByteSizeValue) String() string { return humanize.Bytes(uint64(*b)) }

// Get implements the [cli.GenericFlag].Get method.
func (b *ByteSizeValue) Get() any {
	return *b
}
