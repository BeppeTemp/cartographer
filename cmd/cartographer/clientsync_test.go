package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/artifactsig"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func TestPulledFileJSON_ExecutableBackwardCompatibility(t *testing.T) {
	var current pulledFileJSON
	if err := json.Unmarshal([]byte(`{"path":"run.sh","content_b64":"eA==","executable":true}`), &current); err != nil {
		t.Fatal(err)
	}
	if !current.Executable {
		t.Fatal("current transport payload lost executable")
	}
	var old pulledFileJSON
	if err := json.Unmarshal([]byte(`{"path":"run.sh","content_b64":"eA=="}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.Executable {
		t.Fatal("old payload must default executable to false")
	}
}

func TestFetchMergedManifest_PreservesBinaryExecutableFilesThroughApply(t *testing.T) {
	binary := []byte{0x00, 0xff, 0x80, 0x01}
	first, err := base64.StdEncoding.DecodeString("LS0tXG5uYW1lOiB3aXJlXG5kZXNjcmlwdGlvbjogdGVzdFxuLS0tXG4=")
	if err != nil {
		t.Fatal(err)
	}
	artifactHash := provisioning.ContentHashFiles([]provisioning.ArtifactFile{
		{Path: "SKILL.md", Content: first}, {Path: "run.bin", Content: binary, Executable: true},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			ID int `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		payload := `{"revision":"wire-rev","artifacts":[{"kind":"skill","name":"wire","source":"bundle","content_hash":"` + artifactHash + `","built_in":true,"files":[{"path":"SKILL.md","content_b64":"LS0tXG5uYW1lOiB3aXJlXG5kZXNjcmlwdGlvbjogdGVzdFxuLS0tXG4=","executable":false},{"path":"run.bin","content_b64":"AP+AAQ==","executable":true}]}]}`
		if err := json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  map[string]any{"content": []map[string]string{{"type": "text", "text": payload}}},
		}); err != nil {
			t.Fatal(err)
		}
	}))
	defer srv.Close()

	m, err := fetchMergedManifest(&clientconfig.Config{ServerURL: srv.URL + "/mcp"})
	if err != nil {
		t.Fatalf("fetchMergedManifest: %v", err)
	}
	if m.Revision == "" || len(m.Artifacts) != 1 {
		t.Fatalf("manifest = %+v", m)
	}
	a := m.Artifacts[0]
	if a.ContentHash != artifactHash || len(a.Files) != 2 || !a.Files[1].Executable || !bytes.Equal(a.Files[1].Content, binary) {
		t.Fatalf("wire DTO mapping lost artifact data: %+v", a)
	}

	for provider, rel := range map[string]string{
		"claude":   filepath.Join(".claude", "skills", "wire", "run.bin"),
		"codex":    filepath.Join(".codex", "skills", "wire", "run.bin"),
		"kiro":     filepath.Join(".kiro", "skills", "wire", "run.bin"),
		"opencode": filepath.Join(".opencode", "skills", "wire", "run.bin"),
	} {
		base := t.TempDir()
		res, err := provisioning.Apply(m, provisioning.ApplyOptions{
			Provider: configurator.Provider(provider),
			BaseDir:  base,
			Lock:     provisioning.Lock{},
		})
		if err != nil {
			t.Fatalf("Apply(%s): %v", provider, err)
		}
		if len(res.Written) != 2 || res.Written[0].ContentHash != artifactHash || res.Written[1].ContentHash != artifactHash {
			t.Fatalf("Apply(%s) changed artifact hash: %+v", provider, res.Written)
		}
		data, err := os.ReadFile(filepath.Join(base, rel))
		if err != nil || !bytes.Equal(data, binary) {
			t.Fatalf("Apply(%s) binary = %x, err=%v", provider, data, err)
		}
		info, err := os.Stat(filepath.Join(base, rel))
		if err != nil || info.Mode()&0o111 == 0 {
			t.Fatalf("Apply(%s) mode = %v, err=%v", provider, info.Mode(), err)
		}
	}
}

