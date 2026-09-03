package dictation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The crash rows of the recovery plan's Acceptance Examples, as code. Each row
// drives the REAL promotion into the on-disk state a stop at one step leaves,
// then runs recovery twice and asserts both halves the table asserts: which
// install is live, and where every other copy of an install ended up. Nothing
// here plants a holder by hand, so the fixture is whatever the write path
// actually writes.
//
// A second pass is asserted because recovery keeps no memory: a copy retained on
// pass one must not be reclassified on pass two, and a copy deleted on pass one
// must not come back.

// copyDisposition is where one copy of an install ends up. The five values are
// the only terminal states the table uses, so a row that means "retained" has to
// say which kind of retention, and a delete can never be written down as
// anything else.
type copyDisposition int

const (
	// copyDeleted: gone from disk, under neither prefix.
	copyDeleted copyDisposition = iota
	// copyAtHolder: still under the name the promotion allocated for it.
	copyAtHolder
	// copyAtKept: moved under the Kept prefix, where the scan does not look.
	copyAtKept
	// copyInPlaceUnowned: still at its name and carrying no marker, so nothing
	// on disk attributes it to this code.
	copyInPlaceUnowned
	// copyAtDest: restored, so the copy's content is the live install and its
	// holder is gone.
	copyAtDest
)

// dictationCrashRow is one row of the table. Each of these rows leaves exactly
// one holder, and the assertion compares the WHOLE set of copies beside the
// destination against that one, so a row can never pass while a second copy
// nobody expected sits beside it.
type dictationCrashRow struct {
	id string
	// arrange runs the real promotion with faults injected to stop it at one
	// step. The faults are unwound before recovery runs, so recovery sees a
	// crashed install and not a failing filesystem.
	arrange  func(t *testing.T, txn *destTxn, stage, dest string)
	wantLive string
	wantCopy copyDisposition
	// report1 and report2 assert the copy is named in the recovery report on
	// that pass. A copy recovery retains but never names is one an operator
	// cannot find, which the table counts as a failure of the row.
	report1 bool
	report2 bool
}

// arrangeInterruptedPromotion stops the promotion between its two renames: the
// previous install is aside in the holder, the publish failed, and the restore
// failed too, so the destination is absent and the holder has the only copy of
// it. D2 and D5 differ in how the writer got here and are identical on disk,
// which is why they share one arrangement.
func arrangeInterruptedPromotion(t *testing.T, txn *destTxn, stage, dest string) {
	t.Helper()
	injectFault(t, "rename", func(args ...string) bool {
		return filepath.Base(args[1]) == filepath.Base(dest)
	}, errors.New("injected publish and restore failure"))
	if err := promoteStagedDir(txn, stage, dest, "engine", nil); err == nil {
		t.Fatal("a promotion whose publish and restore both fail must report an error")
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatalf("the interrupted promotion should leave %s absent, got %v", dest, err)
	}
}

