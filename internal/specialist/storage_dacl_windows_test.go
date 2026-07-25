//go:build windows

package specialist

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// TestStorageCreateForceKeepsWindowsDACL is the regression test for the Windows
// half of the atomic-overwrite change: the replacement is a freshly created
// temporary file, so publishing it with a plain rename would hand the destination
// the directory's inherited DACL and drop the restrictive one an explicitly
// locked-down specialist had — exposing its system prompt to anyone the directory
// grants access to. temp.Chmod(0o600) cannot express that on Windows (Go only
// maps the owner-write bit there), so the replacement primitive has to carry the
// descriptor over.
func TestStorageCreateForceKeepsWindowsDACL(t *testing.T) {
	userDir := t.TempDir()
	path := filepath.Join(userDir, "safe.md")
	if err := os.WriteFile(path, []byte("old content"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A protected, owner-only DACL: what an operator restricting one specialist
	// inside a group-readable directory would end up with.
	restricted, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;OW)")
	if err != nil {
		t.Skipf("cannot build a test security descriptor: %v", err)
	}
	dacl, _, err := restricted.DACL()
	if err != nil {
		t.Skipf("cannot read the test DACL: %v", err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	); err != nil {
		t.Skipf("cannot apply a restrictive DACL on this filesystem: %v", err)
	}
	want := specialistDACL(t, path)
	if !strings.Contains(want, "(A;;FA;;;OW)") {
		t.Skipf("the restrictive DACL did not take effect on this filesystem: %q", want)
	}

	storage := NewStorage(Paths{UserDir: userDir})
	manifest, err := storage.Create(CreateInput{
		Name:         "safe",
		Description:  "Safe",
		SystemPrompt: "new content",
		Overwrite:    true,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), FormatMarkdown(manifest); got != want {
		t.Fatalf("file content = %q, want %q", got, want)
	}
	if got := specialistDACL(t, path); got != want {
		t.Fatalf("DACL after overwrite = %q, want the destination's own %q", got, want)
	}
	assertNoTemporarySpecialistFiles(t, userDir)
}

func specialistDACL(t *testing.T, path string) string {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Skipf("cannot read the security descriptor of %s: %v", path, err)
	}
	text := sd.String()
	if index := strings.Index(text, "D:"); index >= 0 {
		return text[index:]
	}
	return text
}
