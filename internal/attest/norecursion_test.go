// Package attest :: norecursion_test.go
// "Zero" means zero recursion — and this test makes it a compile-gate,
// not a slogan. It parses every non-test file in the package, builds the
// intra-package static call graph (conservative: any call to a name
// declared in this package counts as an edge, closures attribute to
// their enclosing function), and fails on any cycle — self-calls and
// mutual recursion alike. The cycle detector itself is iterative.
package attest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestZeroRecursion(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}

	type fn struct {
		name  string
		calls map[string]bool
	}
	graph := map[string]*fn{}
	declared := map[string]bool{}
	var files []*ast.File

	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, f)
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok {
				declared[fd.Name.Name] = true
			}
		}
	}

	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			node := &fn{name: fd.Name.Name, calls: map[string]bool{}}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				var callee string
				switch x := call.Fun.(type) {
				case *ast.Ident:
					callee = x.Name
				case *ast.SelectorExpr:
					callee = x.Sel.Name
				}
				if callee != "" && declared[callee] {
					node.calls[callee] = true
				}
				return true
			})
			// Methods and funcs share a namespace here (conservative).
			if prev, exists := graph[node.name]; exists {
				for c := range node.calls {
					prev.calls[c] = true
				}
			} else {
				graph[node.name] = node
			}
		}
	}

	if len(graph) == 0 {
		t.Fatal("no functions found — wrong directory?")
	}

	// Iterative three-color DFS over the call graph.
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	parent := map[string]string{}

	for start := range graph {
		if color[start] != white {
			continue
		}
		stack := []string{start}
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			if color[cur] == white {
				color[cur] = gray
				for callee := range graph[cur].calls {
					if _, ok := graph[callee]; !ok {
						continue
					}
					switch color[callee] {
					case white:
						parent[callee] = cur
						stack = append(stack, callee)
					case gray:
						// Reconstruct the cycle for the failure message.
						cycle := []string{callee, cur}
						for p := cur; p != callee && parent[p] != ""; p = parent[p] {
							cycle = append(cycle, parent[p])
						}
						t.Fatalf("recursion detected in internal/attest: %s — Zero means ZERO recursion",
							fmt.Sprint(strings.Join(cycle, " → ")))
					}
				}
			} else {
				color[cur] = black
				stack = stack[:len(stack)-1]
			}
		}
	}
}