func TestFetchMergedManifestVerifiesPinnedSignatureAndRejectsTampering(t *testing.T) {
	key, err := artifactsig.ParseSeed(strings.Repeat("03", ed25519.SeedSize))
	if err != nil {
		t.Fatal(err)
	}
	files := []provisioning.ArtifactFile{{Path: "SKILL.md", Content: []byte("skill")}}
	hash := provisioning.ContentHashFiles(files)
	identity := artifactsig.Identity{Source: "kb:homelab", Kind: "skill", Name: "signed", ContentHash: hash}
	sig, err := artifactsig.Sign(key, identity)
	if err != nil {
		t.Fatal(err)
	}

	serve := func(content string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"content": []map[string]string{{"type": "text", "text": content}}}})
		}))
	}
	payloadFor := func(mutate func(map[string]any)) string {
		copySig := *sig
		artifact := map[string]any{
			"kind": "skill", "name": "signed", "source": "kb:homelab", "content_hash": hash, "signature": &copySig,
			"files": []map[string]any{{"path": "SKILL.md", "content_b64": base64.StdEncoding.EncodeToString(files[0].Content), "executable": false}},
		}
		if mutate != nil {
			mutate(artifact)
		}
		payload, marshalErr := json.Marshal(map[string]any{"revision": "r", "artifacts": []map[string]any{artifact}})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return string(payload)
	}
	payload := payloadFor(nil)
	srv := serve(string(payload))
	cfg := &clientconfig.Config{ServerURL: srv.URL, SigningKeys: map[string][]string{"homelab": {artifactsig.PublicKeyHex(key.Public().(ed25519.PublicKey))}}}
	m, err := fetchMergedManifest(cfg)
	srv.Close()
	if err != nil || len(m.Artifacts) != 1 || !m.Artifacts[0].Signed {
		t.Fatalf("verified manifest = %+v, err=%v", m, err)
	}

	target := t.TempDir()
	if _, err := materializeForProviders(m, []string{"claude"}, target, false, false, nil, nil); err != nil {
		t.Fatalf("materialize valid manifest: %v", err)
	}
	lockPath := filepath.Join(target, provisioning.LockFileName)
	providerPath := filepath.Join(target, ".claude", "skills", "signed", "SKILL.md")
	beforeState, err := os.ReadFile(providerPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeLock, _ := os.ReadFile(lockPath)
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"file bytes", func(a map[string]any) {
			a["files"] = []map[string]any{{"path": "SKILL.md", "content_b64": base64.StdEncoding.EncodeToString([]byte("evil")), "executable": false}}
		}},
		{"executable mode", func(a map[string]any) {
			a["files"] = []map[string]any{{"path": "SKILL.md", "content_b64": base64.StdEncoding.EncodeToString(files[0].Content), "executable": true}}
		}},
		{"path", func(a map[string]any) {
			a["files"] = []map[string]any{{"path": "renamed.md", "content_b64": base64.StdEncoding.EncodeToString(files[0].Content), "executable": false}}
		}},
		{"source", func(a map[string]any) { a["source"] = "bundle" }},
		{"kind", func(a map[string]any) { a["kind"] = "hook" }},
		{"signature", func(a map[string]any) {
			a["signature"].(*artifactsig.Signature).Value = base64.RawStdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
		}},
		{"unknown key", func(a map[string]any) { a["signature"].(*artifactsig.Signature).KeyID = strings.Repeat("0", 64) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := serve(payloadFor(tc.mutate))
			cfg.ServerURL = srv.URL
			fetched, fetchErr := fetchMergedManifest(cfg)
			srv.Close()
			if fetchErr == nil {
				_, fetchErr = materializeForProviders(fetched, []string{"claude"}, target, false, false, nil, nil)
			}
			if fetchErr == nil {
				t.Fatal("tampered sync unexpectedly succeeded")
			}
			afterState, stateErr := os.ReadFile(providerPath)
			afterLock, lockErr := os.ReadFile(lockPath)
			if stateErr != nil || lockErr != nil || !bytes.Equal(beforeState, afterState) || !bytes.Equal(beforeLock, afterLock) {
				t.Fatalf("tampering changed local state: state=%q/%v lock=%q/%v", afterState, stateErr, afterLock, lockErr)
			}
		})
	}
}

func TestFetchMergedManifestRejectsMalformedConfiguredPin(t *testing.T) {
	cfg := &clientconfig.Config{SigningKeys: map[string][]string{"homelab": {"not-a-public-key"}}}
	if _, err := pinnedPublicKeys(cfg); err == nil || !strings.Contains(err.Error(), "invalid signing key pin") {
		t.Fatalf("pinnedPublicKeys error = %v", err)
	}
}

