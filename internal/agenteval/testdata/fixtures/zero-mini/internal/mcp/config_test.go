package mcp

import "testing"

func TestServerNamesIncludesConfiguredServers(t *testing.T) {
	names := ServerNames(map[string]string{"docs": "stdio"})
	if len(names) != 1 || names[0] != "docs" {
		t.Fatalf("names = %#v, want docs", names)
	}
}
