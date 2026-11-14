package service

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

var proxyTestEnv = []EnvVar{
	{"HTTPS_PROXY", "http://proxy.example.com:3128"},
	{"NO_PROXY", "localhost,*.example.com"},
	{"SSL_CERT_FILE", `/etc/ssl/my ca&"x%.pem`},
}

func TestProxyEnvIsAnExplicitList(t *testing.T) {
	got := ProxyEnv([]string{
		"PATH=/usr/bin", "HOME=/home/user", "CARTOGRAPHER_TOKENS=secret", "AWS_SECRET_ACCESS_KEY=x",
		"https_proxy=http://lower.example.com", "HTTPS_PROXY=http://proxy.example.com:3128",
		"HTTP_PROXY=", "no_proxy=a.example.com", "SSL_CERT_FILE=/ca.pem", "ALL_PROXY=http://other.example.com",
		"NO_PROXY=bad\nvalue",
	})
	want := []EnvVar{
		{"HTTPS_PROXY", "http://proxy.example.com:3128"}, {"https_proxy", "http://lower.example.com"},
		{"no_proxy", "a.example.com"}, {"SSL_CERT_FILE", "/ca.pem"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ProxyEnv = %v, want %v", got, want)
	}
	if got := ProxyEnv([]string{"PATH=/usr/bin"}); len(got) != 0 {
		t.Errorf("nothing set must give nothing, got %v", got)
	}
}

func TestRenderedDefinitionsCarryExactlyTheListedVariables(t *testing.T) {
	bin, cfg, log := "/opt/bin/cartographer", "/home/user/server.yaml", "/home/user/server.log"
	task := func() string {
		return RenderWindowsTaskXML(bin, cfg, log, `HOST\user`, "")
	}

	// unset: no definition mentions any of the listed variables
	for name, out := range map[string]string{
		"plist": RenderLaunchdPlist(bin, cfg, log), "unit": RenderSystemdUnit(bin, cfg), "task": task(),
	} {
		for _, n := range proxyEnvNames {
			if strings.Contains(out, n) {
				t.Errorf("%s with nothing set mentions %s:\n%s", name, n, out)
			}
		}
	}
	if strings.Contains(task(), "--env-file") {
		t.Error("task with nothing set carries an env flag")
	}

	plist := RenderLaunchdPlist(bin, cfg, log, proxyTestEnv...)
	for _, want := range []string{
		"<key>HTTPS_PROXY</key>\n\t\t<string>http://proxy.example.com:3128</string>",
		"<key>NO_PROXY</key>", "<string>/etc/ssl/my ca&amp;\"x%.pem</string>",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist missing %q:\n%s", want, plist)
		}
	}
	if strings.Contains(plist, "HTTP_PROXY") || strings.Contains(plist, "https_proxy") {
		t.Errorf("plist carries a variable that was not set:\n%s", plist)
	}

	unit := RenderSystemdUnit(bin, cfg, proxyTestEnv...)
	for _, want := range []string{
		`Environment="HTTPS_PROXY=http://proxy.example.com:3128"`,
		`Environment="NO_PROXY=localhost,*.example.com"`,
		`Environment="SSL_CERT_FILE=/etc/ssl/my ca&\"x%%.pem"`,
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit missing %q:\n%s", want, unit)
		}
	}

	// The task carries neither the env file nor a value: its command line is
	// the previous stable's, with and without recorded variables (#2094).
	x := task()
	if want := `<Arguments>serve --config "` + cfg + `" --log-file "` + log + `"</Arguments>`; !strings.Contains(x, want) {
		t.Errorf("task missing %q:\n%s", want, x)
	}
	for _, e := range proxyTestEnv {
		if strings.Contains(x, e.Value) || strings.Contains(x, e.Name) {
			t.Errorf("task carries %s:\n%s", e.Name, x)
		}
	}
	if strings.Contains(x, "env-file") || strings.Contains(x, "--supervised") {
		t.Errorf("task carries a flag the previous stable does not know:\n%s", x)
	}
}

