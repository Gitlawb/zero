package keyring

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// fakeExit is a not-found exit error: it satisfies the ExitCode() seam used by
// isNotFound without spawning a real process.
type fakeExit struct{ code int }

func (e fakeExit) Error() string { return "exit status" }
func (e fakeExit) ExitCode() int { return e.code }

// fakeKeyring is an in-memory simulation of the OS tools driven through the
// runner seam. It records the last stdin so tests can assert the secret never
// travels via argv on Linux.
type fakeKeyring struct {
	goos      string
	data      map[string]string
	lastStdin string
	lastArgs  []string
}

func newFake(goos string) *fakeKeyring {
	return &fakeKeyring{goos: goos, data: map[string]string{}}
}

func (f *fakeKeyring) keyring() *Keyring { return &Keyring{run: f.run, goos: f.goos} }

func flagValue(args []string, flag string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

func attrValue(args []string, attr string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == attr {
			return args[i+1]
		}
	}
	return ""
}

func key(service, account string) string { return service + "\x00" + account }

func (f *fakeKeyring) run(_ context.Context, name string, stdin []byte, args ...string) ([]byte, error) {
	f.lastStdin = string(stdin)
	f.lastArgs = append([]string{name}, args...)
	if len(args) == 0 {
		return nil, fakeExit{1}
	}
	switch f.goos {
	case "darwin":
		svc, acct := flagValue(args, "-s"), flagValue(args, "-a")
		switch args[0] {
		case "add-generic-password":
			f.data[key(svc, acct)] = flagValue(args, "-w")
			return nil, nil
		case "find-generic-password":
			if v, ok := f.data[key(svc, acct)]; ok {
				return []byte(v + "\n"), nil // security prints a trailing newline
			}
			return nil, fakeExit{44}
		case "delete-generic-password":
			if _, ok := f.data[key(svc, acct)]; ok {
				delete(f.data, key(svc, acct))
				return nil, nil
			}
			return nil, fakeExit{44}
		}
	case "linux":
		svc, acct := attrValue(args, "service"), attrValue(args, "account")
		switch args[0] {
		case "store":
			f.data[key(svc, acct)] = string(stdin)
			return nil, nil
		case "lookup":
			if v, ok := f.data[key(svc, acct)]; ok {
				return []byte(v), nil // secret-tool prints no trailing newline
			}
			return nil, fakeExit{1}
		case "clear":
			delete(f.data, key(svc, acct))
			return nil, nil
		}
	}
	return nil, fakeExit{1}
}

func TestKeyringGetSurfacesNonNotFoundError(t *testing.T) {
	// On macOS only exit 44 (errSecItemNotFound) means "no entry"; any other
	// non-zero exit is a real failure that must surface, not be masked as absent.
	k := &Keyring{
		goos: "darwin",
		run: func(_ context.Context, _ string, _ []byte, _ ...string) ([]byte, error) {
			return nil, fakeExit{1}
		},
	}
	if _, ok, err := k.Get("zero", "tokens"); err == nil || ok {
		t.Fatalf("a non-44 exit must surface as an error, got ok=%v err=%v", ok, err)
	}
}

func TestKeyringRoundTripDarwin(t *testing.T) {
	k := newFake("darwin").keyring()
	if err := k.Set("zero", "tokens", "blob-AAA"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok, err := k.Get("zero", "tokens")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got != "blob-AAA" {
		t.Fatalf("Get = %q, want blob-AAA", got)
	}
	existed, err := k.Delete("zero", "tokens")
	if err != nil || !existed {
		t.Fatalf("Delete: existed=%v err=%v", existed, err)
	}
	if _, ok, _ := k.Get("zero", "tokens"); ok {
		t.Fatal("token should be gone after delete")
	}
}

func TestKeyringRoundTripLinuxUsesStdin(t *testing.T) {
	f := newFake("linux")
	k := f.keyring()
	if err := k.Set("zero", "tokens", "blob-BBB"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// The secret must travel via stdin, never the argument vector.
	if f.lastStdin != "blob-BBB" {
		t.Fatalf("secret not sent via stdin: stdin=%q", f.lastStdin)
	}
	for _, a := range f.lastArgs {
		if strings.Contains(a, "blob-BBB") {
			t.Fatalf("secret leaked into argv: %v", f.lastArgs)
		}
	}
	got, ok, err := k.Get("zero", "tokens")
	if err != nil || !ok || got != "blob-BBB" {
		t.Fatalf("Get = %q ok=%v err=%v", got, ok, err)
	}
	existed, err := k.Delete("zero", "tokens")
	if err != nil || !existed {
		t.Fatalf("Delete: existed=%v err=%v", existed, err)
	}
}

func TestKeyringGetMissingIsNotError(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		k := newFake(goos).keyring()
		if _, ok, err := k.Get("zero", "absent"); err != nil || ok {
			t.Fatalf("[%s] Get(absent) = ok=%v err=%v, want false/nil", goos, ok, err)
		}
		if existed, err := k.Delete("zero", "absent"); err != nil || existed {
			t.Fatalf("[%s] Delete(absent) = existed=%v err=%v, want false/nil", goos, existed, err)
		}
	}
}

func TestKeyringUnsupportedPlatform(t *testing.T) {
	k := &Keyring{run: newFake("windows").run, goos: "windows"}
	if k.Available() {
		t.Fatal("windows should report unavailable")
	}
	if err := k.Set("zero", "tokens", "x"); err == nil {
		t.Fatal("Set on unsupported platform should error")
	}
	if _, _, err := k.Get("zero", "tokens"); err == nil {
		t.Fatal("Get on unsupported platform should error")
	}
	if _, err := k.Delete("zero", "tokens"); err == nil {
		t.Fatal("Delete on unsupported platform should error")
	}
}

func TestKeyringValidation(t *testing.T) {
	k := newFake("darwin").keyring()
	if err := k.Set("", "a", "s"); err == nil {
		t.Fatal("empty service should error")
	}
	if err := k.Set("svc", "", "s"); err == nil {
		t.Fatal("empty account should error")
	}
}

func TestKeyringMissingBinaryError(t *testing.T) {
	// A missing tool surfaces as a wrapped, descriptive error (not not-found).
	k := &Keyring{goos: "linux", run: func(context.Context, string, []byte, ...string) ([]byte, error) {
		return nil, &exec.Error{Name: "secret-tool", Err: exec.ErrNotFound}
	}}
	if err := k.Set("zero", "tokens", "x"); err == nil || !strings.Contains(err.Error(), "secret-tool") {
		t.Fatalf("missing-binary Set error = %v, want mention of secret-tool", err)
	}
	// A missing binary on Get must not be misread as not-found.
	if _, ok, err := k.Get("zero", "tokens"); err == nil || ok {
		t.Fatalf("missing-binary Get = ok=%v err=%v, want error", ok, err)
	}
}

func TestAvailable(t *testing.T) {
	if !(newFake("darwin").keyring().Available()) {
		t.Fatal("darwin should be available")
	}
	if !(newFake("linux").keyring().Available()) {
		t.Fatal("linux should be available")
	}
}
