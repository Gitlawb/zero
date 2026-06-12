package lsp

import (
	"os/exec"
	"testing"
)

func TestServerForMapsExtensions(t *testing.T) {
	cases := []struct {
		path string
		bin  string
		ok   bool
	}{
		{"/x/main.go", "gopls", true},
		{"/x/app.tsx", "typescript-language-server", true},
		{"/x/app.ts", "typescript-language-server", true},
		{"/x/script.py", "pyright-langserver", true},
		{"/x/lib.rs", "rust-analyzer", true},
		{"/x/README.md", "", false},
		{"/x/noext", "", false},
	}
	for _, tc := range cases {
		cmd, ok := ServerFor(tc.path)
		if ok != tc.ok {
			t.Fatalf("ServerFor(%q) ok = %v, want %v", tc.path, ok, tc.ok)
		}
		if ok && cmd[0] != tc.bin {
			t.Fatalf("ServerFor(%q) binary = %q, want %q", tc.path, cmd[0], tc.bin)
		}
	}
}

func TestServerForIsCaseInsensitive(t *testing.T) {
	if cmd, ok := ServerFor("/x/MAIN.GO"); !ok || cmd[0] != "gopls" {
		t.Fatalf("uppercase extension should still map: %v %v", cmd, ok)
	}
}

func TestServerForReturnsACopy(t *testing.T) {
	cmd, _ := ServerFor("/x/main.go")
	cmd[0] = "tampered"
	again, _ := ServerFor("/x/main.go")
	if again[0] != "gopls" {
		t.Fatal("ServerFor must not expose the shared command slice")
	}
}

func TestAvailableReflectsPATH(t *testing.T) {
	// An unconfigured extension is never available.
	if Available("/x/README.md") {
		t.Fatal("unconfigured extension must report unavailable")
	}
	// For a configured extension, Available agrees with exec.LookPath — robust
	// whether or not the server binary is installed in this environment.
	cmd, _ := ServerFor("/x/main.go")
	_, lookErr := exec.LookPath(cmd[0])
	if Available("/x/main.go") != (lookErr == nil) {
		t.Fatal("Available must reflect exec.LookPath for the configured binary")
	}
}

func TestLanguageIDMapping(t *testing.T) {
	if id, ok := LanguageID("/x/main.go"); !ok || id != "go" {
		t.Fatalf("go languageId = %q ok=%v", id, ok)
	}
	if _, ok := LanguageID("/x/readme.md"); ok {
		t.Fatal("unconfigured extension should have no languageId")
	}
}
