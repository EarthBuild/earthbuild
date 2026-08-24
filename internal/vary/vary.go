// Package vary fills a struct field with two distinguishable values.
//
// It is test support for the coverage guards, which walk a struct and assert
// that changing any field changes a derived digest. Two such digests exist over
// `the walked struct` - the chain key in `engine/core` and the node identity in `engine/ir` -
// and a guard duplicated per digest is two expressions of one rule kept in step
// by nobody, which is the failure the guards themselves exist to catch (E432).
package vary

import "reflect"

// Value sets v to one of two values and reports whether it could.
//
// `which` is 0 or 1. Reporting false is a legitimate answer - a field of a kind
// this cannot vary - and the caller decides whether that is a gap or a type it
// should be taught about.
// A field whose type is not handled here is reported rather than skipped: an
// unknown type means a guard has silently stopped covering something, which is
// the failure it exists to prevent.
func Value(v reflect.Value, which int) bool {
	// Partial on purpose: the kinds below are the ones a key can be made of, and
	// falling out is the answer for anything else - this reports whether it
	// *could* mutate the value, and "no" is a legitimate report.
	switch v.Kind() { //nolint:exhaustive // partial on purpose, see above
	case reflect.String:
		v.SetString([]string{"alpha", "beta"}[which])

		return true

	case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uint:
		// Indexed rather than converted, exactly as the string case above is.
		// `uint64(which) + 1` is a sign-extending cast of a parameter documented
		// as 0 or 1 and enforced by nobody, so a negative `which` produced an
		// enormous value in silence (gosec G115). Indexing states the same
		// contract and fails on the same input the string case already fails on.
		v.SetUint([]uint64{1, 2}[which])

		return true

	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Int:
		// As above: one contract, stated once, in the form the string case uses.
		v.SetInt([]int64{1, 2}[which])

		return true

	case reflect.Bool:
		v.SetBool(which == 1)

		return true

	case reflect.Struct:
		// A struct differs if any of its fields does. General rather than a case
		// per type, so the next struct field added to the walked struct is covered without
		// this guard needing to be taught about it - which is the point of a
		// reflective guard, and the thing a hand-written one loses first.
		for _, field := range v.Fields() {
			if field.CanSet() && Value(field, which) {
				return true
			}
		}

		return false

	case reflect.Slice:
		e := reflect.New(v.Type().Elem()).Elem()
		if !Value(e, which) {
			return false
		}

		v.Set(reflect.Append(reflect.MakeSlice(v.Type(), 0, 1), e))

		return true

	case reflect.Array:
		if v.Len() == 0 {
			return false
		}

		return Value(v.Index(0), which)

	case reflect.Map:
		k := reflect.New(v.Type().Key()).Elem()
		if !Value(k, 0) { // one key, two values
			return false
		}

		val := reflect.New(v.Type().Elem()).Elem()
		if !Value(val, which) {
			return false
		}

		m := reflect.MakeMap(v.Type())
		m.SetMapIndex(k, val)
		v.Set(m)

		return true

	case reflect.Pointer:
		// A pointer differs if what it points at does. General, like the struct
		// case: an optional field added to the walked struct is covered without this guard
		// being taught about its type - which is what a reflective guard is
		// for, and the first thing a hand-written one loses.
		//
		// Both variants are non-nil on purpose. Varying nil against non-nil
		// would pass for any field at all, including one the key ignores
		// entirely, so it would prove presence rather than coverage.
		e := reflect.New(v.Type().Elem())
		if !Value(e.Elem(), which) {
			return false
		}

		v.Set(e)

		return true

	default:
		return false
	}
}
