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
		recv   string
		fn     string
		want   string
		forbid []string
	}{
		{
			file:   "manager.go",
			recv:   "Manager",
			fn:     "refreshAndSave",
			want:   "SaveRefreshed",
			forbid: []string{"Save"},
		},
		{
			file:   filepath.Join("..", "mcp", "network_client.go"),
			recv:   "storeTokenSource",
			fn:     "Refresh",
			want:   "saveRefreshedForServer",
			forbid: []string{"SaveForServer", "Save"},
		},
		{
			file:   filepath.Join("..", "mcp", "oauth_store.go"),
			recv:   "TokenStore",
			fn:     "saveRefreshedForServer",
			want:   "SaveRefreshed",
			forbid: []string{"Save"},
		},
	}
	for _, check := range checks {
		calls := functionSelectorCalls(t, check.file, check.recv, check.fn)
		if !containsString(calls, check.want) {
			t.Errorf("%s %s.%s does not call %s; got %v", check.file, check.recv, check.fn, check.want, calls)
		}
		for _, name := range check.forbid {
			if containsString(calls, name) {
				t.Errorf("%s %s.%s must not persist via %s; got %v", check.file, check.recv, check.fn, name, calls)
			}
		}
	}
}

func functionSelectorCalls(t *testing.T, path, recv, funcName string) []string {
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
		if recv != "" && recvTypeName(fn) != recv {
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
		t.Fatalf("function %s.%s not found in %s", recv, funcName, path)
	}
	return calls
}

func recvTypeName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	typ := fn.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	ident, ok := typ.(*ast.Ident)
	if !ok {
		return ""
	}
	return ident.Name
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
