package oauth

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

func TestProductionRefreshPersistsWithRefreshIntent(t *testing.T) {
	checks := []struct {
		file   string
		fn     string
		want   string
		forbid []string
	}{
		{
			file:   "manager.go",
			fn:     "refreshAndSave",
			want:   "SaveRefreshed",
			forbid: []string{"Save"},
		},
		{
			file:   filepath.Join("..", "mcp", "network_client.go"),
			fn:     "Refresh",
			want:   "saveRefreshedForServer",
			forbid: []string{"SaveForServer", "Save"},
		},
	}
	for _, check := range checks {
		calls := functionSelectorCalls(t, check.file, check.fn)
		if !containsString(calls, check.want) {
			t.Errorf("%s %s does not call %s; got %v", check.file, check.fn, check.want, calls)
		}
		for _, name := range check.forbid {
			if containsString(calls, name) {
				t.Errorf("%s %s must not persist via %s; got %v", check.file, check.fn, name, calls)
			}
		}
	}
}

func functionSelectorCalls(t *testing.T, path, funcName string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var calls []string
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != funcName || fn.Body == nil {
			return true
		}
		found = true
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			calls = append(calls, sel.Sel.Name)
			return true
		})
		return false
	})
	if !found {
		t.Fatalf("function %s not found in %s", funcName, path)
	}
	return calls
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
