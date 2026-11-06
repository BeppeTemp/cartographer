package plugin

import "testing"

func TestGlobMatch(t *testing.T) {
	for _, c := range []struct {
		glob, path string
		want       bool
	}{
		{"{home}/**", "{home}/a", true},
		{"{home}/**", "{home}/a/b/c", true},
		{"{home}/**", "{home}", false},
		{"{home}/a/**", "{home}/a", false},
		{"{home}/a/*.md", "{home}/a/x.md", true},
		{"{home}/a/*.md", "{home}/a/b/x.md", false},
		{"{home}/a/**/x.md", "{home}/a/x.md", true},
		{"{home}/a/**/x.md", "{home}/a/b/c/x.md", true},
		{"{home}/a/**/x.md", "{home}/b/x.md", false},
		{"{home}/a/**/**/x", "{home}/a/x", true},
		{"{home}/a?", "{home}/ab", true},
		{"{home}/a", "{xdg_config}/a", false},
		{"{home}/a", "{home}/A", false},
		{"{home}/[a", "{home}/a", false},
	} {
		if got := GlobMatch(c.glob, c.path); got != c.want {
			t.Errorf("GlobMatch(%q, %q) = %v", c.glob, c.path, got)
		}
	}
}

func TestExpandAnchor(t *testing.T) {
	env := ProviderEnv{GOOS: "linux", Home: "/home/user", Getenv: func(k string) string {
		return map[string]string{"XDG_CONFIG_HOME": "/home/user/cfg"}[k]
	}, Documents: func(h string) string { return h + "/Docs" }}
	for a, want := range map[string]string{"home": "/home/user", "xdg_config": "/home/user/cfg", "documents": "/home/user/Docs"} {
		if got, err := ExpandAnchor(env, a); err != nil || got != want {
			t.Errorf("%s = %q, %v; want %q", a, got, err, want)
		}
	}
	env.Getenv = func(string) string { return "/etc" }
	if _, err := ExpandAnchor(env, "xdg_config"); err == nil {
		t.Error("an XDG_CONFIG_HOME outside home")
	}
	env.Documents = func(string) string { return "/elsewhere" }
	if _, err := ExpandAnchor(env, "documents"); err == nil {
		t.Error("Documents outside home")
	}
	for _, a := range []string{"app_data", "local_app_data"} {
		if _, err := ExpandAnchor(env, a); err == nil {
			t.Errorf("%s on linux", a)
		}
	}
	env.Home = ""
	if _, err := ExpandAnchor(env, "home"); err == nil {
		t.Error("unknown home")
	}
}

// GlobMatch and the manifest validator are one dialect: every glob below passes
// globProblem and matches what the manifest documents. (A bare ** or * segment
// is not manifest-valid, it reaches Cartographer's own state; ** is still
// matched, by the table above.)
func TestGlobMatchAgreesWithTheValidator(t *testing.T) {
	for _, c := range []struct {
		glob  string
		match []string
		miss  []string
	}{
		{"{home}/notes/*.md", []string{"{home}/notes/a.md"}, []string{"{home}/notes", "{home}/notes/a/b.md", "{home}/other/a.md"}},
		{"{xdg_config}/tool/*.toml", []string{"{xdg_config}/tool/a.toml"}, []string{"{xdg_config}/tool/a/b.toml", "{home}/tool/a.toml"}},
		{"{documents}/a-*.txt", []string{"{documents}/a-1.txt"}, []string{"{home}/a-1.txt"}},
		{"{home}/a/b.txt", []string{"{home}/a/b.txt"}, []string{"{home}/a/b.txt2", "{home}/a"}},
		{"{app_data}/t/f?.json", []string{"{app_data}/t/f1.json"}, []string{"{app_data}/t/f10.json"}},
		{"{local_app_data}/t/[ab].log", []string{"{local_app_data}/t/a.log"}, []string{"{local_app_data}/t/c.log"}},
	} {
		if why := globProblem(c.glob); why != "" {
			t.Errorf("%q is not manifest-valid: %s", c.glob, why)
		}
		for _, m := range c.match {
			if !GlobMatch(c.glob, m) {
				t.Errorf("GlobMatch(%q, %q) = false", c.glob, m)
			}
		}
		for _, m := range c.miss {
			if GlobMatch(c.glob, m) {
				t.Errorf("GlobMatch(%q, %q) = true", c.glob, m)
			}
		}
	}
}
