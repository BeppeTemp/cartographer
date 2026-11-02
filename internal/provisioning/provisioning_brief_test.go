package provisioning_test

// The brief managed block (D1182): per-KB brief section, the shared
// operational section, convergence and the size measurement.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// writeSkill writes a valid skill named name into the KB at root.
func writeBriefSkill(t *testing.T, root, name string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "skills", name, "SKILL.md"),
		"---\nname: "+name+"\ndescription: Use when testing "+name+".\n---\nBody.\n")
}

func briefContent(t *testing.T, root string, routed bool) provisioning.Artifact {
	t.Helper()
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb-a": root}, provisioning.BuildOptions{RoutedMount: routed})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	return findInstructionsArtifact(t, m, "kb-a")
}

func TestBrief_SkillsLine(t *testing.T) {
	for _, n := range []int{0, 1, 41} {
		root := makeKBWithArchives(t, map[string][]string{"entities": {"a.md"}})
		for i := 0; i < n; i++ {
			writeBriefSkill(t, root, fmt.Sprintf("skill-%02d", 40-i+1))
		}
		for _, routed := range []bool{false, true} {
			c := string(briefContent(t, root, routed).Files[0].Content)
			switch n {
			case 0:
				if strings.Contains(c, "Skills:") {
					t.Errorf("no skill, no line:\n%s", c)
				}
			case 1:
				if !strings.Contains(c, "Skills: skill-41.\n") {
					t.Errorf("one skill:\n%s", c)
				}
			case 41:
				if !strings.Contains(c, "skill-01, skill-02,") || !strings.HasSuffix(strings.TrimSpace(c), ", +1 more (kb_guide).") {
					t.Errorf("41 skills must be capped at 40 with the overflow count, sorted:\n%s", c)
				}
				if strings.Contains(c, "skill-41") {
					t.Errorf("the 41st name must not be listed:\n%s", c)
				}
			}
		}
	}
}

func TestBrief_HashFollowsSkillsNotPages(t *testing.T) {
	root := makeKBWithArchives(t, map[string][]string{"entities": {"a.md"}})
	h1 := briefContent(t, root, false).ContentHash
	writeFile(t, filepath.Join(root, "data", "entities", "more.md"), "# more\n")
	if h2 := briefContent(t, root, false).ContentHash; h2 != h1 {
		t.Error("adding a page must not change the brief section hash (D65)")
	}
	writeBriefSkill(t, root, "runbook")
	h3 := briefContent(t, root, false).ContentHash
	if h3 == h1 {
		t.Error("adding a skill must change the hash")
	}
	if err := os.RemoveAll(filepath.Join(root, "skills")); err != nil {
		t.Fatal(err)
	}
	if h4 := briefContent(t, root, false).ContentHash; h4 != h1 {
		t.Error("removing the skill must restore the hash")
	}
}

func TestBrief_DirectiveRun(t *testing.T) {
	cases := []struct {
		name, file         string
		wantSkip, wantFull bool
		wantBody           string
	}{
		{"none alone", "<!-- cartographer: preamble: none -->\nBody.\n", true, false, "Body."},
		{"full alone", "<!-- cartographer: block: full -->\nBody.\n", false, true, "Body."},
		{"none then full", "<!-- cartographer: preamble: none -->\n<!-- cartographer: block: full -->\nBody.\n", true, true, "Body."},
		{"full then none", "<!-- cartographer: block: full -->\n<!-- cartographer: preamble: none -->\nBody.\n", true, true, "Body."},
		{"blank between", "<!-- cartographer: block: full -->\n\n<!-- cartographer: preamble: none -->\nBody.\n", true, true, "Body."},
		{"after prose is text", "Prose first.\n<!-- cartographer: block: full -->\n", false, false, "Prose first.\n<!-- cartographer: block: full -->"},
		{"documented in prose", "Use `<!-- cartographer: block: full -->` to opt in.\n", false, false, "Use `<!-- cartographer: block: full -->` to opt in."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "instructions.md"), c.file)
			body, d := provisioning.KBInstructionsFile(root)
			if d.SkipPreamble != c.wantSkip || d.Full != c.wantFull || body != c.wantBody {
				t.Errorf("body=%q skip=%v full=%v", body, d.SkipPreamble, d.Full)
			}
		})
	}
}

