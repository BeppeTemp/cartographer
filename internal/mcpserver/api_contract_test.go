package mcpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

type jsonObj = map[string]any

func loadOpenAPI(t *testing.T) jsonObj {
	t.Helper()
	raw, err := OpenAPIDocument()
	if err != nil {
		t.Fatal(err)
	}
	var doc jsonObj
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the document is not JSON: %v", err)
	}
	return doc
}

func docPaths(doc jsonObj) jsonObj { p, _ := doc["paths"].(jsonObj); return p }

func routePath(rt apiRoute) string { return APIPrefix + rt.Pattern }

func liveURL(path string) string { return strings.ReplaceAll(path, "{kb}", "docs") }

// The committed copy is the reviewable diff of a shape change.
func TestOpenAPI_CommittedCopyIsCurrent(t *testing.T) {
	t.Parallel()
	want, err := OpenAPIDocument()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join("..", "..", "docs", "openapi.json"))
	if err != nil {
		t.Fatalf("docs/openapi.json: %v; run `make openapi`", err)
	}
	if string(got) != string(want)+"\n" {
		t.Fatal("docs/openapi.json is stale: run `make openapi` and commit it")
	}
}

// Contract 1 and 2: the document and the table agree, both ways, on a live handler.
func TestAPIContract_RoutesAndDocumentAgree(t *testing.T) {
	t.Parallel()
	h := newAPIHandler(t, auth.NewTokenStore(nil), false, true)
	paths := docPaths(loadOpenAPI(t))

	want := map[string]apiRoute{}
	for _, rt := range apiRoutes {
		if rt.Tier&apiPublicTiers != 0 {
			want[routePath(rt)] = rt
			continue
		}
		if _, ok := paths[routePath(rt)]; ok {
			t.Errorf("internal route %s is in the document", rt.Pattern)
		}
		if rr := getUI(t, h, liveURL(routePath(rt)), ""); rr.Code != http.StatusNotFound {
			t.Errorf("internal route %s answers %d on /api/v1, want 404", rt.Pattern, rr.Code)
		}
	}
	for p, item := range paths {
		if p == APIPrefix+apiOpenAPIPath {
			continue
		}
		rt, ok := want[p]
		if !ok {
			t.Errorf("document path %s has no public table route", p)
			continue
		}
		get, _ := item.(jsonObj)["get"].(jsonObj)
		if get == nil {
			t.Errorf("%s: no GET operation", p)
			continue
		}
		if get["x-stability"] != rt.Tier.String() {
			t.Errorf("%s: x-stability %v, table %s", p, get["x-stability"], rt.Tier)
		}
		var params []string
		for _, raw := range get["parameters"].([]any) {
			pm := raw.(jsonObj)
			params = append(params, fmt.Sprintf("%v:%v", pm["in"], pm["name"]))
		}
		var wantParams []string
		if rt.kbScoped() {
			wantParams = append(wantParams, "path:kb")
		}
		for _, q := range rt.Query {
			wantParams = append(wantParams, "query:"+q.Name)
		}
		sort.Strings(params)
		sort.Strings(wantParams)
		if strings.Join(params, ",") != strings.Join(wantParams, ",") {
			t.Errorf("%s: document parameters %v, table %v", p, params, wantParams)
		}
		if rt.Events != nil {
			delete(want, p) // a stream never ends: apistream_test.go
			continue
		}
		if rr := getUI(t, h, liveURL(p), ""); rr.Code == http.StatusNotFound {
			t.Errorf("document path %s answers 404 on a live handler", p)
		}
		delete(want, p)
	}
	for p := range want {
		t.Errorf("public route %s is missing from the document", p)
	}
	if rr := getUI(t, h, APIPrefix+apiOpenAPIPath, ""); rr.Code != http.StatusOK {
		t.Errorf("the document itself answers %d", rr.Code)
	}
}

