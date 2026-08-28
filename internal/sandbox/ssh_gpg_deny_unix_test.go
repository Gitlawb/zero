//go:build unix

package sandbox

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestSSHKeyDiscoverySkipsFIFOAndDeviceWithoutBlocking(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	fifoKey := filepath.Join(sshDir, "custom-key")
	if err := syscall.Mkfifo(fifoKey, 0o600); err != nil {
		t.Fatalf("Mkfifo custom-key: %v", err)
	}
	fifoConfig := filepath.Join(sshDir, "config")
	if err := syscall.Mkfifo(fifoConfig, 0o600); err != nil {
		t.Fatalf("Mkfifo config: %v", err)
	}
	device := filepath.Join(sshDir, "custom-device")
	deviceCreated := syscall.Mknod(device, syscall.S_IFCHR|0o600, 0) == nil

	done := make(chan []string, 1)
	go func() {
		done <- credentialDenyReadPathsIn(credentialPathOptions{
			Homes:      []string{home},
			ConfigDirs: []string{filepath.Join(home, ".config")},
		}, nil).Paths
	}()
	var denied []string
	select {
	case denied = <-done:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("SSH/GPG discovery blocked on a FIFO or device")
	}

	if denyCovered(denied, fifoKey) {
		t.Fatalf("FIFO ~/.ssh/custom-key was denied; special files are not key material: %v", denied)
	}
	if deviceCreated && denyCovered(denied, device) {
		t.Fatalf("device ~/.ssh/custom-device was denied: %v", denied)
	}
	if denyCovered(denied, sshDir) {
		t.Fatalf("~/.ssh was denied wholesale")
	}
}

func TestSSHConfigDiscoverySkipsSymlinkToFIFOWithoutBlocking(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(t.TempDir(), "fifo-config")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("Mkfifo fifo-config: %v", err)
	}
	if err := os.Symlink(fifo, filepath.Join(sshDir, "config")); err != nil {
		t.Fatal(err)
	}
	workKey := filepath.Join(home, "keys", "work_ed25519")
	if err := os.MkdirAll(filepath.Dir(workKey), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workKey, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	done := make(chan []string, 1)
	go func() {
		done <- credentialDenyReadPathsIn(credentialPathOptions{
			Homes:      []string{home},
			ConfigDirs: []string{filepath.Join(home, ".config")},
		}, nil).Paths
	}()
	var denied []string
	select {
	case denied = <-done:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("SSH config discovery blocked on a FIFO behind a config symlink")
	}

	if denyCovered(denied, workKey) {
		t.Fatalf("IdentityFile was discovered through a FIFO config symlink: %v", denied)
	}
	if denyCovered(denied, sshDir) {
		t.Fatalf("~/.ssh was denied wholesale")
	}
}
