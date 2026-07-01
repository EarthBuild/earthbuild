package flagutil

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Duration implements cli.GenericFlag methods to support time.Duration with days, e.g. 1d.
type Duration time.Duration

// String implements the [fmt.Stringer].
func (d *Duration) String() string {
	return time.Duration(*d).String()
}

// Set implements the [cli.GenericFlag].Set method.
func (d *Duration) Set(value string) error {
	if value == "" {
		return nil
	}

	daysToHours := false

	if before, ok := strings.CutSuffix(value, "d"); ok {
		value = fmt.Sprintf("%s%s", before, "h")
		daysToHours = true
	}

	dur, err := time.ParseDuration(value)
	if err != nil {
		return errors.New("parse error")
	}

	if daysToHours {
		dur *= 24
	}

	*d = Duration(dur)

	return nil
}

// Get implements the [cli.GenericFlag].Get method.
func (d *Duration) Get() any {
	return *d
}
