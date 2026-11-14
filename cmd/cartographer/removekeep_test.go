package main

import (
	"os"
	"strings"
	"testing"
)

// #2102: sync removes a client's managed entries and then applies the emitted
// ones. A name about to be re-emitted must not be removed first, or D2056's
// "keep the on-disk shape when the OpenCode version is unreadable" has no
// entry to keep and the native default is written.
func TestRemoveThenApply_OpenCodeKeepsTheOnDiskShape(t *testing.T) {
	const flat = `{"mcp":{"cartographer":{"type":"remote","url":"https://old.example.com/mcp","enabled":true}}}`
	const native = `{"mcp":{"servers":{"cartographer":{"type":"remote","url":"https://old.example.com/mcp"}}}}`
	cases := []struct {
		name, start string
		major       int
		wantFlat    bool // the entry is at mcp.cartographer after the run
		wantNative  bool // the entry is at mcp.servers.cartographer after the run
	}{
		{"unreadable version keeps flat", flat, 0, true, false},
		{"unreadable version keeps native", native, 0, false, true},
		{"unreadable version, no entry, writes native", `{}`, 0, false, true},
		{"major 1 writes flat", flat, 1, true, false},
		{"major 1 converts native to flat", native, 1, true, false},
		{"major 2 writes native", native, 2, false, true},
		{"major 2 converts flat to native", flat, 2, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			restore := openCodeMajorForEmit
			openCodeMajorForEmit = func() int { return tc.major }
			t.Cleanup(func() { openCodeMajorForEmit = restore })
			dir := t.TempDir()
			path := openCodeConfig(t, dir)
			if err := os.WriteFile(path, []byte(tc.start), 0o644); err != nil {
				t.Fatal(err)
			}
			entries := map[string][]mcpEntry{"opencode": {{Name: "cartographer", URL: "https://new.example.com/mcp"}}}
			if _, _, err := removeMCPEntries("cartographer", nil, []string{"opencode"}, dir, nil, false, "", false, entries); err != nil {
				t.Fatalf("removeMCPEntries: %v", err)
			}
			if _, _, err := applyMCPEntries(entries, []string{"opencode"}, dir, nil, false, "", false); err != nil {
				t.Fatalf("applyMCPEntries: %v", err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			got := string(data)
			if !strings.Contains(got, "new.example.com") || strings.Contains(got, "old.example.com") {
				t.Fatalf("entry not refreshed in place: %s", got)
			}
			hasNative := strings.Contains(got, `"servers"`)
			if hasNative != tc.wantNative || (!hasNative) != tc.wantFlat {
				t.Errorf("shape: native=%v, want native=%v flat=%v: %s", hasNative, tc.wantNative, tc.wantFlat, got)
			}
		})
	}
}

// A name that is no longer emitted is still removed, and one that is emitted
// is not touched by the removal.
func TestRemoveMCPEntries_SkipsOnlyTheReemittedNames(t *testing.T) {
	dir := t.TempDir()
	path := openCodeConfig(t, dir)
	const start = `{"mcp":{"wiki":{"type":"remote","url":"https://x.example.com/mcp"},"wiki-kb-a":{"type":"remote","url":"https://x.example.com/mcp?kbs=kb-a"}}}`
	if err := os.WriteFile(path, []byte(start), 0o644); err != nil {
		t.Fatal(err)
	}
	reemit := map[string][]mcpEntry{"opencode": {{Name: "wiki"}}}
	names, _, err := removeMCPEntries("wiki", []string{"kb-a"}, []string{"opencode"}, dir, nil, false, "", false, reemit)
	if err != nil {
		t.Fatal(err)
	}
	if len(names["opencode"]) != 1 || names["opencode"][0] != "wiki-kb-a" {
		t.Errorf("removed = %v, want only wiki-kb-a", names["opencode"])
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"wiki"`) || strings.Contains(string(data), "wiki-kb-a") {
		t.Errorf("config after removal: %s", data)
	}
}