// Contract 5: the structure the builder promises, with every $ref resolved.
func TestAPIContract_DocumentStructure(t *testing.T) {
	t.Parallel()
	doc := loadOpenAPI(t)
	if doc["openapi"] != "3.1.0" {
		t.Errorf("openapi = %v", doc["openapi"])
	}
	if info, _ := doc["info"].(jsonObj); info["version"] != "1" || info["title"] == "" {
		t.Errorf("info = %v", info)
	}
	if _, ok := doc["servers"]; ok {
		t.Error("servers must be omitted (relative)")
	}
	comps := doc["components"].(jsonObj)
	if comps["securitySchemes"].(jsonObj)["bearer"] == nil {
		t.Error("no bearer security scheme")
	}
	ids := map[string]bool{}
	for p, item := range docPaths(doc) {
		get := item.(jsonObj)["get"].(jsonObj)
		id, _ := get["operationId"].(string)
		if id == "" || ids[id] {
			t.Errorf("%s: operationId %q empty or repeated", p, id)
		}
		ids[id] = true
		if get["responses"].(jsonObj)["200"] == nil {
			t.Errorf("%s: no 200 response", p)
		}
	}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case jsonObj:
			if ref, ok := x["$ref"].(string); ok {
				name := strings.TrimPrefix(ref, "#/components/schemas/")
				if comps["schemas"].(jsonObj)[name] == nil {
					t.Errorf("dangling $ref %s", ref)
				}
			}
			for _, c := range x {
				walk(c)
			}
		case []any:
			for _, c := range x {
				walk(c)
			}
		}
	}
	walk(doc)
	codes := comps["schemas"].(jsonObj)["Error"].(jsonObj)["properties"].(jsonObj)["code"].(jsonObj)["enum"]
	if codes == nil {
		t.Error("the error codes are not enumerated")
	}
}

// Contract 3: each stable route's live body fits its schema.
func TestAPIContract_StableBodiesFitTheirSchema(t *testing.T) {
	t.Parallel()
	h := newAPIHandler(t, auth.NewTokenStore(nil), false, true)
	doc := loadOpenAPI(t)
	schemas := doc["components"].(jsonObj)["schemas"].(jsonObj)
	urls := map[string]string{
		"/kbs/{kb}/concept": "?id=visible/alpha", "/kbs/{kb}/search": "?q=alpha",
	}
	for _, rt := range apiRoutes {
		if rt.Tier != tierStable || rt.Events != nil { // a stream never ends: apistream_test.go
			continue
		}
		rr := getUI(t, h, liveURL(routePath(rt))+urls[rt.Pattern], "")
		if rr.Code != http.StatusOK {
			t.Errorf("%s: %d %s", rt.Pattern, rr.Code, rr.Body.String())
			continue
		}
		var body any
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Errorf("%s: %v", rt.Pattern, err)
			continue
		}
		op := docPaths(doc)[routePath(rt)].(jsonObj)["get"].(jsonObj)
		sch := op["responses"].(jsonObj)["200"].(jsonObj)["content"].(jsonObj)["application/json"].(jsonObj)["schema"]
		for _, problem := range checkSchema(schemas, sch, body, "$") {
			t.Errorf("%s: %s", rt.Pattern, problem)
		}
	}
}

// checkSchema validates v against the subset of JSON Schema the builder emits:
// every key declared, every required key present, types match.
func checkSchema(schemas jsonObj, sch any, v any, at string) []string {
	s, _ := sch.(jsonObj)
	if ref, ok := s["$ref"].(string); ok {
		return checkSchema(schemas, schemas[strings.TrimPrefix(ref, "#/components/schemas/")], v, at)
	}
	var out []string
	bad := func(want string) []string { return append(out, fmt.Sprintf("%s: %T is not %s", at, v, want)) }
	typ := s["type"]
	if list, ok := typ.([]any); ok { // ["array","null"]: a nullable field
		if v == nil {
			return nil
		}
		typ = list[0]
	}
	switch typ {
	case "string":
		if _, ok := v.(string); !ok {
			return bad("a string")
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return bad("a boolean")
		}
	case "integer", "number":
		if f, ok := v.(float64); !ok || (typ == "integer" && f != float64(int64(f))) {
			return bad("a " + typ.(string))
		}
	case "array":
		items, ok := v.([]any)
		if !ok {
			return bad("an array")
		}
		for i, it := range items {
			out = append(out, checkSchema(schemas, s["items"], it, fmt.Sprintf("%s[%d]", at, i))...)
		}
	case "object":
		obj, ok := v.(jsonObj)
		if !ok {
			return bad("an object")
		}
		props, _ := s["properties"].(jsonObj)
		extra := s["additionalProperties"]
		for k, val := range obj {
			switch {
			case props[k] != nil:
				out = append(out, checkSchema(schemas, props[k], val, at+"."+k)...)
			case extra == true:
			case extra != nil && extra != false:
				out = append(out, checkSchema(schemas, extra, val, at+"."+k)...)
			default:
				out = append(out, fmt.Sprintf("%s.%s is returned but not declared", at, k))
			}
		}
		req, _ := s["required"].([]any)
		for _, k := range req {
			if _, ok := obj[k.(string)]; !ok {
				out = append(out, fmt.Sprintf("%s.%v is required but absent", at, k))
			}
		}
	}
	return out
}

