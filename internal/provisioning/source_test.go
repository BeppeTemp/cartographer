package provisioning_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/artifactsig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

func srcArtifact(kind, name, source string) contract.Artifact {
	var files []contract.ArtifactFile
	switch kind {
	case "skill":
		files = []contract.ArtifactFile{{Path: "SKILL.md", Content: []byte("---\nname: " + name + "\ndescription: d\n---\nBody.\n")}}
	case "agent":
		files = []contract.ArtifactFile{{Path: name + ".md", Content: []byte("---\nname: " + name + "\n---\nBody.\n")}}
	case "hook":
		files = []contract.ArtifactFile{{Path: "hook.json", Content: []byte(`{"event":"PostToolUse","command":"echo hi"}`)}}
	}
	return contract.Artifact{Kind: kind, Name: name, Source: source, Version: "1.0.0", Files: files, ContentHash: provisioning.ContentHashFiles(files)}
}

func TestPluginSourceHelpers(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"plugin:team-forks", "team-forks"},
		{"plugin:", ""},
		{"plugin:../x", ""},
		{"plugin:a/b", ""},
		{"plugin:a:b", ""},
		{"kb:team-forks", ""},
		{"bundle", ""},
		{"", ""},
	} {
		if got := provisioning.PluginName(tc.in); got != tc.want {
			t.Errorf("PluginName(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if got := contract.IsPluginSource(tc.in); got != (tc.want != "") {
			t.Errorf("IsPluginSource(%q) = %v", tc.in, got)
		}
	}
}

// The matrix: the same artifact as a KB's and as a plugin's. A KB hook waits
// for its approval (D1141); a plugin artifact is authorized by its plugin being
// active and by nothing else.
func TestPluginSourceAuthorizationMatrix(t *testing.T) {
	active := provisioning.ApplyOptions{ActivePlugins: map[string]bool{"team-forks": true}}
	for _, kind := range []string{"skill", "agent", "hook"} {
		kb := srcArtifact(kind, "x", "kb:kb-a")
		pl := srcArtifact(kind, "x", "plugin:team-forks")
		if got, want := provisioning.ArtifactAuthorized(kb, provisioning.ApplyOptions{}), kind != "hook"; got != want {
			t.Errorf("%s: kb artifact authorized = %v, want %v", kind, got, want)
		}
		if got, want := provisioning.PendingApproval(kb, provisioning.ApplyOptions{}), kind == "hook"; got != want {
			t.Errorf("%s: kb pending = %v, want %v", kind, got, want)
		}
		if provisioning.ArtifactAuthorized(pl, provisioning.ApplyOptions{}) {
			t.Errorf("%s: plugin artifact authorized with no active plugin", kind)
		}
		if provisioning.ArtifactAuthorized(pl, provisioning.ApplyOptions{ActivePlugins: map[string]bool{"other": true}}) {
			t.Errorf("%s: plugin artifact authorized by another plugin", kind)
		}
		if !provisioning.ArtifactAuthorized(pl, active) {
			t.Errorf("%s: plugin artifact not authorized with its plugin active", kind)
		}
		pl.Signed = true // never read
		if provisioning.ArtifactAuthorized(pl, provisioning.ApplyOptions{}) {
			t.Errorf("%s: Signed authorized a plugin artifact", kind)
		}
		if provisioning.PendingApproval(srcArtifact(kind, "x", "plugin:team-forks"), active) {
			t.Errorf("%s: a plugin artifact never waits for a per-artifact approval", kind)
		}
	}
	// An unknown source installs nothing.
	if provisioning.ArtifactAuthorized(srcArtifact("skill", "x", "elsewhere:y"), active) {
		t.Error("unknown source authorized")
	}
}

