//go:build windows

package update

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"github.com/Gitlawb/zero/internal/config"
	"golang.org/x/sys/windows"
)

const (
	restoreRenameRetryAttempts = 10
	restoreRenameRetryDelay    = 100 * time.Millisecond
)

func renameWithRetry(oldPath string, newPath string) error {
	var lastErr error
	for attempt := 0; attempt < restoreRenameRetryAttempts; attempt++ {
		if err := os.Rename(oldPath, newPath); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(restoreRenameRetryDelay)
	}
	return lastErr
}

// This file is where ErrTargetPossiblyTampered (declared in apply.go, so
// cross-platform callers can test for it) is produced. It is wrapped into the
// error promote returns when a promotion attempt fails AND restoring the
// original binary to targetPath also fails. That combination means a principal
// who can write in the installation directory occupied targetPath in the gap
// the updater opened by renaming the running binary aside, and Windows would
// not let anything — including the restore — replace it:
// MOVEFILE_REPLACE_EXISTING cannot force past another handle's share-mode lock.
// This is not an ordinary failed update (the previous version simply staying in
// place); the executable path may now hold attacker-controlled bytes, so a
// caller must surface it as a security-relevant condition, not the same "try
// again later" failure as a stalled download.
//
// It is also returned by promote when a PREVIOUS run left that state behind and
// nobody has resolved it yet — see the refusal there.

// restoreOriginalBinary moves the preserved original at oldPath back onto
// targetPath after a failed promotion. A failed immediate restore is surfaced;
// oldPath must not be queued as a reboot source because its pathname can be
// replaced before reboot under the writable-directory threat model.
func restoreOriginalBinary(oldPath string, targetPath string) error {
	err := renameWithRetry(oldPath, targetPath)
	if err == nil {
		return nil
	}
	// The error this produces tells the operator their original is preserved at
	// oldPath. Record that so a later promotion refuses to treat the recovery
	// copy as the ordinary destination for another aside rename.
	if markErr := markOldBinaryPreserved(oldPath); markErr != nil {
		// The marker could not be established, so nothing on disk identifies
		// oldPath as the recovery copy. Move it to a distinct name and report that
		// authoritative location to the operator.
		if kept, keepErr := keepUnmarkedRecoveryCopy(oldPath); keepErr == nil {
			return fmt.Errorf(
				"%w: %v (the recovery marker could not be written: %v; the last binary this updater verified was moved to the distinct recovery path %s)",
				ErrTargetPossiblyTampered, err, markErr, kept,
			)
		} else if kept != "" {
			// The move succeeded but the post-move verification did not, so
			// oldPath is already vacated — point at kept, the path the
			// (possibly substituted) bytes actually landed at, not the
			// path that no longer holds them.
			return fmt.Errorf(
				"%w: %v (the recovery marker could not be written: %v; the last binary this updater verified was moved to %s but could not be verified there: %v)",
				ErrTargetPossiblyTampered, err, markErr, kept, keepErr,
			)
		}
		return fmt.Errorf(
			"%w: %v (the recovery marker could not be written: %v — manually copy %s somewhere safe now)",
			ErrTargetPossiblyTampered, err, markErr, oldPath,
		)
	}
	return fmt.Errorf("%w: %v", ErrTargetPossiblyTampered, err)
}

// oldBinaryPreservedSuffix names the marker written beside a "<path>.old" that
// a failed restore left as the last known-good binary.
const oldBinaryPreservedSuffix = ".keep"

