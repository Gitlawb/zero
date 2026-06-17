//go:build linux

package sandbox

import (
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestValidateLandlockProfileRejectsRestrictedReadRoots(t *testing.T) {
	root := t.TempDir()
	profile := PermissionProfile{
		FileSystem: FileSystemPolicy{
			Kind:       FileSystemRestricted,
			ReadRoots:  []string{root},
			WriteRoots: []WritableRoot{{Root: root}},
		},
		Network: NetworkPolicy{Mode: NetworkAllow},
	}
	err := validateLandlockProfile(profile, false, "")
	if err == nil || !strings.Contains(err.Error(), "restricted read roots") {
		t.Fatalf("validateLandlockProfile error = %v, want restricted read roots rejection", err)
	}
}

func TestValidateLandlockProfileRejectsScopedAndProxyNetworking(t *testing.T) {
	profile := landlockTestProfile(t.TempDir(), NetworkPolicy{Mode: NetworkScoped, ProxyRequired: true})
	if err := validateLandlockProfile(profile, false, ""); err == nil || !strings.Contains(err.Error(), "scoped networking") {
		t.Fatalf("validateLandlockProfile scoped error = %v", err)
	}
	profile.Network = NetworkPolicy{Mode: NetworkAllow}
	if err := validateLandlockProfile(profile, true, ""); err == nil || !strings.Contains(err.Error(), "proxy-routed networking") {
		t.Fatalf("validateLandlockProfile proxy error = %v", err)
	}
	if err := validateLandlockProfile(profile, false, "proxy=127.0.0.1:1"); err == nil || !strings.Contains(err.Error(), "proxy-routed networking") {
		t.Fatalf("validateLandlockProfile proxy spec error = %v", err)
	}
}

func TestValidateLandlockProfileAllowsNetworkDeny(t *testing.T) {
	profile := landlockTestProfile(t.TempDir(), NetworkPolicy{Mode: NetworkDeny})
	if err := validateLandlockProfile(profile, false, ""); err != nil {
		t.Fatalf("validateLandlockProfile network deny: %v", err)
	}
}

func TestValidateLandlockProfileRejectsUnsupportedFilesystemCarveouts(t *testing.T) {
	profile := landlockTestProfile(t.TempDir(), NetworkPolicy{Mode: NetworkAllow})
	profile.FileSystem.DenyRead = []string{t.TempDir()}
	if err := validateLandlockProfile(profile, false, ""); err == nil || !strings.Contains(err.Error(), "deny-read") {
		t.Fatalf("validateLandlockProfile deny-read error = %v", err)
	}
	profile = landlockTestProfile(t.TempDir(), NetworkPolicy{Mode: NetworkAllow})
	profile.FileSystem.DenyWrite = []string{t.TempDir()}
	if err := validateLandlockProfile(profile, false, ""); err == nil || !strings.Contains(err.Error(), "deny-write") {
		t.Fatalf("validateLandlockProfile deny-write error = %v", err)
	}
	profile = landlockTestProfile(t.TempDir(), NetworkPolicy{Mode: NetworkAllow})
	profile.FileSystem.WriteRoots[0].ProtectedMetadataNames = []string{".git"}
	if err := validateLandlockProfile(profile, false, ""); err == nil || !strings.Contains(err.Error(), "read-only write-root subpaths") {
		t.Fatalf("validateLandlockProfile metadata error = %v", err)
	}
}

func TestLandlockAccessMasksFollowABI(t *testing.T) {
	abi1 := landlockHandledFilesystemAccess(1)
	for _, unexpected := range []uint64{
		unix.LANDLOCK_ACCESS_FS_REFER,
		unix.LANDLOCK_ACCESS_FS_TRUNCATE,
		unix.LANDLOCK_ACCESS_FS_IOCTL_DEV,
	} {
		if abi1&unexpected != 0 {
			t.Fatalf("ABI 1 mask unexpectedly includes %#x", unexpected)
		}
	}
	abi5 := landlockHandledFilesystemAccess(5)
	for _, expected := range []uint64{
		unix.LANDLOCK_ACCESS_FS_EXECUTE,
		unix.LANDLOCK_ACCESS_FS_WRITE_FILE,
		unix.LANDLOCK_ACCESS_FS_READ_FILE,
		unix.LANDLOCK_ACCESS_FS_READ_DIR,
		unix.LANDLOCK_ACCESS_FS_REFER,
		unix.LANDLOCK_ACCESS_FS_TRUNCATE,
		unix.LANDLOCK_ACCESS_FS_IOCTL_DEV,
	} {
		if abi5&expected == 0 {
			t.Fatalf("ABI 5 mask missing %#x", expected)
		}
	}
	read := landlockReadFilesystemAccess(5)
	if read&unix.LANDLOCK_ACCESS_FS_WRITE_FILE != 0 {
		t.Fatalf("read mask includes write access: %#x", read)
	}
	file := landlockFileReadWriteAccess(5)
	for _, unexpected := range []uint64{
		unix.LANDLOCK_ACCESS_FS_READ_DIR,
		unix.LANDLOCK_ACCESS_FS_MAKE_DIR,
		unix.LANDLOCK_ACCESS_FS_REMOVE_DIR,
		unix.LANDLOCK_ACCESS_FS_MAKE_REG,
	} {
		if file&unexpected != 0 {
			t.Fatalf("file mask unexpectedly includes %#x", unexpected)
		}
	}
}

func TestLandlockWritableRootsIncludesCWD(t *testing.T) {
	cwd := t.TempDir()
	extra := t.TempDir()
	profile := landlockTestProfile(extra, NetworkPolicy{Mode: NetworkAllow})
	roots := landlockWritableRoots(profile.FileSystem, cwd)
	for _, want := range []string{filepath.Clean(cwd), filepath.Clean(extra)} {
		if !stringSliceContains(roots, want) {
			t.Fatalf("writable roots = %#v, missing %s", roots, want)
		}
	}
}

func landlockTestProfile(root string, network NetworkPolicy) PermissionProfile {
	return PermissionProfile{
		FileSystem: FileSystemPolicy{
			Kind:      FileSystemRestricted,
			ReadRoots: []string{string(filepath.Separator)},
			WriteRoots: []WritableRoot{{
				Root: root,
			}},
			IncludePlatformRoots: true,
			AllowTemp:            true,
		},
		Network: network,
	}
}