func TestResolveTargetProviders_AgentSubsets(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		csv     string
		want    []string
		wantErr string
	}{
		{"csv subset", "", "claude,codex", []string{"claude", "codex"}, ""},
		{"csv trims and deduplicates", "", "claude, codex,claude", []string{"claude", "codex"}, ""},
		{"invalid csv name", "", "claude,unknown", nil, "unknown provider"},
		{"empty csv item", "", "claude,,codex", nil, "invalid --agents"},
		{"csv plus positional", "claude", "codex", nil, "cannot be used with positional"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveTargetProviders(tc.target, tc.csv)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveTargetProviders: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("providers = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveDisconnectProviders_AgentSubsets(t *testing.T) {
	got, err := resolveDisconnectProviders("", "claude,kiro", []string{"claude", "codex", "kiro"})
	if err != nil {
		t.Fatalf("resolveDisconnectProviders: %v", err)
	}
	if want := []string{"claude", "kiro"}; !reflect.DeepEqual(got, want) {
		t.Errorf("providers = %v, want %v", got, want)
	}
	if _, err := resolveDisconnectProviders("all", "codex", nil); err == nil || !strings.Contains(err.Error(), "cannot be used with positional") {
		t.Errorf("csv+positional error = %v, want conflict", err)
	}
}

// kbSkillManifest builds a single-artifact manifest with one unsigned,
// kb:-sourced skill — the shape sync_pull returns before any trust decision
// is applied.
func kbSkillManifest() provisioning.Manifest {
	return provisioning.Manifest{
		Revision: "rev1",
		Artifacts: []provisioning.Artifact{
			{
				Kind:        "skill",
				Name:        "example",
				Source:      "kb:homelab",
				ContentHash: "hash1",
				Signed:      false,
				Files:       []provisioning.ArtifactFile{{Path: "SKILL.md", Content: []byte("# example")}},
			},
		},
	}
}

func TestAuthorizationDoesNotSetSigned(t *testing.T) {
	m := kbSkillManifest()
	if m.Artifacts[0].Signed {
		t.Fatal("test fixture must be unsigned")
	}
	if _, err := materializeForProviders(m, []string{"claude"}, t.TempDir(), true, true, nil, nil); err != nil {
		t.Fatal(err)
	}
	if m.Artifacts[0].Signed {
		t.Error("authorization must not mutate verification state")
	}
}

func TestMaterializeForProviders_TrustAvoidsNeedsApproval(t *testing.T) {
	dir := t.TempDir()
	m := kbSkillManifest()

	results, err := materializeForProviders(m, []string{"claude"}, dir, true, true /* dryRun */, nil, nil)
	if err != nil {
		t.Fatalf("materializeForProviders: %v", err)
	}
	r := results["claude"]
	if len(r.NeedsApproval) != 0 {
		t.Errorf("expected 0 needs-approval with trust=true, got %d: %+v", len(r.NeedsApproval), r.NeedsApproval)
	}
	if len(r.Written) != 1 {
		t.Errorf("expected 1 written artifact with trust=true, got %d", len(r.Written))
	}
}

func TestMaterializeForProviders_NoTrustNeedsApproval(t *testing.T) {
	dir := t.TempDir()
	m := kbSkillManifest()

	results, err := materializeForProviders(m, []string{"claude"}, dir, false, true /* dryRun */, nil, nil)
	if err != nil {
		t.Fatalf("materializeForProviders: %v", err)
	}
	r := results["claude"]
	if len(r.NeedsApproval) != 1 {
		t.Errorf("expected 1 needs-approval with trust=false, got %d", len(r.NeedsApproval))
	}
	if len(r.Written) != 0 {
		t.Errorf("expected 0 written artifacts with trust=false, got %d", len(r.Written))
	}
}

// instructionsManifest builds a single-artifact manifest with one signed,
// kb:-sourced "instructions" artifact (D56) — the shape sync_pull returns for the
// imprinting artifact once decoded client-side (Files populated from base64).
func instructionsManifest(kbName, content string) provisioning.Manifest {
	return provisioning.Manifest{
		Revision: "rev-instr",
		Artifacts: []provisioning.Artifact{
			{
				Kind:        "instructions",
				Name:        kbName,
				Source:      "kb:" + kbName,
				ContentHash: "hash-instr",
				Signed:      true,
				Files:       []provisioning.ArtifactFile{{Path: "instructions.md", Content: []byte(content)}},
			},
		},
	}
}

// TestMaterializeForProviders_Instructions_ClaudeEKiro is the e2e path for the
// "instructions" kind (D56) through the real client entry point used by
// `cartographer connect`/`sync`: it must materialize the managed block into both
// claude's shared CLAUDE.md and kiro's dedicated steering file, and record one
// ManagedFile per provider in the multi-provider lockfile.
func TestMaterializeForProviders_Instructions_ClaudeEKiro(t *testing.T) {
	dir := t.TempDir()
	m := instructionsManifest("homelab", "Contenuto di imprinting per homelab.\n")

	results, err := materializeForProviders(m, []string{"claude", "kiro"}, dir, true, false /* dryRun */, nil, nil)
	if err != nil {
		t.Fatalf("materializeForProviders: %v", err)
	}

	claudePath := filepath.Join(dir, ".claude", "CLAUDE.md")
	kiroPath := filepath.Join(dir, ".kiro", "steering", "cartographer.md")

	for provider, path := range map[string]string{"claude": claudePath, "kiro": kiroPath} {
		r := results[provider]
		if len(r.Written) != 1 || r.Written[0].Kind != "instructions" {
			t.Errorf("%s: Written inatteso: %+v", provider, r.Written)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: file non materializzato in %s: %v", provider, path, err)
		}
		content := string(data)
		if !strings.Contains(content, "cartographer:instructions:begin") {
			t.Errorf("%s: marker del blocco assente: %s", provider, content)
		}
		if !strings.Contains(content, "Contenuto di imprinting per homelab.") {
			t.Errorf("%s: contenuto instructions assente: %s", provider, content)
		}
	}

	lf, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		t.Fatalf("ReadLockFile: %v", err)
	}
	for _, provider := range []string{"claude", "kiro"} {
		lock := lf.ForProvider(provider)
		if len(lock.Managed) != 1 || lock.Managed[0].Kind != "instructions" {
			t.Errorf("%s: lockfile Managed inatteso: %+v", provider, lock.Managed)
		}
	}
}

func TestDiffWithTrustKeepsVerificationState(t *testing.T) {
	m := kbSkillManifest()
	pm := provisioning.FilterForProvider(m, configurator.ProviderClaudeCode)
	d := provisioning.ComputeDiff(pm, provisioning.Lock{})

	if len(d.Added) != 1 {
		t.Fatalf("expected 1 added artifact, got %d", len(d.Added))
	}
	if d.Added[0].Signed {
		t.Error("trust must not change cryptographic verification state")
	}
	if got := formatDiffStatus(d); got != "drift +1 ~0 -0 (1 needs approval, use CLI --auto-trust)" {
		t.Errorf("formatDiffStatus = %q", got)
	}
}

func TestDiffWithoutTrust_NeedsApproval(t *testing.T) {
	m := kbSkillManifest()
	pm := provisioning.FilterForProvider(m, configurator.ProviderClaudeCode)
	d := provisioning.ComputeDiff(pm, provisioning.Lock{})

	got := formatDiffStatus(d)
	if got == "drift +1 ~0 -0" {
		t.Errorf("formatDiffStatus = %q, expected a needs-approval hint without trust", got)
	}
}

// TestDoConnect_PersistsTrust exercises doConnect against an unreachable
// server (so skill materialization is deferred, not fatal — see doConnect's
// doc comment) and checks that the chosen Trust value still ends up in
// .cartographer.yaml (step 3 of doConnect persists it unconditionally).
func TestDoConnect_PersistsTrust(t *testing.T) {
	dir := t.TempDir()

	opts := connectOptions{
		Providers: []string{"claude"},
		Dir:       dir,
		ServerURL: "http://127.0.0.1:1/mcp", // nobody listens here: connection refused
		Name:      "cartographer",
		TokenEnv:  "CARTOGRAPHER_TOKENS",
		Trust:     false,
	}

	res, err := doConnect(opts)
	if err != nil {
		t.Fatalf("doConnect: %v", err)
	}
	if !res.Deferred {
		t.Fatal("expected sync to be deferred (unreachable server)")
	}

	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Trust {
		t.Error("expected persisted Trust=false")
	}

	// Now reconnect choosing Trust=true and confirm it flips.
	opts.Trust = true
	if _, err := doConnect(opts); err != nil {
		t.Fatalf("doConnect (2nd): %v", err)
	}
	cfg, err = clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load (2nd): %v", err)
	}
	if !cfg.Trust {
		t.Error("expected persisted Trust=true after reconnecting with Trust=true")
	}
}

