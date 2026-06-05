package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPermissionStorePersistsIdentityAwareToolGrants(t *testing.T) {
	store := newTestPermissionStore(t)

	_, err := store.GrantTool(GrantToolInput{
		ServerName:     "docs",
		ServerIdentity: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ToolName:       "lookup",
		MaxAutonomy:    AutonomyMedium,
	})
	if err != nil {
		t.Fatalf("GrantTool returned error: %v", err)
	}
	if _, err := os.Stat(store.FilePath()); err != nil {
		t.Fatalf("permission file was not written: %v", err)
	}

	approved, err := store.IsToolPersistentlyApproved(CheckToolInput{
		ServerName:        "docs",
		ServerIdentity:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ToolName:          "lookup",
		RequestedAutonomy: AutonomyMedium,
	})
	if err != nil || !approved {
		t.Fatalf("approved=%v err=%v", approved, err)
	}
	approved, err = store.IsToolPersistentlyApproved(CheckToolInput{
		ServerName:        "docs",
		ServerIdentity:    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		ToolName:          "lookup",
		RequestedAutonomy: AutonomyMedium,
	})
	if err != nil || approved {
		t.Fatalf("wrong identity approved=%v err=%v", approved, err)
	}
	approved, err = store.IsToolPersistentlyApproved(CheckToolInput{
		ServerName:        "docs",
		ServerIdentity:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ToolName:          "lookup",
		RequestedAutonomy: AutonomyHigh,
	})
	if err != nil || approved {
		t.Fatalf("high autonomy approved=%v err=%v", approved, err)
	}
}

func TestPermissionStoreServerGrantCoversToolsAndRevokesCascade(t *testing.T) {
	store := newTestPermissionStore(t)
	if _, err := store.GrantServer(GrantServerInput{ServerName: "docs", ServerIdentity: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", MaxAutonomy: AutonomyHigh}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GrantTool(GrantToolInput{ServerName: "docs", ServerIdentity: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ToolName: "lookup", MaxAutonomy: AutonomyLow}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GrantTool(GrantToolInput{ServerName: "other", ServerIdentity: "cccccccccccccccccccccccccccccccc", ToolName: "search", MaxAutonomy: AutonomyLow}); err != nil {
		t.Fatal(err)
	}

	approved, err := store.IsToolPersistentlyApproved(CheckToolInput{
		ServerName:        "docs",
		ServerIdentity:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ToolName:          "write",
		RequestedAutonomy: AutonomyHigh,
	})
	if err != nil || !approved {
		t.Fatalf("server grant approved=%v err=%v", approved, err)
	}

	revoked, err := store.RevokeServer("docs")
	if err != nil {
		t.Fatalf("RevokeServer returned error: %v", err)
	}
	if revoked != 2 {
		t.Fatalf("revoked = %d, want 2", revoked)
	}
	grants, err := store.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(grants) != 1 || grants[0].ServerName != "other" || grants[0].ToolName != "search" {
		t.Fatalf("unexpected grants after revoke: %#v", grants)
	}
}

func TestPermissionStoreListsStableOrderAndClears(t *testing.T) {
	store := newTestPermissionStore(t)
	if _, err := store.GrantTool(GrantToolInput{ServerName: "zeta", ServerIdentity: "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", ToolName: "lookup", MaxAutonomy: AutonomyLow}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GrantServer(GrantServerInput{ServerName: "alpha", ServerIdentity: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", MaxAutonomy: AutonomyMedium}); err != nil {
		t.Fatal(err)
	}

	grants, err := store.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if got := []PermissionScope{grants[0].Scope, grants[1].Scope}; !reflect.DeepEqual(got, []PermissionScope{ScopeServer, ScopeTool}) {
		t.Fatalf("grant scopes = %#v", got)
	}
	if grants[0].ServerName != "alpha" || grants[1].ServerName != "zeta" {
		t.Fatalf("grant order = %#v", grants)
	}
	cleared, err := store.Clear()
	if err != nil {
		t.Fatalf("Clear returned error: %v", err)
	}
	if cleared != 2 {
		t.Fatalf("cleared = %d, want 2", cleared)
	}
	grants, err = store.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(grants) != 0 {
		t.Fatalf("expected no grants after clear, got %#v", grants)
	}
}

func TestPermissionStoreRejectsInvalidAutonomyBeforeWriting(t *testing.T) {
	store := newTestPermissionStore(t)
	_, err := store.GrantServer(GrantServerInput{
		ServerName:     "docs",
		ServerIdentity: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		MaxAutonomy:    PermissionAutonomy("urgent"),
	})
	if err == nil || !strings.Contains(err.Error(), "invalid MCP permission autonomy") {
		t.Fatalf("expected invalid autonomy error, got %v", err)
	}
	grants, err := store.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(grants) != 0 {
		t.Fatalf("expected no grants after invalid write, got %#v", grants)
	}
}

func TestPermissionStoreSerializesConcurrentStoreInstances(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "mcp-permissions.json")
	const grantCount = 16
	var wait sync.WaitGroup
	errs := make(chan error, grantCount)

	for index := 0; index < grantCount; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			store, err := NewPermissionStore(StoreOptions{
				FilePath: filePath,
				Now:      func() time.Time { return time.Date(2026, 6, 3, 9, 30, index, 0, time.UTC) },
			})
			if err != nil {
				errs <- err
				return
			}
			_, err = store.GrantTool(GrantToolInput{
				ServerName:     fmt.Sprintf("docs_%02d", index),
				ServerIdentity: fmt.Sprintf("%032d", index),
				ToolName:       "lookup",
				MaxAutonomy:    AutonomyLow,
			})
			if err != nil {
				errs <- err
			}
		}(index)
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent grant failed: %v", err)
		}
	}

	store, err := NewPermissionStore(StoreOptions{FilePath: filePath})
	if err != nil {
		t.Fatalf("NewPermissionStore returned error: %v", err)
	}
	grants, err := store.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(grants) != grantCount {
		t.Fatalf("grant count = %d, want %d: %#v", len(grants), grantCount, grants)
	}
}

