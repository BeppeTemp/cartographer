package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// D1894 Secrets: a rule with secrets: none withholds every tool that reveals
// or changes a secret, and the concept fields that point at one. Add a new
// secret-touching tool to this table.
func TestSecretsNoneTools(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	k.AuthName = "docs"
	plain := auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true}}}
	none := auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true, NoSecrets: true}}}
	// A second, unrestricted write rule restores the authority (union).
	union := auth.Policy{Permissions: []auth.Permission{
		{KB: "docs", Write: true, NoSecrets: true}, {KB: "docs", Write: true},
	}}
	// Mixed rules: a scoped rule without the key does not undo the whole-KB
	// restriction; an unselected one does.
	scoped := auth.Policy{Permissions: []auth.Permission{
		{KB: "docs", Write: true, NoSecrets: true}, {KB: "docs", Write: true, Maps: []string{"x"}},
	}}
	if !scoped.WithholdsSecrets("docs") || union.WithholdsSecrets("docs") || plain.WithholdsSecrets("docs") || !none.WithholdsSecrets("docs") {
		t.Error("WithholdsSecrets on mixed rules")
	}
	if err := authorizeTool(scoped, k, "docs", "concept_write", []byte(`{"id":"a/b","frontmatter":{"secret_refs":{}}}`)); err == nil || !strings.Contains(err.Error(), "secrets_restricted") {
		t.Errorf("whole-KB none plus a scoped plain rule: %v", err)
	}
	stranger := auth.Policy{Permissions: []auth.Permission{{KB: "other", Write: true, NoSecrets: true}}}
	cases := []struct {
		tool, args string
	}{
		{"secret_resolve", `{"concept_id":"a/b"}`},
		{"secret_set", `{"path":"secrets/x.sops.yaml","pointer":"/a","value":"v"}`},
		{"service_get", `{"service_id":"s","resolve_secrets":true}`},
		{"concept_write", `{"id":"a/b","body":"x","frontmatter":{"secret_refs":{"k":"secrets/x.sops.yaml#/a"}}}`},
		{"concept_write", `{"id":"a/b","body":"---\nsecrets_source: secrets/x.sops.yaml\n---\n"}`},
		{"concept_new", `{"id":"a/b","template":"t","fields":{"secret_refs":[]}}`},
		{"concept_patch", `{"id":"a/b","new_string":"secret_refs: x"}`},
		{"concept_batch", `{"operations":[{"op":"write","id":"a/b","frontmatter":{"secrets_source":"x"}}]}`},
		{"concept_patch", `{"id":"a/b","frontmatter":{"secret_refs":{"k":"secrets/x.sops.yaml#/a"}}}`},
		{"concept_patch", `{"id":"a/b","frontmatter_append":{"secret_refs":["k"]}}`},
		{"concept_merge", `{"id":"a/b","satellite_id":"a/b","frontmatter":{"secrets_source":"x"}}`},
		{"concept_merge", `{"id":"a/b","satellite_id":"a/b","secret_refs":{}}`},
		{"concept_expand", `{"id":"a/b","secret_refs":{}}`},
	}
	for _, c := range cases {
		err := authorizeTool(none, k, "docs", c.tool, []byte(c.args))
		if err == nil || !strings.Contains(err.Error(), "secrets_restricted") {
			t.Errorf("%s with secrets: none: %v, want secrets_restricted", c.tool, err)
		}
		if err := authorizeTool(union, k, "docs", c.tool, []byte(c.args)); err != nil && strings.Contains(err.Error(), "secrets_restricted") {
			t.Errorf("%s with a union rule: %v", c.tool, err)
		}
		if err := authorizeTool(plain, k, "docs", c.tool, []byte(c.args)); err != nil && strings.Contains(err.Error(), "secrets_restricted") {
			t.Errorf("%s with a plain rw token: %v", c.tool, err)
		}
		// Non-disclosure: no access to the KB never reads as secrets_restricted.
		if err := authorizeTool(stranger, k, "docs", c.tool, []byte(c.args)); err != nil && strings.Contains(err.Error(), "secrets_restricted") {
			t.Errorf("%s without KB access: %v", c.tool, err)
		}
	}
	// Ordinary content writes and reads stay allowed.
	if err := authorizeTool(none, k, "docs", "concept_write", []byte(`{"id":"a/b","body":"x"}`)); err != nil && strings.Contains(err.Error(), "secrets_restricted") {
		t.Errorf("plain concept_write: %v", err)
	}
	if none.AllowsSecrets("docs") || !plain.AllowsSecrets("docs") || none.AllowsSecrets("other") {
		t.Error("AllowsSecrets")
	}
}

func TestSecretsVisibleFollowsTheRule(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	k.AuthName = "docs"
	for _, tc := range []struct {
		name string
		perm auth.Permission
		want bool
	}{
		{"rw", auth.Permission{KB: "docs", Write: true}, true},
		{"r", auth.Permission{KB: "docs"}, true},
		{"rw none", auth.Permission{KB: "docs", Write: true, NoSecrets: true}, false},
		{"r none", auth.Permission{KB: "docs", NoSecrets: true}, false},
	} {
		ctx := auth.ContextWithPrincipal(context.Background(), auth.Principal{ID: "t", Policy: auth.Policy{Permissions: []auth.Permission{tc.perm}}})
		if got := SecretsVisible(ctx, k); got != tc.want {
			t.Errorf("%s: SecretsVisible = %v, want %v", tc.name, got, tc.want)
		}
	}
}
