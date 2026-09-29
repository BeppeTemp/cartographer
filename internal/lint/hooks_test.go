package lint

import (
	"strings"
	"testing"
)

func TestLint_HookInvalid(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "hooks/good/hook.json", `{"event":"Stop","command":"./run.sh"}`)
	writeFile(t, k.Root, "hooks/typo/hook.json", `{"event":"PostTooluse","command":"./run.sh"}`)
	writeFile(t, k.Root, "hooks/broken/hook.json", `{"event":`)
	writeFile(t, k.Root, "hooks/empty/run.sh", "#!/bin/sh\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range findings {
		if f.Check == "hook_invalid" {
			got[f.Path] = f.Message
			if f.Severity != SevWarning {
				t.Errorf("%s: severity %s, want warning", f.Path, f.Severity)
			}
		}
	}
	if len(got) != 3 {
		t.Fatalf("want 3 hook_invalid findings, got %v", got)
	}
	if _, ok := got["hooks/good/hook.json"]; ok {
		t.Error("a valid hook must not be reported")
	}
	if !strings.Contains(got["hooks/typo/hook.json"], "PostTooluse") {
		t.Errorf("typo message should name the event: %s", got["hooks/typo/hook.json"])
	}
}

func TestLint_HookInvalidNotInScopedRun(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "hooks/broken/hook.json", `{"event":`)
	findings, err := Run(k, "some/scope", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Check == "hook_invalid" {
			t.Errorf("a scoped lint must not report KB-level hook findings: %v", f)
		}
	}
}