func dictationCrashRows() []dictationCrashRow {
	return []dictationCrashRow{
		{
			// A stop between the holder and its marker leaves a directory
			// nothing on disk attributes. Deleting it would need proof this code
			// wrote it, and the marker is that proof, so it is retained and
			// named rather than reaped on the strength of its name.
			id: "D1",
			arrange: func(t *testing.T, txn *destTxn, stage, dest string) {
				injectFault(t, "createTemp", func(args ...string) bool {
					return args[1] == holderMarkerFile+"-*"
				}, errors.New("injected marker temp failure"))
				injectFault(t, "removeAll", nil, errors.New("injected cleanup failure"))
				if err := promoteStagedDir(txn, stage, dest, "engine", nil); err == nil {
					t.Fatal("a promotion whose marker cannot be written must fail")
				}
			},
			wantLive: "old",
			wantCopy: copyInPlaceUnowned,
			report1:  true,
			report2:  true,
		},
		{
			// The marker landed and the set-aside did not, so the holder is
			// owned and holds no copy of any install. Nothing can be lost by
			// removing it, and leaving it is what accumulates scratch forever.
			id: "D1b",
			arrange: func(t *testing.T, txn *destTxn, stage, dest string) {
				injected := errors.New("injected set-aside failure")
				injectFault(t, "rename", func(args ...string) bool {
					return filepath.Base(args[1]) == "install"
				}, injected)
				injectFault(t, "removeAll", nil, errors.New("injected cleanup failure"))
				if err := promoteStagedDir(txn, stage, dest, "engine", nil); !errors.Is(err, injected) {
					t.Fatalf("promote = %v, want the injected set-aside failure", err)
				}
			},
			wantLive: "old",
			wantCopy: copyDeleted,
		},
		{
			// The previous install is aside and the destination is absent, so
			// the holder has the only copy of it and it has to come back. An
			// offline caller has no download to fall back on.
			id:       "D2",
			arrange:  arrangeInterruptedPromotion,
			wantLive: "old",
			wantCopy: copyAtDest,
		},
		{
			// The publish landed and the flag did not. Without the flag there is
			// no evidence the copy in the holder was superseded, so it may not
			// be deleted; parking it is what keeps it out of the next pass's way
			// without dropping the last copy of an install.
			id: "D3",
			arrange: func(t *testing.T, txn *destTxn, stage, dest string) {
				injectFault(t, "create", func(args ...string) bool {
					return filepath.Base(args[0]) == committedFile
				}, errors.New("injected commit flag failure"))
				if err := promoteStagedDir(txn, stage, dest, "engine", nil); err != nil {
					t.Fatalf("a published install must be reported as success: %v", err)
				}
			},
			wantLive: "new",
			wantCopy: copyAtKept,
			report1:  true,
		},
		{
			// The publish failed and the restore put the install back, so the
			// holder holds no copy of anything: owned and empty.
			id: "D4",
			arrange: func(t *testing.T, txn *destTxn, stage, dest string) {
				injected := errors.New("injected publish failure")
				injectFault(t, "rename", func(args ...string) bool {
					return filepath.Base(args[0]) == filepath.Base(stage)
				}, injected)
				injectFault(t, "removeAll", nil, errors.New("injected cleanup failure"))
				if err := promoteStagedDir(txn, stage, dest, "engine", nil); !errors.Is(err, injected) {
					t.Fatalf("promote = %v, want the injected publish failure", err)
				}
			},
			wantLive: "old",
			wantCopy: copyDeleted,
		},
		{
			// The writer's own retain branch: publish failed, restore failed,
			// the copy was kept and named in the error. On disk this is D2, and
			// recovery must not tell them apart, because nothing on disk does.
			id:       "D5",
			arrange:  arrangeInterruptedPromotion,
			wantLive: "old",
			wantCopy: copyAtDest,
		},
		{
			// The flag is there, so the copy in the holder is provably
			// superseded by the install now live at the destination. This is the
			// one and only shape that licenses deleting a copy of an install.
			id: "D6",
			arrange: func(t *testing.T, txn *destTxn, stage, dest string) {
				injectFault(t, "removeAll", nil, errors.New("injected cleanup failure"))
				if err := promoteStagedDir(txn, stage, dest, "engine", nil); err != nil {
					t.Fatalf("a cleanup failure must not fail the install: %v", err)
				}
			},
			wantLive: "new",
			wantCopy: copyDeleted,
		},
	}
}

