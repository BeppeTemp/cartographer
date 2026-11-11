// Package openapi builds an OpenAPI document from Go types. It has no
// dependency: a schema reflector over `json` struct tags and a builder that
// lays out paths, security and a shared error envelope. The output is
// deterministic (maps are marshalled sorted), so a committed copy of it is a
// reviewable diff.
package openapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"
)

type schema = map[string]any

// Reflector turns Go types into JSON schemas. A named struct becomes a
// component referenced by $ref, which also ends recursion.
type Reflector struct {
	comps  map[string]schema
	owners map[string]reflect.Type
	err    error
}

// NewReflector returns an empty reflector.
func NewReflector() *Reflector {
	return &Reflector{comps: map[string]schema{}, owners: map[string]reflect.Type{}}
}

// componentName is the Go type name without a lowercase `api`/`ui` prefix
// (`uiError` is `Error`), first letter upper-cased.
func componentName(t reflect.Type) string {
	n := t.Name()
	for _, p := range []string{"api", "ui"} {
		if len(n) > len(p) && strings.HasPrefix(n, p) && n[len(p)] >= 'A' && n[len(p)] <= 'Z' {
			n = n[len(p):]
			break
		}
	}
	return strings.ToUpper(n[:1]) + n[1:]
}

var timeType = reflect.TypeOf(time.Time{})
var rawType = reflect.TypeOf(json.RawMessage{})

// Schema returns the schema of t.
func (r *Reflector) Schema(t reflect.Type) schema {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch {
	case t == timeType:
		return schema{"type": "string", "format": "date-time"}
	case t == rawType:
		return schema{}
	}
	switch t.Kind() {
	case reflect.Bool:
		return schema{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return schema{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return schema{"type": "number"}
	case reflect.String:
		return schema{"type": "string"}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return schema{"type": "string", "contentEncoding": "base64"}
		}
		return schema{"type": "array", "items": r.Schema(t.Elem())}
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			r.fail("map key of %s is not a string", t)
		}
		return schema{"type": "object", "additionalProperties": r.Schema(t.Elem())}
	case reflect.Struct:
		if t.Name() == "" {
			return r.structSchema(t)
		}
		name := componentName(t)
		if owner, ok := r.owners[name]; ok {
			if owner != t {
				r.fail("two types are both named %s: %s and %s", name, owner, t)
			}
		} else {
			r.owners[name] = t
			r.comps[name] = schema{} // before the walk: a recursive type finds itself
			r.comps[name] = r.structSchema(t)
		}
		return schema{"$ref": "#/components/schemas/" + name}
	}
	return schema{} // interface{} and anything else: any value
}

func (r *Reflector) fail(format string, a ...any) {
	if r.err == nil {
		r.err = fmt.Errorf(format, a...)
	}
}

