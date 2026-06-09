package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/cron"
)

func testCronStore(t *testing.T) *cron.Store {
	t.Helper()
	return cron.NewStore(cron.StoreOptions{RootDir: t.TempDir(), Now: func() time.Time { return time.Date(2026, 6, 9, 8, 0, 0, 0, time.UTC) }})
}

func TestCronAddListRemove(t *testing.T) {
	store := testCronStore(t)
	var out, errb bytes.Buffer
	now := func() time.Time { return time.Date(2026, 6, 9, 8, 0, 0, 0, time.UTC) }

	if code := runCronWith(store, now, []string{"add", "0 9 * * *", "--prompt", "daily"}, &out, &errb); code != 0 {
		t.Fatalf("add exit=%d err=%s", code, errb.String())
	}
	jobs, _ := store.List()
	if len(jobs) != 1 || jobs[0].Expr != "0 9 * * *" || jobs[0].Prompt != "daily" {
		t.Fatalf("job not stored: %+v", jobs)
	}
	if jobs[0].NextRunAt.IsZero() {
		t.Fatal("NextRunAt should be set on add")
	}

	out.Reset()
	if code := runCronWith(store, now, []string{"list"}, &out, &errb); code != 0 {
		t.Fatalf("list exit=%d", code)
	}
	if !strings.Contains(out.String(), jobs[0].ID) || !strings.Contains(out.String(), "0 9 * * *") {
		t.Fatalf("list output missing job:\n%s", out.String())
	}

	if code := runCronWith(store, now, []string{"pause", jobs[0].ID}, &out, &errb); code != 0 {
		t.Fatalf("pause exit=%d", code)
	}
	if j, _ := store.Get(jobs[0].ID); j.Status != cron.StatusPaused {
		t.Fatalf("pause did not persist, status=%q", j.Status)
	}

	if code := runCronWith(store, now, []string{"rm", jobs[0].ID}, &out, &errb); code != 0 {
		t.Fatalf("rm exit=%d", code)
	}
	if jobs, _ := store.List(); len(jobs) != 0 {
		t.Fatalf("expected removed, got %v", jobs)
	}
}

func TestCronAddRejectsBadExpr(t *testing.T) {
	store := testCronStore(t)
	var out, errb bytes.Buffer
	now := func() time.Time { return time.Now() }
	if code := runCronWith(store, now, []string{"add", "99 * * * *", "--prompt", "x"}, &out, &errb); code == 0 {
		t.Fatal("expected non-zero exit for invalid cron expr")
	}
	if !strings.Contains(errb.String(), "minute") {
		t.Fatalf("error should name the bad field, got %q", errb.String())
	}
}

func TestCronAddRecipe(t *testing.T) {
	store := testCronStore(t)
	var out, errb bytes.Buffer
	now := func() time.Time { return time.Date(2026, 6, 9, 8, 0, 0, 0, time.UTC) }
	if code := runCronWith(store, now, []string{"add", "--recipe", "git-recap"}, &out, &errb); code != 0 {
		t.Fatalf("add --recipe exit=%d err=%s", code, errb.String())
	}
	jobs, _ := store.List()
	if len(jobs) != 1 || jobs[0].Expr != "*/30 * * * *" {
		t.Fatalf("recipe job not stored: %+v", jobs)
	}
}
