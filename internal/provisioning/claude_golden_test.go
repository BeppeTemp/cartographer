package provisioning

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// The Claude Code golden (D1656): what a sync of a pre-feature KB (2 skills, 2
// agents - one with `tools`, one `strict_tools` -, 1 hook, no `requires`)
// writes on Claude Code: every file, every lock entry, the managed block. It
// was captured on main BEFORE the degradation slice landed and must never
// change with it: Claude Code runs every agent natively. Regenerate only with
// -update-golden, and only when a change to Claude Code's output is intended.
var updateGolden = flag.Bool("update-golden", false, "rewrite internal/provisioning/testdata/golden")

// materializedHashRe matches the hash of bytes that hold this run's temp paths
// (SOURCE.env, the hook command); the files themselves are compared scrubbed.
var materializedHashRe = regexp.MustCompile(`"materialized_hash":\s*"[0-9a-f]+"`)

func goldenKB(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	w := func(rel, content string, mode os.FileMode) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	w("skills/review/SKILL.md", "---\nname: review\ndescription: Review a change\n---\nRead the diff.\n", 0o644)
	w("skills/notes/SKILL.md", "---\nname: notes\ndescription: Take notes\n---\nWrite them down.\n", 0o644)
	w("agents/explorer.md", "---\nname: explorer\ndescription: Reads the repo\ntools: Read, Grep\n---\nExplore.\n", 0o644)
	w("agents/auditor.md", "---\nname: auditor\ndescription: Audits\ntools: Read\nstrict_tools: true\n---\nAudit.\n", 0o644)
	w("hooks/notify/hook.json", `{"event":"Stop","command":"./notify.sh"}`, 0o644)
	w("hooks/notify/notify.sh", "#!/bin/sh\necho ok\n", 0o755)
	return root
}

// goldenDump renders the result of one Apply as stable text.
func goldenDump(t *testing.T, base, kbRoot string, res AppliedResult) string {
	t.Helper()
	var sb strings.Builder
	var paths []string
	_ = filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			paths = append(paths, p)
		}
		return nil
	})
	sort.Strings(paths)
	scrub := func(s string) string {
		s = materializedHashRe.ReplaceAllString(s, `"materialized_hash": "-"`)
		s = strings.ReplaceAll(s, filepath.ToSlash(base), "$BASE")
		return strings.ReplaceAll(s, filepath.ToSlash(kbRoot), "$KB")
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(base, p)
		fmt.Fprintf(&sb, "=== file %s\n%s\n", filepath.ToSlash(rel), scrub(string(b)))
	}
	managed := append([]ManagedFile(nil), res.NewLock.Managed...)
	sort.Slice(managed, func(i, j int) bool { return managed[i].Path < managed[j].Path })
	for _, mf := range managed {
		// The materialized hash covers bytes that hold this run's temp paths
		// (SOURCE.env, the hook command): the files above are compared scrubbed.
		mf.MaterializedHash = ""
		b, _ := json.Marshal(mf)
		fmt.Fprintf(&sb, "=== lock %s\n", scrub(string(b)))
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(&sb, "=== warning %s\n", scrub(w))
	}
	return sb.String()
}

func TestClaudeCodeGoldenIsUnchanged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the golden spells POSIX paths and hook commands")
	}
	kbRoot := goldenKB(t)
	m, err := contract.BuildManifest(nil, map[string]string{"kb-a": kbRoot}, contract.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	p := configurator.ProviderClaudeCode
	base := t.TempDir()
	opts := ApplyOptions{KBRoots: map[string]string{"kb-a": kbRoot}, Provider: p, BaseDir: base, ApprovedMCP: approveAll(m)}
	res, err := Apply(projectForGolden(m, p), opts)
	if err != nil {
		t.Fatal(err)
	}
	got := goldenDump(t, base, kbRoot, res)

	// A second sync is a no-op.
	opts.Lock = res.NewLock
	again, err := Apply(projectForGolden(m, p), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Written) != 0 || len(again.Pruned) != 0 {
		t.Errorf("re-sync is not a no-op: written=%d pruned=%d", len(again.Written), len(again.Pruned))
	}

	golden := filepath.Join("testdata", "golden", "claude-code.txt")
	if *updateGolden {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("Claude Code output changed against %s (it must be byte-identical, D1656):\n--- got\n%s", golden, got)
	}
}

// projectForGolden is the projection under test: since the degradation slice,
// ProjectManifest (the capture on main used FilterForProvider, same output).
func projectForGolden(m contract.Manifest, p configurator.Provider) contract.Manifest {
	pm, _ := ProjectManifest(m, p, contract.ScopeGlobal, contract.ProjectOptions{})
	return pm
}
