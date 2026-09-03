package remote

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The crash rows of the recovery plan's Acceptance Examples, as code. Each row
// drives the REAL extract into the on-disk state a stop at one step leaves, then
// runs recovery twice and asserts both halves the table asserts: which tree is
// live, and where every other copy of a tree ended up. Nothing here plants a
// staging dir by hand, so the fixture is whatever the write path actually
// writes; a hand-built one would only prove recovery agrees with the test's idea
// of the write path.
//
// A second pass is asserted because recovery keeps no memory: a copy retained on
// pass one must not be reclassified on pass two, and a copy deleted on pass one
// must not come back.

// copyDisposition is where one copy of a tree ends up. The five values are the
// only terminal states the table uses, so a row that means "retained" has to say
// which kind of retention, and a delete can never be written down as anything
// else.
type copyDisposition int

const (
	// copyDeleted: gone from disk, under neither prefix.
	copyDeleted copyDisposition = iota
	// copyAtStaging: still under the name the extract allocated for it.
	copyAtStaging
	// copyAtKept: moved under the Kept prefix, where the scan does not look.
	copyAtKept
	// copyInPlaceUnowned: still at its name and carrying no marker, so nothing
	// on disk attributes it to this code.
	copyInPlaceUnowned
	// copyAtDest: restored, so the copy's content is the live tree and its
	// directory is gone.
	copyAtDest
)

// bundleCrashRow is one row of the table. Each of these rows leaves exactly one
// staged copy, and the assertion compares the WHOLE set of copies on disk
// against that one, so a row can never pass while a second copy nobody expected
// sits beside it.
type bundleCrashRow struct {
	id string
	// arrange runs the real extract with faults injected to stop it at one step.
	// The faults are unwound before recovery runs, so recovery sees a crashed
	// tree and not a failing filesystem.
	arrange  func(t *testing.T, dir, dest string)
	wantLive string
	wantCopy copyDisposition
	// report1 and report2 assert the copy is named in the recovery report on
	// that pass. A copy recovery retains but never names is one an operator
	// cannot find, which the table counts as a failure of the row.
	report1 bool
	report2 bool
}

// arrangeInterruptedSwap stops the extract between its two swap renames: the
// prior tree is aside in backup, the publish failed, and the restore failed too,
// so the destination is absent and that backup is the only copy of it. S4 and S7
// differ in how the writer got here and are identical on disk, which is why they
// share one arrangement.
func arrangeInterruptedSwap(t *testing.T, dir, dest string) {
	t.Helper()
	injected := errors.New("injected swap failure")
	injectFault(t, "rename", func(args ...string) bool {
		from := filepath.Base(args[0])
		return from == "repo" || from == "backup"
	}, injected)
	if err := extractBundle(context.Background(), testBundle(t, "a.txt", "v1"), dest, nil); err == nil {
		t.Fatal("an extract whose publish and restore both fail must report an error")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("the interrupted swap should leave %s absent, got %v", dest, err)
	}
}

