package provisioning

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// hookArtifact is a built-in hook (no approval step) with the given files.
func builtInHook(name, hash string, files map[string]string) Artifact {
	a := Artifact{Kind: "hook", Name: name, ContentHash: hash, Source: "bundle", BuiltIn: true}
	for _, p := range []string{"hook.json", "extra.sh"} {
		if body, ok := files[p]; ok {
			a.Files = append(a.Files, ArtifactFile{Path: p, Content: []byte(body)})
		}
	}
	return a
}

// guardHookJSON declares an event the client fires: the vocabularies differ
// (Kiro's has no PreToolUse), and a hook on an event the client never emits is
// installed but not registered.
func guardHookJSON(t *testing.T, p configurator.Provider) string {
	t.Helper()
	for _, ev := range HookEventNames() {
		if hookEventReaches(ev, p) {
			return `{"event":"` + ev + `","command":"./extra.sh"}`
		}
	}
	t.Fatalf("no hook event reaches %s", p)
	return ""
}

// registrationFiles are the files, relative to the base dir, a hook is
// registered in for the provider: the shared settings file or the generated
// plugin file.
func registrationFiles(p configurator.Provider, name string) []string {
	m := hookMechanisms[p]
	var out []string
	if len(m.settingsFile) > 0 {
		out = append(out, filepath.Join(m.settingsFile...))
	}
	if m.pluginPath != nil {
		out = append(out, m.pluginPath(name))
	}
	return out
}

