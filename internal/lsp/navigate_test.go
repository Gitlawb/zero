package lsp

import (
	"encoding/json"
	"testing"
)

func TestDecodeLocationsArrayOfLocation(t *testing.T) {
	raw := json.RawMessage(`[{"uri":"file:///a.go","range":{"start":{"line":4,"character":2},"end":{"line":4,"character":8}}}]`)
	locs, err := decodeLocations(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(locs) != 1 || locs[0].URI != "file:///a.go" || locs[0].Range.Start.Line != 4 {
		t.Fatalf("unexpected locations: %#v", locs)
	}
}

func TestDecodeLocationsSingleLocation(t *testing.T) {
	raw := json.RawMessage(`{"uri":"file:///b.go","range":{"start":{"line":1,"character":0},"end":{"line":1,"character":3}}}`)
	locs, err := decodeLocations(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(locs) != 1 || locs[0].URI != "file:///b.go" {
		t.Fatalf("unexpected single location: %#v", locs)
	}
}

func TestDecodeLocationsLocationLink(t *testing.T) {
	// definition can return LocationLink (targetUri/targetRange) instead of Location.
	raw := json.RawMessage(`[{"targetUri":"file:///c.go","targetRange":{"start":{"line":9,"character":1},"end":{"line":9,"character":5}}}]`)
	locs, err := decodeLocations(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(locs) != 1 || locs[0].URI != "file:///c.go" || locs[0].Range.Start.Line != 9 {
		t.Fatalf("LocationLink not converted: %#v", locs)
	}
}

func TestDecodeLocationsNull(t *testing.T) {
	for _, raw := range []string{`null`, ``, `   `, `[]`} {
		locs, err := decodeLocations(json.RawMessage(raw))
		if err != nil {
			t.Fatalf("decode(%q): %v", raw, err)
		}
		if len(locs) != 0 {
			t.Fatalf("decode(%q) = %#v, want empty", raw, locs)
		}
	}
}

func TestDecodeSymbols(t *testing.T) {
	raw := json.RawMessage(`[{"name":"Run","kind":12,"location":{"uri":"file:///loop.go","range":{"start":{"line":99,"character":0},"end":{"line":99,"character":3}}}}]`)
	syms, err := decodeSymbols(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(syms) != 1 || syms[0].Name != "Run" || syms[0].Kind != "function" {
		t.Fatalf("unexpected symbols: %#v", syms)
	}
}

func TestNavigateUnknownOp(t *testing.T) {
	m := NewManager(t.TempDir())
	_, _, ok, err := m.Navigate(nil, NavRequest{Op: "bogus", Path: "x.go"})
	if err == nil {
		t.Fatal("expected an error for an unknown nav op")
	}
	if ok {
		t.Fatal("ok should be false for an unknown op")
	}
}

func TestNavigateUnsupportedExtensionDegrades(t *testing.T) {
	// A file type with no configured server degrades to ok=false, no error.
	m := NewManager(t.TempDir())
	_, _, ok, err := m.Navigate(nil, NavRequest{Op: NavDefinition, Path: "notes.unknownext", Line: 1, Character: 1})
	if err != nil {
		t.Fatalf("unsupported extension should not error, got %v", err)
	}
	if ok {
		t.Fatal("unsupported extension should degrade to ok=false")
	}
}

func TestSymbolKindName(t *testing.T) {
	cases := map[int]string{5: "class", 11: "interface", 12: "function", 23: "struct", 999: "symbol"}
	for kind, want := range cases {
		if got := symbolKindName(kind); got != want {
			t.Fatalf("symbolKindName(%d) = %q, want %q", kind, got, want)
		}
	}
}