func TestPluginArtifactsApplyOnlyWhenActive(t *testing.T) {
	m := contract.Manifest{Artifacts: []contract.Artifact{
		srcArtifact("skill", "plugin-skill", "plugin:team-forks"),
		srcArtifact("hook", "plugin-hook", "plugin:team-forks"),
	}}
	run := func(active map[string]bool) string {
		base := t.TempDir()
		_, err := provisioning.Apply(m, provisioning.ApplyOptions{
			Provider: configurator.ProviderClaudeCode, BaseDir: base, ActivePlugins: active,
		})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		return base
	}
	off := run(nil)
	for _, rel := range []string{".claude/skills/plugin-skill/SKILL.md", ".claude/hooks/plugin-hook/hook.json"} {
		if _, err := os.Stat(filepath.Join(off, rel)); err == nil {
			t.Errorf("%s installed with no active plugin", rel)
		}
	}
	on := run(map[string]bool{"team-forks": true})
	for _, rel := range []string{".claude/skills/plugin-skill/SKILL.md", ".claude/hooks/plugin-hook/hook.json"} {
		if _, err := os.Stat(filepath.Join(on, rel)); err != nil {
			t.Errorf("%s not installed with its plugin active: %v", rel, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(on, ".claude/skills/plugin-skill/SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `Source: plugin "team-forks" 1.0.0`) || strings.Contains(string(data), "bundled with the Cartographer server") {
		t.Errorf("provenance stamp of a plugin skill:\n%s", data)
	}
}

func TestPluginArtifactSignatureIsRefused(t *testing.T) {
	a := srcArtifact("skill", "x", "plugin:team-forks")
	a.Signature = &artifactsig.Signature{KeyID: "k"}
	if _, err := provisioning.VerifiedManifest(contract.Manifest{Artifacts: []contract.Artifact{a}}, nil); err == nil {
		t.Fatal("a signature on a plugin artifact must be an error")
	}
	if err := provisioning.VerifyArtifactSignature(a, nil); err == nil {
		t.Fatal("VerifyArtifactSignature accepted a plugin artifact")
	}
	ok, err := provisioning.VerifiedManifest(contract.Manifest{Artifacts: []contract.Artifact{srcArtifact("skill", "x", "plugin:team-forks")}}, nil)
	if err != nil || len(ok.Artifacts) != 1 || ok.Artifacts[0].Signed || ok.Artifacts[0].BuiltIn {
		t.Fatalf("unsigned plugin artifact: %+v, %v", ok.Artifacts, err)
	}
}

func TestPluginHookIsNotMigratedByTheD1141Migration(t *testing.T) {
	h := srcArtifact("hook", "guard", "plugin:team-forks")
	lock := provisioning.Lock{Managed: []provisioning.ManagedFile{{Kind: "hook", Name: "guard", ContentHash: h.ContentHash, Source: h.Source}}}
	if provisioning.InstalledBeforeApprovals(h, lock, provisioning.ApplyOptions{}) {
		t.Error("InstalledBeforeApprovals approved a plugin hook that is only in the lockfile")
	}
}

func TestReadArtifactFilesNeverLooksAPluginUpAsAKB(t *testing.T) {
	a := contract.Artifact{Kind: "skill", Name: "x", Source: "plugin:x"}
	_, err := contract.ReadArtifactFiles(a, nil, map[string]string{"plugin:x": t.TempDir(), "x": t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "plugin artifact without content") {
		t.Fatalf("got %v", err)
	}
}

func TestSelectForSourcesDropsPluginArtifacts(t *testing.T) {
	in := []contract.Artifact{srcArtifact("skill", "a", "plugin:team-forks"), srcArtifact("skill", "b", "kb:kb-a"), srcArtifact("skill", "c", "bundle")}
	out := provisioning.SelectForSources(in, []string{"kb-a", "team-forks"})
	if len(out) != 2 {
		t.Fatalf("got %+v", out)
	}
	for _, a := range out {
		if strings.HasPrefix(a.Source, "plugin:") {
			t.Errorf("kept %s", a.Source)
		}
	}
}

func TestBuildManifestNeverEmitsAPluginSource(t *testing.T) {
	kb := t.TempDir()
	if err := os.MkdirAll(filepath.Join(kb, "skills", "s"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kb, "skills", "s", "SKILL.md"), []byte("---\nname: s\ndescription: d\n---\nB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := contract.BuildManifest(nil, map[string]string{"kb-a": kb}, contract.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range m.Artifacts {
		if contract.IsPluginSource(a.Source) {
			t.Errorf("BuildManifest emitted %s", a.Source)
		}
	}
}

func TestDropPluginCollisions(t *testing.T) {
	in := []contract.Artifact{
		srcArtifact("skill", "dup", "plugin:p1"),
		srcArtifact("skill", "dup", "kb:kb-a"),
		srcArtifact("skill", "dup", "kb:kb-b"),
		srcArtifact("skill", "vs-bundle", "plugin:p1"),
		srcArtifact("skill", "vs-bundle", "bundle"),
		srcArtifact("skill", "plugins", "plugin:p1"),
		srcArtifact("skill", "plugins", "plugin:p2"),
		srcArtifact("skill", "kbs", "kb:kb-a"),
		srcArtifact("skill", "kbs", "kb:kb-b"),
		srcArtifact("hook", "dup", "kb:kb-a"), // another kind: kept
		srcArtifact("skill", "same", "bundle"),
		srcArtifact("skill", "same", "bundle"),
	}
	kept, dropped := provisioning.DropPluginCollisions(in)
	if len(dropped) != 3 {
		t.Fatalf("dropped %+v", dropped)
	}
	if d := dropped[0]; d.Kind != "skill" || d.Name != "dup" || strings.Join(d.Sources, ",") != "kb:kb-a,kb:kb-b,plugin:p1" {
		t.Errorf("first collision %+v", d)
	}
	var names []string
	for _, a := range kept {
		names = append(names, a.Kind+"/"+a.Name+"@"+a.Source)
	}
	want := "skill/vs-bundle@bundle skill/kbs@kb:kb-a skill/kbs@kb:kb-b hook/dup@kb:kb-a skill/same@bundle skill/same@bundle"
	if strings.Join(names, " ") != want {
		t.Errorf("kept %v", names)
	}
	// KB against KB is still the strict merge's error.
	if _, err := provisioning.MergeArtifactsStrict(kept); err == nil {
		t.Error("KB vs KB collision no longer an error")
	}
}

func TestPluginNeverBeatsABundleOrKBInMerge(t *testing.T) {
	for _, other := range []string{"bundle", "kb:zzz"} {
		m := contract.MergeArtifacts([]contract.Artifact{srcArtifact("skill", "x", "plugin:aaa"), srcArtifact("skill", "x", other)})
		if len(m.Artifacts) != 1 || m.Artifacts[0].Source != other {
			t.Errorf("against %s: %+v", other, m.Artifacts)
		}
	}
}

func TestPluginSkillShippingSourceEnvIsNotACollision(t *testing.T) {
	a := srcArtifact("skill", "x", "plugin:team-forks")
	a.Files = append(a.Files, contract.ArtifactFile{Path: "SOURCE.env", Content: []byte("mine\n")})
	a.ContentHash = provisioning.ContentHashFiles(a.Files)
	base := t.TempDir()
	res, err := provisioning.Apply(contract.Manifest{Artifacts: []contract.Artifact{a}}, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode, BaseDir: base, ActivePlugins: map[string]bool{"team-forks": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(base, ".claude/skills/x/SOURCE.env"))
	if err != nil || string(data) != "mine\n" {
		t.Errorf("SOURCE.env of a plugin skill: %q, %v (warnings %v)", data, err, res.Warnings)
	}
}

func TestPluginArtifactWithUnmetRequiresIsSkipped(t *testing.T) {
	// requires is evaluated for every artifact that is not bundled (resolver.go):
	// a plugin's is not exempt, and an unmet one is skipped like a KB's.
	files := []contract.ArtifactFile{{Path: "SKILL.md", Content: []byte("---\nname: needs\ndescription: d\nrequires: [skill/gone]\n---\nB\n")}}
	a := contract.Artifact{Kind: "skill", Name: "needs", Source: "plugin:team-forks", Files: files, ContentHash: provisioning.ContentHashFiles(files)}
	in := contract.ProjectionInput{Key: "p", Provider: configurator.ProviderClaudeCode, Scope: contract.ScopeGlobal, Manifest: contract.Manifest{Artifacts: []contract.Artifact{a}}}
	r := contract.ResolveProjections([]contract.ProjectionInput{in}, contract.ProjectOptions{})["p"]
	if len(r.Manifest.Artifacts) != 0 {
		t.Fatalf("an unmet requires kept the plugin skill: %+v", r.Manifest.Artifacts)
	}
}
