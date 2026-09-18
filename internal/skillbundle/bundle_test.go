package skillbundle

import (
	"io/fs"
	"path"
	"strings"
	"testing"
)

// TestBundledReferencesAreEmbedded guards the routing model the onboarding
// skills use (D196): a SKILL.md routes to references/*.md loaded on demand, and
// those files ship inside the binary via //go:embed. A reference added to the
// source tree but not reaching the embedded FS — or shipped empty — would leave
// the skill pointing at a file the agent cannot read, which is exactly the
// silent gap the routing was introduced to close.
func TestBundledReferencesAreEmbedded(t *testing.T) {
	seen := 0
	err := fs.WalkDir(FS, "bundled", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || path.Base(path.Dir(p)) != "references" {
			return nil
		}
		seen++
		data, readErr := fs.ReadFile(FS, p)
		if readErr != nil {
			t.Errorf("%s: not readable from the embedded FS: %v", p, readErr)
			return nil
		}
		if len(strings.TrimSpace(string(data))) == 0 {
			t.Errorf("%s: embedded but empty", p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk bundled: %v", err)
	}
	if seen == 0 {
		t.Fatal("no references/ file found in the embedded bundle: the embed no longer carries them")
	}
}

// TestBundledSkillReferencesAreRouted pairs with the test above from the other
// side: a reference file that no SKILL.md mentions is dead weight the agent will
// never load, and a SKILL.md routing to a file that is not there is worse.
func TestBundledSkillReferencesAreRouted(t *testing.T) {
	skills, err := fs.ReadDir(FS, "bundled")
	if err != nil {
		t.Fatalf("read bundled: %v", err)
	}
	for _, s := range skills {
		if !s.IsDir() {
			continue
		}
		refs, err := fs.ReadDir(FS, path.Join("bundled", s.Name(), "references"))
		if err != nil {
			continue // a skill with no references is fine
		}
		body, err := fs.ReadFile(FS, path.Join("bundled", s.Name(), "SKILL.md"))
		if err != nil {
			t.Fatalf("%s: read SKILL.md: %v", s.Name(), err)
		}
		for _, r := range refs {
			want := "references/" + r.Name()
			if !strings.Contains(string(body), want) {
				t.Errorf("%s/SKILL.md does not route to %s", s.Name(), want)
			}
		}
	}
}

// TestOpsKubernetesUpgradeEndsOnAVerifiedImage guards the one step in the
// upgrade list whose success condition is not the command's own exit code
// (D221). Where the manifest is applied by a GitOps controller rather than by
// the operator, `kubectl rollout status` returns success in under a second
// about the old ReplicaSet, so a bullet ending on the rollout lets an agent
// report success while the previous version is still serving. The step must
// name reading the Deployment's image back as the check. Matched on the
// durable substance, not on a sentence any rewording would break.
func TestOpsKubernetesUpgradeEndsOnAVerifiedImage(t *testing.T) {
	body, err := fs.ReadFile(FS, "bundled/cartographer-ops/SKILL.md")
	if err != nil {
		t.Fatalf("read cartographer-ops SKILL.md: %v", err)
	}
	text := string(body)
	for _, want := range []string{
		"kubectl get deploy",
		"containers[0].image",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the Kubernetes upgrade step no longer verifies the running image: %q missing", want)
		}
	}
	// The skill ships to every user; only docs/deployment.md describes the
	// maintainer's cluster, so no controller product name belongs here.
	for _, banned := range []string{"Flux", "Argo CD", "ArgoCD"} {
		if strings.Contains(text, banned) {
			t.Errorf("the ops skill names a specific GitOps controller (%q): keep it controller-agnostic", banned)
		}
	}
}

// TestOpsWindowsUpgradeKeepsTheZipFallback guards the two things an agent
// upgrading Cartographer on Windows cannot derive from anywhere else (D223).
// The skill is the only thing it reads: without the pointer it reconstructs the
// manual procedure from the repository — which is how it was done the first
// time, and how the checksum step gets dropped — and without the
// stop-before-extract clause it extracts over a running `cartographer.exe`,
// which on Windows fails with a sharing violation rather than succeeding.
// Matched on the durable substance, not on a sentence any rewording would
// break: the destination page, the command, and the reason.
func TestOpsWindowsUpgradeKeepsTheZipFallback(t *testing.T) {
	body, err := fs.ReadFile(FS, "bundled/cartographer-ops/SKILL.md")
	if err != nil {
		t.Fatalf("read cartographer-ops SKILL.md: %v", err)
	}
	text := string(body)
	for _, want := range []string{
		"docs/getting-started.md",
		"cartographer service stop",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the Windows upgrade step lost the zip fallback: %q missing", want)
		}
	}
	// The trap is the lock, not the command: a reader told to stop the service
	// without being told why will skip it on the machine where it matters. The
	// symptom is what identifies it and what the reader will search for, and it
	// fits on one line — a phrase spanning the skill's line wrap would not match.
	if !strings.Contains(text, "sharing violation") {
		t.Error("the Windows upgrade step no longer says why the service is stopped " +
			"before the extract: name the running-executable lock and its symptom")
	}
	// winget stays the channel (D218): the fallback is never presented as an
	// alternative of equal standing, and the skill does not restate its steps.
	if !strings.Contains(text, "winget is the only Windows channel") {
		t.Error("the Windows upgrade step no longer leads with winget as the only channel (D218)")
	}
	for _, banned := range []string{"Expand-Archive", "Get-FileHash", "Invoke-WebRequest"} {
		if strings.Contains(text, banned) {
			t.Errorf("the ops skill restates the manual procedure (%q): it points at "+
				"docs/getting-started.md instead", banned)
		}
	}
}
