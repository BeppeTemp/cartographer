package lint

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

func skillMD(name, desc, body string) string {
	return "---\nname: " + name + "\ndescription: " + desc + "\n---\n" + body
}

func TestLint_SkillInvalid(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "skills/good/SKILL.md", skillMD("good", "Does good things", "# Good\n"))
	writeFile(t, k.Root, "skills/mismatch/SKILL.md", skillMD("other-name", "Mismatched", "# M\n"))
	writeFile(t, k.Root, "skills/empty/run.sh", "#!/bin/sh\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	got := findingsOf(findings, "skill_invalid")
	if len(got) != 2 {
		t.Fatalf("want 2 skill_invalid, got %v", got)
	}
	paths := map[string]bool{}
	for _, f := range got {
		paths[f.Path] = true
		if f.Severity != SevWarning || !f.Artifact {
			t.Errorf("%s: severity %s artifact %v", f.Path, f.Severity, f.Artifact)
		}
	}
	if !paths["skills/mismatch/SKILL.md"] || !paths["skills/empty"] {
		t.Errorf("paths = %v", paths)
	}
}

func TestLint_SkillWarning(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "skills/long/SKILL.md", skillMD("long", "Long one", strings.Repeat("line\n", 600)))
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := findingsOf(findings, "skill_warning"); len(got) != 1 || got[0].Severity != SevInfo {
		t.Fatalf("want 1 info skill_warning, got %v", got)
	}
}

func TestLint_SkillNotInScopedRun(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "skills/mismatch/SKILL.md", skillMD("other", "x", "`git commit -m x` and kb_a__search\n"))
	findings, err := Run(k, "some/scope", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if artifactChecks[f.Check] {
			t.Errorf("a scoped lint must not report artifact findings: %v", f)
		}
	}
}

func TestLint_LegacyToolName(t *testing.T) {
	k := tempKB(t)
	k.AuthName = "kb-a"
	writeFile(t, k.Root, "skills/old/SKILL.md", skillMD("old", "Old", "Call `kb_a__search` then kb_a__search again.\n"))
	writeFile(t, k.Root, "skills/new/SKILL.md", skillMD("new", "New", "Call `search(kb: \"kb-a\")` and concept_read.\n"))
	writeFile(t, k.Root, "skills/new/style.css", ".artifacts__list { color: red }\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	got := findingsOf(findings, "legacy_tool_name")
	if len(got) != 1 {
		t.Fatalf("want 1 legacy_tool_name, got %v", got)
	}
	f := got[0]
	if f.Path != "skills/old/SKILL.md" || !strings.Contains(f.Message, `"kb_a__search"`) || !strings.Contains(f.Message, `kb: "kb-a"`) {
		t.Errorf("finding = %+v", f)
	}
	if f.Fix == nil || f.Fix.Kind != FixStripToolPrefix || f.Fix.Field != "kb_a__search" || f.Fix.To != "search" {
		t.Errorf("fix = %+v", f.Fix)
	}
}

func TestLint_LegacyToolNameInInstructions(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "instructions.md", "Use work_kb__lint before closing.\n")
	writeFile(t, k.Root, "agents/helper.md", "---\nname: helper\ndescription: h\n---\nRun work_kb__search.\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !hasCheck(findings, "instructions.md", "legacy_tool_name") || !hasCheck(findings, "agents/helper.md", "legacy_tool_name") {
		t.Fatalf("want legacy_tool_name on instructions.md and the agent, got %v", findingsOf(findings, "legacy_tool_name"))
	}
}

func TestLint_SkillBrokenRef(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "skills/broken/SKILL.md", skillMD("broken", "b", "Run:\n\n```\npython3 tools/check.py\n```\n"))
	writeFile(t, k.Root, "skills/my-skill/SKILL.md", skillMD("my-skill", "m", "Run `skills/my-skill/run.sh`, then `scripts/helper.sh` and `/opt/tools/x`.\n"))
	writeFile(t, k.Root, "skills/my-skill/run.sh", "#!/bin/sh\n")
	writeFile(t, k.Root, "skills/my-skill/scripts/helper.sh", "#!/bin/sh\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	got := findingsOf(findings, "skill_broken_ref")
	if len(got) != 1 || got[0].Path != "skills/broken/SKILL.md" || !strings.Contains(got[0].Message, "tools/check.py") {
		t.Fatalf("want 1 skill_broken_ref on tools/check.py, got %v", got)
	}
}

