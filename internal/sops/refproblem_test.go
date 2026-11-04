package sops

import "testing"

func TestRefProblem(t *testing.T) {
	t.Parallel()
	root := kbWithSecret(t, "pw: ENC[x]\nnested:\n  k: ENC[y]\nsops:\n  mac: x\n")
	for _, c := range []struct{ file, ptr, want string }{
		{"secrets/app.sops.yaml", "/pw", ""},
		{"secrets/app.sops.yaml", "/nested/k", ""},
		{"secrets/app.sops.yaml", "/nested", "key /nested in secrets/app.sops.yaml is not a single value"},
		{"secrets/app.sops.yaml", "/nope", "key /nope not found in secrets/app.sops.yaml"},
		{"secrets/missing.sops.yaml", "/x", "secrets/missing.sops.yaml not found"},
	} {
		if got := RefProblem(root, c.file, c.ptr); got != c.want {
			t.Errorf("%s%s = %q, want %q", c.file, c.ptr, got, c.want)
		}
	}
}

func TestInSecretsDir(t *testing.T) {
	for in, want := range map[string]bool{
		"secrets/a.sops.yaml":         true,
		"secrets/sub/a.sops.yaml":     true,
		"secrets/../data/x.sops.yaml": false,
		"secrets/./a.sops.yaml":       false,
		"secrets//a.sops.yaml":        false,
		"/secrets/a.sops.yaml":        false,
		"other/a.sops.yaml":           false,
	} {
		if got := InSecretsDir(in); got != want {
			t.Errorf("InSecretsDir(%q) = %v, want %v", in, got, want)
		}
	}
}
