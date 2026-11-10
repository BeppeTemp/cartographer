package contract

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

func TestValidateRequires(t *testing.T) {
	many := make([]string, MaxRequires+1)
	for i := range many {
		many[i] = "skill:s" + strings.Repeat("x", i)
	}
	cases := []struct {
		name    string
		entries []string
		wantErr string // "" = accepted
	}{
		{"none", nil, ""},
		{"artifact refs", []string{"skill:a", "agent:b", "hook:c"}, ""},
		{"capabilities", []string{"agent.delegate", "hook.PreToolUse", "skill", "mcp"}, ""},
		{"absent ref is fine", []string{"skill:from-another-kb"}, ""},
		{"empty", []string{"skill:a", " "}, "empty entry"},
		{"duplicate", []string{"skill:a", "skill:a"}, "duplicated"},
		{"negated", []string{"!agent.invoke"}, "negated"},
		{"negated ref", []string{"!skill:a"}, "negated"},
		{"unknown kind", []string{"template:a"}, "unknown kind"},
		{"mcp", []string{"mcp:a"}, "mcp cannot be required"},
		{"instructions", []string{"instructions:a"}, "instructions cannot be required"},
		{"bad name", []string{"skill:../a"}, "not a valid artifact name"},
		{"no name", []string{"skill:"}, "not a valid artifact name"},
		{"self", []string{"skill:me"}, "itself"},
		{"unknown capability", []string{"agent.teleport"}, "known capability"},
		{"unknown hook event", []string{"hook.Nope"}, "known capability"},
		{"over cap", many, "at most"},
	}
	for _, c := range cases {
		err := ValidateRequires("skill", "me", c.entries)
		if c.wantErr == "" {
			if err != nil {
				t.Errorf("%s: unexpected error %v", c.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: error %v, want %q", c.name, err, c.wantErr)
		}
	}
	// A reference to another kind of the same name is not a self-reference.
	if err := ValidateRequires("skill", "me", []string{"agent:me"}); err != nil {
		t.Errorf("agent:me from skill/me: %v", err)
	}
	// Name "" skips the self check.
	if err := ValidateRequires("hook", "", []string{"hook:me"}); err != nil {
		t.Errorf("empty name: %v", err)
	}
}

func TestValidateRequires_CapabilityIDNotStatus(t *testing.T) {
	// An id absent on every client is still a valid id: the status is
	// evaluated per client, not at write time.
	for _, id := range CapabilityIDs() {
		if err := ValidateRequires("skill", "x", []string{string(id)}); err != nil {
			t.Errorf("%s: %v", id, err)
		}
	}
}

func fmOf(t *testing.T, raw string) *okf.Frontmatter {
	t.Helper()
	fm, err := okf.ParseFrontmatter(raw)
	if err != nil {
		t.Fatal(err)
	}
	return fm
}

func TestValidateFrontmatterRequires_Forms(t *testing.T) {
	ok := []string{
		"name: a\nrequires: [skill:b, agent:c]",
		"name: a\nrequires:\n  - skill:b\n  - agent.delegate",
		"name: a\nrequires: []",
		"name: a",
	}
	for _, raw := range ok {
		if err := ValidateFrontmatterRequires("skill", "a", fmOf(t, raw)); err != nil {
			t.Errorf("%q: %v", raw, err)
		}
	}
	bad := []string{
		"name: a\nrequires: skill:b",            // scalar
		"name: a\nrequires:",                    // empty value
		"name: a\nrequires:\n  x: y",            // nested block
		"name: a\nrequires: [skill:a]",          // self
		"name: a\nrequires: [skill:b, skill:b]", // duplicate
	}
	for _, raw := range bad {
		if err := ValidateFrontmatterRequires("skill", "a", fmOf(t, raw)); err == nil {
			t.Errorf("%q accepted", raw)
		}
	}
}

func TestValidateHookRequires(t *testing.T) {
	good := `{"event":"PostToolUse","command":"run.sh","requires":["skill:a","hook.PostToolUse"]}`
	if err := ValidateHookRequires("me", []byte(good)); err != nil {
		t.Fatalf("good: %v", err)
	}
	for _, bad := range []string{
		`{"event":"Stop","command":"x","requires":"skill:a"}`,
		`{"event":"Stop","command":"x","requires":[1]}`,
		`{"event":"Stop","command":"x","requires":null}`,
		`{"event":"Stop","command":"x","requires":["mcp:a"]}`,
		`{"event":"Stop","command":"x","requires":["!skill:a"]}`,
		`{"event":"Stop","command":"x","requires":["hook:me"]}`,
	} {
		if err := ValidateHookRequires("me", []byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	// An empty list is the spelling of "nothing"; null and a bare key are not.
	if err := ValidateHookRequires("me", []byte(`{"event":"Stop","command":"x","requires":[]}`)); err != nil {
		t.Errorf("empty list: %v", err)
	}
	// ValidateHookJSON stays about what stops a hook registering.
	if _, err := ValidateHookJSON([]byte(`{"event":"Stop","command":"x","requires":"oops"}`)); err != nil {
		t.Errorf("ValidateHookJSON must not judge requires: %v", err)
	}
}

// parseHookSpec stays lenient: a requires of any shape never stops a hook from
// registering on a client (the write gate is the guard, D1656).
func TestParseHookSpec_IgnoresRequires(t *testing.T) {
	for _, data := range []string{
		`{"event":"Stop","command":"x","requires":["skill:a"]}`,
		`{"event":"Stop","command":"x","requires":"oops"}`,
		`{"event":"Stop","command":"x","requires":[1,2]}`,
	} {
		spec, err := ParseHookSpec([]byte(data))
		if err != nil || spec.Event != "Stop" || spec.Command != "x" {
			t.Errorf("%s: %+v, %v", data, spec, err)
		}
	}
}

func TestMCPDescriptorRefusesRequires(t *testing.T) {
	data := []byte(`{"type":"http","url":"https://example.com/mcp","requires":["skill:a"]}`)
	if _, err := ParseMCPServerSpec("example", data); err == nil {
		t.Fatal("an mcp descriptor with requires was accepted")
	}
}

func TestRequiresOf(t *testing.T) {
	skill := Artifact{Kind: "skill", Name: "a", Files: []ArtifactFile{
		{Path: "scripts/run.sh", Content: []byte("#!/bin/sh\n")},
		{Path: "SKILL.md", Content: []byte("---\nname: a\ndescription: d\nrequires: [agent:b, agent.invoke]\n---\nbody\n")},
	}}
	agent := Artifact{Kind: "agent", Name: "b", Files: []ArtifactFile{
		{Path: "b.md", Content: []byte("---\nname: b\ndescription: d\nrequires:\n  - skill:c\n---\nbody\n")},
	}}
	hook := Artifact{Kind: "hook", Name: "h", Files: []ArtifactFile{
		{Path: "run.sh", Content: []byte("x")},
		{Path: "hook.json", Content: []byte(`{"event":"Stop","command":"run.sh","requires":["skill:a"]}`)},
	}}
	for _, c := range []struct {
		a    Artifact
		want string
	}{{skill, "agent:b,agent.invoke"}, {agent, "skill:c"}, {hook, "skill:a"}} {
		got, err := RequiresOf(c.a, nil, nil)
		if err != nil || strings.Join(got, ",") != c.want {
			t.Errorf("%s/%s: %v, %v", c.a.Kind, c.a.Name, got, err)
		}
	}
	// No key, other kinds: nothing.
	plain := Artifact{Kind: "skill", Name: "p", Files: []ArtifactFile{{Path: "SKILL.md", Content: []byte("---\nname: p\ndescription: d\n---\n")}}}
	if got, err := RequiresOf(plain, nil, nil); err != nil || got != nil {
		t.Errorf("plain: %v, %v", got, err)
	}
	if got, err := RequiresOf(Artifact{Kind: "mcp", Name: "m"}, nil, nil); err != nil || got != nil {
		t.Errorf("mcp: %v, %v", got, err)
	}
	// An invalid source is an error the caller turns into a warning.
	broken := Artifact{Kind: "skill", Name: "z", Files: []ArtifactFile{{Path: "SKILL.md", Content: []byte("---\nname: z\nrequires: oops\n---\n")}}}
	if got, err := RequiresOf(broken, nil, nil); err == nil || got != nil {
		t.Errorf("broken: %v, %v", got, err)
	}
}

// Both carriers refuse an empty value the same way (D1661).
func TestRequires_EmptyValueSameOnBothCarriers(t *testing.T) {
	if err := ValidateFrontmatterRequires("skill", "a", fmOf(t, "name: a\nrequires:")); err == nil {
		t.Error("a bare requires: in frontmatter was accepted")
	}
	if err := ValidateHookRequires("a", []byte(`{"event":"Stop","command":"x","requires":null}`)); err == nil {
		t.Error("requires: null in hook.json was accepted")
	}
}
