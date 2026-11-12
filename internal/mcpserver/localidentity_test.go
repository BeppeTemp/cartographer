package mcpserver

import (
	"context"
	"encoding/json"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// A personal install (local server) whose KB has a remote and no real git
// identity refuses a write that would be committed as local-admin; a request
// naming its own author, a configured identity, a KB without a remote and a
// shared server are untouched.
func TestGitWrap_LocalServerWithoutIdentityRefusesOnRemote(t *testing.T) {
	t.Parallel()
	local := auth.LocalAdminPrincipal()
	run := func(t *testing.T, remote, required bool, identity, header string) ToolResult {
		t.Helper()
		kk, _ := setupGitKB(t)
		if remote {
			kk, _ = setupGitKBWithRemote(t)
		}
		kk.SetIdentityRequired(required)
		if identity != "" {
			kk.SetDefaultIdentity("Ada Example", identity)
		}
		tool := gitWrap(kk, Tool{Name: "test_write", Handler: func(ctx requestContext, a json.RawMessage) (ToolResult, error) {
			if err := kk.WriteFileAtomic("data/wrapped.md", []byte("x\n")); err != nil {
				return ToolResult{}, err
			}
			return textResult(`{"ok":true}`), nil
		}})
		res, err := tool.Handler(authorHeaderCtx(local, header), json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	res := run(t, true, true, "", "")
	if !res.IsError || !strings.Contains(res.Content[0].Text, "git.identities.default") || !strings.Contains(res.Content[0].Text, "user.email") {
		t.Fatalf("a local write on a KB with a remote and no identity must be refused naming the keys, got %+v", res)
	}
	for name, c := range map[string]struct {
		remote, required bool
		identity, header string
	}{
		"no remote":          {false, true, "", ""},
		"identity set":       {true, true, "ada@example.test", ""},
		"author header":      {true, true, "", "Ada Example <ada@example.test>"},
		"shared server":      {true, false, "", ""},
		"no remote, no flag": {false, false, "", ""},
	} {
		if res := run(t, c.remote, c.required, c.identity, c.header); res.IsError {
			t.Errorf("%s: write refused: %+v", name, res)
		}
	}
}

// kb_status carries the missing identity as a field the agent can relay, and
// only then.
func TestServer_KBStatus_IdentityMissing(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKBWithRemote(t)
	k.SetIdentityRequired(true)
	s := New("0.7.0")
	RegisterKBTools(s, k, Deps{})
	var msg string
	if err := json.Unmarshal(kbStatusResult(t, s)["identity_missing"], &msg); err != nil || !strings.Contains(msg, "git.identities.default") {
		t.Fatalf("identity_missing = %q (%v), want the key to set", msg, err)
	}
	k.SetDefaultIdentity("Ada Example", "ada@example.test")
	if _, present := kbStatusResult(t, s)["identity_missing"]; present {
		t.Error("identity_missing is reported although the KB has an identity")
	}
}

// A repair has no writer of its own and keeps the fallback identity (D950): the
// unattended auto-repair still commits on a local server whose KB has a remote
// and no git identity.
func TestAutoRepairStillCommitsWithoutAnIdentity(t *testing.T) {
	t.Parallel()
	k, s := repairKB(t, 3)
	bare := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "init", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	if err := gitx.AddRemote(k.Root, "origin", bare); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { waitReadRefresh(k) })
	k.SetIdentityRequired(true)
	run := s.runAutoRepairQuota(context.Background())
	if len(run.Checks) != 1 || run.Checks[0].Commit == "" || run.Checks[0].Error != "" {
		t.Fatalf("auto-repair on a local server with no identity = %+v, want a commit", run)
	}
}

// A client calling kb_repair is a writer like any other: on a local server with
// a remote and no identity it is refused (the exemption is the auto-repair's
// context mark, not the tool name).
func TestKBRepairByAClientIsRefusedWithoutAnIdentity(t *testing.T) {
	t.Parallel()
	k, _ := repairKB(t, 3)
	bare := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "init", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	if err := gitx.AddRemote(k.Root, "origin", bare); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { waitReadRefresh(k) })
	k.SetIdentityRequired(true)
	tool := gitWrap(k, toolKBRepair(k))
	res, err := tool.Handler(auth.ContextWithPrincipal(context.Background(), auth.LocalAdminPrincipal()), json.RawMessage(`{"check":"*","dry_run":false}`))
	if err != nil || !res.IsError || !strings.Contains(res.Content[0].Text, "git.identities.default") {
		t.Fatalf("a client kb_repair must be refused naming the key, got err=%v res=%+v", err, res)
	}
}

// A write refused for want of a git identity never calls a write_veto hook, and
// once the KB has an identity the same write reaches it.
func TestWriteVeto_NotCalledForAWriteTheIdentityRefuses(t *testing.T) {
	t.Parallel()
	h := vetoServer(t, answer(`{"version":1}`), nil)
	bare := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "init", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	if err := gitx.AddRemote(h.k.Root, "origin", bare); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { waitReadRefresh(h.k) })
	h.k.SetIdentityRequired(true)
	res := h.write(auth.LocalAdminPrincipal())
	if !res.IsError || !strings.Contains(resultText(res), "git.identities.default") {
		t.Fatalf("write = %q, want the identity refusal", resultText(res))
	}
	if n := h.hits.Load(); n != 0 {
		t.Fatalf("hook called %d times for a write that is refused", n)
	}
	h.k.SetDefaultIdentity("Ada Example", "ada@example.test")
	if res := h.write(auth.LocalAdminPrincipal()); res.IsError {
		t.Fatalf("write with an identity = %q", resultText(res))
	}
	if n := h.hits.Load(); n != 1 {
		t.Fatalf("hook called %d times for the allowed write, want 1", n)
	}
}
