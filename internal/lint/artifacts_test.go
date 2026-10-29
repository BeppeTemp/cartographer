package lint

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

func skillMD(name, desc, body string) string {
	return "---\nname: " + name + "\ndescription: " + desc + "\n---\n" + body
}

func TestLint_SkillInvalid(t *testing.T) {
	t.Parallel()
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
		if !f.Artifact {
			t.Errorf("%s: artifact %v", f.Path, f.Artifact)
		}
	}
	if !paths["skills/mismatch/SKILL.md"] || !paths["skills/empty"] {
		t.Errorf("paths = %v", paths)
	}
}

func TestLint_SkillWarning(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.Root, "skills/long/SKILL.md", skillMD("long", "Long one", strings.Repeat("line\n", 600)))
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := findingsOf(findings, "skill_warning"); len(got) != 1 {
		t.Fatalf("want 1 info skill_warning, got %v", got)
	}
}

func TestLint_SkillNotInScopedRun(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.Root, "skills/mismatch/SKILL.md", skillMD("other", "x", "`git commit -m x` and kb_a__search\n"))
	findings, err := Run(k, "some/scope", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if spec, ok := Spec(f.Check); ok && spec.Level == LevelArtifact {
			t.Errorf("a scoped lint must not report artifact findings: %v", f)
		}
	}
}

func TestLint_LegacyToolName(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	k.AuthName = "kb-a"
	writeFile(t, k.Root, "skills/old/SKILL.md", skillMD("old", "Old", "Call `kb_a__search` then kb_a__search again.\n"))
	writeFile(t, k.Root, "skills/new/SKILL.md", skillMD("new", "New", "Call `search(kb: \"kb-a\")` and concept_read.\n"))
	writeFile(t, k.Root, "agents/ha.md", "---\nname: ha\ndescription: h\ntools: mcp__homeassistant__ha_get_state, mcp__cartographer__search\n---\nUse mcp__homeassistant__ha_get_state.\n")
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

// A KB copy under a pre-D958 bundled-skill name is a finding with a move fix;
// when the new name exists too, the finding says the repair leaves both.
func TestLint_LegacySkillName(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.Root, "skills/kb-create/SKILL.md", skillMD("kb-create", "Old", "Body.\n"))
	writeFile(t, k.Root, "skills/kb-import/SKILL.md", skillMD("kb-import", "Old", "Body.\n"))
	writeFile(t, k.Root, "skills/cartographer-kb-import/SKILL.md", skillMD("cartographer-kb-import", "New", "Body.\n"))
	writeFile(t, k.Root, "skills/my-own/SKILL.md", skillMD("my-own", "Mine", "Body.\n"))
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	got := findingsOf(findings, "legacy_skill_name")
	if len(got) != 2 {
		t.Fatalf("want 2 legacy_skill_name, got %v", got)
	}
	byPath := map[string]Finding{got[0].Path: got[0], got[1].Path: got[1]}
	f := byPath["skills/kb-create"]
	if f.Fix == nil || f.Fix.Kind != FixMoveSkill || f.Fix.Field != "skills/kb-create" || f.Fix.To != "skills/cartographer-kb-create" {
		t.Errorf("fix = %+v", f.Fix)
	}
	if c := byPath["skills/kb-import"]; !strings.Contains(c.Message, "both copies") {
		t.Errorf("a conflict must say both copies stay: %q", c.Message)
	}
}