func TestLint_MissingInstructions(t *testing.T) {
	small := tempKB(t)
	for i := 0; i < 3; i++ {
		writeFile(t, small.Root, fmt.Sprintf("data/ops/c%d.md", i), "---\ntype: Note\ntitle: C\n---\n# C\n")
	}
	big := tempKB(t)
	for i := 0; i < 15; i++ {
		writeFile(t, big.Root, fmt.Sprintf("data/ops/c%d.md", i), "---\ntype: Note\ntitle: C\n---\n# C\n")
	}
	for name, tc := range map[string]struct {
		k    *kb.KB
		want int
	}{"small": {small, 0}, "big": {big, 1}} {
		k := tc.k
		findings, err := Run(k, "", false)
		if err != nil {
			t.Fatal(err)
		}
		if got := findingsOf(findings, "missing_instructions"); len(got) != tc.want {
			t.Errorf("%s: want %d missing_instructions, got %v", name, tc.want, got)
		}
	}
}

func TestLint_MissingInstructionsPresent(t *testing.T) {
	k := tempKB(t)
	for i := 0; i < 15; i++ {
		writeFile(t, k.Root, fmt.Sprintf("data/ops/c%d.md", i), "---\ntype: Note\ntitle: C\n---\n# C\n")
	}
	writeFile(t, k.Root, "instructions.md", "Route ops questions here.\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := findingsOf(findings, "missing_instructions"); len(got) != 0 {
		t.Errorf("want 0, got %v", got)
	}
}

func TestLint_JunkFile(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, ".DS_Store", "x")
	writeFile(t, k.Root, "data/ops/dossier/index.md", "---\ntype: Note\ntitle: D\n---\n# D\n")
	writeFile(t, k.Root, "data/ops/dossier/__pycache__/mod.pyc", "x")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !hasCheck(findings, ".DS_Store", "junk_file") {
		t.Errorf("want junk_file on .DS_Store, got %v", findingsOf(findings, "junk_file"))
	}
	asset := "ops/dossier/__pycache__/mod.pyc"
	if !hasCheck(findings, asset, "junk_asset") {
		t.Errorf("want junk_asset on %s, got %v", asset, findingsOf(findings, "junk_asset"))
	}
	if hasCheck(findings, asset, "orphan_asset") {
		t.Error("a junk asset must not also be an orphan_asset")
	}
	if len(findingsOf(findings, "junk_file")) != 1 {
		t.Errorf("the junk asset must not be reported twice: %v", findingsOf(findings, "junk_file"))
	}
}

func TestLint_JunkIgnoredByDataGitignore(t *testing.T) {
	k := tempKB(t)
	// data/.gitignore (kb.Init) keeps this out of git: nothing to report.
	writeFile(t, k.Root, "data/ops/.DS_Store", "x")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := findingsOf(findings, "junk_file"); len(got) != 0 {
		t.Errorf("an ignored file is not tracked: %v", got)
	}
}

func TestLint_SkillGitCommand(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "skills/committer/SKILL.md", skillMD("committer", "c", "Then `git commit -m \"save\"`.\n"))
	writeFile(t, k.Root, "skills/cloner/SKILL.md", skillMD("cloner", "c", "```\ngit clone <url>\ngit status\n```\n"))
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	got := findingsOf(findings, "skill_git_command")
	if len(got) != 1 || got[0].Path != "skills/committer/SKILL.md" || got[0].Severity != SevInfo || !strings.Contains(got[0].Message, "git commit") {
		t.Fatalf("want 1 skill_git_command on committer, got %v", got)
	}
}