func TestBrief_FullKeepsTheCut(t *testing.T) {
	root := makeKBWithArchives(t, nil)
	line := strings.Repeat("x", 99) + "\n"
	writeFile(t, filepath.Join(root, "instructions.md"), "<!-- cartographer: block: full -->\n"+strings.Repeat(line, 1000))
	c := string(briefContent(t, root, false).Files[0].Content)
	if !strings.Contains(c, "[instructions.md was cut at") {
		t.Errorf("the 64 KiB cut must still apply in full mode")
	}
	root2 := makeKBWithArchives(t, nil)
	writeFile(t, filepath.Join(root2, "instructions.md"), strings.Repeat(line, 1000))
	if c := string(briefContent(t, root2, false).Files[0].Content); strings.Contains(c, "xxxx") {
		t.Errorf("brief mode must carry no curated text")
	}
}

// sharedKBs builds n KBs named kb-1..kb-n and a signed manifest.
func sharedKBs(t *testing.T, n int, curated func(i int) string) provisioning.Manifest {
	t.Helper()
	roots := map[string]string{}
	for i := 1; i <= n; i++ {
		r := makeKBWithArchives(t, map[string][]string{"entities": {"a.md"}})
		if curated != nil {
			if c := curated(i); c != "" {
				writeFile(t, filepath.Join(r, "instructions.md"), c)
			}
		}
		roots[fmt.Sprintf("kb-%d", i)] = r
	}
	return buildSignedManifest(t, roots)
}

func applyBlock(t *testing.T, m provisioning.Manifest, base string, lock provisioning.Lock) (provisioning.AppliedResult, string) {
	t.Helper()
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode, BaseDir: base, Lock: lock, ExpandPlaceholders: true,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(base, ".claude", "CLAUDE.md"))
	return res, string(data)
}

func TestShared_OncePerBlock(t *testing.T) {
	for _, n := range []int{1, 2, 9} {
		_, content := applyBlock(t, sharedKBs(t, n, nil), t.TempDir(), provisioning.Lock{})
		if got := strings.Count(content, "Operational instructions:"); got != 1 {
			t.Errorf("%d KBs: %d operational sections, want 1", n, got)
		}
		last := strings.LastIndex(content, fmt.Sprintf("cartographer:kb:kb-%d:end", n))
		shared := strings.Index(content, "Operational instructions:")
		paths := strings.Index(content, "### Local paths")
		if !(last < shared && shared < paths) {
			t.Errorf("order must be sections, shared, paths (%d, %d, %d)", last, shared, paths)
		}
		if !strings.Contains(content, "read whole with `concept_read`") || !strings.Contains(content, "`kb_guide` returns the full operating guide") {
			t.Errorf("the new bullets are missing:\n%s", content)
		}
	}
}

func TestShared_NoneWhenEveryKBOptedOut(t *testing.T) {
	m := sharedKBs(t, 2, func(int) string { return "<!-- cartographer: preamble: none -->\nRule.\n" })
	_, content := applyBlock(t, m, t.TempDir(), provisioning.Lock{})
	if strings.Contains(content, "Operational instructions:") {
		t.Errorf("all KBs opted out, no shared section:\n%s", content)
	}
	m = sharedKBs(t, 2, func(i int) string {
		if i == 1 {
			return "<!-- cartographer: preamble: none -->\nRule.\n"
		}
		return ""
	})
	_, content = applyBlock(t, m, t.TempDir(), provisioning.Lock{})
	if strings.Count(content, "Operational instructions:") != 1 {
		t.Errorf("one KB did not opt out, the section stays:\n%s", content)
	}
}

