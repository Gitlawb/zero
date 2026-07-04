// Package keyring provides a small, dependency-free secret store backed by the
// operating system's native credential tooling: the `security` keychain CLI on
// macOS and `secret-tool` (libsecret) on Linux. It stores a single secret string
// per (service, account). Windows and other platforms report unsupported.
//
// It shells out to the OS tools rather than taking a third-party dependency. On
// macOS the secret is passed to `security` as an argument, so it is briefly
// visible to other processes via the process list; on Linux the secret is passed
// over stdin and is not exposed in the argument vector. Callers that need to keep
// a secret out of the process list on macOS should prefer the file backend.
package keyring

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"
)

// commandTimeout bounds a single keyring tool invocation.
const commandTimeout = 10 * time.Second

// ErrUnsupported is returned when no OS keyring backend is available for the
// current platform.
var ErrUnsupported = errors.New("keyring: no OS keyring backend on this platform")

// runner executes name with args and optional stdin, returning stdout. It is the
// single seam tests replace to drive the platform command logic without touching
// a real keychain.
type runner func(ctx context.Context, name string, stdin []byte, args ...string) ([]byte, error)

// Keyring is an OS-native secret store.
type Keyring struct {
	run  runner
	goos string
}

// New returns a Keyring for the current platform.
func New() *Keyring {
	return &Keyring{run: execRunner, goos: runtime.GOOS}
}

// Available reports whether this platform has a supported keyring backend. The
// backing tool (`secret-tool` on Linux) must also be installed; a missing tool
// surfaces as an error from Get/Set/Delete.
func (k *Keyring) Available() bool {
	switch k.goos {
	case "darwin", "linux":
		return true
	default:
		return false
	}
}

// Set stores secret under (service, account), replacing any existing value.
func (k *Keyring) Set(service, account, secret string) error {
	if err := validate(service, account); err != nil {
		return err
	}
	switch k.goos {
	case "darwin":
		// -U updates the item if it already exists rather than failing.
		_, err := k.exec(nil, "security", "add-generic-password", "-U", "-s", service, "-a", account, "-w", secret)
		return wrap("set", err)
	case "linux":
		// secret-tool reads the secret from stdin, keeping it out of the argv.
		_, err := k.exec([]byte(secret), "secret-tool", "store", "--label", "zero", "service", service, "account", account)
		return wrap("set", err)
	default:
		return ErrUnsupported
	}
}

// Get returns the secret stored under (service, account). The bool is false when
// no entry exists (which is not an error).
func (k *Keyring) Get(service, account string) (string, bool, error) {
	if err := validate(service, account); err != nil {
		return "", false, err
	}
	switch k.goos {
	case "darwin":
		out, err := k.exec(nil, "security", "find-generic-password", "-s", service, "-a", account, "-w")
		if err != nil {
			if isNotFound(err, securityNotFoundExit) {
				return "", false, nil
			}
			return "", false, wrap("get", err)
		}
		return strings.TrimRight(string(out), "\r\n"), true, nil
	case "linux":
		out, err := k.exec(nil, "secret-tool", "lookup", "service", service, "account", account)
		if err != nil {
			if isNotFound(err, secretToolNotFoundExit) {
				return "", false, nil
			}
			return "", false, wrap("get", err)
		}
		value := strings.TrimRight(string(out), "\r\n")
		if value == "" {
			return "", false, nil
		}
		return value, true, nil
	default:
		return "", false, ErrUnsupported
	}
}

// Delete removes the entry under (service, account), reporting whether one
// existed.
func (k *Keyring) Delete(service, account string) (bool, error) {
	if err := validate(service, account); err != nil {
		return false, err
	}
	switch k.goos {
	case "darwin":
		_, err := k.exec(nil, "security", "delete-generic-password", "-s", service, "-a", account)
		if err != nil {
			if isNotFound(err, securityNotFoundExit) {
				return false, nil
			}
			return false, wrap("delete", err)
		}
		return true, nil
	case "linux":
		// `secret-tool clear` always exits 0, so probe existence first.
		_, existed, err := k.Get(service, account)
		if err != nil {
			return false, err
		}
		if _, err := k.exec(nil, "secret-tool", "clear", "service", service, "account", account); err != nil {
			return false, wrap("delete", err)
		}
		return existed, nil
	default:
		return false, ErrUnsupported
	}
}

func (k *Keyring) exec(stdin []byte, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	return k.run(ctx, name, stdin, args...)
}

func execRunner(ctx context.Context, name string, stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if len(stdin) > 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return out.Bytes(), &runError{err: err, stderr: strings.TrimSpace(errBuf.String())}
	}
	return out.Bytes(), nil
}

// runError carries a tool failure with its stderr and preserves the underlying
// error for errors.As (so exit-code / not-found detection still works).
type runError struct {
	err    error
	stderr string
}

func (e *runError) Error() string {
	if e.stderr != "" {
		return e.stderr
	}
	return e.err.Error()
}

func (e *runError) Unwrap() error { return e.err }

// Not-found exit codes for the OS tools: macOS `security` exits 44
// (errSecItemNotFound) when no matching item exists; `secret-tool` exits 1 when a
// lookup finds nothing. Any other non-zero exit is a real failure.
const (
	securityNotFoundExit   = 44
	secretToolNotFoundExit = 1
)

// isNotFound reports whether err is a tool exit whose code is one of the given
// "no such entry" codes (as opposed to a missing binary or a genuine failure,
// which must not be masked). It matches on the ExitCode behavior (satisfied by
// *exec.ExitError) so the logic is testable without spawning a real process.
func isNotFound(err error, codes ...int) bool {
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		return false
	}
	code := coder.ExitCode()
	return slices.Contains(codes, code)
}

// wrap adds operation context to a tool error, leaving nil untouched.
func wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return fmt.Errorf("keyring: %s: %q not found (install the OS keyring tool): %w", op, execErr.Name, err)
	}
	return fmt.Errorf("keyring: %s: %w", op, err)
}

func validate(service, account string) error {
	if strings.TrimSpace(service) == "" {
		return errors.New("keyring: service is required")
	}
	if strings.TrimSpace(account) == "" {
		return errors.New("keyring: account is required")
	}
	return nil
}