func TestParseEnvFileHonoursOnlyTheListedNamesAndNeverEchoesAValue(t *testing.T) {
	env, ignored := ParseEnvFile([]byte("# c\nHTTPS_PROXY=http://user:pw@proxy.example.com:3128\n\nAWS_SECRET=topsecret\nnoequalsecret\nNO_PROXY=\nSSL_CERT_FILE=/a=b.pem\r\n"))
	want := []EnvVar{{"HTTPS_PROXY", "http://user:pw@proxy.example.com:3128"}, {"SSL_CERT_FILE", "/a=b.pem"}}
	if !reflect.DeepEqual(env, want) {
		t.Errorf("env = %v, want %v", env, want)
	}
	if got := strings.Join(ignored, ","); got != "AWS_SECRET,line 5,NO_PROXY" {
		t.Errorf("ignored = %q", got)
	}
	_, bad := ParseEnvFile([]byte("sk-abc123def=whatever\nmy secret=x\nnoequalsecret\n"))
	if got := strings.Join(bad, ","); got != "line 1,line 2,line 3" {
		t.Errorf("a key that is not an identifier must be reported by line, got %q", got)
	}
	if strings.Contains(strings.Join(ignored, ""), "topsecret") || strings.Contains(strings.Join(ignored, ""), "noequalsecret") {
		t.Error("an ignored line must be reported by key only")
	}
}

func installWithEnv(t *testing.T, goos_ string) (m *Manager, home string, install func(keep bool, env ...string)) {
	t.Helper()
	home = withTestHome(t, goos_)
	binPath := filepath.Join(t.TempDir(), "cartographer")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	origExe, origEnv := osExecutable, environ
	osExecutable = func() (string, error) { return binPath, nil }
	t.Cleanup(func() { osExecutable, environ = origExe, origEnv })
	m, _ = newTestManager()
	return m, home, func(keep bool, env ...string) {
		t.Helper()
		environ = func() []string { return env }
		if _, err := m.Install(InstallOptions{DataDir: filepath.Join(home, "data"), HTTPAddr: "127.0.0.1:39273", KeepEnv: keep}); err != nil {
			t.Fatalf("Install: %v", err)
		}
	}
}

func TestInstallRecordsTheShellProxyKeepsItOnSwitchAndDropsItOnInstall_Darwin(t *testing.T) {
	m, home, install := installWithEnv(t, "darwin")
	plistPath := filepath.Join(home, "Library", "LaunchAgents", "com.cartographer.serve.plist")
	envPath := filepath.Join(home, ".config", "cartographer", "service.env")
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	mode := func(p string) os.FileMode {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		return fi.Mode().Perm()
	}

	install(false, "HTTPS_PROXY=http://proxy.example.com:3128", "SECRET_TOKEN=x")
	if f := read(envPath); !strings.Contains(f, "HTTPS_PROXY=http://proxy.example.com:3128\n") || strings.Contains(f, "SECRET_TOKEN") {
		t.Errorf("env file must hold the proxy and nothing else:\n%s", f)
	}
	if p := read(plistPath); !strings.Contains(p, "http://proxy.example.com:3128") || strings.Contains(p, "SECRET_TOKEN") {
		t.Errorf("plist must carry the proxy and nothing else:\n%s", p)
	}
	if runtime.GOOS != "windows" {
		if got := mode(envPath); got != 0o600 {
			t.Errorf("env file mode = %o, want 600", got)
		}
		if got := mode(plistPath); got != 0o600 {
			t.Errorf("plist carrying a value must be 600, got %o", got)
		}
	}
	if st, err := m.Status(""); err != nil || !reflect.DeepEqual(st.ServiceEnv, []string{"HTTPS_PROXY"}) {
		t.Errorf("Status.ServiceEnv = %v, %v", st.ServiceEnv, err)
	}

	// A binary switch (upgrade-repair) keeps what is recorded, whatever this
	// shell has now.
	environ = func() []string { return []string{"HTTPS_PROXY=http://other.example.com"} }
	if err := m.SwitchBinary(filepath.Join(home, ".config", "cartographer", "server.yaml")); err != nil {
		t.Fatalf("SwitchBinary: %v", err)
	}
	if !strings.Contains(read(plistPath), "proxy.example.com:3128") || strings.Contains(read(plistPath), "other.example.com") {
		t.Errorf("a switch must keep the recorded variables:\n%s", read(plistPath))
	}

	// An explicit install takes the shell's and removes what is gone from it.
	install(false, "PATH=/usr/bin")
	if p := read(plistPath); strings.Contains(p, "proxy.example.com") {
		t.Errorf("a variable removed from the shell must leave the plist:\n%s", p)
	}
	if _, err := os.Stat(envPath); !os.IsNotExist(err) {
		t.Errorf("env file must be removed when nothing is recorded: %v", err)
	}
}