// markOldBinaryPreserved records that oldPath must survive routine cleanup.
//
// The marker is created with the same exclusive, no-follow semantics as a
// staging file, and never truncates. Its pathname is predictable, so under the
// writable-install-directory threat model a lower-privileged writer can
// pre-create it as a hard link or reparse point; opening that with O_TRUNC
// would let the elevated updater write through it into a file of the attacker's
// choosing. CREATE_NEW plus FILE_FLAG_OPEN_REPARSE_POINT plus the same
// fresh-regular-file check refuses that object instead of writing to it.
//
// An existing marker is success, not a rewrite: its contents carry no state
// beyond "this .old is the last verified binary", so there is nothing to update
// and nothing worth opening a pre-existing object for. That also means an entry
// planted at the name by someone else reads as marked, which is the fail-safe
// direction: it makes the next run PRESERVE the recovery copy.
//
// A var so a test can force the failure branch. Every way to make the real
// CreateFile fail here (an unwritable directory, an over-length name) either
// takes the recovery copy down with it or is too platform-fragile to assert on.
var markOldBinaryPreserved = func(oldPath string) error {
	markerPath := oldPath + oldBinaryPreservedSuffix
	if _, err := os.Lstat(markerPath); err == nil {
		return nil
	}
	pathPtr, err := windows.UTF16PtrFromString(markerPath)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_WRITE|windows.DELETE,
		0,
		nil,
		windows.CREATE_NEW,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_EXISTS) || errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			// Something already occupies the marker name. Whatever it is, this
			// function's only job is to make the next run preserve oldPath, and a
			// present name does that — without this process writing through an
			// object it did not create.
			return nil
		}
		return fmt.Errorf("create recovery marker %s: %w", markerPath, err)
	}
	marker := os.NewFile(uintptr(handle), markerPath)
	if err := verifyFreshRegularFile(handle, markerPath); err != nil {
		_ = deleteFileByHandle(marker)
		_ = marker.Close()
		return err
	}
	writeErr := writeRecoveryMarker(marker)
	// A partial marker must not be left beside a recovery copy that the caller
	// then relocates. Delete the object this process created through its own
	// handle — never by pathname, which the threat principal can point at
	// something else once the handle is released.
	var deleteErr error
	if writeErr != nil {
		deleteErr = deleteFileByHandle(marker)
	}
	closeErr := marker.Close()
	if writeErr == nil {
		// A close failure still leaves the fully written marker in place, and
		// its presence is the entire state this function records.
		_ = closeErr
		return nil
	}
	// Below, the write failed. Each remaining branch decides between reporting
	// that failure and conservatively claiming success — and only a confirmed
	// absence of the marker earns the report, because claiming success keeps
	// oldPath as the authoritative recovery location, which is the safe answer.
	if deleteErr != nil {
		// The partial marker could not be removed, so it is still there.
		return nil
	}
	if _, statErr := os.Lstat(markerPath); statErr == nil || !errors.Is(statErr, os.ErrNotExist) {
		// Something occupies the marker name, or its state cannot be established.
		return nil
	}
	return fmt.Errorf("write recovery marker %s: %w", markerPath, writeErr)
}

var writeRecoveryMarker = func(marker *os.File) error {
	_, err := marker.WriteString("The update at this path failed and could not restore the original binary.\n" +
		"The file without this marker's .keep suffix is the last binary this updater verified.\n")
	return err
}

// keepUnmarkedRecoveryCopy moves oldPath out from under routine cleanup when its
// marker could not be established, returning the path it now lives at.
//
// The name is unpredictable for the same reason the staging name is: this runs
// in a directory a lower-privileged principal may be able to write, and a fixed
// recovery name could be pre-created there to make this rename fail or land
// somewhere chosen by someone else. Being unpredictable also means being opaque,
// which is why the caller's error names the path.
func keepUnmarkedRecoveryCopy(oldPath string) (string, error) {
	file, err := openRecoveryCopy(oldPath)
	if err != nil {
		return "", fmt.Errorf("%w: open recovery copy %s: %v", ErrTargetPossiblyTampered, oldPath, err)
	}
	defer func() { _ = file.Close() }()

	suffix, err := randomStagingSuffix()
	if err != nil {
		return "", fmt.Errorf("%w: choose recovery path: %v", ErrTargetPossiblyTampered, err)
	}
	kept := filepath.Join(filepath.Dir(oldPath), filepath.Base(oldPath)+"."+suffix+".recovery")
	if _, statErr := os.Lstat(kept); statErr == nil {
		return "", fmt.Errorf("%w: recovery path %s already exists", ErrTargetPossiblyTampered, kept)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", fmt.Errorf("%w: inspect recovery path %s: %v", ErrTargetPossiblyTampered, kept, statErr)
	}
	if err := renameRecoveryFileByHandle(file, kept); err != nil {
		return "", fmt.Errorf("%w: move verified recovery copy to %s: %v", ErrTargetPossiblyTampered, kept, err)
	}
	if err := verifyPromotedTarget(file, kept); err != nil {
		// The rename already reported success, so oldPath is no longer a
		// reliable location for the caller to fall back to — surface kept
		// anyway so the operator is pointed at the actual (if unverified)
		// destination instead of a path the move already vacated.
		return kept, fmt.Errorf("%w: verify recovery copy at %s: %v", ErrTargetPossiblyTampered, kept, err)
	}
	return kept, nil
}

func openRecoveryCopy(path string) (*os.File, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.DELETE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return nil, err
	}
	if err := verifyFreshRegularFile(handle, path); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}

var renameRecoveryFileByHandle = renameOpenFile

// clearOldBinaryPreserved models the operator accepting the installed binary
// by deleting the recovery marker. The recovery copy itself remains preserved.
func clearOldBinaryPreserved(oldPath string) {
	_ = os.Remove(oldPath + oldBinaryPreservedSuffix)
}

// oldBinaryPreserved reports whether a failed restore marked oldPath as the last
// known-good binary.
//
// Only a definite "the marker is not there" answers false. An Lstat that fails
// for any other reason (permissions, a transient sharing error) leaves the
// question open, and the conservative answer to an open question here is to keep
// the copy: deleting it is irreversible, while keeping it costs one stale file.
func oldBinaryPreserved(oldPath string) bool {
	_, err := os.Lstat(oldPath + oldBinaryPreservedSuffix)
	return err == nil || !errors.Is(err, os.ErrNotExist)
}