func bundleCrashRows() []bundleCrashRow {
	return []bundleCrashRow{
		{
			// A stop between the directory and its marker leaves a directory
			// nothing on disk attributes. Deleting it would need proof this code
			// wrote it, and the marker is that proof, so it is retained and
			// named rather than reaped on the strength of its name.
			id: "S1",
			arrange: func(t *testing.T, dir, dest string) {
				injectFault(t, "createTemp", func(args ...string) bool {
					return args[1] == stagingMarkerFile+"-*"
				}, errors.New("injected marker temp failure"))
				injectFault(t, "removeAll", nil, errors.New("injected cleanup failure"))
				if err := extractBundle(context.Background(), testBundle(t, "a.txt", "v1"), dest, nil); err == nil {
					t.Fatal("an extract whose marker cannot be written must fail")
				}
			},
			wantLive: "v0",
			wantCopy: copyInPlaceUnowned,
			report1:  true,
			report2:  true,
		},
		{
			// The marker landed and the clone did not, so the directory is owned
			// and holds no copy of any tree. Nothing can be lost by removing it,
			// and leaving it is what accumulates scratch forever.
			id: "S2",
			arrange: func(t *testing.T, dir, dest string) {
				injectFault(t, "removeAll", nil, errors.New("injected cleanup failure"))
				if err := extractBundle(context.Background(), filepath.Join(dir, "missing.bundle"), dest, nil); err == nil {
					t.Fatal("cloning a bundle that does not exist must fail")
				}
			},
			wantLive: "v0",
			wantCopy: copyDeleted,
		},
		{
			// The prior tree is aside and the destination is absent, so the
			// staged copy is the only copy of it and has to come back.
			id:       "S4",
			arrange:  arrangeInterruptedSwap,
			wantLive: "v0",
			wantCopy: copyAtDest,
		},
		{
			// The publish landed and the flag did not. Without the flag there is
			// no evidence the copy in backup was superseded, so it may not be
			// deleted; parking it is what keeps it out of the next pass's way
			// without dropping the last copy of a tree.
			id: "S5",
			arrange: func(t *testing.T, dir, dest string) {
				injectFault(t, "create", func(args ...string) bool {
					return filepath.Base(args[0]) == committedFile
				}, errors.New("injected commit flag failure"))
				if err := extractBundle(context.Background(), testBundle(t, "a.txt", "v1"), dest, nil); err != nil {
					t.Fatalf("a committed publish must be reported as success: %v", err)
				}
			},
			wantLive: "v1",
			wantCopy: copyAtKept,
			report1:  true,
		},
		{
			// The publish failed and the restore put the tree back, so the
			// directory holds no copy of anything: owned and empty.
			id: "S6",
			arrange: func(t *testing.T, dir, dest string) {
				injected := errors.New("injected publish failure")
				injectFault(t, "rename", func(args ...string) bool {
					return filepath.Base(args[0]) == "repo"
				}, injected)
				injectFault(t, "removeAll", nil, errors.New("injected cleanup failure"))
				if err := extractBundle(context.Background(), testBundle(t, "a.txt", "v1"), dest, nil); !errors.Is(err, injected) {
					t.Fatalf("extract = %v, want the injected publish failure", err)
				}
			},
			wantLive: "v0",
			wantCopy: copyDeleted,
		},
		{
			// The writer's own retain branch: publish failed, restore failed, the
			// copy was kept and named in the error. On disk this is S4, and
			// recovery must not tell them apart, because nothing on disk does.
			id:       "S7",
			arrange:  arrangeInterruptedSwap,
			wantLive: "v0",
			wantCopy: copyAtDest,
		},
		{
			// The flag is there, so the copy in backup is provably superseded by
			// the tree now live at the destination. This is the one and only
			// shape that licenses deleting a copy of a tree.
			id: "S8",
			arrange: func(t *testing.T, dir, dest string) {
				injectFault(t, "removeAll", nil, errors.New("injected cleanup failure"))
				if err := extractBundle(context.Background(), testBundle(t, "a.txt", "v1"), dest, nil); err != nil {
					t.Fatalf("a cleanup failure must be logged, not returned: %v", err)
				}
			},
			wantLive: "v1",
			wantCopy: copyDeleted,
		},
	}
}

func TestBundleCrashMatrix(t *testing.T) {
	for _, row := range bundleCrashRows() {
		t.Run(row.id, func(t *testing.T) {
			dir := t.TempDir()
			dest := filepath.Join(dir, "proj-1")
			if err := extractBundle(context.Background(), testBundle(t, "a.txt", "v0"), dest, nil); err != nil {
				t.Fatalf("seed extract: %v", err)
			}
			// The faults belong to the crash, not to recovery: restoring the
			// seam here is what makes the next lines a recovery over a real
			// filesystem rather than over a failing one.
			func() {
				saved := stagingFS
				defer func() { stagingFS = saved }()
				row.arrange(t, dir, dest)
			}()
			staging := soleStaging(t, dir)

			for pass, wantReport := range []bool{row.report1, row.report2} {
				var logged []string
				recoverBundleDir(dir, func(format string, args ...any) {
					logged = append(logged, fmt.Sprintf(format, args...))
				})
				assertBundleTerminalState(t, dir, dest, staging, row.wantCopy, row.wantLive, pass+1)
				if wantReport {
					assertCopyReported(t, logged, stagingSeqDigitsOf(t, staging), pass+1)
				}
			}
		})
	}
}

