package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/sops"
)

var (
	initRecipientA = "age1" + strings.Repeat("q", 58)
	initRecipientB = "age1" + strings.Repeat("p", 58)
)

func TestSecretsInit(t *testing.T) {
	fakeEncryptSOPS(t)
	k := secretKB(t, "")
	k.AuthName = "kb-a"
	tool := gitWrap(k, toolSecretsInit(k))
	call := func(ctx context.Context, rec string, force bool) ToolResult {
		b, _ := json.Marshal(map[string]any{"recipient": rec, "force": force})
		res, err := tool.Handler(ctx, b)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	text := func(r ToolResult) string { return r.Content[0].Text }
	head := func() string { s, _ := gitx.HeadSHA(k.Root); return s }
	rulePath := filepath.Join(k.Root, ".sops.yaml")

	// A non-admin principal is refused and nothing is written.
	user := auth.ContextWithPrincipal(context.Background(), auth.Principal{ID: "u", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-a", Write: true}}}})
	if res := call(user, initRecipientA, false); !res.IsError || !strings.Contains(text(res), "administrator") || strings.Contains(text(res), "cartographer ") {
		t.Fatalf("non-admin: %#v", res)
	}
	if _, err := os.Lstat(rulePath); err == nil {
		t.Fatal("non-admin wrote the rule")
	}

	if res := call(authLocalContext(), "not-a-key", false); !res.IsError {
		t.Fatalf("invalid recipient: %#v", res)
	}

	// Created: file content, one commit (a root dotfile), then secret_set uses the rule.
	before := head()
	res := call(authLocalContext(), initRecipientA, false)
	if res.IsError || !strings.Contains(text(res), "created") || res.CommitSHA == "" || head() == before {
		t.Fatalf("create: %#v", res)
	}
	if rec, ok, err := sops.RuleRecipients(k.Root, "secrets/x.sops.yaml"); err != nil || !ok || len(rec) != 1 || rec[0] != initRecipientA {
		t.Fatalf("rule: %v %v %v", rec, ok, err)
	}
	if tr, _ := gitx.TrackedPaths(k.Root, ".sops.yaml"); !tr[".sops.yaml"] {
		t.Fatalf(".sops.yaml not committed: %v", tr)
	}

	// Identical call: unchanged, no commit.
	h := head()
	if res := call(authLocalContext(), initRecipientA, false); res.IsError || !strings.Contains(text(res), "unchanged") || head() != h {
		t.Fatalf("unchanged: %#v", res)
	}

	// Different rule without force is refused and untouched; with force it is replaced.
	content, _ := os.ReadFile(rulePath)
	if res := call(authLocalContext(), initRecipientB, false); !res.IsError || !strings.Contains(text(res), initRecipientA) || !strings.Contains(text(res), "force") {
		t.Fatalf("different: %#v", res)
	}
	if now, _ := os.ReadFile(rulePath); string(now) != string(content) || head() != h {
		t.Fatal("refused call changed the KB")
	}
	if res := call(authLocalContext(), initRecipientB, true); res.IsError || !strings.Contains(text(res), "replaced") || !strings.Contains(text(res), "updatekeys") || head() == h {
		t.Fatalf("force: %#v", res)
	}

	// A symlinked .sops.yaml is refused.
	if runtime.GOOS != "windows" {
		if err := os.Remove(rulePath); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(t.TempDir(), "x"), rulePath); err != nil {
			t.Fatal(err)
		}
		if res := call(authLocalContext(), initRecipientA, true); !res.IsError {
			t.Fatalf("symlink: %#v", res)
		}
	}
}

func TestSecretsInitThenSecretSet(t *testing.T) {
	fakeEncryptSOPS(t)
	k := secretKB(t, "")
	b, _ := json.Marshal(map[string]string{"recipient": initRecipientA})
	if res, _ := gitWrap(k, toolSecretsInit(k)).Handler(authLocalContext(), b); res.IsError {
		t.Fatalf("init: %#v", res)
	}
	// The fixture key holds recipientA (test fixture), the rule names initRecipientA:
	// secret_set must get past "no .sops.yaml"; the recipient check is sops' own.
	res := callSecretSet(t, k, "secrets/new.sops.yaml", "/a", "v")
	if strings.Contains(res.Content[0].Text, "no .sops.yaml") {
		t.Fatalf("rule not seen: %#v", res)
	}
}

func TestSecretsInitIsAdvancedAndSecretGated(t *testing.T) {
	t.Parallel()
	if !ToolAdvanced("secrets_init") || !secretTools["secrets_init"] || !ToolRequiresWrite("secrets_init") {
		t.Error("secrets_init classification")
	}
}