func readAll(t *testing.T, base string, rels []string) string {
	t.Helper()
	var sb strings.Builder
	for _, rel := range rels {
		data, err := os.ReadFile(filepath.Join(base, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		sb.Write(data)
	}
	return sb.String()
}

// #1151: dropping a file from a hook that is still in the manifest removes that
// file only. The registration goes when the hook itself is pruned, on every
// provider with a hook cell.
func TestApply_DroppedHookFileKeepsTheRegistration(t *testing.T) {
	providers := Destinations("hook", "guard")
	if len(providers) == 0 {
		t.Fatal("no provider has a hook cell")
	}
	for _, p := range providers {
		t.Run(string(p), func(t *testing.T) {
			if _, ok := hookMechanisms[p]; !ok {
				t.Fatalf("%s has a hook cell and no registration mechanism", p)
			}
			base := t.TempDir()
			first := Manifest{Revision: "r1", Artifacts: []Artifact{
				builtInHook("guard", "h1", map[string]string{"hook.json": guardHookJSON(t, p), "extra.sh": "#!/bin/sh\n"}),
			}}
			res, err := Apply(first, ApplyOptions{Provider: p, BaseDir: base, SkipLockWrite: true})
			if err != nil {
				t.Fatalf("first apply: %v", err)
			}
			regs := registrationFiles(p, "guard")
			if len(regs) == 0 {
				t.Fatalf("%s: no registration target", p)
			}
			before := readAll(t, base, regs)
			if !strings.Contains(before, "guard") {
				t.Fatalf("%s: the first apply did not register the hook (warnings %v): %s", p, res.Warnings, before)
			}

			second := Manifest{Revision: "r2", Artifacts: []Artifact{
				builtInHook("guard", "h2", map[string]string{"hook.json": guardHookJSON(t, p)}),
			}}
			res2, err := Apply(second, ApplyOptions{Provider: p, BaseDir: base, SkipLockWrite: true, Lock: res.NewLock})
			if err != nil {
				t.Fatalf("second apply: %v", err)
			}

			if got := readAll(t, base, regs); got != before {
				t.Errorf("%s: the registration changed when a file was dropped:\nbefore %s\nafter  %s", p, before, got)
			}
			var dropped, kept bool
			for _, mf := range res2.Pruned {
				dropped = dropped || strings.HasSuffix(mf.Path, "extra.sh")
			}
			for _, mf := range res2.NewLock.Managed {
				if strings.HasSuffix(mf.Path, "extra.sh") {
					t.Errorf("%s: the lock still claims the dropped file: %+v", p, mf)
				}
				kept = kept || strings.HasSuffix(mf.Path, "hook.json")
			}
			if !dropped || !kept {
				t.Errorf("%s: pruned %v / lock %v, want extra.sh pruned and hook.json kept", p, res2.Pruned, res2.NewLock.Managed)
			}
			destRel := destDirScoped("hook", "guard", p, ScopeGlobal)
			if _, err := os.Stat(filepath.Join(base, destRel, "extra.sh")); !os.IsNotExist(err) {
				t.Errorf("%s: the dropped file is still on disk (err=%v)", p, err)
			}
			if _, err := os.Stat(filepath.Join(base, destRel, "hook.json")); err != nil {
				t.Errorf("%s: the remaining file is gone: %v", p, err)
			}

			// The hook itself leaving the manifest still strips the registration.
			res3, err := Apply(Manifest{Revision: "r3"}, ApplyOptions{Provider: p, BaseDir: base, SkipLockWrite: true, Lock: res2.NewLock})
			if err != nil {
				t.Fatalf("third apply: %v", err)
			}
			if len(res3.Pruned) == 0 {
				t.Fatalf("%s: removing the hook pruned nothing", p)
			}
			for _, rel := range regs {
				if data, err := os.ReadFile(filepath.Join(base, rel)); err == nil && strings.Contains(string(data), "guard") {
					t.Errorf("%s: the registration outlived the hook in %s: %s", p, rel, data)
				}
			}
		})
	}
}

// #1155: a refused update of an artifact already in the lockfile keeps its
// previous lock entries, because the previous files are still on disk and ours.
func TestApply_RefusedUpdateKeepsThePreviousLockEntry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	lockHashes := func(l Lock, kind string) map[string]string {
		out := map[string]string{}
		for _, mf := range l.Managed {
			if mf.Kind == kind {
				out[mf.Path] = mf.ContentHash
			}
		}
		return out
	}

	t.Run("hook registration target behind a link", func(t *testing.T) {
		base, elsewhere := t.TempDir(), t.TempDir()
		first := Manifest{Revision: "r1", Artifacts: []Artifact{
			builtInHook("guard", "h1", map[string]string{"hook.json": guardHookJSON(t, configurator.ProviderClaudeCode), "extra.sh": "old script\n"}),
		}}
		res, err := Apply(first, ApplyOptions{Provider: configurator.ProviderClaudeCode, BaseDir: base, SkipLockWrite: true})
		if err != nil {
			t.Fatal(err)
		}
		settings := filepath.Join(base, ".claude", "settings.json")
		if err := os.Remove(settings); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(elsewhere, "settings.json"), settings); err != nil {
			t.Fatal(err)
		}

		second := Manifest{Revision: "r2", Artifacts: []Artifact{
			builtInHook("guard", "h2", map[string]string{"hook.json": guardHookJSON(t, configurator.ProviderClaudeCode), "extra.sh": "new script\n"}),
		}}
		res2, err := Apply(second, ApplyOptions{Provider: configurator.ProviderClaudeCode, BaseDir: base, SkipLockWrite: true, Lock: res.NewLock})
		if err != nil {
			t.Fatalf("second apply: %v", err)
		}
		if len(res2.Refused) != 1 {
			t.Fatalf("Refused = %+v, want the hook", res2.Refused)
		}
		got := lockHashes(res2.NewLock, "hook")
		want := lockHashes(res.NewLock, "hook")
		if len(want) == 0 || len(got) != len(want) {
			t.Fatalf("lock after the refusal = %v, want the previous entries %v", got, want)
		}
		for p, h := range want {
			if got[p] != h {
				t.Errorf("entry %s = %q, want the previous hash %q", p, got[p], h)
			}
		}
		// The script is not overwritten while its registration is refused.
		data, err := os.ReadFile(filepath.Join(base, destDirScoped("hook", "guard", configurator.ProviderClaudeCode, ScopeGlobal), "extra.sh"))
		if err != nil || string(data) != "old script\n" {
			t.Errorf("the script was overwritten by a refused update: %q, %v", data, err)
		}
		if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
			t.Errorf("the link target gained %d entries", len(entries))
		}
	})

	t.Run("skill file behind a link", func(t *testing.T) {
		base, elsewhere := t.TempDir(), t.TempDir()
		skill := func(hash, body string) Manifest {
			return Manifest{Revision: hash, Artifacts: []Artifact{{
				Kind: "skill", Name: "alpha", ContentHash: hash, Source: "bundle", BuiltIn: true,
				Files: []ArtifactFile{{Path: "SKILL.md", Content: []byte(body)}},
			}}}
		}
		res, err := Apply(skill("h1", "# alpha\n"), ApplyOptions{Provider: configurator.ProviderClaudeCode, BaseDir: base, SkipLockWrite: true})
		if err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(base, ".claude", "skills", "alpha", "SKILL.md")
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(elsewhere, "SKILL.md"), file); err != nil {
			t.Fatal(err)
		}
		res2, err := Apply(skill("h2", "# alpha v2\n"), ApplyOptions{Provider: configurator.ProviderClaudeCode, BaseDir: base, SkipLockWrite: true, NoHeal: true, Lock: res.NewLock})
		if err != nil {
			t.Fatalf("second apply: %v", err)
		}
		if len(res2.Refused) != 1 {
			t.Fatalf("Refused = %+v, want the skill", res2.Refused)
		}
		got, want := lockHashes(res2.NewLock, "skill"), lockHashes(res.NewLock, "skill")
		if len(want) == 0 || len(got) != len(want) {
			t.Fatalf("lock after the refusal = %v, want the previous entries %v", got, want)
		}
		for p, h := range want {
			if got[p] != h {
				t.Errorf("entry %s = %q, want the previous hash %q", p, got[p], h)
			}
		}
	})
}