func (r *Reflector) structSchema(t reflect.Type) schema {
	props := schema{}
	required := []string{}
	var walk func(reflect.Type)
	walk = func(t reflect.Type) {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := f.Tag.Get("json")
			if tag == "-" || (!f.IsExported() && !f.Anonymous) {
				continue
			}
			name, opts, _ := strings.Cut(tag, ",")
			if f.Anonymous && name == "" && f.Type.Kind() == reflect.Struct {
				walk(f.Type)
				continue
			}
			if !f.IsExported() {
				continue
			}
			if name == "" {
				name = f.Name
			}
			props[name] = r.Schema(f.Type)
			if f.Tag.Get("openapi") == "nullable" { // a nil slice marshals as null
				ps := props[name].(schema)
				if f.Type.Kind() != reflect.Slice || ps["type"] != "array" {
					r.fail("%s.%s: openapi:\"nullable\" is for slice fields only", t, f.Name)
				} else {
					ps["type"] = []any{"array", "null"}
				}
			}
			if !strings.Contains(","+opts+",", ",omitempty,") && !strings.Contains(","+opts+",", ",omitzero,") {
				required = append(required, name)
			}
		}
	}
	walk(t)
	sort.Strings(required)
	out := schema{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

// Param is a path or query parameter. Type defaults to string.
type Param struct {
	Name, In, Description, Type string
	Required                    bool
}

// Operation is one GET operation.
type Operation struct {
	ID, Path, Summary string
	// Stability is the tier name, emitted as x-stability.
	Stability string
	Params    []Param
	// Response is the type of the 200 body; nil is a free-form object.
	Response reflect.Type
	// Errors are the HTTP statuses answered with the error envelope.
	Errors []int
	// Public says no bearer token is needed.
	Public bool
	// Events makes the operation a server-sent event stream: the 200 body is
	// text/event-stream and each event's data has the given type.
	Events []Event
}

// Event is one named event of a stream.
type Event struct {
	Name string
	Type reflect.Type
}

// Document describes the whole API.
type Document struct {
	Title, Description, Version string
	// Error is the envelope type of every failure; ErrorCodes its machine codes.
	Error      reflect.Type
	ErrorCodes []string
	Ops        []Operation
}

// Build renders d as indented OpenAPI 3.1 JSON.
func Build(d Document) ([]byte, error) {
	r := NewReflector()
	errRef := r.Schema(d.Error)
	paths := schema{}
	for _, op := range d.Ops {
		params := []any{}
		for _, p := range op.Params {
			typ := p.Type
			if typ == "" {
				typ = "string"
			}
			pm := schema{"name": p.Name, "in": p.In, "required": p.Required || p.In == "path", "schema": schema{"type": typ}}
			if p.Description != "" {
				pm["description"] = p.Description
			}
			params = append(params, pm)
		}
		ok := schema{"type": "object", "additionalProperties": true}
		if op.Response != nil {
			ok = r.Schema(op.Response)
		}
		content := schema{"application/json": schema{"schema": ok}}
		var events schema
		if len(op.Events) > 0 {
			events = schema{}
			for _, ev := range op.Events {
				events[ev.Name] = r.Schema(ev.Type)
			}
			content = schema{"text/event-stream": schema{"schema": schema{"type": "string"}, "x-events": events}}
		}
		responses := schema{"200": schema{"description": "OK", "content": content}}
		for _, code := range op.Errors {
			responses[fmt.Sprint(code)] = schema{"description": http.StatusText(code),
				"content": schema{"application/json": schema{"schema": errRef}}}
		}
		o := schema{"operationId": op.ID, "summary": op.Summary, "parameters": params,
			"responses": responses, "x-stability": op.Stability}
		if op.Public {
			o["security"] = []any{}
		}
		paths[op.Path] = schema{"get": o}
	}
	if r.err != nil {
		return nil, r.err
	}
	if len(d.ErrorCodes) > 0 {
		if e, ok := r.comps[componentName(derefType(d.Error))]; ok {
			if props, ok := e["properties"].(schema); ok {
				addEnum(r, props, d.ErrorCodes)
			}
		}
	}
	doc := schema{
		"openapi":  "3.1.0",
		"info":     schema{"title": d.Title, "description": d.Description, "version": d.Version},
		"paths":    paths,
		"security": []any{schema{"bearer": []any{}}},
		"components": schema{
			"securitySchemes": schema{"bearer": schema{"type": "http", "scheme": "bearer"}},
			"schemas":         r.comps,
		},
	}
	return json.MarshalIndent(doc, "", "  ")
}

func derefType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t
}

// addEnum sets the enum of the `code` property of the error object the
// envelope's `error` property points to.
func addEnum(r *Reflector, envelope schema, codes []string) {
	e, _ := envelope["error"].(schema)
	ref, _ := e["$ref"].(string)
	name := strings.TrimPrefix(ref, "#/components/schemas/")
	if c, ok := r.comps[name]; ok {
		if props, ok := c["properties"].(schema); ok {
			if code, ok := props["code"].(schema); ok {
				code["enum"] = codes
			}
		}
	}
}
