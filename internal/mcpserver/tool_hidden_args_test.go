package mcpserver

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// handlerJSONKeys maps a tool name to the json keys of the struct variables
// its inline Handler declares (`var params struct{...}`), read from source: the
// handlers decode their own arguments, so the source is the only place the
// accepted keys are written down.
func handlerJSONKeys(t *testing.T) map[string]map[string]bool {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]bool{}
	fset := token.NewFileSet()
	var parsed []*ast.File
	funcs := map[string]*ast.FuncDecl{}     // package functions: a handler may delegate to one
	structs := map[string]*ast.StructType{} // package struct types a handler may decode into
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		parsed = append(parsed, file)
		for _, d := range file.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					funcs[d.Name.Name] = d
				}
			case *ast.GenDecl:
				for _, sp := range d.Specs {
					if ts, ok := sp.(*ast.TypeSpec); ok {
						if st, ok := ts.Type.(*ast.StructType); ok {
							structs[ts.Name.Name] = st
						}
					}
				}
			}
		}
	}
	for _, file := range parsed {
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if id, ok := lit.Type.(*ast.Ident); !ok || id.Name != "Tool" {
				return true
			}
			var name string
			var handler *ast.FuncLit
			for _, el := range lit.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				switch k, _ := kv.Key.(*ast.Ident); {
				case k != nil && k.Name == "Name":
					if bl, ok := kv.Value.(*ast.BasicLit); ok {
						name, _ = strconv.Unquote(bl.Value)
					}
				case k != nil && k.Name == "Handler":
					handler, _ = kv.Value.(*ast.FuncLit)
				}
			}
			if name == "" || handler == nil {
				return true
			}
			keys := out[name]
			if keys == nil {
				keys = map[string]bool{}
				out[name] = keys
			}
			// The variables the handler unmarshals its raw arguments into; an
			// output struct declared in the same body is not an argument.
			bodies := []ast.Node{handler.Body}
			ast.Inspect(handler.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				id, ok := call.Fun.(*ast.Ident)
				if !ok || funcs[id.Name] == nil {
					return true
				}
				for _, a := range call.Args {
					if ai, ok := a.(*ast.Ident); ok && ai.Name == "args" {
						bodies = append(bodies, funcs[id.Name].Body)
					}
				}
				return true
			})
			decoded := map[string]bool{}
			for _, body := range bodies {
				ast.Inspect(body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok || len(call.Args) != 2 {
						return true
					}
					if sel, ok := call.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "Unmarshal" {
						return true
					}
					if a0, ok := call.Args[0].(*ast.Ident); !ok || a0.Name != "args" {
						return true
					}
					if u, ok := call.Args[1].(*ast.UnaryExpr); ok {
						if id, ok := u.X.(*ast.Ident); ok {
							decoded[id.Name] = true
						}
					}
					return true
				})
			}
			for _, body := range bodies {
				ast.Inspect(body, func(n ast.Node) bool {
					vs, ok := n.(*ast.ValueSpec)
					if !ok || len(vs.Names) != 1 || !decoded[vs.Names[0].Name] {
						return true
					}
					stt, ok := vs.Type.(*ast.StructType)
					if id, isID := vs.Type.(*ast.Ident); !ok && isID {
						stt, ok = structs[id.Name], structs[id.Name] != nil
					}
					if !ok {
						return true
					}
					for _, fld := range stt.Fields.List {
						if fld.Tag == nil {
							continue
						}
						tag, _ := strconv.Unquote(fld.Tag.Value)
						key := strings.Split(reflect.StructTag(tag).Get("json"), ",")[0]
						if key != "" && key != "-" {
							keys[key] = true
						}
					}
					return true
				})
			}
			return true
		})
	}
	return out
}

// Every top-level key a tool's handler decodes is either declared in its input
// schema or listed in HiddenArguments: the unknown-argument check (#1279)
// refuses anything else with -32602, so an undeclared key a handler reads would
// be dead on every real client path.
func TestHandlerArgumentsAreDeclaredOrHidden(t *testing.T) {
	t.Parallel()
	s, _ := mapToolsServer(t)
	keys := handlerJSONKeys(t)
	if len(keys) < 20 {
		t.Fatalf("source scan found only %d tools: the scanner is broken", len(keys))
	}
	var bad []string
	for name, tool := range s.Tools() {
		var decl struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		schema := tool.InputSchema
		if schema == nil {
			schema = json.RawMessage(`{}`)
		}
		if err := json.Unmarshal(schema, &decl); err != nil {
			t.Fatalf("%s: schema: %v", name, err)
		}
		for k := range keys[name] {
			if _, ok := decl.Properties[k]; ok {
				continue
			}
			if k == "reason" && tool.GitWrapped {
				continue
			}
			if !slices.Contains(tool.HiddenArguments, k) {
				bad = append(bad, name+"."+k)
			}
		}
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		t.Fatalf("handler keys neither in the schema nor in HiddenArguments: %v", bad)
	}
}