func TestLint_SopsFormatMismatch(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "data/ops/bad.md", "---\ntype: Note\ntitle: B\n---\n# B\n\n`sops decrypt secrets/db.sops.yaml | jq .password`\n")
	writeFile(t, k.Root, "data/ops/good.md", "---\ntype: Note\ntitle: G\n---\n# G\n\n`sops decrypt --output-type json secrets/db.sops.yaml | jq .password`\n\n`sops decrypt --output-type yaml secrets/db.sops.yaml`\n")
	writeFile(t, k.Root, "skills/vault/SKILL.md", skillMD("vault", "v", "```\nsops -d secrets/x.yaml | python3 -c 'import json,sys; json.load(sys.stdin)'\n```\n"))
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !hasCheck(findings, "ops/bad.md", "sops_format_mismatch") {
		t.Errorf("want sops_format_mismatch on ops/bad.md: %v", findingsOf(findings, "sops_format_mismatch"))
	}
	if hasCheck(findings, "ops/good.md", "sops_format_mismatch") {
		t.Error("--output-type json (or no JSON consumer) must not be reported")
	}
	if !hasCheck(findings, "skills/vault/SKILL.md", "sops_format_mismatch") {
		t.Errorf("want sops_format_mismatch on the skill: %v", findingsOf(findings, "sops_format_mismatch"))
	}
	// Dismissible on the concept that shows the wrong way on purpose.
	writeFile(t, k.Root, "data/ops/bad.md", "---\ntype: Note\ntitle: B\nlint_ignore: [sops_format_mismatch]\n---\n# B\n\n`sops decrypt secrets/db.sops.yaml | jq .password`\n")
	findings, _ = Run(k, "", false)
	if hasCheck(findings, "ops/bad.md", "sops_format_mismatch") {
		t.Error("lint_ignore must suppress sops_format_mismatch")
	}
}

func TestLint_SopsMissingFile(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "secrets/real.sops.yaml", "enc")
	writeFile(t, k.Root, "data/ops/missing.md", "---\ntype: Note\ntitle: M\n---\n# M\n\n`sops decrypt secrets/nonexistent.sops.yaml`\n")
	writeFile(t, k.Root, "data/ops/real.md", "---\ntype: Note\ntitle: R\n---\n# R\n\n`sops decrypt --output-type json secrets/real.sops.yaml | jq .`\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := findingsOf(findings, "sops_missing_file"); len(got) != 1 || got[0].Path != "ops/missing.md" {
		t.Fatalf("want 1 sops_missing_file on ops/missing.md, got %v", got)
	}
}

func TestLint_SopsNoSecretsDir(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "data/ops/missing.md", "---\ntype: Note\ntitle: M\n---\n# M\n\n`sops decrypt secrets/nonexistent.sops.yaml`\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := findingsOf(findings, "sops_missing_file"); len(got) != 0 {
		t.Errorf("without secrets/ the cited-file check does not run: %v", got)
	}
}