func TestShared_SkippedAgainstOldFullSections(t *testing.T) {
	old := "The \"x\" KB is served via MCP by the \"cartographer\" server.\n\nOperational instructions:\n- old bullet.\n"
	m := provisioning.MergeArtifacts([]provisioning.Artifact{instructionsArtifact("x", old), instructionsArtifact("y", "Brief y.\n")})
	_, content := applyBlock(t, m, t.TempDir(), provisioning.Lock{})
	if strings.Count(content, "Operational instructions:") != 1 {
		t.Errorf("a second copy must not be emitted:\n%s", content)
	}
}

// A `block: full` KB whose curated body holds a line equal to the heading is
// not an old full section: the shared section stays.
func TestShared_CuratedHeadingLineDoesNotSuppressIt(t *testing.T) {
	m := sharedKBs(t, 2, func(i int) string {
		if i == 1 {
			return "<!-- cartographer: block: full -->\nOperational instructions:\n- my own rule.\n"
		}
		return ""
	})
	_, content := applyBlock(t, m, t.TempDir(), provisioning.Lock{})
	if !strings.Contains(content, "`kb_guide` returns the full operating guide") {
		t.Errorf("the shared section was dropped by a curated line:\n%s", content)
	}
}

func TestShared_Convergence(t *testing.T) {
	variants := map[string]func(i int) string{
		"brief": nil,
		"full":  func(int) string { return "<!-- cartographer: block: full -->\nRule.\n" },
		"mixed": func(i int) string {
			if i%2 == 0 {
				return "<!-- cartographer: block: full -->\nRule.\n"
			}
			return ""
		},
	}
	for name, curated := range variants {
		t.Run(name, func(t *testing.T) {
			m := sharedKBs(t, 3, curated)
			base := t.TempDir()
			r1, content1 := applyBlock(t, m, base, provisioning.Lock{})
			if len(r1.Written) == 0 {
				t.Fatal("the first apply must write")
			}
			r2, content2 := applyBlock(t, m, base, r1.NewLock)
			if len(r2.Written) != 0 || content2 != content1 {
				t.Errorf("second apply must write nothing: %+v", r2.Written)
			}
			// A release that edits the shared text changes the recorded hash:
			// simulate the lock an older release left.
			stale := r1.NewLock
			stale.PathsSectionHash = "from-an-older-release"
			r3, _ := applyBlock(t, m, base, stale)
			// The block on disk already carries the current text, so nothing is
			// written (#1326), but the lock converges on the current hash.
			if len(r3.Written) != 0 || r3.NewLock.PathsSectionHash != r1.NewLock.PathsSectionHash {
				t.Errorf("an unchanged block must not be rewritten, and the hash must converge: %+v", r3.Written)
			}
			// What an older release left on disk is rewritten.
			old := strings.Replace(content1, "kb_guide", "kb_guide_old", 1)
			if err := os.WriteFile(filepath.Join(base, ".claude", "CLAUDE.md"), []byte(old), 0o644); err != nil {
				t.Fatal(err)
			}
			stale.PathsSectionHash = "from-an-older-release"
			r3, _ = applyBlock(t, m, base, stale)
			if len(r3.Written) == 0 {
				t.Error("a changed client-owned section must rewrite the block")
			}
			r4, _ := applyBlock(t, m, base, r3.NewLock)
			if len(r4.Written) != 0 {
				t.Errorf("and then converge: %+v", r4.Written)
			}
		})
	}
}

