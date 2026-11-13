package gitx

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGlobalIdentReadsTheXDGFileToo: git reads $XDG_CONFIG_HOME/git/config
// first and ~/.gitconfig after it, so the later wins key by key. `git config
// --global` alone reads only ~/.gitconfig when it exists, which hid the XDG
// identity (and an XDG includeIf) from a client entry.
func TestGlobalIdentReadsTheXDGFileToo(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping gitx tests")
	}
	const xdgPerson = "[user]\n\tname = Xdg Person\n\temail = xdg@example.test\n"
	const homePerson = "[user]\n\tname = Home Person\n\temail = home@example.test\n"
	cases := []struct {
		name, xdg, home, want string
		perFolder             bool
		xdgEnv                string // "set" (default), "unset", "empty"
		defaultXDG            string // content of $HOME/.config/git/config
		globalEmpty           bool   // GIT_CONFIG_GLOBAL set to ""
	}{
		{name: "default xdg dir with XDG_CONFIG_HOME unset", xdgEnv: "unset",
			defaultXDG: xdgPerson, want: "Xdg Person <xdg@example.test>"},
		{name: "default xdg dir with XDG_CONFIG_HOME empty", xdgEnv: "empty",
			defaultXDG: xdgPerson, want: "Xdg Person <xdg@example.test>"},
		{name: "GIT_CONFIG_GLOBAL set but empty reads no global file", xdgEnv: "unset", globalEmpty: true,
			defaultXDG: xdgPerson, home: homePerson, want: ""},
		{name: "xdg alone", xdg: xdgPerson, want: "Xdg Person <xdg@example.test>"},
		{name: "home alone", home: homePerson, want: "Home Person <home@example.test>"},
		{name: "home overrides xdg", xdg: xdgPerson, home: homePerson, want: "Home Person <home@example.test>"},
		{name: "keys merge across files", xdg: xdgPerson, home: "[user]\n\temail = home@example.test\n",
			want: "Xdg Person <home@example.test>"},
		{name: "xdg includeIf with home present", home: homePerson, perFolder: true,
			xdg: "[includeIf \"gitdir:~/work/\"]\n\tpath = /nonexistent.gitconfig\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := isolateGitIdentity(t)
			t.Setenv("GIT_CONFIG_GLOBAL", "")
			if !tc.globalEmpty {
				os.Unsetenv("GIT_CONFIG_GLOBAL")
			}
			xdg := filepath.Join(home, "xdg")
			switch tc.xdgEnv {
			case "unset":
				t.Setenv("XDG_CONFIG_HOME", "")
				os.Unsetenv("XDG_CONFIG_HOME")
			case "empty":
				t.Setenv("XDG_CONFIG_HOME", "")
			default:
				t.Setenv("XDG_CONFIG_HOME", xdg)
			}
			for path, content := range map[string]string{
				filepath.Join(xdg, "git", "config"):             tc.xdg,
				filepath.Join(home, ".config", "git", "config"): tc.defaultXDG,
				filepath.Join(home, ".gitconfig"):               tc.home,
			} {
				if content == "" {
					continue
				}
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, perFolder := GlobalIdent()
			if got != tc.want || perFolder != tc.perFolder {
				t.Errorf("GlobalIdent = %q, %v; want %q, %v", got, perFolder, tc.want, tc.perFolder)
			}
		})
	}
}