// #1155: the MCP removal is remembered per name and target file. The same name
// in a second config file (a relocated entry) is a second removal, not
// "already pruned".
func TestPruneManaged_MCPEntryInTwoConfigFilesIsPrunedInBoth(t *testing.T) {
	base := t.TempDir()
	entry := `{"mcpServers":{"wiki-tools":{"type":"http","url":"https://tools.example.com/mcp"},"keep":{"type":"http","url":"https://keep.example.com/mcp"}}}`
	files := []string{".claude.json", ".mcp.json"}
	var managed []ManagedFile
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(base, f), []byte(entry), 0o644); err != nil {
			t.Fatal(err)
		}
		managed = append(managed, ManagedFile{Kind: "mcp", Name: "wiki-tools", Path: f})
	}
	pruned, err := PruneManaged(managed, base, false)
	if err != nil {
		t.Fatalf("PruneManaged: %v", err)
	}
	if len(pruned) != 2 {
		t.Errorf("pruned = %v, want both entries", pruned)
	}
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(base, f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if strings.Contains(string(data), "wiki-tools") {
			t.Errorf("%s still holds the pruned server: %s", f, data)
		}
		if !strings.Contains(string(data), "keep") {
			t.Errorf("%s lost a server that was not pruned: %s", f, data)
		}
	}
}

// registrationText is what the provider's registration files hold now; a file
// that is gone holds nothing.
func registrationText(base string, rels []string) string {
	var sb strings.Builder
	for _, rel := range rels {
		if data, err := os.ReadFile(filepath.Join(base, rel)); err == nil {
			sb.Write(data)
		}
	}
	return sb.String()
}

