package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestGrantRequestPermissionsReadDoesNotGrantWrite(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "notes.txt")
	scope, err := NewScope(workspace, nil)
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: workspace,
		Policy:        DefaultPolicy(),
		Scope:         scope,
	})

	cleanup, err := engine.GrantRequestPermissions(RequestPermissionProfile{
		FileSystem: &FileSystemPermissions{Read: []string{outside}},
	}, PermissionGrantScopeTurn)
	if err != nil {
		t.Fatalf("GrantRequestPermissions: %v", err)
	}
	t.Cleanup(cleanup)

	read := engine.Evaluate(context.Background(), Request{
		WorkspaceRoot: workspace,
		ToolName:      "read_file",
		SideEffect:    SideEffectRead,
		Permission:    PermissionAllow,
		Args:          map[string]any{"path": target},
	})
	if read.Action != ActionAllow {
		t.Fatalf("read grant should allow reads, got %#v", read)
	}
	write := engine.Evaluate(context.Background(), Request{
		WorkspaceRoot: workspace,
		ToolName:      "write_file",
		SideEffect:    SideEffectWrite,
		Permission:    PermissionAllow,
		Args:          map[string]any{"path": target},
	})
	if write.Action != ActionDeny {
		t.Fatalf("read grant must not allow writes, got %#v", write)
	}

	cleanup()
	readAfterCleanup := engine.Evaluate(context.Background(), Request{
		WorkspaceRoot: workspace,
		ToolName:      "read_file",
		SideEffect:    SideEffectRead,
		Permission:    PermissionAllow,
		Args:          map[string]any{"path": target},
	})
	if readAfterCleanup.Action != ActionDeny {
		t.Fatalf("turn read grant should be cleaned up, got %#v", readAfterCleanup)
	}
}

func TestGrantRequestPermissionsNetworkOverlaysPolicyForTurn(t *testing.T) {
	workspace := t.TempDir()
	policy := DefaultPolicy()
	policy.EnforceToolNetwork = true
	engine := NewEngine(EngineOptions{WorkspaceRoot: workspace, Policy: policy})
	request := Request{
		WorkspaceRoot: workspace,
		ToolName:      "web_fetch",
		SideEffect:    SideEffectNetwork,
		Permission:    PermissionAllow,
		Args:          map[string]any{"url": "https://example.com"},
	}
	if before := engine.Evaluate(context.Background(), request); before.Action != ActionDeny {
		t.Fatalf("default policy should deny shell network, got %#v", before)
	}
	enabled := true
	cleanup, err := engine.GrantRequestPermissions(RequestPermissionProfile{
		Network: &NetworkPermissions{Enabled: &enabled},
	}, PermissionGrantScopeTurn)
	if err != nil {
		t.Fatalf("GrantRequestPermissions: %v", err)
	}
	if after := engine.Evaluate(context.Background(), request); after.Action != ActionAllow {
		t.Fatalf("network turn grant should allow shell network, got %#v", after)
	}
	cleanup()
	if afterCleanup := engine.Evaluate(context.Background(), request); afterCleanup.Action != ActionDeny {
		t.Fatalf("network turn grant should be cleaned up, got %#v", afterCleanup)
	}
}

func TestGrantRequestPermissionsSessionPersists(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "notes.txt")
	engine := NewEngine(EngineOptions{WorkspaceRoot: workspace, Policy: DefaultPolicy()})

	cleanup, err := engine.GrantRequestPermissions(RequestPermissionProfile{
		FileSystem: &FileSystemPermissions{Write: []string{outside}},
	}, PermissionGrantScopeSession)
	if err != nil {
		t.Fatalf("GrantRequestPermissions: %v", err)
	}
	cleanup()

	decision := engine.Evaluate(context.Background(), Request{
		WorkspaceRoot: workspace,
		ToolName:      "write_file",
		SideEffect:    SideEffectWrite,
		Permission:    PermissionAllow,
		Args:          map[string]any{"path": target},
	})
	if decision.Action != ActionAllow {
		t.Fatalf("session grant should persist after cleanup no-op, got %#v", decision)
	}
}

func TestPermissionProfileIncludesReadOnlyGrantAsReadRootOnly(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	scope, err := NewScope(workspace, nil)
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	expectedReadRoot, err := scope.AddRead(outside)
	if err != nil {
		t.Fatalf("AddRead: %v", err)
	}

	profile := PermissionProfileFromPolicy(workspace, DefaultPolicy(), scope)
	if !containsString(profile.FileSystem.ReadRoots, expectedReadRoot) {
		t.Fatalf("read roots = %#v, want %s", profile.FileSystem.ReadRoots, expectedReadRoot)
	}
	for _, root := range profile.FileSystem.WriteRoots {
		if root.Root == expectedReadRoot {
			t.Fatalf("read-only grant must not become writable, write roots = %#v", profile.FileSystem.WriteRoots)
		}
	}
}

func TestRequestPermissionGrantProfileWidensFilesToGrantRoots(t *testing.T) {
	workspace := t.TempDir()
	readDir := filepath.Join(workspace, "read")
	writeDir := filepath.Join(workspace, "write")
	if err := os.MkdirAll(readDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(writeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	readFile := filepath.Join(readDir, "secret.txt")
	writeFile := filepath.Join(writeDir, "output.txt")
	if err := os.WriteFile(readFile, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	grant, err := RequestPermissionGrantProfile(RequestPermissionProfile{
		FileSystem: &FileSystemPermissions{
			Read:  []string{readFile},
			Write: []string{writeFile},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if grant.FileSystem == nil {
		t.Fatal("expected filesystem grant")
	}
	expectedReadRoot, err := normalizeScopeRoot(readDir)
	if err != nil {
		t.Fatal(err)
	}
	expectedWriteRoot, err := normalizeScopeRoot(writeDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(grant.FileSystem.Read) != 1 || grant.FileSystem.Read[0] != expectedReadRoot {
		t.Fatalf("read grant roots = %#v, want %#v", grant.FileSystem.Read, []string{expectedReadRoot})
	}
	if len(grant.FileSystem.Write) != 1 || grant.FileSystem.Write[0] != expectedWriteRoot {
		t.Fatalf("write grant roots = %#v, want %#v", grant.FileSystem.Write, []string{expectedWriteRoot})
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
