package service

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

// utf16le is what wsl.exe -l prints: UTF-16LE, here with a BOM and CRLF.
func utf16le(s string, bom bool) string {
	var b []byte
	if bom {
		b = append(b, 0xFF, 0xFE)
	}
	for _, u := range utf16.Encode([]rune(s)) {
		b = append(b, byte(u), byte(u>>8))
	}
	return string(b)
}

func TestWslMarker(t *testing.T) {
	home := withTestHome(t, "windows")
	p, err := WslMarkerPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "AppData", "Local", "cartographer", "wsl-server.json"); p != want {
		t.Errorf("WslMarkerPath = %q, want %q", p, want)
	}
	if d, err := ReadWslMarker(); d != "" || err != nil {
		t.Errorf("no marker = %q, %v", d, err)
	}
	if err := WriteWslMarker("Ubuntu-24.04"); err != nil {
		t.Fatal(err)
	}
	if d, err := ReadWslMarker(); d != "Ubuntu-24.04" || err != nil {
		t.Errorf("marker = %q, %v", d, err)
	}
	if err := WriteWslMarker("bad name"); err == nil {
		t.Error("an invalid distro was recorded")
	}
	if err := RemoveWslMarker(); err != nil {
		t.Fatal(err)
	}
	if err := RemoveWslMarker(); err != nil {
		t.Errorf("removing an absent marker: %v", err)
	}
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(`{"distro":"a;calc"}`), 0o644)
	if _, err := ReadWslMarker(); err == nil {
		t.Error("a marker naming an invalid distro was accepted")
	}
}

func TestListWslDistros_DecodesUTF16(t *testing.T) {
	withTestHome(t, "windows")
	for name, out := range map[string]string{
		"bom":    utf16le("Ubuntu-24.04\r\nDebian\r\n\r\n", true),
		"no bom": utf16le("Ubuntu-24.04\r\nDebian\r\n", false),
		"utf8":   "Ubuntu-24.04\r\nDebian\r\n", // WSL_UTF8=1
	} {
		var got []string
		m := &Manager{run: func(n string, a ...string) (string, error) {
			got = append([]string{n}, a...)
			return out, nil
		}}
		names, err := m.ListWslDistros(true)
		if err != nil || strings.Join(names, ",") != "Ubuntu-24.04,Debian" {
			t.Errorf("%s: names = %q, %v", name, names, err)
		}
		if strings.Join(got, " ") != "wsl.exe -l -q --running" {
			t.Errorf("%s: ran %v", name, got)
		}
	}
}

func TestWslCommands_NoWslExe(t *testing.T) {
	withTestHome(t, "windows")
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	m := &Manager{run: func(string, ...string) (string, error) { t.Fatal("ran a command"); return "", nil }}
	if _, err := m.ListWslDistros(false); !errors.Is(err, ErrNoWsl) {
		t.Errorf("list = %v", err)
	}
	if _, err := m.DelegateToWsl("Ubuntu", []string{"status"}); !errors.Is(err, ErrNoWsl) {
		t.Errorf("delegate = %v", err)
	}
	if _, err := m.WslCartographerVersion("Ubuntu"); !errors.Is(err, ErrNoWsl) {
		t.Errorf("version = %v", err)
	}
}

func TestDelegateToWsl(t *testing.T) {
	withTestHome(t, "windows")
	orig := wslAttached
	t.Cleanup(func() { wslAttached = orig })
	var got []string
	wslAttached = func(n string, a ...string) (int, error) { got = append([]string{n}, a...); return 3, nil }
	code, err := (&Manager{}).DelegateToWsl("Ubuntu", []string{"status", "--json"})
	if err != nil || code != 3 {
		t.Fatalf("code %d, err %v", code, err)
	}
	if strings.Join(got, " ") != "wsl.exe -d Ubuntu -- cartographer service status --json" {
		t.Errorf("ran %v", got)
	}
	if _, err := (&Manager{}).DelegateToWsl("a b", nil); err == nil {
		t.Error("an invalid distro was passed to wsl.exe")
	}
}
