package lint

import (
	"strings"
	"testing"
)

// D276: the glossary's forbidden_term check.

const lintGlossary = "terms:\n  - canonical: Zephyr Controller\n    aliases: [zephyrctl]\n    forbidden: [ZephyrController, zctl-old]\n"

func TestRun_ForbiddenTerm(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "glossary.yaml", lintGlossary)
	writeFile(t, k.DataRoot(), "arch/prose.md", "---\ntype: Note\ntitle: Prose\n---\nRestart the zephyrcontroller, then the ZephyrController again. zctl-old too.\n")
	writeFile(t, k.DataRoot(), "arch/code.md", "---\ntype: Note\ntitle: Code\n---\nRun `ZephyrController --restart`.\n\n```\nZephyrController status\n```\n")
	writeFile(t, k.DataRoot(), "arch/clean.md", "---\ntype: Note\ntitle: Clean\n---\nThe Zephyr Controller, a.k.a. zephyrctl. Not MyZephyrControllerX.\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	got := findingsOf(findings, "forbidden_term")
	if len(got) != 2 {
		t.Fatalf("forbidden_term = %+v, want two findings on arch/prose.md", got)
	}
	for _, f := range got {
		if f.Path != "arch/prose.md" || f.Severity != SevWarning {
			t.Errorf("finding %+v", f)
		}
	}
	if !strings.Contains(got[0].Message, `uses "ZephyrController" — the glossary's canonical term is "Zephyr Controller"`) {
		t.Errorf("message = %q", got[0].Message)
	}
}

func TestRun_ForbiddenTerm_Suppressible(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "glossary.yaml", lintGlossary)
	writeFile(t, k.DataRoot(), "arch/migration.md", "---\ntype: Note\ntitle: Migration\nlint_ignore: [forbidden_term]\n---\nZephyrController was renamed.\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := findingsOf(findings, "forbidden_term"); len(got) != 0 {
		t.Errorf("lint_ignore did not silence it: %v", got)
	}
	if got := findingsOf(findings, "lint_ignore_invalid"); len(got) != 0 {
		t.Errorf("forbidden_term must be a valid lint_ignore name: %v", got)
	}
}

func TestRun_GlossaryMalformed(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "glossary.yaml", lintGlossary+"  - canonical: Other\n    aliases: [zephyrctl]\n")
	writeFile(t, k.DataRoot(), "arch/a.md", "---\ntype: Note\ntitle: A\n---\nZephyrController\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	var onGlossary []Finding
	for _, f := range findingsOf(findings, "contract_malformed") {
		if f.Path == "glossary.yaml" {
			onGlossary = append(onGlossary, f)
		}
	}
	if len(onGlossary) != 1 || !strings.Contains(onGlossary[0].Message, `"terms[1]"`) {
		t.Fatalf("contract_malformed on glossary.yaml = %+v", onGlossary)
	}
	// The good term still applies.
	if got := findingsOf(findings, "forbidden_term"); len(got) != 1 {
		t.Fatalf("forbidden_term = %+v", got)
	}
}

func TestRun_NoGlossary_NoForbiddenTerm(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/a.md", "---\ntype: Note\ntitle: A\n---\nZephyrController\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := findingsOf(findings, "forbidden_term"); len(got) != 0 {
		t.Errorf("forbidden_term without a glossary: %v", got)
	}
}
