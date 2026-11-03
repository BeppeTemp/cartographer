package clientconfig

import (
	"os"
	"testing"
)

// TestMain gives the run a private home: the repo cache and the derived
// bindings resolve under $HOME/.config/cartographer, and a test that forgot to
// set HOME would overwrite the developer's real files.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "cartographer-test-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home)
	os.Unsetenv("XDG_CONFIG_HOME")
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
