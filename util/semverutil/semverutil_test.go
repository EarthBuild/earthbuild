package semverutil

import (
	"reflect"
	"testing"
)

func version(major, minor, patch int, tail string) Version {
	return Version{Major: major, Minor: minor, Patch: patch, Tail: tail}
}

func TestParse(t *testing.T) {
	t.Parallel()

	type args struct {
		s string
	}

	tests := []struct {
		name    string
		args    args
		want    Version
		wantErr bool
	}{
		{"v1.2.3", args{"v1.2.3"}, version(1, 2, 3, ""), false},
		{"1.2.3", args{"1.2.3"}, version(1, 2, 3, ""), false},
		{"123.234.345", args{"123.234.345"}, version(123, 234, 345, ""), false},
		{"v1.2.3-alpha", args{"v1.2.3-alpha"}, version(1, 2, 3, "-alpha"), false},
		{"1.2.3-alpha", args{"1.2.3-alpha"}, version(1, 2, 3, "-alpha"), false},
		{"v1.2.3-alpha.1", args{"v1.2.3-alpha.1"}, version(1, 2, 3, "-alpha.1"), false},
		{"1.2.3-alpha.1", args{"1.2.3-alpha.1"}, version(1, 2, 3, "-alpha.1"), false},
		{"v1.2.3-alpha.1+001", args{"v1.2.3-alpha.1+001"}, version(1, 2, 3, "-alpha.1+001"), false},
		{"1.2.3-alpha.1+001", args{"1.2.3-alpha.1+001"}, version(1, 2, 3, "-alpha.1+001"), false},
		{"v1.2.3+001", args{"v1.2.3+001"}, version(1, 2, 3, "+001"), false},
		{"1.2.3+001", args{"1.2.3+001"}, version(1, 2, 3, "+001"), false},
		{"not-a-version", args{"not-a-version"}, version(0, 0, 0, ""), true},
		{"v.1.2", args{"v.1.2"}, version(0, 0, 0, ""), true},
		{"v1.2", args{"v1.2"}, version(0, 0, 0, ""), true},
		{"1.2", args{"1.2"}, version(0, 0, 0, ""), true},
		{"1", args{"1"}, version(0, 0, 0, ""), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := Parse(tt.args.s)
			if (err != nil) != tt.wantErr {
				t.Errorf("Parse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse() = %v, want %v", got, tt.want)
			}
		})
	}
}
