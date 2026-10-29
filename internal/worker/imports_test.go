package worker

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const modPath = "github.com/BeppeTemp/cartographer/"

func importsOf(t *testing.T, file string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, im := range f.Imports {
		p, _ := strconv.Unquote(im.Path.Value)
		out = append(out, p)
	}
	return out
}

// serve never starts or links the worker: no package under internal/ outside
// internal/worker, and not cmd/cartographer/serve.go, imports it. A package main
// cannot keep the roles apart by package, so the file-level check is the pin.
func TestServeNeverLinksTheWorker(t *testing.T) {
	root := filepath.Join("..", "..")
	checked := 0
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "internal/worker/") {
			return nil
		}
		checked++
		for _, p := range importsOf(t, path) {
			if p == modPath+"internal/worker" || strings.HasPrefix(p, modPath+"internal/worker/") {
				t.Errorf("%s imports %s: only the worker command may", rel, p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("no file checked")
	}
	for _, p := range importsOf(t, filepath.Join(root, "cmd", "cartographer", "serve.go")) {
		if strings.HasPrefix(p, modPath+"internal/worker") {
			t.Errorf("serve.go imports %s", p)
		}
	}
}

// The worker package holds no KB and no control plane: it only speaks MCP.
func TestWorkerImportsNoServerSide(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		for _, p := range importsOf(t, f) {
			if p == modPath+"internal/mcpserver" || p == modPath+"internal/kb" {
				t.Errorf("%s imports %s", f, p)
			}
		}
	}
}
