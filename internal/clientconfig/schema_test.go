package clientconfig

import (
	"reflect"
	"strings"
	"testing"
)

func structPaths(t reflect.Type, prefix string, out *[]string) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := strings.Split(f.Tag.Get("yaml"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		// Every key is a leaf, described by one entry: the maps and lists of
		// structs are not descended into.
		*out = append(*out, prefix+tag)
	}
}

// The schema documents the file, so a key the file accepts without an entry
// here would go undocumented, and an entry for a key that is gone would
// document nothing. Adding a YAML key starts in schema.go.
func TestSchemaMatchesYAMLStruct(t *testing.T) {
	var want []string
	structPaths(reflect.TypeOf(yamlConfig{}), "", &want)
	have := map[string]bool{}
	for _, k := range Schema.Keys {
		have[k.Path] = true
	}
	real := map[string]bool{}
	for _, p := range want {
		real[p] = true
		if !have[p] {
			t.Errorf("YAML key %s is in the client file but has no entry in Schema (internal/clientconfig/schema.go)", p)
		}
	}
	for p := range have {
		if !real[p] {
			t.Errorf("Schema entry %s maps to no YAML key", p)
		}
	}
	// The caches are not settings: they must never come back into the user file.
	for _, cache := range []string{"known_kbs", "server_mount_mode", "server_routed_path", "agents", "sync_timer_opt_out"} {
		if have[cache] || real[cache] {
			t.Errorf("%s is a server-owned cache; it belongs to the state file", cache)
		}
	}
}

func TestSchemaEntriesAreComplete(t *testing.T) {
	for _, k := range Schema.Keys {
		if strings.TrimSpace(k.Description) == "" || strings.TrimSpace(k.Default) == "" || k.Type == "" {
			t.Errorf("%s: description, default and type are required", k.Path)
		}
		if first := k.Description[0]; first < 'A' || first > 'Z' {
			t.Errorf("%s: a description starts with a capital, or it reads as a commented-out key", k.Path)
		}
	}
}