// TestMaterializeForProviders_StdioPreflightIsAtomic verifies the atomic
// multi-provider preflight added ahead of the per-provider Apply loop: a
// missing local command fails before any provider file or the lockfile is
// touched, and the error names the provider it protected.
func TestMaterializeForProviders_StdioPreflightIsAtomic(t *testing.T) {
	dir := t.TempDir()
	content := []byte(`{"type":"stdio","command":"cartographer-definitely-missing-command"}`)
	file := provisioning.ArtifactFile{Path: "missing.json", Content: content}
	m := provisioning.Manifest{Artifacts: []provisioning.Artifact{{
		Kind: "mcp", Name: "missing", Source: "kb:kb", Signed: true,
		ContentHash: provisioning.ContentHashFiles([]provisioning.ArtifactFile{file}), Files: []provisioning.ArtifactFile{file},
	}}}
	_, err := materializeForProviders(m, []string{"claude", "codex"}, dir, false, false, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "not found on PATH") || !strings.Contains(err.Error(), "for claude") {
		t.Fatalf("preflight error = %v", err)
	}
	for _, path := range []string{filepath.Join(dir, ".claude.json"), filepath.Join(dir, ".codex", "config.toml"), lockFilePath(dir)} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("%s changed after failed multi-provider preflight: %v", path, statErr)
		}
	}
}