// A registrar that skips a hook silently (no hook.json, an invalid one, an event
// the client never fires) must not leave the previous registration firing: an
// update that the registrar skips strips the old entry, and the warning stays.
func TestApply_UpdateTheRegistrarSkipsStripsThePreviousRegistration(t *testing.T) {
	for _, p := range Destinations("hook", "guard") {
		unreached := ""
		for _, ev := range HookEventNames() {
			if !hookEventReaches(ev, p) {
				unreached = ev
				break
			}
		}
		cases := map[string]string{
			"hook.json dropped": "",
			"invalid spec":      `{"event":"","command":"./extra.sh"}`,
		}
		if unreached != "" {
			cases["event the client never fires"] = `{"event":"` + unreached + `","command":"./extra.sh"}`
		}
		for name, spec := range cases {
			t.Run(string(p)+"/"+name, func(t *testing.T) {
				base := t.TempDir()
				first := Manifest{Revision: "r1", Artifacts: []Artifact{
					builtInHook("guard", "h1", map[string]string{"hook.json": guardHookJSON(t, p), "extra.sh": "#!/bin/sh\n"}),
				}}
				res, err := Apply(first, ApplyOptions{Provider: p, BaseDir: base, SkipLockWrite: true})
				if err != nil {
					t.Fatal(err)
				}
				regs := registrationFiles(p, "guard")
				if !strings.Contains(registrationText(base, regs), "guard") {
					t.Fatalf("the first apply did not register the hook (warnings %v)", res.Warnings)
				}

				files := map[string]string{"extra.sh": "#!/bin/sh\n"}
				if spec != "" {
					files["hook.json"] = spec
				}
				second := Manifest{Revision: "r2", Artifacts: []Artifact{builtInHook("guard", "h2", files)}}
				res2, err := Apply(second, ApplyOptions{Provider: p, BaseDir: base, SkipLockWrite: true, Lock: res.NewLock})
				if err != nil {
					t.Fatalf("second apply: %v", err)
				}
				if got := registrationText(base, regs); strings.Contains(got, "guard") {
					t.Errorf("the previous registration survived an update the registrar skips: %s", got)
				}
				if w := warningsMentioning(res2, `hook "guard"`); len(w) == 0 {
					t.Errorf("no warning for the skipped registration: %v", res2.Warnings)
				}
				if _, err := os.Stat(filepath.Join(base, destDirScoped("hook", "guard", p, ScopeGlobal), "extra.sh")); err != nil {
					t.Errorf("the hook's remaining file is gone: %v", err)
				}
			})
		}
	}
}

func warningsMentioning(res AppliedResult, sub string) []string {
	var out []string
	for _, w := range res.Warnings {
		if strings.Contains(w, sub) {
			out = append(out, w)
		}
	}
	return out
}

// In a workspace the registration lives elsewhere than in the global config
// (Antigravity's .agents/hooks.json, Crush's .crush.json): a refused update is
// decided on that file, before the script is touched.
func TestApply_RefusedWorkspaceHookUpdateKeepsThePreviousLockEntry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	for p, regRel := range map[configurator.Provider]string{
		configurator.ProviderAntigravity: ".agents/hooks.json",
		configurator.ProviderCrush:       ".crush.json",
	} {
		t.Run(string(p), func(t *testing.T) {
			base, elsewhere := t.TempDir(), t.TempDir()
			opts := func(lock Lock) ApplyOptions {
				return ApplyOptions{Provider: p, Scope: ScopeProject, BaseDir: base, SkipLockWrite: true, Lock: lock}
			}
			hook := func(hash, script string) Manifest {
				return Manifest{Revision: hash, Artifacts: []Artifact{
					builtInHook("guard", hash, map[string]string{"hook.json": guardHookJSON(t, p), "extra.sh": script}),
				}}
			}
			res, err := Apply(hook("h1", "old script\n"), opts(Lock{}))
			if err != nil {
				t.Fatal(err)
			}
			reg := filepath.Join(base, filepath.FromSlash(regRel))
			if _, err := os.Stat(reg); err != nil {
				t.Fatalf("the workspace registration %s was not written: %v", regRel, err)
			}
			if err := os.Remove(reg); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(elsewhere, "target.json"), reg); err != nil {
				t.Fatal(err)
			}
			res2, err := Apply(hook("h2", "new script\n"), opts(res.NewLock))
			if err != nil {
				t.Fatalf("second apply: %v", err)
			}
			if len(res2.Refused) != 1 {
				t.Fatalf("Refused = %+v, want the hook", res2.Refused)
			}
			if len(res2.NewLock.Managed) != len(res.NewLock.Managed) || len(res.NewLock.Managed) == 0 {
				t.Errorf("lock after the refusal = %+v, want %+v", res2.NewLock.Managed, res.NewLock.Managed)
			}
			for _, mf := range res2.NewLock.Managed {
				if mf.ContentHash != "h1" {
					t.Errorf("entry %s = %q, want the previous hash", mf.Path, mf.ContentHash)
				}
			}
			data, err := os.ReadFile(filepath.Join(base, destDirScoped("hook", "guard", p, ScopeProject), "extra.sh"))
			if err != nil || string(data) != "old script\n" {
				t.Errorf("the script was overwritten by a refused update: %q, %v", data, err)
			}
			if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
				t.Errorf("the link target gained %d entries", len(entries))
			}
		})
	}
}

