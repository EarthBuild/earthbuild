package stringutil

import (
	"fmt"
	"strings"

	"github.com/iancoleman/strcase"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type caser interface {
	String(s string) string
}

type ProtoEnum interface {
	fmt.Stringer
	Type() protoreflect.EnumType
}

// EnumToStringFunc takes an ProtoEnum and returns a string.
type EnumToStringFunc func(item ProtoEnum) string

// Title takes an enum and returns its string value in title mode.
func Title(e ProtoEnum) string {
	// Casers are stateful and not goroutine-safe; construct per call.
	return pretty(cases.Title(language.English), e)
}

// Lower takes an enum and returns its string value in lower case mode.
func Lower(e ProtoEnum) string {
	return pretty(cases.Lower(language.English), e)
}

// EnumToStringArray takes an array of enum values and returns an array of their
// string values after applying EnumToStringFunc on each item.
func EnumToStringArray[T ProtoEnum](items []T, f EnumToStringFunc) []string {
	strs := make([]string, 0, len(items))
	for _, item := range items {
		strs = append(strs, f(item))
	}

	return strs
}

func pretty(caser caser, e ProtoEnum) string {
	val := string(e.Type().Descriptor().Name())
	idx := strings.Index(val, "_")
	val = val[idx+1:]
	prefix := strcase.ToScreamingSnake(val) + "_"

	return caser.String(strings.ReplaceAll(strings.TrimPrefix(e.String(), prefix), "_", " "))
}
