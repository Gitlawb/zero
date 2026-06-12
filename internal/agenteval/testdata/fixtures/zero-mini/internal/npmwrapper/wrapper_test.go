package npmwrapper

import "testing"

func TestDirectCommandUsesNodeWrapper(t *testing.T) {
	command := DirectCommand()
	if len(command) < 2 || command[0] != "node" || command[1] != "bin/zero.js" {
		t.Fatalf("command = %#v, want direct node wrapper", command)
	}
}
