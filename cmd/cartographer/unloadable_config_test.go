package main

import (
	"os"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// An unloadable .cartographer.yaml is never replaced by defaults (D1514): the
// typo below makes Load fail, and connect/disconnect must stop with the file
// byte-identical instead of treating it as a first connect.
const unloadableConfig = `signing_keys:
  kb-a:
    - 00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff
identities:
  kb-a: user@example.com
clients:
  claude:
    access: read
`

func writeUnloadable(t *testing.T) string {
	t.Helper()
	setHome(t, t.TempDir())
	dir := t.TempDir()
	if err := os.WriteFile(clientconfig.Path(dir), []byte(unloadableConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func assertUntouched(t *testing.T, dir string) {
	t.Helper()
	got, err := os.ReadFile(clientconfig.Path(dir))
	if err != nil || string(got) != unloadableConfig {
		t.Fatalf("config changed: err=%v\n%s", err, got)
	}
}

func TestDoConnect_UnloadableConfigIsNotReplaced(t *testing.T) {
	dir := writeUnloadable(t)
	_, err := doConnect(connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: "http://127.0.0.1:1/mcp", Name: "cartographer"})
	if err == nil || !strings.Contains(err.Error(), "clients.claude.access") {
		t.Fatalf("err = %v, want the Load error", err)
	}
	assertUntouched(t, dir)
}

func TestDoDisconnect_UnloadableConfigIsNotReplaced(t *testing.T) {
	dir := writeUnloadable(t)
	_, err := doDisconnect(disconnectOptions{Providers: []string{"claude"}, Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "clients.claude.access") {
		t.Fatalf("err = %v, want the Load error", err)
	}
	assertUntouched(t, dir)
}

func TestSave_MalformedConfigIsNotReplaced(t *testing.T) {
	dir := t.TempDir()
	bad := "signing_keys: [unclosed\n"
	if err := os.WriteFile(clientconfig.Path(dir), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(dir, clientconfig.Default()); err == nil {
		t.Fatal("Save replaced a file it cannot parse")
	}
	if got, _ := os.ReadFile(clientconfig.Path(dir)); string(got) != bad {
		t.Fatalf("file changed: %s", got)
	}
}

func TestDoConnect_MissingConfigStillConnects(t *testing.T) {
	setHome(t, t.TempDir())
	dir := t.TempDir()
	if _, err := doConnect(connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: "http://127.0.0.1:1/mcp", Name: "cartographer"}); err != nil {
		t.Fatalf("first connect: %v", err)
	}
}

func TestCheckClientConfig_UnreadableFileSaysSo(t *testing.T) {
	dir := writeUnloadable(t)
	_, loadErr := clientconfig.Load(dir)
	f := checkClientConfig(dir, nil, loadErr, "")
	if len(f) != 1 || !strings.Contains(f[0].Message, "cannot be read") || strings.Contains(f[0].Fix, "connect") {
		t.Fatalf("findings = %+v", f)
	}
}