func TestDictationCrashMatrix(t *testing.T) {
	for _, row := range dictationCrashRows() {
		t.Run(row.id, func(t *testing.T) {
			root := t.TempDir()
			dest := filepath.Join(root, "engine-dir")
			stagedTree(t, dest, "old")
			// One handle for the whole row: the lock is per open file
			// description, so a second acquire in this process would contend
			// with the first rather than nest.
			txn := lockFor(t, dest)
			stage := stagedTree(t, filepath.Join(root, "stage"), "new")
			// The faults belong to the crash, not to recovery: restoring the
			// seam here is what makes the next lines a recovery over a real
			// filesystem rather than over a failing one.
			func() {
				saved := holderFS
				defer func() { holderFS = saved }()
				row.arrange(t, txn, stage, dest)
			}()
			holder := soleHolder(t, dest)

			for pass, wantReport := range []bool{row.report1, row.report2} {
				var reported []string
				restoreInterruptedPromotion(txn, dest, testPublished, func(m string) {
					reported = append(reported, m)
				})
				assertDictationTerminalState(t, dest, holder, row.wantCopy, row.wantLive, pass+1)
				if wantReport {
					assertCopyReported(t, reported, holderSeqDigitsOf(t, dest, holder), pass+1)
				}
			}
		})
	}
}

// assertDictationTerminalState asserts both halves of a row: the live install,
// and the full set of copies still beside the destination under either prefix.
// The set is compared whole rather than one path at a time, so an extra copy
// nobody expected fails the row instead of going unnoticed.
func assertDictationTerminalState(t *testing.T, dest, holder string, want copyDisposition, wantLive string, pass int) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dest, "engine"))
	switch {
	case wantLive == "":
		if err == nil {
			t.Errorf("pass %d: the destination should be absent, it holds %q", pass, got)
		}
	case err != nil || string(got) != wantLive:
		t.Errorf("pass %d: dest engine = %q (err %v), want %q", pass, got, err, wantLive)
	}

	var wantPaths []string
	switch want {
	case copyAtHolder, copyInPlaceUnowned:
		wantPaths = []string{holder}
	case copyAtKept:
		wantPaths = []string{keptHolderName(t, dest, holder)}
	}
	base := filepath.Base(dest)
	assertCopySet(t, filepath.Dir(dest), []string{base + holderSuffix, base + keptSuffix}, wantPaths, pass)

	switch want {
	case copyInPlaceUnowned:
		// Retained is not the whole claim: the reason it is retained is that
		// nothing on disk attributes it, and a row that stopped checking that
		// would pass over a copy recovery could have proved was its own.
		if _, err := readHolderMarker(holder); !errors.Is(err, errMarkerMissing) {
			t.Errorf("pass %d: %s should carry no marker, readHolderMarker = %v", pass, holder, err)
		}
	case copyAtDest:
		if _, err := os.Stat(dest); err != nil {
			t.Errorf("pass %d: the copy should have been restored to the destination: %v", pass, err)
		}
	}
}

// keptHolderName is the name a parked copy takes, derived the way parkKeptHolder
// derives it: the same sequence under the Kept prefix.
func keptHolderName(t *testing.T, dest, holder string) string {
	t.Helper()
	name := filepath.Base(holder)
	cut := strings.LastIndex(name, holderSuffix)
	if cut < 0 {
		t.Fatalf("holder name %q carries no holder suffix", name)
	}
	return filepath.Join(filepath.Dir(holder), name[:cut]+keptSuffix+name[cut+len(holderSuffix):])
}

// holderSeqDigitsOf is the sequence a copy carries in its name. It survives a
// park, so a report assertion keyed on it holds whether the copy is still under
// the holder prefix or has moved under the Kept one.
func holderSeqDigitsOf(t *testing.T, dest, holder string) string {
	t.Helper()
	seq, ok := holderStamp(dest, holder)
	if !ok {
		t.Fatalf("holder name %q carries no sequence", filepath.Base(holder))
	}
	return fmt.Sprintf("%020d", seq)
}

// A copy recovery keeps and never names is one an operator cannot find, so the
// report is half of every retain row rather than a nicety.
func assertCopyReported(t *testing.T, reported []string, seq string, pass int) {
	t.Helper()
	if !slices.ContainsFunc(reported, func(m string) bool { return strings.Contains(m, seq) }) {
		t.Errorf("pass %d: the retained copy (sequence %s) should be named in the report, got %v", pass, seq, reported)
	}
}

