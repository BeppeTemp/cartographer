package sops

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

var (
	initRecA = "age1" + strings.Repeat("q", 58)
	initRecB = "age1" + strings.Repeat("p", 58)
)

func TestInitRule(t *testing.T) {
	t.Parallel()
	if !ValidRecipient(initRecA) || !ValidRecipient(initRecB) {
		t.Fatal("fixtures are not valid recipients")
	}
	root := t.TempDir()
	if st, prev, err := InitRule(root, initRecA, false); err != nil || st != RuleCreated || prev != nil {
		t.Fatalf("create: %q %v %v", st, prev, err)
	}
	rec, matched, err := RuleRecipients(root, "secrets/x.sops.yaml")
	if err != nil || !matched || !slices.Equal(rec, []string{initRecA}) {
		t.Fatalf("rule read back: %v %v %v", rec, matched, err)
	}
	if st, _, err := InitRule(root, initRecA, false); err != nil || st != RuleUnchanged {
		t.Fatalf("same rule: %q %v", st, err)
	}
	before, _ := os.ReadFile(filepath.Join(root, ".sops.yaml"))
	_, prev, err := InitRule(root, initRecB, false)
	var ex *RuleExistsError
	if !errors.Is(err, ErrRuleExists) || !errors.As(err, &ex) || !slices.Equal(prev, []string{initRecA}) {
		t.Fatalf("different without force: %v %v", prev, err)
	}
	if after, _ := os.ReadFile(filepath.Join(root, ".sops.yaml")); string(after) != string(before) {
		t.Fatal("file changed without force")
	}
	if st, prev, err := InitRule(root, initRecB, true); err != nil || st != RuleReplaced || !slices.Equal(prev, []string{initRecA}) {
		t.Fatalf("force: %q %v %v", st, prev, err)
	}
	if rec, _, _ := RuleRecipients(root, "secrets/x.sops.yaml"); !slices.Equal(rec, []string{initRecB}) {
		t.Fatalf("after force: %v", rec)
	}
	if ents, _ := filepath.Glob(filepath.Join(root, ".sops.yaml.tmp-*")); len(ents) != 0 {
		t.Fatalf("temp file left: %v", ents)
	}
}

func TestInitRuleRefusals(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if _, _, err := InitRule(root, "age1nope", false); err == nil {
		t.Fatal("invalid recipient accepted")
	}
	if runtime.GOOS == "windows" {
		return
	}
	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(target, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, ".sops.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := InitRule(root, initRecA, true); err == nil {
		t.Fatal("symlinked .sops.yaml accepted, even with force")
	}
	if b, _ := os.ReadFile(target); string(b) != "keep" {
		t.Fatal("wrote through the symlink")
	}
}