func TestLint_LegacyToolNameInInstructions(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.Root, "skills/broken/SKILL.md", skillMD("broken", "b", "Run:\n\n```\npython3 tools/check.py\n```\n"))
	writeFile(t, k.Root, "skills/my-skill/SKILL.md", skillMD("my-skill", "m", "Run `skills/my-skill/run.sh`, then `scripts/helper.sh` and `/opt/tools/x`. Never re-read `tools/list` before `tools/call`.\n"))
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
	t.Parallel()
	small := tempKB(t)
	for i := 0; i < 3; i++ {
		writeFile(t, small.Root, fmt.Sprintf("data/ops/c%d.md", i), "---\ntype: Note\ntitle: C\n---\n# C\n")
	}
	big := tempKB(t)
	for i := 0; i < 15; i++ {
		writeFile(t, big.Root, fmt.Sprintf("data/ops/c%d.md", i), "---\ntype: Note\ntitle: C\n---\n# C\n")
	}
	// kb.Init scaffolds an instructions.md (D325); this case is about its absence.
	if err := os.Remove(filepath.Join(big.Root, "instructions.md")); err != nil {
		t.Fatal(err)
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

// A KB created by Init never raises missing_instructions, however large it
// grows (D325).
func TestLint_ScaffoldedKBHasInstructions(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	for i := 0; i < 15; i++ {
		writeFile(t, k.Root, fmt.Sprintf("data/ops/c%d.md", i), "---\ntype: Note\ntitle: C\n---\n# C\n")
	}
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := findingsOf(findings, "missing_instructions"); len(got) != 0 {
		t.Errorf("a scaffolded KB must not raise missing_instructions: %v", got)
	}
}

func TestLint_MissingInstructionsPresent(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.Root, ".DS_Store", "x")
	trackFile(t, k.Root, ".DS_Store")
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

// trackFile forces rel into the index: .git/info/exclude keeps junk out of
// git (#1377), so only a KB that tracked it before carries one.
func trackFile(t *testing.T, root, rel string) {
	t.Helper()
	if out, err := exec.Command("git", "-C", root, "add", "-f", "--", rel).CombinedOutput(); err != nil {
		t.Fatalf("git add -f %s: %v: %s", rel, err, out)
	}
}

func TestLint_SymlinkedPath(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "f.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, k.Root, "data/ops/real.md", "---\ntype: Note\ntitle: R\n---\n# R\n")
	writeFile(t, k.Root, "data/ops/dossier/index.md", "---\ntype: Note\ntitle: D\n---\n# D\n")

	clean, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := findingsOf(clean, "symlinked_path"); len(got) != 0 {
		t.Fatalf("clean KB: want none, got %v", got)
	}

	links := map[string]string{
		"data/linkedmap":           outside,
		"data/ops/linked.md":       filepath.Join(outside, "f.md"),
		"data/ops/dossier/lnk.csv": filepath.Join(outside, "f.md"),
	}
	for rel, target := range links {
		if err := os.Symlink(target, filepath.Join(k.Root, rel)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := findingsOf(findings, "symlinked_path"); len(got) != 3 {
		t.Fatalf("want 3 symlinked_path, got %v", got)
	}
	for rel := range links {
		if !hasCheck(findings, rel, "symlinked_path") {
			t.Errorf("want symlinked_path on %s", rel)
		}
	}
	scoped, err := Run(k, "ops", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := findingsOf(scoped, "symlinked_path"); len(got) != 0 {
		t.Errorf("a scoped lint does not report it: %v", got)
	}
}

func TestLint_JunkIgnoredByDataGitignore(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.Root, "skills/committer/SKILL.md", skillMD("committer", "c", "Then `git commit -m \"save\"`.\n"))
	writeFile(t, k.Root, "skills/cloner/SKILL.md", skillMD("cloner", "c", "```\ngit clone <url>\ngit status\n```\n"))
	// The step that names another repository is how the finding closes, now
	// that nothing accepts it (D907).
	writeFile(t, k.Root, "skills/deployer/SKILL.md", skillMD("deployer", "d", "```\ngit -C /srv/app pull\n```\n"))
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	got := findingsOf(findings, "skill_git_command")
	if len(got) != 1 || got[0].Path != "skills/committer/SKILL.md" || !strings.Contains(got[0].Message, "git commit") || !strings.Contains(got[0].Message, "git -C <that repository> commit") {
		t.Fatalf("want 1 skill_git_command on committer, got %v", got)
	}
}

func TestLint_SopsFormatMismatch(t *testing.T) {
	t.Parallel()
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
}

func TestLint_SopsMissingFile(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	if len(got) != 1 || got[0].Path != "skills/build/SKILL.md" {
		t.Fatalf("want 1 skill_missing_perimeter on build, got %v", got)
	}
}

func TestLint_SkillMissingPerimeter_NoPerimeter(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	got := parseLegacyPaths("  \"wiki/\": \"\"\n  'wiki/ops/': 'ops/'\n  plain/: x/\n")
	want := []legacyPath{{"wiki/ops/", "ops/"}, {"plain/", "x/"}, {"wiki/", ""}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func agoDays(d int) time.Time { return time.Now().Add(-time.Duration(d) * 24 * time.Hour) }

// UsageState is the one classifier of a skill's or agent's usage: kb_status
// counts by it, the Artifacts panel shows it and artifact_unused (D1043) reports by it.
func TestUsageState(t *testing.T) {
	t.Parallel()
	now := time.Now()
	for _, tc := range []struct {
		name string
		u    kb.UsageSummary
		seen bool
		want string
	}{
		{"absent", kb.UsageSummary{}, false, UsageNever},
		{"zero time", kb.UsageSummary{Count: 3}, true, UsageNever},
		{"catalogue only", kb.UsageSummary{LastUsed: agoDays(1), Count: 0, Provider: "codex"}, true, UsageCatalog},
		{"stale", kb.UsageSummary{LastUsed: agoDays(50), Count: 4, Provider: "claude"}, true, UsageStale},
		{"active", kb.UsageSummary{LastUsed: agoDays(5), Count: 1, Provider: "claude"}, true, UsageActive},
	} {
		if got := UsageState(tc.u, tc.seen, 42, now); got != tc.want {
			t.Errorf("%s: UsageState = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// commitAt commits everything in the KB with the given author date.
func commitAt(t *testing.T, k *kb.KB, at time.Time, msg string) {
	t.Helper()
	d := at.UTC().Format(time.RFC3339)
	if err := gitx.Commit(k.Root, msg, "user", "user@example.com", "GIT_AUTHOR_DATE="+d, "GIT_COMMITTER_DATE="+d); err != nil {
		t.Fatal(err)
	}
}

func unusedFindings(t *testing.T, k *kb.KB) []Finding {
	t.Helper()
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	return findingsOf(findings, "artifact_unused")
}

func reportUsage(t *testing.T, k *kb.KB, entries ...kb.UsageEntry) {
	t.Helper()
	if _, err := k.MergeUsage(entries); err != nil {
		t.Fatal(err)
	}
}

// artifact_unused (D1043): the guards are the point of the test: an empty store,
// a fresh artifact and an artifact with no history all report nothing.
func TestLint_ArtifactUnused(t *testing.T) {
	t.Parallel()
	anchor := kb.UsageEntry{Name: "anchor", Kind: "skill", Provider: "claude", LastUsed: agoDays(1), Count: 3}
	setup := func(t *testing.T, bornDaysAgo int, commit bool) *kb.KB {
		k := tempKB(t)
		writeFile(t, k.Root, "skills/anchor/SKILL.md", skillMD("anchor", "Anchor", "# A\n"))
		writeFile(t, k.Root, "skills/idle/SKILL.md", skillMD("idle", "Idle", "# I\n"))
		writeFile(t, k.Root, "agents/helper.md", "# helper\n")
		if commit {
			commitAt(t, k, agoDays(bornDaysAgo), "add artifacts")
		}
		return k
	}
	paths := func(fs []Finding) string {
		var p []string
		for _, f := range fs {
			if !f.Artifact || f.Fix != nil {
				t.Errorf("%s: want an artifact finding with no fix, got %+v", f.Path, f)
			}
			p = append(p, f.Path)
		}
		return strings.Join(p, ",")
	}

	t.Run("empty store reports nothing", func(t *testing.T) {
		t.Parallel()
		if got := unusedFindings(t, setup(t, 200, true)); len(got) != 0 {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("never used and old", func(t *testing.T) {
		t.Parallel()
		k := setup(t, 200, true)
		reportUsage(t, k, anchor)
		got := unusedFindings(t, k)
		if p := paths(got); p != "agents/helper.md,skills/idle/SKILL.md" {
			t.Fatalf("paths = %s", p)
		}
		if !strings.Contains(got[1].Message, "never invoked since it was added on") || !strings.Contains(got[1].Message, "delete it") {
			t.Errorf("message = %q", got[1].Message)
		}
	})
	t.Run("never used but fresh", func(t *testing.T) {
		t.Parallel()
		k := setup(t, 2, true)
		reportUsage(t, k, anchor)
		if got := unusedFindings(t, k); len(got) != 0 {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("no git history", func(t *testing.T) {
		t.Parallel()
		k := setup(t, 0, false)
		reportUsage(t, k, anchor)
		if got := unusedFindings(t, k); len(got) != 0 {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("stale", func(t *testing.T) {
		t.Parallel()
		k := setup(t, 200, true)
		reportUsage(t, k, anchor,
			kb.UsageEntry{Name: "idle", Kind: "skill", Provider: "codex", LastUsed: agoDays(90), Count: 2},
			kb.UsageEntry{Name: "helper", Kind: "agent", Provider: "claude", LastUsed: agoDays(1), Count: 1})
		got := unusedFindings(t, k)
		if len(got) != 1 || got[0].Path != "skills/idle/SKILL.md" || !strings.Contains(got[0].Message, "last used 90 days ago by codex") {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("active", func(t *testing.T) {
		t.Parallel()
		k := setup(t, 200, true)
		reportUsage(t, k, anchor,
			kb.UsageEntry{Name: "idle", Kind: "skill", Provider: "claude", LastUsed: agoDays(3), Count: 1},
			kb.UsageEntry{Name: "helper", Kind: "agent", Provider: "claude", LastUsed: agoDays(3), Count: 1})
		if got := unusedFindings(t, k); len(got) != 0 {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("catalogue only counts as never", func(t *testing.T) {
		t.Parallel()
		k := setup(t, 200, true)
		reportUsage(t, k, anchor,
			kb.UsageEntry{Name: "idle", Kind: "skill", Provider: "codex", LastUsed: agoDays(1), Count: 0},
			kb.UsageEntry{Name: "helper", Kind: "agent", Provider: "claude", LastUsed: agoDays(1), Count: 1})
		got := unusedFindings(t, k)
		if len(got) != 1 || got[0].Path != "skills/idle/SKILL.md" || !strings.Contains(got[0].Message, "never invoked") {
			t.Fatalf("got %v", got)
		}
	})
}

// The write gate never refuses a write over artifact_unused (D1043).
func TestArtifactUnusedNotAvoidableAtWrite(t *testing.T) {
	t.Parallel()
	spec, ok := Spec("artifact_unused")
	if !ok || spec.AvoidableAtWrite || len(spec.FixKinds) != 0 || !spec.Panel {
		t.Fatalf("spec = %+v ok=%v", spec, ok)
	}
}