// assertBundleTerminalState asserts both halves of a row: the live tree, and the
// full set of copies still on disk under either prefix. The set is compared
// whole rather than one path at a time, so an extra copy nobody expected fails
// the row instead of going unnoticed.
func assertBundleTerminalState(t *testing.T, dir, dest, staging string, want copyDisposition, wantLive string, pass int) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dest, "a.txt"))
	switch {
	case wantLive == "":
		if err == nil {
			t.Errorf("pass %d: the destination should be absent, it holds %q", pass, got)
		}
	case err != nil || string(got) != wantLive:
		t.Errorf("pass %d: dest a.txt = %q (err %v), want %q", pass, got, err, wantLive)
	}

	var wantPaths []string
	switch want {
	case copyAtStaging, copyInPlaceUnowned:
		wantPaths = []string{staging}
	case copyAtKept:
		wantPaths = []string{parkedStaging(staging)}
	}
	assertCopySet(t, dir, []string{stagingPrefix, keptPrefix}, wantPaths, pass)

	switch want {
	case copyInPlaceUnowned:
		// Retained is not the whole claim: the reason it is retained is that
		// nothing on disk attributes it, and a row that stopped checking that
		// would pass over a copy recovery could have proved was its own.
		if _, err := readMarker(staging); !errors.Is(err, errMarkerMissing) {
			t.Errorf("pass %d: %s should carry no marker, readMarker = %v", pass, staging, err)
		}
	case copyAtDest:
		if _, err := os.Stat(dest); err != nil {
			t.Errorf("pass %d: the copy should have been restored to the destination: %v", pass, err)
		}
	}
}

// stagingSeqDigitsOf is the sequence a copy carries in its name. It survives a
// park, so a report assertion keyed on it holds whether the copy is still under
// the staging prefix or has moved under the Kept one.
func stagingSeqDigitsOf(t *testing.T, staging string) string {
	t.Helper()
	base := filepath.Base(staging)
	digits := strings.TrimSuffix(strings.TrimPrefix(base, stagingPrefix), stagingSeqSuffix)
	if len(digits) != stagingSeqDigits {
		t.Fatalf("staging name %q carries no sequence", base)
	}
	return digits
}

// A copy recovery keeps and never names is one an operator cannot find, so the
// report is half of every retain row rather than a nicety.
func assertCopyReported(t *testing.T, logged []string, seq string, pass int) {
	t.Helper()
	if !slices.ContainsFunc(logged, func(m string) bool { return strings.Contains(m, seq) }) {
		t.Errorf("pass %d: the retained copy (sequence %s) should be named in the report, got %v", pass, seq, logged)
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
// A directory that only carries the generated name is a legacy work tree or a
// sibling someone renamed, and recovery restoring from one, or reaping one, is
// the whole class of defect the marker exists to close. Each case here is a
// single check dropped: the marker, its kind, its sequence, its destination.
func TestOwnershipCannotBeForgedBySiblingNames(t *testing.T) {
	const forgedSeq = 7
	for _, tc := range []struct {
		name   string
		marker *txnMarker
	}{
		{name: "no marker at all"},
		{name: "another site's kind", marker: &txnMarker{Kind: "dictation-promote", Dest: "proj-1", Seq: forgedSeq}},
		{name: "sequence disagrees with the name", marker: &txnMarker{Kind: txnKindBundleExtract, Dest: "proj-1", Seq: forgedSeq + 1}},
		{name: "destination is not a link id", marker: &txnMarker{Kind: txnKindBundleExtract, Dest: ".hidden", Seq: forgedSeq}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			// Built outside and renamed in, so nothing about it was ever written
			// by this code: the name is the only thing it shares with a staging
			// dir, and it is shaped like a restorable one.
			unrelated := filepath.Join(t.TempDir(), "unrelated")
			if err := os.MkdirAll(filepath.Join(unrelated, "backup", ".git"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(unrelated, "backup", "a.txt"), []byte("forged"), 0o644); err != nil {
				t.Fatal(err)
			}
			forged := filepath.Join(dir, fmt.Sprintf("%s%020d%s", stagingPrefix, forgedSeq, stagingSeqSuffix))
			if err := os.Rename(unrelated, forged); err != nil {
				t.Fatal(err)
			}
			if tc.marker != nil {
				if err := writeMarker(forged, *tc.marker); err != nil {
					t.Fatal(err)
				}
			}

			for pass := 1; pass <= 2; pass++ {
				recoverBundleDir(dir, nil)
				got, err := os.ReadFile(filepath.Join(forged, "backup", "a.txt"))
				if err != nil || string(got) != "forged" {
					t.Fatalf("pass %d: the forged directory must be left exactly as it was, got %q (err %v)", pass, got, err)
				}
				// A restore, a park, or a reap all show up here: any of them
				// either creates a destination or moves the directory.
				assertNoOtherEntries(t, dir, filepath.Base(forged), lockDirName)
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
