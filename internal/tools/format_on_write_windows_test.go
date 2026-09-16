//go:build windows

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func setFormatterTestDACL(t *testing.T, path, sddl string) {
	t.Helper()
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
}

func requirePrivateFormatterDACL(t *testing.T, path string) {
	t.Helper()
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sddl := descriptor.String()
	if strings.Contains(sddl, ";;;WD)") || !strings.Contains(sddl, ";;;"+user.User.Sid.String()+")") {
		t.Fatalf("formatter staging DACL is not current-user-only: %s: %s", path, sddl)
	}
}

// The real subprocess verifies the copy and an auxiliary file created by the
// formatter, so a restrictive initial file alone cannot make the test pass.
func TestPrivateFormatterDACLHelper(t *testing.T) {
	if os.Getenv("ZERO_PRIVATE_FORMAT_HELPER") != "1" {
		return
	}
	path := os.Args[len(os.Args)-1]
	requirePrivateFormatterDACL(t, path)
	requirePrivateFormatterDACL(t, filepath.Dir(path))
	auxiliary := filepath.Join(filepath.Dir(path), "formatter-auxiliary")
	if err := os.WriteFile(auxiliary, []byte("sensitive formatter scratch"), 0o666); err != nil {
		t.Fatal(err)
	}
	requirePrivateFormatterDACL(t, auxiliary)
	if err := os.WriteFile(os.Getenv("ZERO_PRIVATE_FORMAT_PROOF"), []byte("checked"), 0o600); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("ZERO_PRIVATE_FORMAT_FAIL") == "1" {
		t.Fatal("injected formatter failure")
	}
	if err := os.WriteFile(path, []byte("formatted"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFormatOnWriteProtectsWindowsStagingThroughoutFormatter(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZERO_FORMAT_ON_WRITE", "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const extension = ".privateprobe"
	previous, present := formatterCommands[extension]
	formatterCommands[extension] = []string{executable, "-test.run=^TestPrivateFormatterDACLHelper$", "--"}
	defer func() {
		if present {
			formatterCommands[extension] = previous
		} else {
			delete(formatterCommands, extension)
		}
	}()
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			dir := t.TempDir()
			setFormatterTestDACL(t, dir, "D:P(A;OICI;FA;;;WD)")
			target := filepath.Join(dir, "restricted"+extension)
			if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			setFormatterTestDACL(t, target, "D:P(A;;FA;;;"+user.User.Sid.String()+")")
			proof := filepath.Join(t.TempDir(), "proof")
			t.Setenv("ZERO_PRIVATE_FORMAT_HELPER", "1")
			t.Setenv("ZERO_PRIVATE_FORMAT_PROOF", proof)
			t.Setenv("ZERO_PRIVATE_FORMAT_FAIL", map[bool]string{false: "0", true: "1"}[fail])
			result := maybeFormatWrittenFile(context.Background(), target, "secret")
			want := "formatted"
			if fail {
				want = "secret"
			}
			if result.Content != want {
				t.Fatalf("formatter result = %q, want %q", result.Content, want)
			}
			if got, err := os.ReadFile(proof); err != nil || string(got) != "checked" {
				t.Fatalf("formatter protection was not exercised: %q, %v", got, err)
			}
			if got, err := os.ReadFile(target); err != nil || string(got) != "original" {
				t.Fatalf("destination changed: %q, %v", got, err)
			}
			requirePrivateFormatterDACL(t, target)
			leftovers, err := filepath.Glob(filepath.Join(dir, ".zero-fmt-*"))
			if err != nil || len(leftovers) != 0 {
				t.Fatalf("formatter staging leaked: %v, %v", leftovers, err)
			}
		})
	}
}
