package config

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/settingschema"
)

// yamlPaths lists the YAML keys reachable from t. A slice of KBSpec is the one
// list of mappings the schema descends into (as "kbs[]"); every other slice or
// map is a leaf, described by one entry.
func yamlPaths(t reflect.Type, prefix string, out *[]string) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := strings.Split(f.Tag.Get("yaml"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		path := prefix + tag
		*out = append(*out, path)
		ft := f.Type
		for ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		switch {
		case ft.Kind() == reflect.Struct:
			yamlPaths(ft, path+".", out)
		case ft.Kind() == reflect.Slice && ft.Elem() == reflect.TypeOf(rawKB{}):
			yamlPaths(ft.Elem(), path+"[].", out)
		}
	}
}

// groups are mappings that only hold other keys: they need no entry of their own.
var groups = map[string]bool{"auth": true, "git": true, "audit": true, "sops": true, "tools": true, "mcp": true, "web": true}

// The schema is the source of the comments written into config files, so a key
// the parser accepts without an entry here is a key that goes undocumented.
func TestSchemaMatchesStructs(t *testing.T) {
	var want []string
	yamlPaths(reflect.TypeOf(rawConfig{}), "", &want)
	have := map[string]bool{}
	for _, k := range Schema.Keys {
		if have[k.Path] {
			t.Errorf("schema: %s is declared twice", k.Path)
		}
		have[k.Path] = true
	}
	real := map[string]bool{}
	for _, p := range want {
		if groups[p] {
			continue
		}
		real[p] = true
		if !have[p] {
			t.Errorf("YAML key %s is accepted by the parser but has no entry in Schema (internal/config/schema.go)", p)
		}
	}
	for p := range have {
		if !real[p] {
			t.Errorf("Schema entry %s maps to no YAML key of rawConfig/KBSpec", p)
		}
	}
}

func TestSchemaEntriesAreComplete(t *testing.T) {
	for _, k := range Schema.Keys {
		if strings.TrimSpace(k.Description) == "" {
			t.Errorf("%s: empty description", k.Path)
		}
		if strings.TrimSpace(k.Default) == "" {
			t.Errorf("%s: empty default (write \"\" for an empty string)", k.Path)
		}
		if k.Type == "" || k.Scope == "" {
			t.Errorf("%s: type and scope are required", k.Path)
		}
		if first := k.Description[0]; first < 'A' || first > 'Z' {
			t.Errorf("%s: a description starts with a capital, or it reads as a commented-out key", k.Path)
		}
	}
}

// The KB keys are the ones a KB carries itself (D746) and only there (D906):
// this pins the list so adding or moving a key is a decision, not a side effect.
// The server schema holds none of them.
func TestSchemaKBScope(t *testing.T) {
	for _, k := range Schema.Keys {
		if k.Scope == settingschema.ScopeKB {
			t.Errorf("%s is a KB key in the server schema (D906)", k.Path)
		}
	}
	var got []string
	for _, k := range KBKeys {
		if k.Scope != settingschema.ScopeKB || strings.HasPrefix(k.Path, "kbs[]") {
			t.Errorf("%s: scope %s", k.Path, k.Scope)
		}
		if strings.TrimSpace(k.Description) == "" || strings.TrimSpace(k.Default) == "" {
			t.Errorf("%s: description and default are required", k.Path)
		}
		got = append(got, k.Path)
	}
	want := []string{"usage_stale_days", "branch"}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("KB keys = %v, want %v", got, want)
	}
}

// The committed config.example.yaml is generated from the schema: edit the
// schema and run `make settings-example`.
func TestExampleIsUpToDate(t *testing.T) {
	got, err := Schema.Example(ExampleHeader)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("..", "..", "config.example.yaml")
	if os.Getenv("SETTINGS_EXAMPLE_UPDATE") != "" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(got) {
		t.Error("config.example.yaml differs from the schema: run `make settings-example`")
	}
}

// Comments are additive: the commented example loads as the defaults plus the
// values it sets explicitly.
func TestExampleLoadsAsDefaultsPlusExplicitValues(t *testing.T) {
	data, err := Schema.Example(ExampleHeader)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatalf("Load(example): %v", err)
	}
	want := Default()
	want.HTTP = ":39273"
	want.Data = "/data"
	want.CredentialsDir = "/etc/cartographer/credentials"
	want.KBs = []KBSpec{{Remote: "https://git.example.com/user/kb-a.git", Name: "kb-a"}}
	want.GitIdentity = "Ada Example <ada@example.com>"
	want.Origins = got.Origins // origins are covered in origin_test.go
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Load(example) = %+v\nwant %+v", got, want)
	}
}

// A file with the comments stripped behaves as the commented one.
func TestFormattedFileLoadsLikeTheOriginal(t *testing.T) {
	src := "http: \":8080\"\nkbs:\n  - remote: https://git.example.com/user/kb-a.git\n    path: /data/kb-a\n"
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.yaml")
	if err := os.WriteFile(plain, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := Schema.Format([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	commented := filepath.Join(dir, "commented.yaml")
	if err := os.WriteFile(commented, out, 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := Load(plain)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Load(commented)
	if err != nil {
		t.Fatalf("Load(commented): %v\n%s", err, out)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("commented file loads differently:\n%+v\n%+v", a, b)
	}
	again, err := Schema.Format(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(out) {
		t.Errorf("formatting is not idempotent:\n%s\n---\n%s", out, again)
	}
}