func TestInstallRecordsTheShellProxyInTheUnitAt0600_Linux(t *testing.T) {
	_, home, install := installWithEnv(t, "linux")
	install(false, "NO_PROXY=localhost")
	unit := filepath.Join(home, ".config", "systemd", "user", "cartographer.service")
	b, err := os.ReadFile(unit)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `Environment="NO_PROXY=localhost"`) {
		t.Errorf("unit:\n%s", b)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(unit); fi.Mode().Perm() != 0o600 {
			t.Errorf("unit mode = %o, want 600", fi.Mode().Perm())
		}
	}
}

func TestEnvFileTightenedBeforeItIsReplaced(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("modes are not enforced on Windows")
	}
	home := withTestHome(t, "darwin")
	origEnv := environ
	t.Cleanup(func() { environ = origEnv })
	cfg := filepath.Join(home, ".config", "cartographer", "server.yaml")
	path := EnvFilePath(cfg)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("HTTPS_PROXY=old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	environ = func() []string { return []string{"HTTPS_PROXY=http://user:pw@proxy.example.com"} }
	if _, err := recordEnv(cfg, false); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("a replaced 0644 env file must end 0600: %v %v", fi, err)
	}
	// The temp file takes the replaced file's mode before the rename, so the
	// tightening happens first: no temp is left behind either.
	if m, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".service.env.cartographer-tmp-*")); len(m) != 0 {
		t.Errorf("temp file left: %v", m)
	}
}

// On Windows the env file carries the supervision marker, since the task cannot
// carry an environment and its command line stays the previous stable's (#2094).
// The marker is never "recorded": a binary switch keeps the proxy variables only.
func TestRecordEnv_WindowsWritesTheSupervisionMarker(t *testing.T) {
	home := withTestHome(t, "windows")
	origEnv := environ
	t.Cleanup(func() { environ = origEnv })
	environ = func() []string { return nil }
	cfg := filepath.Join(home, "server.yaml")
	env, err := recordEnv(cfg, false)
	if err != nil || len(env) != 0 {
		t.Fatalf("recordEnv = %v, %v", env, err)
	}
	data, err := os.ReadFile(EnvFilePath(cfg))
	if err != nil || !strings.Contains(string(data), SupervisedEnv+"=1\n") {
		t.Fatalf("env file = %q, %v", data, err)
	}
	if names := EnvFileNames(cfg); len(names) != 0 {
		t.Errorf("the marker is not a recorded variable, names = %v", names)
	}
	parsed, _ := ParseEnvFile(data)
	if len(parsed) != 1 || parsed[0] != (EnvVar{SupervisedEnv, "1"}) {
		t.Errorf("ParseEnvFile = %v", parsed)
	}
	// Keeping the variables keeps the marker, and never adds it to the list.
	environ = func() []string { return []string{"HTTPS_PROXY=http://proxy.example.com"} }
	if _, err := recordEnv(cfg, false); err != nil {
		t.Fatal(err)
	}
	kept, err := recordEnv(cfg, true)
	if err != nil || len(kept) != 1 || kept[0].Name != "HTTPS_PROXY" {
		t.Errorf("kept = %v, %v", kept, err)
	}
	if data, _ := os.ReadFile(EnvFilePath(cfg)); !strings.Contains(string(data), SupervisedEnv+"=1\n") {
		t.Errorf("env file lost the marker: %q", data)
	}
}

// Off Windows nothing but the proxy variables is recorded, and with none the
// file is removed, as before.
func TestRecordEnv_OtherPlatformsWriteNoMarker(t *testing.T) {
	home := withTestHome(t, "linux")
	origEnv := environ
	t.Cleanup(func() { environ = origEnv })
	environ = func() []string { return nil }
	cfg := filepath.Join(home, "server.yaml")
	if _, err := recordEnv(cfg, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(EnvFilePath(cfg)); !os.IsNotExist(err) {
		t.Errorf("no variables, no env file: %v", err)
	}
}