// A nullable field (used_by for a principal that cannot see the whole KB) is
// declared so, and the narrowed body fits the schema.
func TestAPIContract_NullableFieldFits(t *testing.T) {
	t.Parallel()
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "narrow", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"visible"}}}}},
	})
	h := newAPIHandler(t, ts, false, true)
	rr := getUI(t, h, APIPrefix+"/kbs/docs/concept?id=visible/alpha", "narrow")
	var body any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || body.(jsonObj)["used_by"] != nil {
		t.Fatalf("used_by = %v (%v), want null", body.(jsonObj)["used_by"], err)
	}
	doc := loadOpenAPI(t)
	schemas := doc["components"].(jsonObj)["schemas"].(jsonObj)
	for _, p := range checkSchema(schemas, jsonObj{"$ref": "#/components/schemas/ConceptBody"}, body, "$") {
		t.Error(p)
	}
}

// The checker itself must fail on an undeclared key, or the contract is vacuous.
func TestAPIContract_CheckerCatchesDrift(t *testing.T) {
	t.Parallel()
	sch := jsonObj{"type": "object", "properties": jsonObj{"a": jsonObj{"type": "string"}}, "required": []any{"a"}}
	if p := checkSchema(nil, sch, jsonObj{"a": "x", "b": 1.0}, "$"); len(p) != 1 || !strings.Contains(p[0], "$.b") {
		t.Errorf("undeclared key: %v", p)
	}
	if p := checkSchema(nil, sch, jsonObj{}, "$"); len(p) != 1 {
		t.Errorf("missing required key: %v", p)
	}
}

// The document is served without a token, holds no per-KB data and does not
// read the principal; every other path is behind the token; only GET/HEAD.
func TestAPIContract_DocumentIsPublicAndStatic(t *testing.T) {
	t.Parallel()
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "narrow", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"visible"}}}}},
	})
	h := newAPIHandler(t, ts, false, true)
	anon := getUI(t, h, APIPrefix+apiOpenAPIPath, "")
	named := getUI(t, h, APIPrefix+apiOpenAPIPath, "narrow")
	if anon.Code != http.StatusOK || anon.Body.String() != named.Body.String() {
		t.Errorf("anonymous %d, narrowed %d, bodies equal %v", anon.Code, named.Code, anon.Body.String() == named.Body.String())
	}
	for _, leak := range []string{"docs", "hidden", "alpha", "secret"} {
		for _, quoted := range []string{`"` + leak + `"`, "/" + leak + "/"} {
			if strings.Contains(anon.Body.String(), quoted) {
				t.Errorf("the document mentions %s", quoted)
			}
		}
	}
	if rr := getUI(t, h, APIPrefix+"/kbs", ""); rr.Code != http.StatusUnauthorized {
		t.Errorf("/kbs without a token: %d", rr.Code)
	}
	// Only read methods are exempt: an anonymous POST gets the 401 any other
	// path gives, not the 405 of the read-only mount.
	req := httptest.NewRequest(http.MethodPost, APIPrefix+apiOpenAPIPath, nil)
	rr := serve(h, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("anonymous POST: %d, want 401", rr.Code)
	}
}

func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	return rr
}
