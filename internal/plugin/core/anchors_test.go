package core

import "testing"

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