func TestLint_CrossKBPath(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "skills/helper/SKILL.md", skillMD("helper", "h", "Read /tmp/test-kbs/kb-b/skills/helper/SKILL.md first.\n"))
	findings, err := RunWithOptions(k, "", false, Options{CrossKBRoots: map[string]string{"kb-b": "/tmp/test-kbs/kb-b"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := findingsOf(findings, "cross_kb_path"); len(got) != 1 || got[0].Path != "skills/helper/SKILL.md" {
		t.Fatalf("want 1 cross_kb_path, got %v", got)
	}
	// A sibling whose name extends this one is a different KB.
	findings, _ = RunWithOptions(k, "", false, Options{CrossKBRoots: map[string]string{"kb": "/tmp/test-kbs/kb"}})
	if got := findingsOf(findings, "cross_kb_path"); len(got) != 0 {
		t.Errorf("a longer root must not match a shorter sibling: %v", got)
	}
	findings, _ = Run(k, "", false)
	if got := findingsOf(findings, "cross_kb_path"); len(got) != 0 {
		t.Errorf("without siblings: %v", got)
	}
	// Run reads the siblings the server injected on the KB.
	k.SiblingRoots = map[string]string{"kb-b": "/tmp/test-kbs/kb-b"}
	findings, _ = Run(k, "", false)
	if got := findingsOf(findings, "cross_kb_path"); len(got) != 1 {
		t.Errorf("Run must use k.SiblingRoots: %v", got)
	}
}

func TestLint_CrossKBPath_OwnRoot_NoFinding(t *testing.T) {
	k := tempKB(t)
	own := filepath.ToSlash(k.Root)
	writeFile(t, k.Root, "skills/helper/SKILL.md", skillMD("helper", "h", "Read "+own+"/skills/helper/run.sh.\n"))
	findings, err := RunWithOptions(k, "", false, Options{CrossKBRoots: map[string]string{"kb-b": "/tmp/test-kbs/kb-b"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := findingsOf(findings, "cross_kb_path"); len(got) != 0 {
		t.Errorf("the KB's own root is not a cross-KB path: %v", got)
	}
}

func TestLint_SkillMissingPerimeter(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "instructions.md", "---\nperimeter: ops\n---\nOps KB.\n")
	writeFile(t, k.Root, "skills/build/SKILL.md", skillMD("build", "Run the build", "# B\n"))
	writeFile(t, k.Root, "skills/runner/SKILL.md", skillMD("runner", "Ops build runner", "# R\n"))
	writeFile(t, k.Root, "skills/devops/SKILL.md", skillMD("devops", "DevOps chores", "# D\n"))
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	got := findingsOf(findings, "skill_missing_perimeter")
	if len(got) != 1 || got[0].Path != "skills/build/SKILL.md" || got[0].Severity != SevInfo {
		t.Fatalf("want 1 skill_missing_perimeter on build, got %v", got)
	}
}

func TestLint_SkillMissingPerimeter_NoPerimeter(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "instructions.md", "---\ntitle: x\n---\nNo perimeter.\n")
	writeFile(t, k.Root, "skills/build/SKILL.md", skillMD("build", "Run the build", "# B\n"))
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := findingsOf(findings, "skill_missing_perimeter"); len(got) != 0 {
		t.Errorf("no perimeter declared, no finding: %v", got)
	}
}

func TestLint_LegacyPath(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "instructions.md", "---\nlegacy_paths:\n  \"wiki/\": \"old/\"\n  \"wiki/ops/\": \"ops/\"\n---\nKB.\n")
	writeFile(t, k.Root, "data/ops/a.md", "---\ntype: Note\ntitle: A\n---\n# A\n\nSee wiki/ops/runbook-x and wiki/ops/runbook-y.\n")
	writeFile(t, k.Root, "data/ops/b.md", "---\ntype: Note\ntitle: B\n---\n# B\n\nNothing old.\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	got := findingsOf(findings, "legacy_path")
	// One per declared prefix found, never per occurrence: "wiki/ops/" and
	// the shorter "wiki/" it contains.
	if len(got) != 2 {
		t.Fatalf("want 2 legacy_path on ops/a.md, got %v", got)
	}
	for _, f := range got {
		if f.Path != "ops/a.md" || f.Fix == nil || f.Fix.Kind != FixReplacePrefix {
			t.Errorf("finding = %+v", f)
		}
	}
	if hasCheck(findings, "ops/b.md", "legacy_path") {
		t.Error("a body without the prefix is clean")
	}
}

func TestLint_LegacyPath_NoMapping(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "data/ops/a.md", "---\ntype: Note\ntitle: A\n---\n# A\n\nSee wiki/ops/runbook-x.\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := findingsOf(findings, "legacy_path"); len(got) != 0 {
		t.Errorf("no mapping, no finding: %v", got)
	}
}

func TestParseLegacyPaths_LongestFirst(t *testing.T) {
	got := parseLegacyPaths("  \"wiki/\": \"\"\n  'wiki/ops/': 'ops/'\n  plain/: x/\n")
	want := []legacyPath{{"wiki/ops/", "ops/"}, {"plain/", "x/"}, {"wiki/", ""}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestLint_ArtifactCheckNotConceptIgnorable(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "data/ops/a.md", "---\ntype: Note\ntitle: A\nlint_ignore: [skill_invalid]\n---\n# A\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !hasCheck(findings, "ops/a.md", "lint_ignore_invalid") {
		t.Error("lint_ignore naming an artifact check must be reported as invalid")
	}
}