type recoveryCleanupRecord struct {
	Path          string `json:"path"`
	VolumeSerial  uint32 `json:"volumeSerial"`
	FileIndexHigh uint32 `json:"fileIndexHigh"`
	FileIndexLow  uint32 `json:"fileIndexLow"`
}

var recoveryCleanupStateDir = func() (string, error) {
	root, err := config.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "zero", "update-recovery"), nil
}

func recoveryCleanupRecordPath(targetPath string) (string, error) {
	dir, err := recoveryCleanupStateDir()
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(targetPath)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(absolute))))
	return filepath.Join(dir, fmt.Sprintf("%x.json", digest)), nil
}

func validUpdaterRecoveryPath(targetPath string, recoveryPath string) bool {
	if !strings.EqualFold(filepath.Clean(filepath.Dir(targetPath)), filepath.Clean(filepath.Dir(recoveryPath))) {
		return false
	}
	name := strings.ToLower(filepath.Base(recoveryPath))
	prefix := strings.ToLower(filepath.Base(targetPath)) + ".zero-update-"
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".old") {
		return false
	}
	suffix := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".old")
	if len(suffix) != 32 {
		return false
	}
	for _, character := range suffix {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}

// prepareRecoveryCleanup opens only the exact object vouched for by trusted
// per-user state from the previous successful promotion. Recovery discovery is
// intentionally broad and filename-based so suspicious state fails closed, but
// destructive cleanup never treats an install-directory name as provenance.
func prepareRecoveryCleanup(targetPath string) []*os.File {
	recordPath, err := recoveryCleanupRecordPath(targetPath)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(recordPath)
	if err != nil {
		return nil
	}
	var record recoveryCleanupRecord
	if json.Unmarshal(data, &record) != nil ||
		!validUpdaterRecoveryPath(targetPath, record.Path) || oldBinaryPreserved(record.Path) {
		return nil
	}
	file, err := openRecoveryCopy(record.Path)
	if err != nil {
		return nil
	}
	identity, err := recoveryFileIdentity(file)
	if err != nil || identity.VolumeSerial != record.VolumeSerial ||
		identity.FileIndexHigh != record.FileIndexHigh || identity.FileIndexLow != record.FileIndexLow {
		_ = file.Close()
		return nil
	}
	return []*os.File{file}
}

type recoveryIdentity struct {
	VolumeSerial  uint32
	FileIndexHigh uint32
	FileIndexLow  uint32
}

func recoveryFileIdentity(file *os.File) (recoveryIdentity, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		return recoveryIdentity{}, err
	}
	return recoveryIdentity{
		VolumeSerial:  info.VolumeSerialNumber,
		FileIndexHigh: info.FileIndexHigh,
		FileIndexLow:  info.FileIndexLow,
	}, nil
}

func openIdentityFile(path string) (*os.File, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		pathPtr,
		0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return nil, err
	}
	if err := verifyFreshRegularFile(handle, path); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}

func recordRecoveryCleanup(targetPath string, recoveryPath string, expected recoveryIdentity) error {
	if !validUpdaterRecoveryPath(targetPath, recoveryPath) {
		return fmt.Errorf("invalid updater recovery path %s", recoveryPath)
	}
	file, err := openRecoveryCopy(recoveryPath)
	if err != nil {
		return err
	}
	identity, err := recoveryFileIdentity(file)
	_ = file.Close()
	if err != nil {
		return err
	}
	if identity != expected {
		return fmt.Errorf("recovery path %s does not name the moved-aside binary", recoveryPath)
	}
	record := recoveryCleanupRecord{
		Path:          recoveryPath,
		VolumeSerial:  identity.VolumeSerial,
		FileIndexHigh: identity.FileIndexHigh,
		FileIndexLow:  identity.FileIndexLow,
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	recordPath, err := recoveryCleanupRecordPath(targetPath)
	if err != nil {
		return err
	}
	dir := filepath.Dir(recordPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dir, filepath.Base(recordPath)+".*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, recordPath)
}

type fileDispositionInfo struct {
	DeleteFile byte
}

// deleteFileByHandle marks the object file refers to for deletion, which takes
// effect when its last handle closes. It never resolves a pathname, so it can
// only ever delete the object this process already holds open.
func deleteFileByHandle(file *os.File) error {
	info := fileDispositionInfo{DeleteFile: 1}
	return windows.SetFileInformationByHandle(
		windows.Handle(file.Fd()),
		windows.FileDispositionInfo,
		(*byte)(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
}

func closeRecoveryCleanupCandidates(candidates []*os.File) {
	for _, file := range candidates {
		_ = file.Close()
	}
}

// cleanupSupersededRecoveryCopies marks the exact pre-promotion objects for
// deletion only after the replacement has been verified. The handles deny
// delete sharing, so their entries cannot be substituted in the meantime.
func cleanupSupersededRecoveryCopies(candidates []*os.File) {
	for _, file := range candidates {
		_ = deleteFileByHandle(file)
	}
}
