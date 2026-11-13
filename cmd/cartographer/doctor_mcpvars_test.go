package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

func mcpVarsArtifact(name, hash, body string) contract.Artifact {
	return contract.Artifact{Kind: "mcp", Name: name, Source: "kb:kb-a", ContentHash: hash,
		Files: []contract.ArtifactFile{{Path: name + ".json", Content: []byte(body)}}}
}

func TestCheckMCPVariables(t *testing.T) {
	const withVars = `{"type":"http","url":"https://svc.example.com/mcp","headers":{"A":"${DOC_TEST_A}","B":"Bearer ${DOC_TEST_B}"}}`
	const withSecret = `{"type":"http","url":"https://svc.example.com/mcp","headers":{"A":"${secret:svc#TOKEN}"}}`
	cfg := &clientconfig.Config{}
	for _, n := range []string{"svc", "sec"} {
		if err := cfg.ApproveMCP("kb-a", n, "h1", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	arts := []contract.Artifact{
		mcpVarsArtifact("svc", "h1", withVars),
		mcpVarsArtifact("sec", "h1", withSecret),
		mcpVarsArtifact("new", "h1", withVars),    // not approved
		mcpVarsArtifact("old", "h-new", withVars), // approved hash differs: not approved as it is
	}
	fetch := func(*clientconfig.Config) (contract.Manifest, error) { return contract.Manifest{Artifacts: arts}, nil }
	old := doctorLookupEnv
	t.Cleanup(func() { doctorLookupEnv = old })

	doctorLookupEnv = func(k string) (string, bool) {
		if k == "DOC_TEST_B" {
			return "v", true
		}
		return "", false
	}
	f := checkMCPVariables(cfg, fetch)
	if len(f) != 1 || f[0].Severity != doctorWarning || f[0].Check != "mcp-variables" {
		t.Fatalf("findings = %+v, want one warning for kb-a/svc", f)
	}
	if !strings.Contains(f[0].Message, "kb-a/svc") || !strings.Contains(f[0].Message, "${DOC_TEST_A}") || strings.Contains(f[0].Message, "DOC_TEST_B") || strings.Contains(f[0].Message, "TOKEN") {
		t.Errorf("message = %q", f[0].Message)
	}
	if f[0].Fix != "cartographer mcp probe kb-a/svc" {
		t.Errorf("fix = %q", f[0].Fix)
	}

	doctorLookupEnv = func(string) (string, bool) { return "v", true }
	if f := checkMCPVariables(cfg, fetch); len(f) != 0 {
		t.Errorf("all set: %+v", f)
	}
	// A fetch error is not a finding, and nothing approved makes no fetch.
	doctorLookupEnv = func(string) (string, bool) { return "", false }
	if f := checkMCPVariables(cfg, func(*clientconfig.Config) (contract.Manifest, error) { return contract.Manifest{}, errors.New("down") }); len(f) != 0 {
		t.Errorf("fetch error: %+v", f)
	}
	called := false
	checkMCPVariables(&clientconfig.Config{}, func(*clientconfig.Config) (contract.Manifest, error) { called = true; return contract.Manifest{}, nil })
	if called {
		t.Error("no approval: the manifest must not be fetched")
	}
}

func TestCheckMCPVariables_NeedsNetwork(t *testing.T) {
	cfg := &clientconfig.Config{}
	for _, n := range []string{"up", "down"} {
		if err := cfg.ApproveMCP("kb-a", n, "h1", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	mk := func(name, host string) contract.Artifact {
		return mcpVarsArtifact(name, "h1", `{"type":"http","url":"https://`+host+`/mcp","headers":{"A":"${DOC_TEST_A}"},"requires":"vpn"}`)
	}
	fetch := func(*clientconfig.Config) (contract.Manifest, error) {
		return contract.Manifest{Artifacts: []contract.Artifact{mk("up", "up.example.com"), mk("down", "down.example.com")}}, nil
	}
	oldL, oldD := doctorLookupEnv, doctorDial
	t.Cleanup(func() { doctorLookupEnv, doctorDial = oldL, oldD })
	doctorLookupEnv = func(string) (string, bool) { return "v", true }
	doctorDial = func(_ context.Context, u string, _ time.Duration) error {
		if strings.Contains(u, "down.") {
			return context.DeadlineExceeded
		}
		return nil
	}
	f := checkMCPVariables(cfg, fetch)
	if len(f) != 1 || f[0].Severity != doctorInfo || !strings.Contains(f[0].Message, "kb-a/down needs vpn: unreachable now") {
		t.Fatalf("findings = %+v, want one info for kb-a/down", f)
	}
}
