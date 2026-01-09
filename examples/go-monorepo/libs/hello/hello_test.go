package hello

import "testing"

func TestGreet(t *testing.T) {
	t.Parallel()

	expected := "Hello, Earth!"
	actual := Greet("Earth")
	if expected != actual {
		t.Fail()
	}
}