// A refused instructions block leaves the old block on disk, so its lock
// entries and the paths-section hash stay as they were.
func TestApply_RefusedInstructionsBlockKeepsThePreviousLockEntries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	base, elsewhere := t.TempDir(), t.TempDir()
	instr := func(hash, body string) Manifest {
		return Manifest{Revision: hash, Artifacts: []Artifact{{
			Kind: "instructions", Name: "kb-a", ContentHash: hash, Source: "kb:kb-a",
			Files: []ArtifactFile{{Path: "instructions.md", Content: []byte(body)}},
		}}}
	}
	opts := func(lock Lock) ApplyOptions {
		return ApplyOptions{Provider: configurator.ProviderClaudeCode, BaseDir: base, SkipLockWrite: true, Lock: lock}
	}
	res, err := Apply(instr("h1", "first\n"), opts(Lock{}))
	if err != nil {
		t.Fatal(err)
	}
	res.NewLock.PathsSectionHash = "paths-hash"
	file := filepath.Join(base, ".claude", "CLAUDE.md")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(elsewhere, "CLAUDE.md")
	if err := os.WriteFile(target, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, file); err != nil {
		t.Fatal(err)
	}
	res2, err := Apply(instr("h2", "second\n"), opts(res.NewLock))
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if len(res2.Refused) != 1 {
		t.Fatalf("Refused = %+v, want the instructions", res2.Refused)
	}
	var got []ManagedFile
	for _, mf := range res2.NewLock.Managed {
		if mf.Kind == "instructions" {
			got = append(got, mf)
		}
	}
	if len(got) != 1 || got[0].ContentHash != "h1" {
		t.Errorf("instructions entries after the refusal = %+v, want the previous one (h1)", got)
	}
	if res2.NewLock.PathsSectionHash != "paths-hash" {
		t.Errorf("PathsSectionHash = %q, want the previous value kept", res2.NewLock.PathsSectionHash)
	}
}

// A refusal part-way through a multi-file artifact touches nothing: every
// destination is checked before the first file is written.
func TestApply_RefusedMultiFileUpdateRewritesNoFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	base, elsewhere := t.TempDir(), t.TempDir()
	skill := func(hash, suffix string) Manifest {
		return Manifest{Revision: hash, Artifacts: []Artifact{{
			Kind: "skill", Name: "alpha", ContentHash: hash, Source: "bundle", BuiltIn: true,
			Files: []ArtifactFile{
				{Path: "SKILL.md", Content: []byte("# alpha " + suffix + "\n")},
				{Path: "zz-reference.md", Content: []byte("reference " + suffix + "\n")},
			},
		}}}
	}
	opts := func(lock Lock) ApplyOptions {
		return ApplyOptions{Provider: configurator.ProviderClaudeCode, BaseDir: base, SkipLockWrite: true, NoHeal: true, Lock: lock}
	}
	res, err := Apply(skill("h1", "v1"), opts(Lock{}))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, ".claude", "skills", "alpha")
	if err := os.Remove(filepath.Join(dir, "zz-reference.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(elsewhere, "ref.md"), filepath.Join(dir, "zz-reference.md")); err != nil {
		t.Fatal(err)
	}
	res2, err := Apply(skill("h2", "v2"), opts(res.NewLock))
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if len(res2.Refused) != 1 {
		t.Fatalf("Refused = %+v, want the skill", res2.Refused)
	}
	data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil || !strings.Contains(string(data), "v1") || strings.Contains(string(data), "v2") {
		t.Errorf("SKILL.md was rewritten by a refused update: %q, %v", data, err)
	}
	for _, mf := range res2.NewLock.Managed {
		if mf.ContentHash != "h1" {
			t.Errorf("entry %s = %q, want the previous hash", mf.Path, mf.ContentHash)
		}
	}
}
