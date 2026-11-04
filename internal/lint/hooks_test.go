package lint

import (
	"strings"
	"testing"
)

func TestLint_HookInvalid(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

// A bad `requires` is reported as an ignored declaration on a hook that still
// registers, never as a skipped hook; a self-reference is caught with the
// folder name, as artifact_write does (D1661).
func TestLint_HookInvalidRequires(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.Root, "hooks/ok/hook.json", `{"event":"Stop","command":"./run.sh","requires":["skill:a"]}`)
	writeFile(t, k.Root, "hooks/bad/hook.json", `{"event":"Stop","command":"./run.sh","requires":"skill:a"}`)
	writeFile(t, k.Root, "hooks/self/hook.json", `{"event":"Stop","command":"./run.sh","requires":["hook:self"]}`)
	writeFile(t, k.Root, "hooks/null/hook.json", `{"event":"Stop","command":"./run.sh","requires":null}`)

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range findings {
		if f.Check == "hook_invalid" {
			got[f.Path] = f.Message
		}
	}
	if len(got) != 3 {
		t.Fatalf("want 3 hook_invalid findings (bad, self, null), got %v", got)
	}
	if _, ok := got["hooks/ok/hook.json"]; ok {
		t.Error("a valid requires must not be reported")
	}
	for _, p := range []string{"hooks/bad/hook.json", "hooks/self/hook.json", "hooks/null/hook.json"} {
		m := got[p]
		if !strings.Contains(m, "invalid requires") || !strings.Contains(m, "still registered") || strings.Contains(m, "would be skipped") {
			t.Errorf("%s: message must say the hook still registers: %q", p, m)
		}
	}
	if !strings.Contains(got["hooks/self/hook.json"], "itself") {
		t.Errorf("self-reference: %q", got["hooks/self/hook.json"])
	}
}
