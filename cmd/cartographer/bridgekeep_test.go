package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// #1715: a sync run that cannot find `cartographer` on PATH (a timer's short
// PATH) must leave the MCP entries of OpenCode and Kiro as they are.

func keepFixture(t *testing.T) (dir, exe string, entries map[string][]mcpEntry, providers []string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("PATH lookup of an extensionless executable")
	}
	bin := t.TempDir()
	exe = filepath.Join(bin, "cartographer")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Cleanup(func() { onDiskBridgeCommand, onDiskBridgeMissing = "", "" })
	dir = t.TempDir()
	providers = []string{"opencode", "kiro"}
	entries = map[string][]mcpEntry{}
	for _, p := range providers {
		entries[p] = []mcpEntry{{Name: "cartographer", Bridge: true}}
	}
	if _, _, err := applyMCPEntries(entries, providers, dir, nil, false, "", false); err != nil {
		t.Fatal(err)
	}
	for _, p := range providers {
		if b := readProviderConfig(t, dir, p); !strings.Contains(string(b), exe) {
			t.Fatalf("%s: setup wrote no bridge entry: %s", p, b)
		}
	}
	return dir, exe, entries, providers
}

func readProviderConfig(t *testing.T, dir, provider string) []byte {
	t.Helper()
	d, _ := configurator.Lookup(configurator.Provider(provider))
	b, err := os.ReadFile(filepath.Join(dir, d.ConfigPath()))
	if err != nil {
		return nil
	}
	return b
}

// syncRound is the order sync runs the entries in: note, remove, apply.
func syncRound(t *testing.T, dir string, entries map[string][]mcpEntry, providers []string) []string {
	t.Helper()
	noteOnDiskBridgeCommand(providers, entries, dir, nil)
	if _, _, err := removeMCPEntries("cartographer", []string{"kb-a"}, providersToClear(providers, entries), dir, nil, false, "", false, nil); err != nil {
		t.Fatal(err)
	}
	_, warnings, err := applyMCPEntries(entries, providers, dir, nil, false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	return warnings
}

func TestSyncWithoutPATHKeepsTheEntriesOnTheOnDiskCommand(t *testing.T) {
	dir, exe, entries, providers := keepFixture(t)
	before := map[string]string{}
	for _, p := range providers {
		before[p] = string(readProviderConfig(t, dir, p))
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := bridgeCommand(); err == nil {
		t.Fatal("setup: cartographer is still on PATH")
	}
	noteOnDiskBridgeCommand(providers, entries, dir, nil)
	if got, err := bridgeCommand(); err != nil || got != exe {
		t.Fatalf("bridgeCommand = %q, %v; want the on-disk command %q", got, err, exe)
	}
	if w := syncRound(t, dir, entries, providers); len(w) != 0 {
		t.Errorf("warnings = %v", w)
	}
	for _, p := range providers {
		if got := string(readProviderConfig(t, dir, p)); got != before[p] {
			t.Errorf("%s: config changed:\n%s\nwas:\n%s", p, got, before[p])
		}
	}
}

func TestSyncWithoutPATHAndWithoutAUsableCommandKeepsTheEntries(t *testing.T) {
	dir, exe, entries, providers := keepFixture(t)
	before := map[string]string{}
	for _, p := range providers {
		before[p] = string(readProviderConfig(t, dir, p))
	}
	if err := os.Remove(exe); err != nil { // the binary moved: no usable command anywhere
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	warnings := syncRound(t, dir, entries, providers)
	if len(warnings) != 2 || !strings.Contains(warnings[0], "kept") {
		t.Errorf("warnings = %q, want one per provider saying the entry is kept", warnings)
	}
	for _, w := range warnings {
		if !strings.Contains(w, "the kept command "+exe+" no longer exists") {
			t.Errorf("warning %q does not say the kept command %s is missing", w, exe)
		}
	}
	for _, p := range providers {
		if got := string(readProviderConfig(t, dir, p)); got != before[p] {
			t.Errorf("%s: entry dropped or rewritten:\n%s\nwas:\n%s", p, got, before[p])
		}
	}
}

func TestOnDiskBridgeCommandRejectsACellarPath(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"Cellar/cartographer/0.27.3/bin", "bin"} {
		d := filepath.Join(root, rel)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "cartographer"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if usableBridgeFile(filepath.Join(root, "Cellar/cartographer/0.27.3/bin/cartographer")) {
		t.Error("a versioned Cellar path was accepted (D737)")
	}
	if !usableBridgeFile(filepath.Join(root, "bin/cartographer")) {
		t.Error("an existing executable was rejected")
	}
	if usableBridgeFile(filepath.Join(root, "bin/missing")) || usableBridgeFile("cartographer") {
		t.Error("a missing or relative command was accepted")
	}
}
