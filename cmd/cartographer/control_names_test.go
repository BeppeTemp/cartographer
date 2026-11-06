package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func TestResolveKBTargets_RefusesAdvertisedNameWithControlCharacters(t *testing.T) {
	for _, name := range []string{"kb\x1b[2Jx", "kb\u009bx", "kb\nx"} {
		health := healthWithKBs(client.HealthKB{Name: name})
		_, err := resolveKBTargets(health, nil)
		if err == nil {
			t.Fatalf("%q: want a refusal", name)
		}
		if strings.ContainsAny(err.Error(), "\x1b\u009b\n") {
			t.Errorf("%q: the error carries the raw control character: %q", name, err)
		}
	}
}

func TestHasControlRune(t *testing.T) {
	for s, want := range map[string]bool{"abc": false, "café": false, "a\x1b": true, "a\x7f": true, "a\u0085": true} {
		if hasControlRune(s) != want {
			t.Errorf("hasControlRune(%q) != %v", s, want)
		}
	}
}

func TestPrintApplySummary_NeverPrintsAControlCharacterRaw(t *testing.T) {
	bad := provisioning.Artifact{Kind: "agent", Name: "a\x1b]0;pwn\x07\nforged", Source: "kb:k\x1b[2J"}
	results := map[string]provisioning.AppliedResult{"claude-code": {
		NeedsApproval: []provisioning.Artifact{bad},
		Unsupported:   []provisioning.Artifact{bad},
		Refused:       []provisioning.Artifact{bad},
		Warnings:      []string{"agent " + bad.Name + " not installed"},
	}}
	var buf bytes.Buffer
	printApplySummary(&buf, t.TempDir(), results, false)
	out := buf.String()
	if strings.ContainsAny(out, "\x1b\x07") || strings.Contains(out, "\nforged") {
		t.Fatalf("raw control character in the output: %q", out)
	}
	if !strings.Contains(out, `\x1b`) {
		t.Fatalf("the name must be shown escaped: %q", out)
	}
}

func TestPullTarget_RefusesControlCharactersInArtifactFields(t *testing.T) {
	cases := map[string]pulledArtifactJSON{
		"name":    {Kind: "agent", Name: "a\x1b[2Jb", Source: "kb:k"},
		"kind":    {Kind: "agent\x1b", Name: "a", Source: "kb:k"},
		"source":  {Kind: "agent", Name: "a", Source: "kb:k\x1b]0;x\x07"},
		"version": {Kind: "agent", Name: "a", Source: "kb:k", Version: "1\u009b"},
	}
	for field, art := range cases {
		art.ContentHash = provisioning.ContentHashFiles(nil)
		body, err := json.Marshal(pulledManifestJSON{Revision: "r", Artifacts: []pulledArtifactJSON{art}})
		if err != nil {
			t.Fatal(err)
		}
		srv := syncPullServer(t, func(string) (string, bool) { return string(body), false })
		res := pullTarget(client.New(srv.URL+"/mcp", ""), kbTarget{})
		srv.Close()
		if res.err == nil {
			t.Errorf("%s: a control character must be refused", field)
			continue
		}
		if strings.ContainsAny(res.err.Error(), "\x1b\x07\u009b") {
			t.Errorf("%s: the refusal carries a raw control character: %q", field, res.err)
		}
	}
}

func TestEnumerateKBs_RefusesAdvertisedNameWithControlCharacters(t *testing.T) {
	srv := healthOnlyServer(t, "kb-a", "kb\x1b[2J-b")
	defer srv.Close()
	_, err := enumerateKBs(srv.URL+"/mcp", false, "", true)
	if err == nil || strings.Contains(err.Error(), "\x1b") {
		t.Fatalf("want an escaped refusal, got %v", err)
	}
}

// Every helper that prints a name a server chose must escape it: the table
// feeds an ESC and a newline to each and asserts neither reaches the output.
func TestServerChosenTextNeverReachesTheTerminalRaw(t *testing.T) {
	const evil = "x\x1b]0;t\x07\nforged‮"
	art := provisioning.Artifact{Kind: evil, Name: evil, Source: evil}
	mf := provisioning.ManagedFile{Kind: evil, Name: evil, Path: evil, Source: evil}
	results := map[string]provisioning.AppliedResult{"claude-code": {
		Written:       []provisioning.ManagedFile{mf},
		Pruned:        []provisioning.ManagedFile{mf},
		Healed:        []provisioning.ManagedFile{mf},
		Relocated:     []provisioning.ManagedFile{mf},
		Divergent:     []provisioning.DriftFinding{{Kind: evil, Name: evil, Path: evil, Reason: evil}},
		NeedsApproval: []provisioning.Artifact{art},
		Unsupported:   []provisioning.Artifact{art},
		Refused:       []provisioning.Artifact{art},
		Warnings:      []string{evil},
	}}
	var buf bytes.Buffer
	printApplySummary(&buf, t.TempDir(), results, false)
	outputs := map[string]string{
		"printApplySummary": buf.String(),
		"partialSyncError":  partialSyncError{Failures: []kbPullFailure{{KB: evil, Err: errors.New("x")}}}.Error(),
		"escapeControl":     escapeControl(evil),
		"safeField":         safeField(evil),
	}
	for name, out := range outputs {
		if strings.ContainsAny(out, "\x1b\x07‮") || strings.Contains(out, "\nforged") {
			t.Errorf("%s: raw control character in %q", name, out)
		}
	}
}

func TestSnapshotForConfig_RefusesAdvertisedKBNameWithControlCharacters(t *testing.T) {
	old := statusHealthFn
	t.Cleanup(func() { statusHealthFn = old })
	kbs := []client.HealthKB{{Name: "kb-a"}, {Name: "kb\x1b[2Jb"}}
	statusHealthFn = func(*clientconfig.Config) (*client.Health, error) {
		return &client.Health{Version: "v1.0.0", KBs: &kbs}, nil
	}
	s := snapshotForConfig(t.TempDir(), &clientconfig.Config{ServerURL: "http://127.0.0.1:1/mcp"}, false)
	if s.State != "unavailable" || s.Error == nil || s.Error.Code != "invalid_health" {
		t.Fatalf("state=%q error=%+v, want unavailable/invalid_health", s.State, s.Error)
	}
	if strings.Contains(s.Error.Message, "\x1b") || len(s.KBs) != 0 {
		t.Errorf("raw control character or KB list leaked: %+v", s)
	}
}

func TestResponseResult_ServerMessageIsEscaped(t *testing.T) {
	_, err := responseResult([]byte(`{"error":{"message":"boom\u001b]0;t\u0007"}}`))
	if err == nil || strings.ContainsAny(err.Error(), "\x1b\x07") {
		t.Fatalf("err = %q", err)
	}
}