func TestPermissionStoreDoesNotBlockOnStaleDirectoryLock(t *testing.T) {
	store := newTestPermissionStore(t)
	if err := os.Mkdir(store.FilePath()+".lock", 0o700); err != nil {
		t.Fatalf("failed to create stale directory lock: %v", err)
	}

	if _, err := store.GrantServer(GrantServerInput{
		ServerName:     "docs",
		ServerIdentity: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		MaxAutonomy:    AutonomyLow,
	}); err != nil {
		t.Fatalf("GrantServer should ignore stale directory locks, got %v", err)
	}
}

func TestPermissionStoreRejectsInvalidPersistedKeys(t *testing.T) {
	validGrant := `{"serverIdentity":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","maxAutonomy":"low","approvedAt":"2026-06-03T09:30:00Z"}`
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{
			name: "server key",
			body: `{"schemaVersion":1,"servers":{"../escape":` + validGrant + `},"tools":{}}`,
			want: "invalid MCP server name",
		},
		{
			name: "tool key",
			body: `{"schemaVersion":1,"servers":{},"tools":{"docs":{"":` + validGrant + `}}}`,
			want: "MCP tool name is required",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filePath := filepath.Join(t.TempDir(), "mcp-permissions.json")
			if err := os.WriteFile(filePath, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			store, err := NewPermissionStore(StoreOptions{FilePath: filePath})
			if err != nil {
				t.Fatalf("NewPermissionStore returned error: %v", err)
			}
			_, err = store.List()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q error, got %v", tc.want, err)
			}
		})
	}
}

func TestResolvePermissionPathHonorsOverrideAndConfigHome(t *testing.T) {
	dir := t.TempDir()
	override, err := ResolvePermissionPath(map[string]string{"ZERO_MCP_PERMISSIONS_PATH": filepath.Join(dir, "custom.json")})
	if err != nil {
		t.Fatalf("ResolvePermissionPath override returned error: %v", err)
	}
	if override != filepath.Join(dir, "custom.json") {
		t.Fatalf("override path = %q", override)
	}
	resolved, err := ResolvePermissionPath(map[string]string{"XDG_CONFIG_HOME": filepath.Join(dir, "xdg")})
	if err != nil {
		t.Fatalf("ResolvePermissionPath xdg returned error: %v", err)
	}
	if resolved != filepath.Join(dir, "xdg", "zero", "mcp-permissions.json") {
		t.Fatalf("xdg path = %q", resolved)
	}
}

func newTestPermissionStore(t *testing.T) *PermissionStore {
	t.Helper()
	store, err := NewPermissionStore(StoreOptions{
		FilePath: filepath.Join(t.TempDir(), "mcp-permissions.json"),
		Now:      func() time.Time { return time.Date(2026, 6, 3, 9, 30, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("NewPermissionStore returned error: %v", err)
	}
	return store
}
