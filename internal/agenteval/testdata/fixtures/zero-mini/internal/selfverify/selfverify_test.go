package selfverify

import (
	"reflect"
	"testing"
)

func TestChecksAreLocal(t *testing.T) {
	got := Checks()
	want := []string{"version", "fixtures"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Checks() = %#v, want %#v", got, want)
	}
}
