package tray

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// A parse-only check, so it runs on every OS: the Win32 layer passes pointers
// as uintptr, which the compiler keeps alive only for a call to a function
// marked //go:uintptrescapes (or syscall.SyscallN, which is marked itself) and
// only when the conversion is written in the call expression.
func TestWin32PointersStayAlive(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "tray_windows.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	var callFn *ast.FuncDecl
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "call" && fn.Recv == nil {
			callFn = fn
		}
	}
	if callFn == nil {
		t.Fatal("func call not found in tray_windows.go")
	}
	marked := false
	if callFn.Doc != nil {
		for _, c := range callFn.Doc.List {
			if c.Text == "//go:uintptrescapes" {
				marked = true
			}
		}
	}
	if !marked {
		t.Error("func call lacks //go:uintptrescapes: an object whose address is passed as uintptr may move or be collected during the call")
	}

	// Every uintptr(unsafe.Pointer(x)) must be a direct argument of call or
	// syscall.SyscallN.
	ok := map[ast.Node]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		c, isCall := n.(*ast.CallExpr)
		if !isCall || !isCallOrSyscallN(c) {
			return true
		}
		for _, a := range c.Args {
			ok[a] = true
		}
		return true
	})
	ast.Inspect(f, func(n ast.Node) bool {
		c, isCall := n.(*ast.CallExpr)
		if !isCall || !isUintptrOfUnsafePointer(c) {
			return true
		}
		if !ok[c] {
			t.Errorf("%s: uintptr(unsafe.Pointer(...)) outside an argument of call or syscall.SyscallN", fset.Position(c.Pos()))
		}
		return true
	})
}

func isCallOrSyscallN(c *ast.CallExpr) bool {
	switch fn := c.Fun.(type) {
	case *ast.Ident:
		return fn.Name == "call"
	case *ast.SelectorExpr:
		id, ok := fn.X.(*ast.Ident)
		return ok && id.Name == "syscall" && fn.Sel.Name == "SyscallN"
	}
	return false
}

func isUintptrOfUnsafePointer(c *ast.CallExpr) bool {
	id, ok := c.Fun.(*ast.Ident)
	if !ok || id.Name != "uintptr" || len(c.Args) != 1 {
		return false
	}
	inner, ok := c.Args[0].(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := inner.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	return ok && x.Name == "unsafe" && sel.Sel.Name == "Pointer"
}
