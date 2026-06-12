package selfverify

import "testing"

func TestChecksAreLocal(t *testing.T) {
	for _, check := range Checks() {
		if check == "" {
			t.Fatal("check names must not be empty")
		}
	}
}