// assertCopySet compares every directory under the given prefixes against the
// paths the row expects, resolving both sides through EvalSymlinks because the
// temp root is a symlink on macOS and a path built from t.TempDir would then
// never equal one read back out of the directory.
func assertCopySet(t *testing.T, parent string, prefixes, want []string, pass int) {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		for _, prefix := range prefixes {
			if strings.HasPrefix(entry.Name(), prefix) {
				got = append(got, resolvedPath(t, filepath.Join(parent, entry.Name())))
				break
			}
		}
	}
	wantResolved := make([]string, 0, len(want))
	for _, path := range want {
		wantResolved = append(wantResolved, resolvedPath(t, path))
	}
	slices.Sort(got)
	slices.Sort(wantResolved)
	if !slices.Equal(got, wantResolved) {
		t.Errorf("pass %d: copies on disk = %v, want %v", pass, got, wantResolved)
	}
}

// resolvedPath resolves symlinks so the two sides of a path comparison are the
// same path. A path that does not exist cannot be resolved, and its cleaned form
// is what the comparison then reports.
func resolvedPath(t *testing.T, path string) string {
	t.Helper()
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

// Ownership is a name plus a marker that agrees with it, and neither half alone.
// A directory that only carries the generated name is a sibling someone renamed
// or an unrelated install, and recovery restoring from one, or reaping one, is
// the whole class of defect the marker exists to close. Each case here is a
// single check dropped: the marker, its kind, its sequence, its destination.
func TestOwnershipCannotBeForgedBySiblingNames(t *testing.T) {
	const forgedSeq = 7
	for _, tc := range []struct {
		name   string
		marker *txnMarker
	}{
		{name: "no marker at all"},
		{name: "another site's kind", marker: &txnMarker{Kind: "bundle-extract", Dest: "engine-dir", Seq: forgedSeq}},
		{name: "sequence disagrees with the name", marker: &txnMarker{Kind: holderMarkerKind, Dest: "engine-dir", Seq: forgedSeq + 1}},
		{name: "destination is another install", marker: &txnMarker{Kind: holderMarkerKind, Dest: "model-dir", Seq: forgedSeq}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dest := filepath.Join(root, "engine-dir")
			// Built outside and renamed in, so nothing about it was ever written
			// by this code: the name is the only thing it shares with a holder,
			// and it is shaped like a restorable one.
			unrelated := filepath.Join(t.TempDir(), "unrelated")
			if err := os.MkdirAll(filepath.Join(unrelated, "install"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(unrelated, "install", "engine"), []byte("forged"), 0o644); err != nil {
				t.Fatal(err)
			}
			forged := fmt.Sprintf("%s%s%020d%s", dest, holderSuffix, forgedSeq, holderSeqSuffix)
			if err := os.Rename(unrelated, forged); err != nil {
				t.Fatal(err)
			}
			if tc.marker != nil {
				if err := writeHolderMarker(forged, *tc.marker); err != nil {
					t.Fatal(err)
				}
			}
			txn := lockFor(t, dest)

			for pass := 1; pass <= 2; pass++ {
				restoreInterruptedPromotion(txn, dest, testPublished, nil)
				got, err := os.ReadFile(filepath.Join(forged, "install", "engine"))
				if err != nil || string(got) != "forged" {
					t.Fatalf("pass %d: the forged directory must be left exactly as it was, got %q (err %v)", pass, got, err)
				}
				// A restore, a park, or a reap all show up here: any of them
				// either creates a destination or moves the directory.
				assertNoOtherEntries(t, root, filepath.Base(forged), installLockDir)
			}
		})
	}
}

// assertNoOtherEntries fails when anything but the named entries is in dir. It
// is how "nothing was owned" is asserted without naming every way ownership
// could have been acted on: a restore, a park, and a reap all change this set.
func assertNoOtherEntries(t *testing.T, dir string, allowed ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !slices.Contains(allowed, entry.Name()) {
			t.Errorf("%s should hold only %v, found %s", dir, allowed, entry.Name())
		}
	}
}