func TestBrief_DirectiveCollisionScope(t *testing.T) {
	build := func(directive string, full bool) provisioning.Artifact {
		root := makeKBWithArchives(t, nil)
		prefix := ""
		if full {
			prefix = "<!-- cartographer: block: full -->\n"
		}
		writeFile(t, filepath.Join(root, "instructions.md"), prefix+directive+"\n")
		m, err := provisioning.BuildManifest(nil, map[string]string{"x": root}, provisioning.BuildOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return findInstructionsArtifact(t, m, "x")
	}
	a := build("<!-- cartographer:directive:timezone:UTC+2 -->", false)
	b := build("<!-- cartographer:directive:timezone:UTC-5 -->", false)
	b.Name, b.Source = "y", "kb:y"
	if got := provisioning.DetectDirectiveCollisions([]provisioning.Artifact{a, b}); len(got) != 0 {
		t.Errorf("brief KBs carry no directives, no collision: %+v", got)
	}
	fa := build("<!-- cartographer:directive:timezone:UTC+2 -->", true)
	fb := build("<!-- cartographer:directive:timezone:UTC-5 -->", true)
	fb.Name, fb.Source = "y", "kb:y"
	_, err := provisioning.MergeArtifactsStrict([]provisioning.Artifact{fa, fb})
	var ce *provisioning.CollisionError
	if !errors.As(err, &ce) {
		t.Errorf("block: full KBs still collide, got %v", err)
	}
}

func TestMeasureInstructionsBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	if s := provisioning.MeasureInstructionsBlock(path); s.Bytes != 0 {
		t.Errorf("missing file measures nothing: %+v", s)
	}
	m := sharedKBs(t, 3, func(i int) string {
		return "<!-- cartographer: block: full -->\n" + strings.Repeat("x", i*1000)
	})
	applyBlock(t, m, dir, provisioning.Lock{})
	s := provisioning.MeasureInstructionsBlock(filepath.Join(dir, ".claude", "CLAUDE.md"))
	if s.Bytes < 6000 || len(s.KBs) != 3 || s.KBs[0].KB != "kb-3" || s.KBs[0].Bytes < s.KBs[1].Bytes {
		t.Errorf("unexpected measure: %+v", s)
	}
	if s.ApproxTokens() != (s.Bytes+2)/4 || s.Over() {
		t.Errorf("tokens/over: %+v", s)
	}
	writeFile(t, path, "<!-- cartographer:instructions:begin — x -->\nno end\n")
	if s := provisioning.MeasureInstructionsBlock(path); s.Bytes != 0 {
		t.Errorf("unterminated block measures nothing: %+v", s)
	}
}

// The block is paid on every model call of every session (D1182): pin the
// generated parts so a growth is a decision, not an accident. Budgets are the
// measured size plus at most 10 %, with a hard cap. The whole block, for 1 and
// 9 KBs on a routed connection, is a cell of TestFixedCallCostBudget
// (internal/mcpserver, #1206), which this package cannot import.
func TestBlock_Budget(t *testing.T) {
	// One KB's brief region, from the fixture the D1182 measurement used.
	r := makeKBWithArchives(t, map[string][]string{"backlog": {"a.md"}, "clients": {"a.md"}, "incidents": {"a.md"}, "infra": {"a.md"}, "notes": {"a.md"}})
	for s := 0; s < 12; s++ {
		writeBriefSkill(t, r, fmt.Sprintf("kb-1-procedure-%02d", s))
	}
	writeFile(t, filepath.Join(r, "instructions.md"), strings.Repeat("curated text stays out. ", 500))
	m := buildSignedManifest(t, map[string]string{"kb-1": r})
	section := string(findInstructionsArtifact(t, m, "kb-1").Files[0].Content)
	budget := func(name string, got, measured, hard int) {
		t.Helper()
		if got > measured+measured/10 || got > hard {
			t.Errorf("%s is %d bytes, budget %d (+10%%, hard cap %d): the managed block is paid on every model call of every session; shrink it or raise the budget deliberately", name, got, measured, hard)
		}
	}
	budget("shared section", len(provisioning.SharedOperationalSection()), 2146, 2560)
	budget("brief section", len(section), 357, 512)
}
